package main

import (
	"context"
	"errors"
	"testing"
)

func TestWorktreeCreateSendsExpectedParams(t *testing.T) {
	herdr := &fakeHerdr{results: map[string]string{
		"worktree.create": `{"type":"worktree_created","workspace":{"workspace_id":"w7","label":"x"},"worktree":{"path":"/wt","label":"x"}}`,
	}}

	ws, err := WorktreeCreate(context.Background(), herdr, "/clone", "swift-owlbear", "/wt", "org/repo@swift-owlbear")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ws.WorkspaceID != "w7" {
		t.Fatalf("got workspace %q, want w7", ws.WorkspaceID)
	}

	params := herdr.paramsFor("worktree.create")
	for key, want := range map[string]string{
		"cwd":    "/clone",
		"branch": "swift-owlbear",
		"path":   "/wt",
	} {
		if got, _ := params[key].(string); got != want {
			t.Fatalf("param %s: got %q, want %q", key, got, want)
		}
	}
}

// The response carries no pane id, so a missing workspace id is fatal rather
// than something to paper over.
func TestWorktreeCreateRequiresWorkspaceID(t *testing.T) {
	herdr := &fakeHerdr{results: map[string]string{
		"worktree.create": `{"type":"worktree_created","worktree":{"path":"/wt","label":"x"}}`,
	}}

	if _, err := WorktreeCreate(context.Background(), herdr, "/clone", "b", "/wt", "l"); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestWorktreeCreatePropagatesError(t *testing.T) {
	herdr := &fakeHerdr{err: errors.New("socket down")}
	if _, err := WorktreeCreate(context.Background(), herdr, "/clone", "b", "/wt", "l"); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestWorktreeOpenFocuses(t *testing.T) {
	herdr := &fakeHerdr{results: map[string]string{
		"worktree.open": `{"type":"worktree_opened","already_open":true,"workspace":{"workspace_id":"w1"},"worktree":{"path":"/wt","label":"x"}}`,
	}}

	ws, err := WorktreeOpen(context.Background(), herdr, "/wt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ws.WorkspaceID != "w1" {
		t.Fatalf("got %q, want w1", ws.WorkspaceID)
	}
	if focus, _ := herdr.paramsFor("worktree.open")["focus"].(bool); !focus {
		t.Fatal("expected focus to be requested")
	}
}

func TestPaneList(t *testing.T) {
	herdr := &fakeHerdr{results: map[string]string{
		"pane.list": `{"type":"pane_list","panes":[{"pane_id":"p1","focused":false},{"pane_id":"p2","focused":true}]}`,
	}}

	panes, err := PaneList(context.Background(), herdr, "w1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(panes) != 2 || panes[1].PaneID != "p2" || !panes[1].Focused {
		t.Fatalf("got %+v", panes)
	}
	if got, _ := herdr.paramsFor("pane.list")["workspace_id"].(string); got != "w1" {
		t.Fatalf("got workspace_id %q, want w1", got)
	}
}

func TestRootPane(t *testing.T) {
	if _, err := RootPane(nil); err == nil {
		t.Fatal("expected error for a workspace with no panes")
	}

	got, err := RootPane([]HerdrPane{{PaneID: "p1"}, {PaneID: "p2", Focused: true}})
	if err != nil || got != "p2" {
		t.Fatalf("got %q, %v; want focused pane p2", got, err)
	}

	// With nothing focused, fall back to the first pane.
	if got, err = RootPane([]HerdrPane{{PaneID: "p1"}}); err != nil || got != "p1" {
		t.Fatalf("got %q, %v; want p1", got, err)
	}
}

// The agent must be typed into the existing pane. agent.start would add a
// second pane, which is not wanted.
func TestRunInPaneSendsTextAndEnter(t *testing.T) {
	herdr := &fakeHerdr{}

	if err := RunInPane(context.Background(), herdr, "p1", "claude"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if herdr.called("agent.start") || herdr.called("pane.split") {
		t.Fatalf("expected no pane creation, got %v", herdr.methods)
	}

	params := herdr.paramsFor("pane.send_input")
	if got, _ := params["pane_id"].(string); got != "p1" {
		t.Fatalf("got pane_id %q, want p1", got)
	}
	if got, _ := params["text"].(string); got != "claude" {
		t.Fatalf("got text %q, want claude", got)
	}
	keys, ok := params["keys"].([]any)
	if !ok || len(keys) != 1 || keys[0] != "enter" {
		t.Fatalf("got keys %v, want [enter]", params["keys"])
	}
}

func TestWorkspaceListParsesWorktree(t *testing.T) {
	herdr := &fakeHerdr{results: map[string]string{
		"workspace.list": `{"type":"workspace_list","workspaces":[
			{"workspace_id":"w1","label":"org/repo@a","agent_status":"working","worktree":{"checkout_path":"/wt/a"}},
			{"workspace_id":"w2","label":"other","agent_status":"idle"}
		]}`,
	}}

	got, err := WorkspaceList(context.Background(), herdr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d workspaces, want 2", len(got))
	}
	if got[0].Worktree == nil || got[0].Worktree.CheckoutPath != "/wt/a" {
		t.Fatalf("got worktree %+v", got[0].Worktree)
	}
	if got[0].AgentStatus != AgentWorking {
		t.Fatalf("got status %q", got[0].AgentStatus)
	}
	if got[1].Worktree != nil {
		t.Fatalf("expected no worktree for w2, got %+v", got[1].Worktree)
	}
}

func TestWorkspaceFocusTargetsWorkspace(t *testing.T) {
	herdr := &fakeHerdr{}
	if err := WorkspaceFocus(context.Background(), herdr, "w9"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, _ := herdr.paramsFor("workspace.focus")["workspace_id"].(string); got != "w9" {
		t.Fatalf("got workspace_id %q, want w9", got)
	}
}

func TestWorktreeRemovePassesForce(t *testing.T) {
	herdr := &fakeHerdr{results: map[string]string{
		"worktree.remove": `{"type":"worktree_removed","path":"/wt/a","workspace_id":"w1","forced":true}`,
	}}

	path, err := WorktreeRemove(context.Background(), herdr, "w1", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/wt/a" {
		t.Fatalf("got path %q, want /wt/a", path)
	}
	params := herdr.paramsFor("worktree.remove")
	if got, _ := params["workspace_id"].(string); got != "w1" {
		t.Fatalf("got workspace_id %q, want w1", got)
	}
	if force, _ := params["force"].(bool); !force {
		t.Fatal("expected force to be passed")
	}
}

func TestWorktreeRemovePropagatesError(t *testing.T) {
	herdr := &fakeHerdr{err: errors.New("dirty tree")}
	if _, err := WorktreeRemove(context.Background(), herdr, "w1", false); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestPingRejectsProtocolMismatch(t *testing.T) {
	herdr := &fakeHerdr{results: map[string]string{"ping": `{"type":"pong","protocol":999}`}}
	if err := Ping(context.Background(), herdr); err == nil {
		t.Fatal("expected error, got nil")
	}

	ok := &fakeHerdr{results: map[string]string{"ping": `{"type":"pong","protocol":17}`}}
	if err := Ping(context.Background(), ok); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewSocketHerdrPrefersEnvironment(t *testing.T) {
	t.Setenv("HERDR_SOCKET_PATH", "/custom/herdr.sock")

	h, err := NewSocketHerdr()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.Path != "/custom/herdr.sock" {
		t.Fatalf("got %q", h.Path)
	}
}

func TestNewSocketHerdrUsesSession(t *testing.T) {
	t.Setenv("HERDR_SOCKET_PATH", "")
	t.Setenv("HERDR_SESSION", "work")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")

	h, err := NewSocketHerdr()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "/xdg/herdr/sessions/work/herdr.sock"; h.Path != want {
		t.Fatalf("got %q, want %q", h.Path, want)
	}
}

// The socket lives under ~/.config even on macOS, because that is where herdr
// puts it. os.UserConfigDir would look in ~/Library and never find it.
func TestNewSocketHerdrDefaultPath(t *testing.T) {
	t.Setenv("HERDR_SOCKET_PATH", "")
	t.Setenv("HERDR_SESSION", "")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")

	h, err := NewSocketHerdr()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "/xdg/herdr/herdr.sock"; h.Path != want {
		t.Fatalf("got %q, want %q", h.Path, want)
	}
}
