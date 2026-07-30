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
		"workspace.create": `{"workspace":{"workspace_id":"ws1"},"root_pane":{"pane_id":"p1","workspace_id":"ws1"}}`,
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

	want := "cd '" + ns.Path + "' && claudebox"
	if got := herdr.paramsFor("pane.send_input")["text"]; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	// None of herdr's worktree methods are used: members are checked out with
	// git, and a namespace is a directory of checkouts rather than one checkout.
	for _, method := range []string{"worktree.open", "worktree.create", "worktree.remove"} {
		if herdr.called(method) {
			t.Fatalf("%s should not be called", method)
		}
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
		"pane.list":        `{"panes":[{"pane_id":"p7","workspace_id":"ws1","focused":true}]}`,
		"workspace.create": `{"workspace":{"workspace_id":"ws1"}}`,
	}}

	if err := OpenNamespace(context.Background(), herdr, Config{Agent: []string{"claudebox"}}, ns); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, _ := herdr.paramsFor("pane.send_input")["pane_id"].(string); got != "p7" {
		t.Fatalf("sent to pane %q, want p7", got)
	}
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
