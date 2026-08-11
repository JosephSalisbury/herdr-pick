package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Namespace is the unit of work: a named directory holding one checkout per
// participating repository, opened as a single herdr workspace with a single
// agent over all of them.
//
// A one-repo project is a namespace with one member. There is no separate
// single-repo path.
type Namespace struct {
	// Name is the directory name and also the branch name in every member.
	Name string
	// Path is the namespace directory, which is the agent's working directory
	// and the one path claudebox mounts.
	Path string
	// Members are the repo names checked out inside Path, sorted.
	Members []string
}

// RepoRef names a repository to check out as a namespace member.
type RepoRef struct {
	Org  string
	Repo string
}

// String renders the reference as org/repo.
func (r RepoRef) String() string {
	return r.Org + "/" + r.Repo
}

// ParseRepo parses an "org/repo" reference. It replaces the old picker grammar:
// there is no "@branch" form any more, because a checkout's branch is its
// namespace's name.
func ParseRepo(s string) (RepoRef, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return RepoRef{}, errors.New("empty repository reference")
	}

	org, repo, ok := strings.Cut(s, "/")
	if !ok || org == "" || repo == "" || strings.ContainsAny(repo, "/@") {
		return RepoRef{}, fmt.Errorf("repository reference %q must be org/repo", s)
	}
	return RepoRef{Org: org, Repo: repo}, nil
}

// NamespaceMembers returns the checkouts inside a namespace directory, sorted.
//
// A subdirectory counts only if it holds a .git entry, which for a linked
// worktree is a file rather than a directory.
func NamespaceMembers(nsPath string) ([]string, error) {
	names, err := readDirNames(nsPath)
	if err != nil {
		return nil, err
	}

	var members []string
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(nsPath, name, ".git")); err != nil {
			continue
		}
		members = append(members, name)
	}
	sortFold(members)
	return members, nil
}

// DiscoverNamespaces returns every namespace on disk, sorted. The filesystem is
// the source of truth; herdr does not need to be running and there is no state
// file.
//
// A directory with no members is skipped. That is what keeps a half-built
// namespace — a create that failed partway — out of the picker.
func DiscoverNamespaces(root string) ([]Namespace, error) {
	nsRootDir := NamespaceRoot(root)
	names, err := readDirNames(nsRootDir)
	if err != nil {
		return nil, err
	}
	sortFold(names)

	var out []Namespace
	for _, name := range names {
		path := filepath.Join(nsRootDir, name)
		members, err := NamespaceMembers(path)
		if err != nil {
			return nil, err
		}
		if len(members) == 0 {
			continue
		}
		out = append(out, Namespace{Name: name, Path: path, Members: members})
	}
	return out, nil
}

// LoadNamespace reads one namespace by name.
func LoadNamespace(root, name string) (Namespace, error) {
	path := NamespaceDir(root, name)
	members, err := NamespaceMembers(path)
	if err != nil {
		return Namespace{}, err
	}
	if len(members) == 0 {
		return Namespace{}, fmt.Errorf("no namespace %q in %s", name, NamespaceRoot(root))
	}
	return Namespace{Name: name, Path: path, Members: members}, nil
}

