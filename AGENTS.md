# herdr-pick

A namespace picker for [herdr](https://herdr.dev). Hit a key, fuzzy-find the
repositories you want to work on, name the work, and get a herdr workspace with
an agent already running over all of them.

## Why this exists

herdr already does most of the work: `worktree.open` opens a checkout as a
workspace and is idempotent, and it groups a workspace with its parent repo's.

What it does not do, which is all this tool is:

1. **Cold start.** Nothing clones `giantswarm/foo` for you.
2. **Discovery.** There is no fuzzy picker spanning "repos I could work on" and
   "work I already have in flight".
3. **Launching the agent** in the workspace that comes back.
4. **One workspace over several repositories.** herdr's unit is one worktree,
   and coupled work spans more than one.
5. **Herding.** herdr can focus a workspace but does not know which are *yours*
   or which have a live agent.

Anything herdr can already do is herdr's job. Resist adding to this.

## Namespaces

A **namespace** is the unit of work: a named directory holding one checkout per
participating repository, opened as a single herdr workspace with a single agent
over all of them.

```
prefix+o  →  herdr-pick pick
                │
                ├─ candidates: existing namespaces, then cached GitHub repos
                ├─ fzf --multi
                ├─ one namespace selected  →  resume it
                └─ repos marked            →  prompt name
                                           →  clone what's missing
                                           →  sync each clone
                                           →  git worktree add each member
                                           →  open, then start the agent
```

The namespace name is also the **branch name in every member**, so one feature
name identifies the work everywhere it lands.

**A one-repo project is a namespace with one member.** There is no separate
single-repo path, and selecting a single repo behaves exactly as picking a
project always did: choose it, then name the work.

Namespaces exist because the work that needs them is *coupled* — one related
change across fewer than five repos, where the agent has to see every member to
get any of them right. The same change repeated across many repos is a different
problem, better served by many single-repo agents; don't grow namespaces toward
it.

### Disk layout

Three fixed roots under `~/.local/share/herdr-pick`, and nothing user-named at
the top:

```
<root>/
├── clones/<org>/<repo>/    # bare clones, only ever worktree sources
├── ns/<name>/<repo>/       # every checkout, one per namespace member
└── cache/<org>.txt         # one repo per line
```

Keeping orgs and namespace names a level below the roots is what removes the
reserved-name problem instead of creating one: no org, namespace or repo can
shadow a root, so **no walk needs a skip list**. The alternative — orgs sitting
directly at `<root>` — saves a level but shadows any org sharing a root's name.

A clone needs no `.bare` suffix, because a checkout is never its sibling and so
there is nothing to collide with. It stays bare so the worktree source has no
working tree to be committed into, and so herdr cannot open it as a stray "main"
workspace when it creates the parent for a worktree group.

The org is in every clone path deliberately: `giantswarm/cluster-api` and
`kubernetes-sigs/cluster-api` must not collide.

Inside a namespace the org is **dropped** — a member is just `<repo>`, so the
agent's view of its own cwd is a flat, readable list. The price is that two orgs
sharing a repo name cannot both be members, which `CreateNamespace` rejects
rather than silently resolves.

A namespace name is always exactly one path segment — it is a branch name, so
`ValidateBranchName` applies and slashes are rejected.

### Source of truth

The filesystem. `<root>/ns` is walked to find namespaces, and a subdirectory
counts as a member only if it contains a `.git` entry. There is no database and
no state file. The cache is derived data and is safe to delete at any time.

A namespace directory with no members is skipped, which is what keeps a
half-built namespace — a create that failed partway — out of the picker.

### The picker

One keybinding does both verbs, because **what you selected decides which verb
it is**: a namespace can only be resumed, and repositories can only start a new
namespace. So there is nothing extra to confirm.

- one namespace → resume
- one repo, unmarked → a one-member namespace
- several repos, TAB-marked → a namespace over all of them
- a namespace mixed with repos → an error; the two verbs are not combinable

Namespaces lead the list because returning to work already in flight is the
commoner case.

`fzf --multi` rather than asking repo-by-repo until done: marks are visible
inline, unmarking works, and it is one screen and one Enter rather than one
invocation per repository. For `claudebox` plus `claudebox-image` it is one query
and two TABs.

The name is asked for **after** the repositories. That is what lets the branch
check run against them, and it keeps the one-repo case in the order it has always
been.

Picker lines do not round-trip. A `map[string]Candidate` carries the candidate
instead, as `switchLines` does with workspace ids — which frees the line to be
readable rather than parseable, so a namespace can list its members:

```
add-foo  (claudebox, claudebox-image)
JosephSalisbury/claudebox
JosephSalisbury/claudebox-image
```

Typing `claudebox-image` therefore finds both the namespace already containing it
and the repo itself. Namespace and repo lines stay distinguishable without a
sigil: a namespace name never contains a slash and a repo always contains exactly
one.

### The GitHub cache

Configured orgs are fetched with `gh repo list --limit 5000`. That is far too
slow to sit in front of a keypress for a 1,700-repo org, so the picker only ever
reads the cache. When a cache is older than `cache_ttl` the picker spawns a
detached `herdr-pick refresh` and carries on with what it has — stale now, fresh
next time. Orgs refresh concurrently and one failing org does not stop the
others.

The consequence to remember: the very first run on a cold cache lists only
existing namespaces.

### Talking to herdr

Over the socket API (newline-delimited JSON), not the CLI. The socket's method
and parameter names are pinned by a published schema (`herdr api schema`); the
CLI's flags are not.

Methods used: `ping`, `workspace.create`, `workspace.list`, `workspace.focus`,
`pane.list`, `pane.send_input`.

`herdrMinProtocol` is a **floor, not an equality**. herdr bumps its protocol
whenever it adds a method, so asserting equality broke herdr-pick on every herdr
release even though none of the six methods above had changed. A newer herdr is
taken as compatible; only an older one is refused on connect.

The incompatibility that actually matters — herdr changing or dropping one of
those methods — surfaces at the call instead: herdr answers `invalid_request`,
and `Call` reports that as herdr-pick being out of date. **That** is the signal
to fix the call and raise the floor; a bumped protocol number on its own is not.

`schema.json` is a snapshot for offline reference, not a contract. Regenerate it
with `herdr api schema --output schema.json` and diff the methods above to see
whether a herdr release touched anything herdr-pick uses.

**None of herdr's `worktree.*` methods are used.** herdr-pick does its own git and
asks herdr only to open a directory. That is the single most important thing to
know about this client.

### The namespace directory is the workspace

`workspace.create{cwd, label, focus}` opens any directory as a workspace and
returns it with its `root_pane` — so creating a namespace's workspace is two
calls, with no `pane.list` on that path.

`worktree.open` was the obvious-looking alternative and is wrong twice over.
Practically, it refuses a checkout whose clone is not its neighbour:

```
New and open worktree actions start from the repo parent workspace.
  (linked_worktree_source)
```

Under the old layout a checkout sat beside its `.bare`, so herdr could infer the
parent repo from the path; a namespace member's clone is under `clones/`, so it
cannot. But the deeper reason is that a namespace is a *directory of checkouts*,
not a checkout — pointing `worktree.open` at one member would make herdr believe
the workspace were that member's, and `worktree.create` would open one workspace
per member when the entire point is one workspace over all of them.

`label` is set to the namespace name, so `status` and `switch` read `add-foo`.

### Finding a namespace's workspace

A directory-backed workspace has no worktree for herdr to report (`worktree` is
nullable in `WorkspaceInfo`) and `WorkspaceInfo` carries no path of its own. So a
workspace is matched to a namespace by its **pane's working directory** —
`PaneInfo.cwd`, falling back to `foreground_cwd`.

`pane.list` takes an *optional* `workspace_id`, so omitting it returns every pane
in the session: one call locates every workspace on disk. This is still matching
by where something is rather than by a state file — the same principle as the old
`checkout_path` match, just relocated.

It is also what lets opening a namespace focus an existing workspace instead of
creating a second one onto the same checkouts.

### Why not agent.start

`agent.start` looks like the obvious way to launch the agent, but it has no
`pane_id` parameter and its only `split` values are `right` and `down` — so it
always adds a *second* pane alongside the one the workspace already has. One pane
is the requirement.

Instead: `pane.list` the workspace, take the focused (or only) pane, and
`pane.send_input` the agent command with `keys: ["enter"]` — text and Enter in
one call, which is how herdr's own `pane run` submits atomically under bracketed
paste. herdr still tracks the agent, because it detects agents from the
foreground process rather than from registration.

The command is `cd <namespace> && <agent>`, absolute rather than relative, so it
does not depend on where the pane's shell starts. **claudebox mounts exactly one
directory — its cwd** — so the namespace has to *be* the cwd for the agent to see
every member.

### Check all, then act

`CreateNamespace` batches its checks ahead of any mutation, so the predictable
failures — a bad name, a duplicate repo name, a branch already taken — arrive
before anything is written. Cloning and fetching are per-member network
operations and cannot be batched, so the branch check runs after them, once every
clone exists.

An existing branch is **refused, never adopted** (`-b`, never `-B`): silently
reusing old work because the name happened to match is worse than failing, and
`-B` would reset it.

There is deliberately **no rollback**. If a later member fails, the earlier
checkouts stay and the error names the directory to delete — with no state file
recording the intended member list, nothing can tell later that a namespace is
incomplete. `EnsureClone` is idempotent, so re-running is cheap.

### Why the clone is synced before every new member

`SyncClone` runs before every checkout, for two problems that share one cause —
`git clone --bare` is not a normal clone.

**New work must start from current main.** A worktree branches off the clone's
own `HEAD`, frozen at whatever the *first* clone of that repo captured. Left
alone, every later checkout starts from an ever-older main and the merge
conflicts grow with it.

**Members must be able to merge main.** `clone --bare` copies remote heads
straight into `refs/heads/*` and writes no `remote.origin.fetch`, so
`refs/remotes/origin/*` never exists. In a checkout that means `git merge
origin/main` fails on an unknown revision and `git status` has nothing to count
ahead/behind against. `SyncClone` configures the refspec a normal clone would
have had.

Four details make this less obvious than it looks:

- The refspec is *configured* (`remote.origin.fetch`) so the user's own later
  `git fetch` behaves normally, and also *passed explicitly* to our fetch,
  because an explicit refspec overrides the configured one and we need both
  namespaces updated in a single round trip.
- The `refs/heads` half is scoped to the branch `HEAD` points at. A wildcard
  `+refs/heads/*:refs/heads/*` fails on any branch already checked out in a
  worktree, which here is most of them.
- Both halves are forced. A rewritten main — force-push, squashed merge — would
  otherwise be rejected and leave the stale ref. Overwriting is safe here; the
  clone is a worktree source and is never committed into.
- Fresh clones are synced too. They are current commit-wise, so this looks like a
  wasted round trip on the slowest path there is — but `clone --bare` leaves no
  remote-tracking refs, so skipping it would hand back the one repo that cannot
  merge main.

A sync failure is a warning, not an error, like the cache refresh: offline or VPN
down, work started from a stale main still beats no work started.

## Known limitations

- **The agent has no git.** claudebox does not install `git` or `gh`, and a
  member's `.git` file points at a `gitdir:` under `<root>/clones`, outside the
  single mounted directory. The agent reads and edits; the human commits. This
  caps how much of a coupled change the agent can verify for itself — it cannot
  diff what it has changed across members. Accepted deliberately, to get
  experience with the design first. Fixing it means either mounting each member's
  clone at its host path, or making members `git clone --local` copies (hardlinked
  objects, so a real `.git` directory inside the mount) rather than worktrees.
- **No teardown.** Namespaces are removed by hand. There is no `clean`: herdr's
  `worktree.remove` takes a workspace and removes the one checkout backing it, and
  a namespace's workspace is backed by a directory rather than a checkout, so
  there is nothing correct for it to remove. A teardown has to understand every
  member.
- **No adding a member to a live namespace.** Create a new one.
- **No multi-repo brief.** The agent starts bare, with no prompt and no merged
  view of the members' conventions.

## Platform

macOS only. Don't add cross-platform handling for its own sake.

One thing that looks cross-platform but isn't: config and socket paths resolve
via `$XDG_CONFIG_HOME` or `~/.config`, **not** `os.UserConfigDir()`. On macOS
that would return `~/Library/Application Support`, but herdr keeps its own config
and socket under `~/.config`. We have to find herdr's socket, so we follow herdr
rather than the Apple convention.

## Config

`~/.config/herdr-pick/config.yaml`, all keys optional:

```yaml
orgs: [giantswarm, JosephSalisbury]
root: ~/.local/share/herdr-pick
cache_ttl: 6h
agent: [claudebox]
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

Both management commands speak to herdr over the socket and share one safety
rule: they only ever touch workspaces sitting under `<root>/ns`, so they can never
focus your unrelated herdr work. A workspace is matched to herdr-pick by its
pane's working directory, not by a state file.

- **`status`** prints one line per open namespace as `status<TAB>label`, ordered
  by urgency — the non-interactive glance at what the whole fleet of agents is
  doing. Read-only and pipeable (`herdr-pick status | grep blocked`).
- **`switch`** lists namespaces with a live agent and focuses the one you pick
  (`workspace.focus`). "Live" is herdr's own agent status — `working`, `blocked`
  or `idle`. `blocked` (an agent waiting on you) sorts first, then `working`,
  then `idle`, so the picker leads with what most wants attention. `--all` widens
  the list to finished and agent-less namespaces too.

Both lean entirely on herdr's agent-status detection; herdr-pick adds no status
tracking of its own. A status only exists while a workspace is open.

## Commands

- `pick` — the keybound entry point: candidates, fzf, resume or create
- `new <name> <org/repo>...` — create a namespace without the picker
- `open <name>` — resume a namespace without the picker
- `list` — one line per namespace as `name<TAB>member,member`, for piping
- `status` — one line per open namespace with its agent status, for piping
- `switch` — fuzzy-pick a namespace with a live agent and focus it (`--all`)
- `refresh` — refill every org cache
- `ping` — check the herdr socket, independently of the open flow

`new` and `open` are the non-interactive halves of `pick` — the same two verbs
without fzf or a prompt.

## Conventions

- `gofmt` enforced, `golangci-lint` with strict rules.
- TDD. Tests in `_test.go` files alongside the code they test.
- External commands go through `Executor`; the herdr socket goes through `Herdr`.
  Both are interfaces so tests never touch the network or a real process.
  `fakes_test.go` holds the doubles — `fakeExecutor.matches` keys canned replies
  by argv substring, which is how one fake answers several `git` subcommands
  differently.
- Return errors, don't panic. Wrap with context: `fmt.Errorf("doing thing: %w", err)`.
- Shell out to `git` and `gh` rather than using libraries. The user's own config
  handles authentication.
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
- The smallest thing that closes the gap herdr leaves. If herdr grows a feature,
  delete ours — namespaces most of all: they are a herdr concept being prototyped
  here, and the first place this tool's model is not 1:1 with herdr's.
- Namespaces are cheap and disposable.
- Never block the picker on the network.
