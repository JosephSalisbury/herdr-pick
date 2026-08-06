package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeMember lays down a checkout the way a linked worktree looks on disk: a
// .git *file* rather than a directory.
func writeMember(t *testing.T, root, ns, repo string) {
	t.Helper()
	dir := MemberDir(root, ns, repo)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating member: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatalf("writing .git: %v", err)
	}
}

func TestParseRepo(t *testing.T) {
	got, err := ParseRepo("giantswarm/cluster-api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Org != "giantswarm" || got.Repo != "cluster-api" {
		t.Fatalf("got %+v", got)
	}

	for _, bad := range []string{"", "  ", "noslash", "/repo", "org/", "a/b/c", "org/repo@branch"} {
		if _, err := ParseRepo(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

func TestDiscoverNamespaces(t *testing.T) {
	root := t.TempDir()
	writeMember(t, root, "add-foo", "claudebox")
	writeMember(t, root, "add-foo", "claudebox-image")
	writeMember(t, root, "other", "herdr-pick")

	got, err := DiscoverNamespaces(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d namespaces, want 2: %+v", len(got), got)
	}
	if got[0].Name != "add-foo" {
		t.Fatalf("expected add-foo first, got %q", got[0].Name)
	}
	if want := []string{"claudebox", "claudebox-image"}; strings.Join(got[0].Members, ",") != strings.Join(want, ",") {
		t.Fatalf("got members %v, want %v", got[0].Members, want)
	}
	if got[0].Path != NamespaceDir(root, "add-foo") {
		t.Fatalf("got path %q", got[0].Path)
	}
}

// A directory with no checkouts in it is a half-built namespace. Keeping it out
// of discovery is what stops a failed create from reappearing in the picker.
func TestDiscoverNamespacesIgnoresMemberless(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(MemberDir(root, "broken", "not-a-checkout"), 0o755); err != nil {
		t.Fatalf("creating dir: %v", err)
	}
	if err := os.MkdirAll(NamespaceDir(root, "empty"), 0o755); err != nil {
		t.Fatalf("creating dir: %v", err)
	}

	got, err := DiscoverNamespaces(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want none", got)
	}
}

func TestDiscoverNamespacesMissingRootIsEmpty(t *testing.T) {
	got, err := DiscoverNamespaces(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want none", got)
	}
}

func TestWorkspaceAt(t *testing.T) {
	panes := []HerdrPane{
		{PaneID: "p1", WorkspaceID: "w1", Cwd: "/ns/other"},
		{PaneID: "p2", WorkspaceID: "w2", Cwd: "/ns/add-foo/"},
		{PaneID: "p3", WorkspaceID: "w3", ForegroundCwd: "/ns/third"},
	}

	// Paths are compared cleaned, so a trailing separator still matches.
	if got := WorkspaceAt(panes, "/ns/add-foo"); got != "w2" {
		t.Fatalf("got %q, want w2", got)
	}
	// A pane reporting only its foreground process's directory still counts.
	if got := WorkspaceAt(panes, "/ns/third"); got != "w3" {
		t.Fatalf("got %q, want w3", got)
	}
	if got := WorkspaceAt(panes, "/ns/absent"); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
	// A member of a namespace is not the namespace: only an exact match counts,
	// or resuming would focus a workspace sitting inside one.
	if got := WorkspaceAt(panes, "/ns"); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestCreateNamespace(t *testing.T) {
	root := t.TempDir()
	exec := &fakeExecutor{matches: []fakeMatch{
		// A bare clone's HEAD, for SyncClone.
		{contains: "symbolic-ref", out: "main"},
		// No branch of this name exists anywhere yet.
		{contains: "branch --list", out: ""},
	}}

	members := []RepoRef{{"JosephSalisbury", "claudebox"}, {"JosephSalisbury", "claudebox-image"}}
	ns, err := CreateNamespace(context.Background(), exec, root, "add-foo", members)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ns.Name != "add-foo" || ns.Path != NamespaceDir(root, "add-foo") {
		t.Fatalf("got %+v", ns)
	}
	if strings.Join(ns.Members, ",") != "claudebox,claudebox-image" {
		t.Fatalf("got members %v", ns.Members)
	}

	// Every member is checked out from its own clone, on the namespace's branch,
	// into the namespace directory.
	for _, repo := range []string{"claudebox", "claudebox-image"} {
		clone := CloneDir(root, "JosephSalisbury", repo)
		if !exec.ran("git", "-C", clone, "worktree", "add", "-b", "add-foo", MemberDir(root, "add-foo", repo)) {
			t.Fatalf("missing worktree add for %s in %v", repo, exec.calls)
		}
	}
	if _, err := os.Stat(NamespaceDir(root, "add-foo")); err != nil {
		t.Fatalf("namespace directory not created: %v", err)
	}
}

// One member is enough: a single-repo project is just a namespace of one, and
// is the replacement for the old per-repo flow.
func TestCreateNamespaceWithOneMember(t *testing.T) {
	root := t.TempDir()
	exec := &fakeExecutor{matches: []fakeMatch{
		{contains: "symbolic-ref", out: "main"},
		{contains: "branch --list", out: ""},
	}}

	ns, err := CreateNamespace(context.Background(), exec, root, "solo",
		[]RepoRef{{"JosephSalisbury", "claudebox"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ns.Members) != 1 || ns.Members[0] != "claudebox" {
		t.Fatalf("got %+v", ns)
	}
}

// The org is dropped from a member's directory name, so two orgs sharing a repo
// name would land on the same path. That is rejected rather than resolved.
func TestCreateNamespaceRejectsDuplicateRepoNames(t *testing.T) {
	root := t.TempDir()
	exec := &fakeExecutor{}

	_, err := CreateNamespace(context.Background(), exec, root, "add-foo",
		[]RepoRef{{"giantswarm", "cluster-api"}, {"kubernetes-sigs", "cluster-api"}})
	if err == nil {
		t.Fatal("expected an error")
	}
	requireContains(t, err.Error(), "cluster-api")
	if len(exec.calls) != 0 {
		t.Fatalf("nothing should have run, got %v", exec.calls)
	}
}

// An existing branch is refused rather than adopted: silently reusing old work
// because the name happened to match is worse than failing.
func TestCreateNamespaceRejectsExistingBranch(t *testing.T) {
	root := t.TempDir()
	exec := &fakeExecutor{matches: []fakeMatch{
		{contains: "symbolic-ref", out: "main"},
		// The second repo already has the branch.
		{contains: "claudebox-image branch --list", out: "  add-foo"},
		{contains: "branch --list", out: ""},
	}}

	_, err := CreateNamespace(context.Background(), exec, root, "add-foo",
		[]RepoRef{{"JosephSalisbury", "claudebox"}, {"JosephSalisbury", "claudebox-image"}})
	if err == nil {
		t.Fatal("expected an error")
	}
	requireContains(t, err.Error(), "claudebox-image")
	requireContains(t, err.Error(), "add-foo")

	// Checked before any worktree is added, so a rejected namespace leaves
	// nothing behind — not even the member whose branch was free.
	if exec.ran("git", "-C", CloneDir(root, "JosephSalisbury", "claudebox"), "worktree", "add") {
		t.Fatalf("a worktree was added despite the failure: %v", exec.calls)
	}
}

func TestCreateNamespaceRejectsBadNameAndExistingDir(t *testing.T) {
	root := t.TempDir()

	if _, err := CreateNamespace(context.Background(), &fakeExecutor{}, root, "has/slash",
		[]RepoRef{{"o", "p"}}); err == nil {
		t.Fatal("expected an error for a name that is not a valid branch")
	}
	if _, err := CreateNamespace(context.Background(), &fakeExecutor{}, root, "add-foo",
		nil); err == nil {
		t.Fatal("expected an error for a namespace with no members")
	}

	writeMember(t, root, "taken", "claudebox")
	if _, err := CreateNamespace(context.Background(), &fakeExecutor{}, root, "taken",
		[]RepoRef{{"o", "p"}}); err == nil {
		t.Fatal("expected an error for an existing namespace")
	}
}

func TestOpenNamespace(t *testing.T) {
	root := t.TempDir()
	writeMember(t, root, "add-foo", "claudebox")
	writeMember(t, root, "add-foo", "claudebox-image")

	ns, err := LoadNamespace(root, "add-foo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	herdr := &fakeHerdr{results: map[string]string{
		"pane.list":        `{"panes":[]}`,
		"workspace.create": `{"workspace":{"workspace_id":"ws1"},"root_pane":{"pane_id":"p1","workspace_id":"ws1","tab_id":"tab1"}}`,
		"layout.apply":     appliedLayout("p1", "status", "shell"),
	}}
	cfg := Config{Agent: []string{"claudebox"}}

	if err := OpenNamespace(context.Background(), herdr, cfg, ns); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The namespace directory is the workspace — it is what claudebox mounts —
	// and it is labelled with the namespace name.
	params := herdr.paramsFor("workspace.create")
	if got := params["cwd"]; got != ns.Path {
		t.Fatalf("created workspace at %v, want %v", got, ns.Path)
	}
	if got := params["label"]; got != "add-foo" {
		t.Fatalf("got label %v", got)
	}

	if want := "cd '" + ns.Path + "' && claudebox"; herdr.sentInput("p1") != want {
		t.Fatalf("got %q, want %q", herdr.sentInput("p1"), want)
	}

	// The layout goes to the agent pane's own tab. Sent the workspace instead,
	// herdr opens a second tab and the workspace ends up split across two: the
	// agent alone in one, the new panes empty in the other.
	layout := herdr.paramsFor("layout.apply")
	if got, _ := layout["tab_id"].(string); got != "tab1" {
		t.Fatalf("laid out tab %q, want tab1", got)
	}
	if _, ok := layout["workspace_id"]; ok {
		t.Fatalf("workspace_id must not be sent alongside tab_id: %v", layout)
	}

	// The status pane watches every member, from the namespace directory.
	got := herdr.sentInput("status")
	requireContains(t, got, "cd '"+ns.Path+"' && ")
	requireContains(t, got, "watch ")
	requireContains(t, got, "status --short --branch")
	// The shell pane is left as a shell, sitting in the same directory.
	if want := "cd '" + ns.Path + "'"; herdr.sentInput("shell") != want {
		t.Fatalf("got %q, want %q", herdr.sentInput("shell"), want)
	}

	// The agent's pane is the one the human lands in, not whichever pane herdr
	// left focused after reshaping the workspace.
	if id, _ := herdr.paramsFor("pane.focus")["pane_id"].(string); id != "p1" {
		t.Fatalf("focused %q, want p1", id)
	}

	// None of herdr's worktree methods are used: members are checked out with
	// git, and a namespace is a directory of checkouts rather than one checkout.
	for _, method := range []string{"worktree.open", "worktree.create", "worktree.remove"} {
		if herdr.called(method) {
			t.Fatalf("%s should not be called", method)
		}
	}
}

// appliedLayout is herdr's reply to layout.apply: the tree it was sent, with a
// pane id filled in on every node.
func appliedLayout(agent, status, shell string) string {
	return `{"layout":{"root":{"type":"split","direction":"right","ratio":0.5,
		"first":{"type":"pane","pane_id":"` + agent + `"},
		"second":{"type":"split","direction":"down","ratio":0.25,
			"first":{"type":"pane","pane_id":"` + status + `"},
			"second":{"type":"pane","pane_id":"` + shell + `"}}}}}`
}

// The layout herdr is asked for: the agent keeps the pane it already has, and
// the two panes beside it open in the namespace directory.
func TestNamespaceLayout(t *testing.T) {
	root := namespaceLayout("p1", "/ns/add-foo")

	if root.Type != layoutSplit || root.Direction != "right" || root.Ratio != 0.5 {
		t.Fatalf("got %+v", root)
	}
	// Naming the existing pane is what keeps the agent's pane rather than
	// replacing it, and it must be the left-hand child.
	if root.First.PaneID != "p1" || root.First.Cwd != "" {
		t.Fatalf("agent pane is %+v", root.First)
	}

	right := root.Second
	if right.Type != layoutSplit || right.Direction != "down" {
		t.Fatalf("got %+v", right)
	}
	// The upper child is the status pane, and a quarter of the column's height.
	if right.Ratio != 0.25 {
		t.Fatalf("got status ratio %v, want 0.25", right.Ratio)
	}
	for _, pane := range []*LayoutNode{right.First, right.Second} {
		if pane.Type != layoutPane || pane.Cwd != "/ns/add-foo" || pane.PaneID != "" {
			t.Fatalf("got %+v", pane)
		}
	}
}

// A herdr that answers layout.apply with something else shaped must not have
// its panes guessed at.
func TestLayoutPanesRejectsAnUnexpectedShape(t *testing.T) {
	for _, applied := range []LayoutNode{
		{Type: layoutPane, PaneID: "p1"},
		{Type: layoutSplit, First: &LayoutNode{Type: layoutPane}, Second: &LayoutNode{Type: layoutPane}},
		{Type: layoutSplit, First: &LayoutNode{Type: layoutPane, PaneID: "p1"}, Second: &LayoutNode{
			First:  &LayoutNode{Type: layoutPane},
			Second: &LayoutNode{Type: layoutPane, PaneID: "p3"},
		}},
	} {
		if _, _, _, err := layoutPanes(applied); err == nil {
			t.Fatalf("expected an error for %+v", applied)
		}
	}
}

// herdr is free to build a pane of its own where the agent's was, so the id the
// request named is not the id to type the agent into. Believing otherwise left
// the workspace correctly laid out and completely empty.
func TestOpenNamespaceStartsTheAgentInThePaneTheLayoutReturned(t *testing.T) {
	root := t.TempDir()
	writeMember(t, root, "add-foo", "claudebox")

	ns, err := LoadNamespace(root, "add-foo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	herdr := &fakeHerdr{results: map[string]string{
		"pane.list":        `{"panes":[]}`,
		"workspace.create": `{"workspace":{"workspace_id":"ws1"},"root_pane":{"pane_id":"p1","tab_id":"tab1"}}`,
		// The pane herdr kept for the agent is not the one it was handed.
		"layout.apply": appliedLayout("p9", "status", "shell"),
	}}

	if err := OpenNamespace(context.Background(), herdr, Config{Agent: []string{"claudebox"}}, ns); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The request still names the pane the workspace came with — that is what
	// asks herdr to reuse it — but nothing afterwards trusts that it did.
	if got, _ := herdr.paramsFor("layout.apply")["root"].(map[string]any); got == nil {
		t.Fatal("no layout applied")
	} else if first, _ := got["first"].(map[string]any); first["pane_id"] != "p1" {
		t.Fatalf("asked to lay out around %v, want p1", first["pane_id"])
	}

	requireContains(t, herdr.sentInput("p9"), "claudebox")
	if herdr.sentInput("p1") != "" {
		t.Fatalf("agent typed into the stale pane p1: %q", herdr.sentInput("p1"))
	}
	if id, _ := herdr.paramsFor("pane.focus")["pane_id"].(string); id != "p9" {
		t.Fatalf("focused %q, want p9", id)
	}
}

// The extra panes are a convenience; the agent is the point. An older herdr
// that cannot apply a layout still gets the namespace open.
func TestOpenNamespaceStartsTheAgentWhenTheLayoutFails(t *testing.T) {
	root := t.TempDir()
	writeMember(t, root, "add-foo", "claudebox")

	ns, err := LoadNamespace(root, "add-foo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	herdr := &fakeHerdr{results: map[string]string{
		"pane.list":        `{"panes":[]}`,
		"workspace.create": `{"workspace":{"workspace_id":"ws1"},"root_pane":{"pane_id":"p1","tab_id":"tab1"}}`,
		// No layout.apply result: herdr answers with an empty layout.
	}}

	if err := OpenNamespace(context.Background(), herdr, Config{Agent: []string{"claudebox"}}, ns); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "cd '" + ns.Path + "' && claudebox"; herdr.sentInput("p1") != want {
		t.Fatalf("got %q, want %q", herdr.sentInput("p1"), want)
	}
}

// Reopening a namespace that already has a workspace focuses it rather than
// opening a second one onto the same checkouts.
func TestOpenNamespaceFocusesAnAlreadyOpenWorkspace(t *testing.T) {
	root := t.TempDir()
	writeMember(t, root, "add-foo", "claudebox")

	ns, err := LoadNamespace(root, "add-foo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	herdr := &fakeHerdr{results: map[string]string{
		"pane.list": `{"panes":[{"pane_id":"p1","workspace_id":"ws9","cwd":"` + ns.Path + `"}]}`,
	}}

	if err := OpenNamespace(context.Background(), herdr, Config{}, ns); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, _ := herdr.paramsFor("workspace.focus")["workspace_id"].(string); got != "ws9" {
		t.Fatalf("focused %q, want ws9", got)
	}
	if herdr.called("workspace.create") {
		t.Fatal("expected the existing workspace to be focused, not a second one created")
	}
	// No agent is started: one is already running in there.
	if herdr.called("pane.send_input") {
		t.Fatal("expected no agent launch into an already-open workspace")
	}
}

// workspace.create should return its root pane; if it does not, fall back to
// asking rather than failing.
func TestOpenNamespaceFallsBackToPaneList(t *testing.T) {
	root := t.TempDir()
	writeMember(t, root, "add-foo", "claudebox")

	ns, err := LoadNamespace(root, "add-foo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	herdr := &fakeHerdr{results: map[string]string{
		"pane.list":        `{"panes":[{"pane_id":"p7","workspace_id":"ws1","tab_id":"tab7","focused":true}]}`,
		"workspace.create": `{"workspace":{"workspace_id":"ws1"}}`,
		"layout.apply":     appliedLayout("p7", "status", "shell"),
	}}

	if err := OpenNamespace(context.Background(), herdr, Config{Agent: []string{"claudebox"}}, ns); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The pane found by asking is the one the layout is built around and the
	// agent is started in — and its tab is the one laid out, so the fallback
	// path lands in the same single tab as the direct one.
	layout := herdr.paramsFor("layout.apply")
	if got, _ := layout["tab_id"].(string); got != "tab7" {
		t.Fatalf("laid out tab %q, want tab7", got)
	}
	if got, _ := layout["root"].(map[string]any); got == nil {
		t.Fatal("no layout applied")
	} else if first, _ := got["first"].(map[string]any); first["pane_id"] != "p7" {
		t.Fatalf("laid out around pane %v, want p7", first["pane_id"])
	}
	requireContains(t, herdr.sentInput("p7"), "claudebox")
}

// Without a tab there is nowhere safe to put the layout: applying it to the
// workspace would open a second tab. The agent still starts, alone.
func TestOpenNamespaceSkipsTheLayoutWithoutATab(t *testing.T) {
	root := t.TempDir()
	writeMember(t, root, "add-foo", "claudebox")

	ns, err := LoadNamespace(root, "add-foo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	herdr := &fakeHerdr{results: map[string]string{
		"pane.list":        `{"panes":[]}`,
		"workspace.create": `{"workspace":{"workspace_id":"ws1"},"root_pane":{"pane_id":"p1"}}`,
	}}

	if err := OpenNamespace(context.Background(), herdr, Config{Agent: []string{"claudebox"}}, ns); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if herdr.called("layout.apply") {
		t.Fatalf("expected no layout without a tab to apply it to, got %v", herdr.methods)
	}
	requireContains(t, herdr.sentInput("p1"), "claudebox")
}

func TestAgentCommandQuotesTheNamespacePath(t *testing.T) {
	got := agentCommand([]string{"claudebox"}, "/tmp/it's here")
	if want := `cd '/tmp/it'\''s here' && claudebox`; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	// An absolute path, so the command does not depend on the pane's starting
	// directory.
	if !strings.Contains(agentCommand([]string{"claude", "--flag"}, "/ns/x"), "&& claude --flag") {
		t.Fatalf("agent argv not preserved: %q", agentCommand([]string{"claude", "--flag"}, "/ns/x"))
	}
}

func TestLoadNamespaceMissing(t *testing.T) {
	if _, err := LoadNamespace(t.TempDir(), "absent"); err == nil {
		t.Fatal("expected an error")
	}
}