// CreateNamespace checks out every member on a branch named after the namespace.
//
// Checks are batched ahead of any mutation, so the predictable failures — a bad
// name, a duplicate repo name, a branch already taken — arrive before anything
// is written to disk. Cloning and fetching are per-member network operations and
// cannot be batched, so the branch check runs after them, once every clone
// exists.
//
// There is no rollback. If a later member fails, the earlier checkouts stay and
// the error names the directory to delete: with no state file recording the
// intended member list, nothing can tell later that a namespace is incomplete.
func CreateNamespace(ctx context.Context, executor Executor, root, name string, members []RepoRef) (Namespace, error) {
	if err := ValidateName(name); err != nil {
		return Namespace{}, fmt.Errorf("namespace name: %w", err)
	}
	if len(members) == 0 {
		return Namespace{}, errors.New("a namespace needs at least one repository")
	}
	if err := checkDistinctRepos(members); err != nil {
		return Namespace{}, err
	}

	nsPath := NamespaceDir(root, name)
	if _, err := os.Stat(nsPath); err == nil {
		return Namespace{}, fmt.Errorf("namespace %q already exists at %s", name, nsPath)
	}

	for _, m := range members {
		clone := CloneDir(root, m.Org, m.Repo)
		if err := EnsureClone(ctx, executor, m.Org, m.Repo, clone); err != nil {
			return Namespace{}, err
		}
		// A sync failure is a warning, not an error: offline or VPN down, work
		// started from a stale main still beats no work started.
		if err := SyncClone(ctx, executor, clone); err != nil {
			fmt.Fprintf(os.Stderr, "warning: %v\n", err)
		}
	}

	for _, m := range members {
		exists, err := branchExists(ctx, executor, CloneDir(root, m.Org, m.Repo), name)
		if err != nil {
			return Namespace{}, err
		}
		if exists {
			return Namespace{}, fmt.Errorf("%s already has a branch %q: pick another namespace name", m, name)
		}
	}

	if err := os.MkdirAll(nsPath, 0o755); err != nil {
		return Namespace{}, fmt.Errorf("creating namespace %s: %w", nsPath, err)
	}

	var names []string
	for _, m := range members {
		path := MemberDir(root, name, m.Repo)
		if err := addWorktree(ctx, executor, CloneDir(root, m.Org, m.Repo), name, path); err != nil {
			return Namespace{}, fmt.Errorf("%w\nnamespace %s is incomplete: remove it and retry", err, nsPath)
		}
		names = append(names, m.Repo)
	}

	sortFold(names)
	return Namespace{Name: name, Path: nsPath, Members: names}, nil
}

// OpenNamespace opens the namespace as a herdr workspace and starts the agent in
// it, or focuses the workspace if one is already open on it.
//
// The namespace *directory* is the workspace, via workspace.create — herdr's
// worktree.* methods are not used at all. A namespace is a directory of
// checkouts rather than one checkout, so worktree.open has nothing correct to
// open: it refuses a checkout whose clone is not its neighbour, and pointing it
// at one member would make herdr believe the workspace were that member's.
//
// The directory is also what claudebox mounts, so it has to be the agent's cwd.
func OpenNamespace(ctx context.Context, h Herdr, cfg Config, ns Namespace) error {
	return openWorkspace(ctx, h, cfg, ns.Path, ns.Name, layoutWorkspace)
}

// layoutFunc shapes a freshly created workspace and returns the pane the agent
// is to start in. It is what a namespace and a temp directory differ by, and
// the only thing.
type layoutFunc func(ctx context.Context, h Herdr, pane HerdrPane, cwd string) (string, error)

// openWorkspace opens a directory as a herdr workspace and starts the agent in
// it, or focuses the workspace if one is already open on that directory.
func openWorkspace(ctx context.Context, h Herdr, cfg Config, path, label string, layout layoutFunc) error {
	// A workspace is usually opened after the last one on it was closed, but
	// focusing an open one is nearly free here and beats opening a second
	// workspace onto the same directory.
	panes, err := PaneList(ctx, h, "")
	if err != nil {
		return err
	}
	if id := WorkspaceAt(panes, path); id != "" {
		return WorkspaceFocus(ctx, h, id)
	}

	workspace, root, err := WorkspaceCreate(ctx, h, path, label)
	if err != nil {
		return err
	}

	pane := root
	if pane.PaneID == "" {
		// workspace.create should hand back its root pane; ask if it did not.
		created, err := PaneList(ctx, h, workspace.WorkspaceID)
		if err != nil {
			return err
		}
		if pane, err = RootPane(created); err != nil {
			return fmt.Errorf("finding pane for %s: %w", label, err)
		}
	}

	// The panes beside the agent's are a convenience; the agent is the point. A
	// herdr too old for layout.apply should still open the workspace, so this is
	// a warning rather than an error, like a clone that could not be synced —
	// and the agent then starts in the one pane the workspace came with.
	agent := pane.PaneID
	if laid, err := layout(ctx, h, pane, path); err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
	} else {
		agent = laid
	}

	if err := RunInPane(ctx, h, agent, agentCommand(cfg.AgentArgv(), path)); err != nil {
		return fmt.Errorf("starting agent in %s: %w", label, err)
	}
	return nil
}

