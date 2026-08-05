package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// The protocol is a floor, not an equality. herdr bumps it when it adds
// methods, and requiring an exact match meant every herdr release broke
// herdr-pick even though nothing it calls had changed.
func TestPingAcceptsNewerProtocol(t *testing.T) {
	for _, protocol := range []int{herdrMinProtocol, herdrMinProtocol + 2, 999} {
		h := &fakeHerdr{results: map[string]string{
			"ping": fmt.Sprintf(`{"type":"pong","protocol":%d}`, protocol),
		}}
		if err := Ping(context.Background(), h); err != nil {
			t.Fatalf("protocol %d: unexpected error: %v", protocol, err)
		}
	}
}

func TestPingRejectsOlderProtocol(t *testing.T) {
	h := &fakeHerdr{results: map[string]string{
		"ping": fmt.Sprintf(`{"type":"pong","protocol":%d}`, herdrMinProtocol-1),
	}}
	err := Ping(context.Background(), h)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	requireContains(t, err.Error(), "upgrade herdr")
}

// The one incompatibility that matters — herdr changing or dropping a method
// herdr-pick calls — is caught at the call, not on connect.
func TestCallReportsInvalidRequestAsOutOfDate(t *testing.T) {
	message := "invalid request: unknown variant pane.send_input, expected one of " +
		strings.Repeat("a, ", 60)
	reply := fmt.Sprintf(`{"id":"herdr-pick","error":{"code":"invalid_request","message":%q}}`, message)
	h := &SocketHerdr{Path: fakeSocket(t, reply)}

	err := h.Call(context.Background(), "pane.send_input", map[string]any{"pane_id": "p1"}, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	requireContains(t, err.Error(), "out of date with this herdr")
	// herdr's unknown-method reply lists all ninety of its methods; echoing it
	// whole buries the point.
	if len(err.Error()) > 400 {
		t.Fatalf("error message not truncated: %d bytes", len(err.Error()))
	}
}

// Any other error code is herdr refusing at runtime, which is not a version
// problem and must not be reported as one.
func TestCallReportsRuntimeErrorAsIs(t *testing.T) {
	h := &SocketHerdr{Path: fakeSocket(t, `{"id":"herdr-pick","error":{"code":"linked_worktree_source","message":"nope"}}`)}

	err := h.Call(context.Background(), "workspace.create", nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	requireContains(t, err.Error(), "linked_worktree_source")
	if strings.Contains(err.Error(), "out of date") {
		t.Fatalf("runtime refusal misreported as a version problem: %v", err)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Fatalf("got %q", got)
	}
	if got := truncate("abcdefghij", 4); got != "abcd…" {
		t.Fatalf("got %q", got)
	}
	// Cutting mid-rune must not produce invalid UTF-8.
	if got := truncate("aa€bb", 3); got != "aa…" {
		t.Fatalf("got %q", got)
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
