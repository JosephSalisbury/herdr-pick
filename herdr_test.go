package main

import (
	"context"
	"errors"
	"testing"
)

func TestWorkspaceCreateSendsCwdAndLabel(t *testing.T) {
	herdr := &fakeHerdr{results: map[string]string{
		"workspace.create": `{"type":"workspace_created","workspace":{"workspace_id":"w1","label":"add-foo"},
			"root_pane":{"pane_id":"p1","workspace_id":"w1","focused":true,"cwd":"/ns/add-foo"}}`,
	}}

	ws, root, err := WorkspaceCreate(context.Background(), herdr, "/ns/add-foo", "add-foo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ws.WorkspaceID != "w1" || ws.Label != "add-foo" {
		t.Fatalf("got %+v", ws)
	}
	// The root pane comes back with the workspace, so the create path needs no
	// pane.list.
	if root.PaneID != "p1" {
		t.Fatalf("got root pane %+v", root)
	}

	params := herdr.paramsFor("workspace.create")
	if got, _ := params["cwd"].(string); got != "/ns/add-foo" {
		t.Fatalf("got cwd %q", got)
	}
	if got, _ := params["label"].(string); got != "add-foo" {
		t.Fatalf("got label %q", got)
	}
	if focus, _ := params["focus"].(bool); !focus {
		t.Fatal("expected focus to be requested")
	}
}

// A workspace we cannot address is fatal rather than something to paper over.
func TestWorkspaceCreateRequiresWorkspaceID(t *testing.T) {
	herdr := &fakeHerdr{results: map[string]string{
		"workspace.create": `{"type":"workspace_created","root_pane":{"pane_id":"p1"}}`,
	}}

	if _, _, err := WorkspaceCreate(context.Background(), herdr, "/ns/x", "x"); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestWorkspaceCreatePropagatesError(t *testing.T) {
	herdr := &fakeHerdr{err: errors.New("socket down")}
	if _, _, err := WorkspaceCreate(context.Background(), herdr, "/ns/x", "x"); err == nil {
		t.Fatal("expected error, got nil")
	}
}

// An empty workspace id means "every pane in the session", which is what lets one
// call locate every workspace on disk.
func TestPaneListOmitsWorkspaceWhenListingAll(t *testing.T) {
	herdr := &fakeHerdr{results: map[string]string{
		"pane.list": `{"type":"pane_list","panes":[{"pane_id":"p1","workspace_id":"w1","cwd":"/ns/a"}]}`,
	}}

	panes, err := PaneList(context.Background(), herdr, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(panes) != 1 || panes[0].WorkspaceID != "w1" || panes[0].Dir() != "/ns/a" {
		t.Fatalf("got %+v", panes)
	}
	if _, ok := herdr.paramsFor("pane.list")["workspace_id"]; ok {
		t.Fatal("workspace_id should be omitted when listing every pane")
	}
}

// The shell's own cwd is preferred, but a pane running an agent may only report
// the foreground process's.
func TestPaneDirPrefersShellCwd(t *testing.T) {
	if got := (HerdrPane{Cwd: "/a", ForegroundCwd: "/b"}).Dir(); got != "/a" {
		t.Fatalf("got %q, want /a", got)
	}
	if got := (HerdrPane{ForegroundCwd: "/b"}).Dir(); got != "/b" {
		t.Fatalf("got %q, want /b", got)
	}
	if got := (HerdrPane{}).Dir(); got != "" {
		t.Fatalf("got %q, want empty", got)
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

// A namespace's workspace is directory-backed, so herdr reports worktree: null
// for it. Parsing must not depend on that field being present.
func TestWorkspaceListParsesWithoutWorktree(t *testing.T) {
	herdr := &fakeHerdr{results: map[string]string{
		"workspace.list": `{"type":"workspace_list","workspaces":[
			{"workspace_id":"w1","label":"add-foo","agent_status":"working","worktree":null},
			{"workspace_id":"w2","label":"other","agent_status":"idle","worktree":{"checkout_path":"/wt/a"}}
		]}`,
	}}

	got, err := WorkspaceList(context.Background(), herdr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d workspaces, want 2", len(got))
	}
	if got[0].Label != "add-foo" || got[0].AgentStatus != AgentWorking {
		t.Fatalf("got %+v", got[0])
	}
	// A worktree herdr does report is simply ignored.
	if got[1].Label != "other" {
		t.Fatalf("got %+v", got[1])
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