// Shares of the workspace given to each pane. The agent takes the left half;
// the status pane takes a quarter of the right column's height, which is a
// quarter of the screen's, and the shell gets the rest.
const (
	agentPaneRatio  = 0.5
	statusPaneRatio = 0.25
)

// statusCommand is the line the status pane runs: the branch and the working
// tree of every member, under its own heading, refreshed every couple of
// seconds.
//
// Each member is visited in a subshell rather than with a matching `cd -`, so a
// member whose git call fails does not leave the loop somewhere else and report
// the wrong repository from then on. `watch -c` and `color.status=always` are a
// pair: git suppresses colour when its output is not a terminal, which under
// watch it never is, and watch discards the escapes unless told to keep them.
const statusCommand = `watch -c -n2 'for d in */; do (cd "$d" && printf "\033[1;36m%s\033[0m\n" "$d" && git -c color.status=always status --short --branch); done'`

// layoutWorkspace gives a namespace's workspace the shape it is worked in:
//
//	┌───────────┬───────────┐
//	│           │  status   │  a quarter of the height
//	│   agent   ├───────────┤
//	│           │   shell   │
//	└───────────┴───────────┘
//
// The two right-hand panes open in the namespace directory, so the human's half
// of the workspace sees the same members the agent does. It returns the pane the
// agent is to start in.
//
// The layout goes to the agent pane's *tab*. Applying it to the workspace
// instead puts it in a second tab, which is a workspace split across two tabs:
// the agent alone in one and three empty panes in the other.
//
// **The pane ids come out of the reply, including the agent's.** The request
// names the existing pane's id, but herdr does not promise to keep it —
// reshaping a tab can hand the agent's corner to a pane of its own making, and
// the old id then belongs to nothing. Typing the agent command into the id we
// sent is how the workspace ended up correctly laid out and completely empty.
func layoutWorkspace(ctx context.Context, h Herdr, pane HerdrPane, cwd string) (string, error) {
	if pane.TabID == "" {
		return "", fmt.Errorf("herdr reported no tab for pane %s: leaving %s as one pane", pane.PaneID, cwd)
	}

	applied, err := LayoutApply(ctx, h, pane.TabID, namespaceLayout(pane.PaneID, cwd))
	if err != nil {
		return "", fmt.Errorf("laying out the workspace for %s: %w", cwd, err)
	}

	agent, status, shell, err := layoutPanes(applied)
	if err != nil {
		return "", err
	}

	// Both are cd'd as well as opened in the namespace, for the same reason
	// agentCommand is: a shell rc that changes directory on startup would
	// otherwise leave them somewhere else, and git status would report on
	// whatever happened to be there.
	if err := RunInPane(ctx, h, status, cdCommand(cwd)+" && "+statusCommand); err != nil {
		return "", fmt.Errorf("starting the status pane: %w", err)
	}
	if err := RunInPane(ctx, h, shell, cdCommand(cwd)); err != nil {
		return "", fmt.Errorf("preparing the shell pane: %w", err)
	}

	// Applying a layout leaves focus wherever herdr puts it, and the pane worth
	// typing into is the agent's.
	if err := PaneFocus(ctx, h, agent); err != nil {
		return "", fmt.Errorf("focusing the agent pane: %w", err)
	}
	return agent, nil
}

// namespaceLayout builds the tree layoutWorkspace applies.
func namespaceLayout(agentPane, cwd string) LayoutNode {
	return LayoutNode{
		Type:      layoutSplit,
		Direction: "right",
		Ratio:     agentPaneRatio,
		First:     &LayoutNode{Type: layoutPane, PaneID: agentPane},
		Second: &LayoutNode{
			Type:      layoutSplit,
			Direction: "down",
			Ratio:     statusPaneRatio,
			First:     &LayoutNode{Type: layoutPane, Cwd: cwd},
			Second:    &LayoutNode{Type: layoutPane, Cwd: cwd},
		},
	}
}

