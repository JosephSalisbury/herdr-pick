# herdr-pick

A project picker for [herdr](https://herdr.dev). Hit a key, fuzzy-find a project across your GitHub orgs and your existing worktrees, and get a herdr workspace with an agent already running in it.

## Why this exists

herdr already does most of the work: `worktree.create` makes the branch and the checkout, opens it as a workspace, and groups it with the parent repo's workspace. `worktree.open` is idempotent. `worktree.remove` cleans up.

Four things it does not do, which is all this tool is:

1. **Cold start.** `worktree.create` needs a checkout that already exists on disk. Nothing clones `giantswarm/foo` for you.
2. **Discovery.** There is no fuzzy picker spanning "repos I could work on" and "worktrees I already have".
3. **Launching the agent** in the workspace that comes back.
4. **Herding.** herdr can focus a workspace and remove a worktree, but it does not know which of them are *yours*, which have a live agent, or which are finished. The management commands answer those questions and then hand the actual work back to herdr.

Anything herdr can already do is herdr's job. Resist adding to this. The management commands only ever *select* — herdr does the focusing and removing.

## How it works

```
prefix+o  →  herdr-pick pick
                │
                ├─ candidates: worktrees on disk, then cached GitHub repos
                ├─ fzf
                ├─ existing worktree  →  worktree.open (focus)
                └─ bare repo          →  prompt branch (default: generated name)
                                      →  git clone if absent
                                      →  sync clone (fetch + remote-tracking refs)
                                      →  worktree.create
                                      →  agent.start in the new workspace
```

### Disk layout

Everything lives under one root, hidden by default (`~/.local/share/herdr-pick`):

```
<root>/
├── repos/<org>/<repo>/              # parent clone, only ever a worktree source
├── worktrees/<org>/<repo>/<branch>/ # checkouts, passed to herdr as --path
└── cache/<org>.txt                  # one repo per line
```

The org is in every path deliberately. herdr's default layout is
`<worktrees.directory>/<repo>/<branch-slug>` with no org component, so
`giantswarm/cluster-api` and `kubernetes-sigs/cluster-api` would collide. We
always pass an explicit path and never rely on `[worktrees] directory`.

A branch name is always exactly one path segment — slashes are rejected — so a
directory name round-trips to a branch name with no slug table.

### Source of truth

The filesystem. `worktrees/` is walked to find checkouts; a directory counts
only if it contains a `.git` entry. There is no database and no state file.
The cache is derived data and is safe to delete at any time.

### The GitHub cache

Configured orgs are fetched with `gh repo list --limit 5000`. That is far too
slow to sit in front of a keypress for a 1,700-repo org, so the picker only
ever reads the cache. When a cache is older than `cache_ttl` the picker spawns
a detached `herdr-pick refresh` and carries on with what it has — stale now,
fresh next time. Orgs refresh concurrently and one failing org does not stop
the others.

The consequence to remember: the very first run on a cold cache lists only
local worktrees.

### Talking to herdr

Over the socket API (newline-delimited JSON), not the CLI. The socket's method
and parameter names are pinned by a published schema (`herdr api schema`);
the CLI's flags are not. `herdrProtocol` asserts the version so an upgrade
fails loudly rather than strangely.

Methods used: `ping`, `worktree.create`, `worktree.open`, `worktree.remove`,
`workspace.list`, `workspace.focus`, `pane.list`, `pane.send_input`.

`worktree.remove` takes a `workspace_id`, not a path — another reason "done"
cleanup only reaches open workspaces.

### Why not agent.start

`agent.start` looks like the obvious way to launch claude, but it has no
`pane_id` parameter and its only `split` values are `right` and `down` — so it
always adds a *second* pane alongside the one `worktree.create` already made.
One pane is the requirement.

Instead: `pane.list` the new workspace, take the focused (or only) pane, and
`pane.send_input` the agent command with `keys: ["enter"]` — text and Enter in
one call, which is how herdr's own `pane run` submits atomically under
bracketed paste. herdr still tracks the agent, because it detects agents from
the foreground process rather than from registration.

### Why the parent clone is bare

`worktree.create` groups the new workspace with the parent repo's workspace,
creating that parent from `--cwd` if it doesn't exist. With a normal clone that
parent is a real checkout on `main`, so you get two workspaces per project and
one of them is a checkout you must never commit into — it is the worktree
source. A bare clone has no working tree, so there is nothing to open.

`isBareRepo` detects an existing clone by `HEAD` at the top level rather than a
`.git` directory. A leftover non-bare checkout is reported as an error rather
than left for git to fail on confusingly.

### Why the clone is synced on every open

`SyncClone` runs before every `worktree.create`, and exists for two problems
that share one cause — `git clone --bare` is not a normal clone.

**New work must start from current main.** `worktree.create` branches the new
checkout off the clone's `HEAD`, so without a fetch the second and every later
worktree for a repo starts from whatever main was when the clone was first made.
The round trip is worth paying for on each open, and it happens after the branch
prompt, so it is not in the way of the picker.

**Worktrees must be able to merge main.** `clone --bare` copies remote heads
straight into `refs/heads/*` and writes no `remote.origin.fetch`, so
`refs/remotes/origin/*` never exists. In a worktree that means `git merge
origin/main` and `git rebase origin/main` fail on an unknown revision and
`git status` has nothing to count ahead/behind against. `SyncClone` configures
the refspec a normal clone would have had.

Three details make this less obvious than it looks:

- The refspec is *configured* (`remote.origin.fetch`) so the user's own later
  `git fetch` behaves normally, and also *passed explicitly* to our fetch,
  because an explicit refspec overrides the configured one and we need both
  namespaces updated in a single round trip.
- The `refs/heads` half is scoped to the default branch, read from `HEAD`. A
  wildcard `+refs/heads/*:refs/heads/*` fails on any branch a worktree has
  checked out — which here is every branch herdr-pick creates.
- Fresh clones are synced too. `clone --bare` leaves no remote-tracking refs, so
  skipping the sync would hand back the one repo that cannot merge main.

`--replace-all` on the config write means a clone made before this existed
converges on its next open rather than staying broken forever.

A sync failure is a warning, not an error: offline, a worktree off a stale main
still beats no worktree.

## Platform

macOS only. Don't add cross-platform handling for its own sake.

One thing that looks cross-platform but isn't: config and socket paths resolve
via `$XDG_CONFIG_HOME` or `~/.config`, **not** `os.UserConfigDir()`. On macOS
that would return `~/Library/Application Support`, but herdr keeps its own
config and socket under `~/.config`. We have to find herdr's socket, so we
follow herdr rather than the Apple convention.

## Config

`~/.config/herdr-pick/config.yaml`, all keys optional:

```yaml
orgs: [giantswarm, JosephSalisbury]
root: ~/.local/share/herdr-pick
cache_ttl: 6h
agent: [claude]
include_archived: false
```

`include_archived` matters more than it sounds: giantswarm has 1,738 repos of
which 1,076 are archived, so leaving it off cuts the picker list by ~60%.

Repository lists sort case-insensitively (`lessFold`). A byte-value sort would
put every `JosephSalisbury/*` ahead of every `giantswarm/*`.

herdr side:

```toml
[[keys.command]]
key = "prefix+o"
type = "popup"
command = "herdr-pick pick"
description = "open project"
width = "80%"
height = "60%"
```

## Managing work

Once you have several worktrees in flight, three commands herd them. All three
speak to herdr over the socket and share one safety rule: they only ever touch
workspaces whose checkout lives under `<root>/worktrees`, so they can never
close or focus your unrelated herdr work. A workspace is matched to herdr-pick
by the `checkout_path` herdr reports in `workspace.list`, not by a state file.

- **`status`** prints one line per open worktree as `status<TAB>label`, ordered
  by urgency — the non-interactive glance at what the whole fleet of agents is
  doing. Read-only and pipeable (`herdr-pick status | grep blocked`).
- **`switch`** lists worktrees with a live agent and focuses the one you pick
  (`workspace.focus`). "Live" is herdr's own agent status — `working`,
  `blocked` or `idle`. `blocked` (an agent waiting on you) sorts first, then
  `working`, then `idle`, so the picker leads with what most wants attention.
  `--all` widens the list to finished and agent-less worktrees too.
- **`clean`** removes worktrees whose agent status is `done` (`worktree.remove`,
  which closes the workspace and deletes the checkout). Without `--force`,
  herdr refuses a checkout with uncommitted changes, so a stray "done" cannot
  discard unfinished work. `--dry-run` prints what would go without removing it.
- **`issue`** takes a GitHub issue (`org/repo#123` or a URL), derives the branch
  name from it (`<number>-<title-slug>`), opens the worktree, and launches the
  agent briefed to start on that issue. The brief is a one-line prompt that
  points the agent at `gh issue view` rather than stuffing the whole issue body
  through the shell — the agent reads the full issue itself.

`switch` and `clean` lean entirely on herdr's agent-status detection; herdr-pick
adds no status tracking of its own. "done" cleanup is therefore always about
*open* workspaces, since a status only exists while a workspace is open.

## Commands

- `pick` — the keybound entry point: candidates, fzf, prompt, open
- `list` — candidates one per line, for piping
- `open <org/repo[@branch]>` — resolve a selection without the picker
- `issue <org/repo#number | url>` — open a worktree for an issue and start on it
- `status` — one line per open worktree with its agent status, for piping
- `switch` — fuzzy-pick a worktree with a live agent and focus it (`--all` for every owned worktree)
- `clean` — remove worktrees whose agent has finished (`--dry-run`, `--force`)
- `refresh` — refill every org cache
- `ping` — check the herdr socket, independently of the open flow

## Conventions

- `gofmt` enforced, `golangci-lint` with strict rules.
- TDD. Tests in `_test.go` files alongside the code they test.
- External commands go through `Executor`; the herdr socket goes through
  `Herdr`. Both are interfaces so tests never touch the network or a real
  process. `fakes_test.go` holds the doubles.
- Return errors, don't panic. Wrap with context: `fmt.Errorf("doing thing: %w", err)`.
- Shell out to `git` and `gh` rather than using libraries. The user's own
  config handles authentication.
- Keep it flat until complexity demands packages.
- Plain text output, designed for piping.

## Build

```
make build       # go build
make test        # go test ./...
make lint        # golangci-lint run
make check       # all three
```

## Philosophy

- Solo project. Optimise for the author's workflow, not generalisation.
- The smallest thing that closes the gap herdr leaves. If herdr grows a
  feature, delete ours.
- Worktrees are cheap and disposable.
- Never block the picker on the network.
