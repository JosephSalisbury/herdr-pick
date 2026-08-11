package main

import (
	"context"
	"os"
	"testing"
)

func TestCreateTemp(t *testing.T) {
	root := t.TempDir()

	got, err := CreateTemp(root, "trying-something")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "trying-something" || got.Path != TempDir(root, "trying-something") {
		t.Fatalf("got %+v", got)
	}
	if info, err := os.Stat(got.Path); err != nil || !info.IsDir() {
		t.Fatalf("temp directory not created: %v", err)
	}

	// Nothing is checked out and nothing is cloned: an empty directory is the
	// whole of it.
	entries, err := os.ReadDir(got.Path)
	if err != nil || len(entries) != 0 {
		t.Fatalf("expected an empty directory, got %v (%v)", entries, err)
	}
}

func TestCreateTempRejectsBadNameAndExistingDir(t *testing.T) {
	root := t.TempDir()

	for _, bad := range []string{"", "has/slash", "-leading", ".hidden", "two..dots"} {
		if _, err := CreateTemp(root, bad); err == nil {
			t.Fatalf("expected an error for %q", bad)
		}
	}

	if _, err := CreateTemp(root, "taken"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Refused rather than reused: the caller decides whether it meant to resume.
	if _, err := CreateTemp(root, "taken"); err == nil {
		t.Fatal("expected an error for an existing temp directory")
	}
}

// Unlike a namespace there is nothing to filter on — an empty directory is
// exactly what a temp directory is.
func TestDiscoverTemps(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"Zebra", "add-foo", "empty"} {
		if _, err := CreateTemp(root, name); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	got, err := DiscoverTemps(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d temp directories, want 3: %+v", len(got), got)
	}
	// Sorted case-insensitively, as repository lists are.
	if got[0].Name != "add-foo" || got[1].Name != "empty" || got[2].Name != "Zebra" {
		t.Fatalf("wrong order: %+v", got)
	}
	if got[0].Path != TempDir(root, "add-foo") {
		t.Fatalf("got path %q", got[0].Path)
	}
}

func TestDiscoverTempsMissingRootIsEmpty(t *testing.T) {
	got, err := DiscoverTemps(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want none", got)
	}
}

func TestLoadTemp(t *testing.T) {
	root := t.TempDir()
	if _, err := CreateTemp(root, "trying-something"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := LoadTemp(root, "trying-something")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Path != TempDir(root, "trying-something") {
		t.Fatalf("got %+v", got)
	}
	if _, err := LoadTemp(root, "absent"); err == nil {
		t.Fatal("expected an error")
	}
}

// appliedTempLayout is herdr's reply to layout.apply for a temp directory: the
// two-pane tree it was sent, with a pane id filled in on both nodes.
func appliedTempLayout(agent, shell string) string {
	return `{"layout":{"root":{"type":"split","direction":"right","ratio":0.5,
		"first":{"type":"pane","pane_id":"` + agent + `"},
		"second":{"type":"pane","pane_id":"` + shell + `"}}}}`
}

func TestOpenTemp(t *testing.T) {
	root := t.TempDir()
	temp, err := CreateTemp(root, "trying-something")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	herdr := &fakeHerdr{results: map[string]string{
		"pane.list":        `{"panes":[]}`,
		"workspace.create": `{"workspace":{"workspace_id":"ws1"},"root_pane":{"pane_id":"p1","workspace_id":"ws1","tab_id":"tab1"}}`,
		"layout.apply":     appliedTempLayout("p1", "shell"),
	}}

	if err := OpenTemp(context.Background(), herdr, Config{Agent: []string{"claudebox"}}, temp); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The directory is the workspace, labelled with its name so status and
	// switch read it like any namespace.
	params := herdr.paramsFor("workspace.create")
	if got := params["cwd"]; got != temp.Path {
		t.Fatalf("created workspace at %v, want %v", got, temp.Path)
	}
	if got := params["label"]; got != "trying-something" {
		t.Fatalf("got label %v", got)
	}

	if want := "cd '" + temp.Path + "' && claudebox"; herdr.sentInput("p1") != want {
		t.Fatalf("got %q, want %q", herdr.sentInput("p1"), want)
	}
	// The shell pane is left as a shell in the same directory — and there is no
	// status pane to watch members that do not exist.
	if want := "cd '" + temp.Path + "'"; herdr.sentInput("shell") != want {
		t.Fatalf("got %q, want %q", herdr.sentInput("shell"), want)
	}

	// The layout goes to the pane's own tab, never its workspace.
	layout := herdr.paramsFor("layout.apply")
	if got, _ := layout["tab_id"].(string); got != "tab1" {
		t.Fatalf("laid out tab %q, want tab1", got)
	}
	if _, ok := layout["workspace_id"]; ok {
		t.Fatalf("workspace_id must not be sent alongside tab_id: %v", layout)
	}

	if id, _ := herdr.paramsFor("pane.focus")["pane_id"].(string); id != "p1" {
		t.Fatalf("focused %q, want p1", id)
	}
}

// The agent goes into the pane the reply named, not the one the request did —
// herdr is free to have built a new one there.
func TestOpenTempStartsTheAgentInThePaneTheLayoutReturned(t *testing.T) {
	root := t.TempDir()
	temp, err := CreateTemp(root, "trying-something")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	herdr := &fakeHerdr{results: map[string]string{
		"pane.list":        `{"panes":[]}`,
		"workspace.create": `{"workspace":{"workspace_id":"ws1"},"root_pane":{"pane_id":"p1","tab_id":"tab1"}}`,
		"layout.apply":     appliedTempLayout("p9", "shell"),
	}}

	if err := OpenTemp(context.Background(), herdr, Config{Agent: []string{"claudebox"}}, temp); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	requireContains(t, herdr.sentInput("p9"), "claudebox")
	if herdr.sentInput("p1") != "" {
		t.Fatalf("agent typed into the stale pane p1: %q", herdr.sentInput("p1"))
	}
}

// The shell pane is a convenience; the agent is the point. An older herdr that
// cannot apply a layout still gets the directory open.
func TestOpenTempStartsTheAgentWhenTheLayoutFails(t *testing.T) {
	root := t.TempDir()
	temp, err := CreateTemp(root, "trying-something")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	herdr := &fakeHerdr{results: map[string]string{
		"pane.list":        `{"panes":[]}`,
		"workspace.create": `{"workspace":{"workspace_id":"ws1"},"root_pane":{"pane_id":"p1","tab_id":"tab1"}}`,
		// No layout.apply result: herdr answers with an empty layout.
	}}

	if err := OpenTemp(context.Background(), herdr, Config{Agent: []string{"claudebox"}}, temp); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "cd '" + temp.Path + "' && claudebox"; herdr.sentInput("p1") != want {
		t.Fatalf("got %q, want %q", herdr.sentInput("p1"), want)
	}
}

// Reopening a temp directory focuses the workspace already on it rather than
// opening a second one — and starts no second agent.
func TestOpenTempFocusesAnAlreadyOpenWorkspace(t *testing.T) {
	root := t.TempDir()
	temp, err := CreateTemp(root, "trying-something")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	herdr := &fakeHerdr{results: map[string]string{
		"pane.list": `{"panes":[{"pane_id":"p1","workspace_id":"ws9","cwd":"` + temp.Path + `"}]}`,
	}}

	if err := OpenTemp(context.Background(), herdr, Config{}, temp); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, _ := herdr.paramsFor("workspace.focus")["workspace_id"].(string); got != "ws9" {
		t.Fatalf("focused %q, want ws9", got)
	}
	if herdr.called("workspace.create") {
		t.Fatal("expected the existing workspace to be focused, not a second one created")
	}
	if herdr.called("pane.send_input") {
		t.Fatal("expected no agent launch into an already-open workspace")
	}
}

// Two panes, not three: the agent keeps the pane it already has, and the shell
// beside it opens in the temp directory.
func TestTempLayout(t *testing.T) {
	root := tempLayout("p1", "/tmp/trying")

	if root.Type != layoutSplit || root.Direction != "right" || root.Ratio != 0.5 {
		t.Fatalf("got %+v", root)
	}
	if root.First.Type != layoutPane || root.First.PaneID != "p1" || root.First.Cwd != "" {
		t.Fatalf("agent pane is %+v", root.First)
	}
	if root.Second.Type != layoutPane || root.Second.Cwd != "/tmp/trying" || root.Second.PaneID != "" {
		t.Fatalf("shell pane is %+v", root.Second)
	}
	// Nothing below the shell: there are no members for a status pane to watch.
	if root.Second.First != nil || root.Second.Second != nil {
		t.Fatalf("expected two panes, got %+v", root.Second)
	}
}

// A herdr that answers with something else shaped must not have its panes
// guessed at — the workspace opens as one pane instead.
func TestOpenTempSurvivesAnUnexpectedLayoutShape(t *testing.T) {
	root := t.TempDir()
	temp, err := CreateTemp(root, "trying-something")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	herdr := &fakeHerdr{results: map[string]string{
		"pane.list":        `{"panes":[]}`,
		"workspace.create": `{"workspace":{"workspace_id":"ws1"},"root_pane":{"pane_id":"p1","tab_id":"tab1"}}`,
		"layout.apply":     `{"layout":{"root":{"type":"pane","pane_id":"p1"}}}`,
	}}

	if err := OpenTemp(context.Background(), herdr, Config{Agent: []string{"claudebox"}}, temp); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	requireContains(t, herdr.sentInput("p1"), "claudebox")
}