// layoutPanes reads all three pane ids out of the tree layout.apply returned.
// herdr answers with the shape it was sent, so they are found by position —
// there is nothing on a pane to match them by.
//
// The agent's comes from the reply like the others, rather than being carried
// over from the request: herdr is free to have built a new pane there, and the
// id we asked for would then name nothing.
func layoutPanes(applied LayoutNode) (agent, status, shell string, err error) {
	right := applied.Second
	if applied.First == nil || right == nil || right.First == nil || right.Second == nil {
		return "", "", "", errors.New("herdr applied a layout of a different shape")
	}

	agent, status, shell = applied.First.PaneID, right.First.PaneID, right.Second.PaneID
	if agent == "" || status == "" || shell == "" {
		return "", "", "", errors.New("herdr's layout left a pane unnamed")
	}
	return agent, status, shell, nil
}

// WorkspaceAt returns the id of the workspace whose pane sits in dir, or an empty
// string if none does.
func WorkspaceAt(panes []HerdrPane, dir string) string {
	want := filepath.Clean(dir)
	for _, p := range panes {
		if p.Dir() != "" && filepath.Clean(p.Dir()) == want {
			return p.WorkspaceID
		}
	}
	return ""
}

// agentCommand builds the shell line typed into the workspace pane: change to
// the namespace directory, then run the agent there.
//
// workspace.create already starts the pane in that directory, so the cd is
// belt and braces — but a shell rc that changes directory on startup would
// otherwise silently launch the agent somewhere else, and the failure would look
// like a herdr-pick bug. Absolute, so it does not depend on where the shell
// started.
func agentCommand(argv []string, cwd string) string {
	return cdCommand(cwd) + " && " + strings.Join(argv, " ")
}

// cdCommand changes to a directory. Absolute and quoted, so it survives a path
// with a space in it and does not depend on where the pane's shell started.
func cdCommand(cwd string) string {
	return "cd " + shellQuote(cwd)
}

// shellQuote single-quotes a string so it survives as one argument when typed
// into a shell, escaping any embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// checkDistinctRepos rejects members that would share a directory. A member is
// named by repo alone, so two orgs with the same repo name collide.
func checkDistinctRepos(members []RepoRef) error {
	seen := make(map[string]RepoRef, len(members))
	for _, m := range members {
		if first, ok := seen[m.Repo]; ok {
			return fmt.Errorf("%s and %s would both be checked out as %q: pick one", first, m, m.Repo)
		}
		seen[m.Repo] = m
	}
	return nil
}

// branchExists reports whether a clone already has a branch of this name.
//
// `branch --list` rather than `show-ref --verify`, because it exits zero either
// way: absence is empty output rather than an error, so a genuinely broken repo
// stays distinguishable from a free branch name.
func branchExists(ctx context.Context, executor Executor, clone, branch string) (bool, error) {
	out, err := executor.Run(ctx, "git", "-C", clone, "branch", "--list", branch)
	if err != nil {
		return false, fmt.Errorf("checking for branch %s in %s: %w", branch, clone, err)
	}
	return strings.TrimSpace(out) != "", nil
}

// addWorktree checks a new branch out of a clone into path.
//
// -b, never -B: an existing branch is refused by the caller's pre-flight, and
// -B would reset it and discard whatever was on it.
func addWorktree(ctx context.Context, executor Executor, clone, branch, path string) error {
	if _, err := executor.Run(ctx, "git", "-C", clone, "worktree", "add", "-b", branch, path); err != nil {
		return fmt.Errorf("checking out %s in %s: %w", branch, path, err)
	}
	return nil
}

// readDirNames returns the subdirectory names of dir, treating a missing
// directory as empty.
func readDirNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}
