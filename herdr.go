package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"unicode/utf8"
)

// herdrMinProtocol is the oldest herdr socket protocol this client works
// against. It is a floor, not an equality: herdr bumps the protocol as it adds
// methods, and the handful this client calls have been stable across those
// bumps, so a newer herdr is taken as compatible. Asserting equality instead
// meant every herdr upgrade broke herdr-pick for no reason.
//
// A herdr that genuinely does change one of those methods is caught where it
// matters — the call itself comes back invalid_request, which Call reports as
// herdr-pick being out of date. Raise this floor only when that happens.
const herdrMinProtocol = 17

// maxErrMessage caps how much of herdr's error message is echoed. Its
// unknown-method reply enumerates every method it has, which buries the point.
const maxErrMessage = 160

// Agent statuses herdr reports for a workspace's foreground process. "done"
// means the agent finished — those worktrees are cleanup candidates. The
// working/blocked/idle trio is work still in flight.
const (
	AgentIdle    = "idle"
	AgentWorking = "working"
	AgentBlocked = "blocked"
	AgentDone    = "done"
	AgentUnknown = "unknown"
)

// Herdr is the subset of herdr's socket API that herdr-pick uses.
type Herdr interface {
	Call(ctx context.Context, method string, params, result any) error
}

// SocketHerdr speaks herdr's newline-delimited JSON protocol over a local
// socket. The socket API is used in preference to the herdr CLI because its
// method and parameter names are pinned by a published schema.
type SocketHerdr struct {
	Path string
}

// NewSocketHerdr resolves the herdr socket path from the environment.
func NewSocketHerdr() (*SocketHerdr, error) {
	if p := os.Getenv("HERDR_SOCKET_PATH"); p != "" {
		return &SocketHerdr{Path: p}, nil
	}

	dir, err := configHome()
	if err != nil {
		return nil, err
	}
	if session := os.Getenv("HERDR_SESSION"); session != "" {
		return &SocketHerdr{Path: filepath.Join(dir, "herdr", "sessions", session, "herdr.sock")}, nil
	}
	return &SocketHerdr{Path: filepath.Join(dir, "herdr", "herdr.sock")}, nil
}

// herdrResponse is the envelope every socket reply arrives in.
type herdrResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Call sends a single request and decodes its result into out.
func (h *SocketHerdr) Call(ctx context.Context, method string, params, out any) error {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", h.Path)
	if err != nil {
		return fmt.Errorf("connecting to herdr socket %s: %w", h.Path, err)
	}
	defer func() { _ = conn.Close() }()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	if params == nil {
		params = struct{}{}
	}
	payload, err := json.Marshal(map[string]any{
		"id":     "herdr-pick",
		"method": method,
		"params": params,
	})
	if err != nil {
		return fmt.Errorf("encoding %s request: %w", method, err)
	}
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("sending %s request: %w", method, err)
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return fmt.Errorf("reading %s response: %w", method, err)
	}

	var resp herdrResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("decoding %s response: %w", method, err)
	}
	if resp.Error != nil {
		// invalid_request is how herdr rejects a request it cannot even parse: an
		// unknown method, or params of the wrong shape. That is protocol drift
		// rather than a runtime refusal, and it is the only thing a protocol
		// assertion on connect would have caught.
		if resp.Error.Code == "invalid_request" {
			return fmt.Errorf("herdr rejected the %s request (%s): herdr-pick's socket calls are out of date with this herdr", method, truncate(resp.Error.Message, maxErrMessage))
		}
		return fmt.Errorf("herdr %s failed: %s (%s)", method, resp.Error.Message, resp.Error.Code)
	}
	if out != nil && len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return fmt.Errorf("decoding %s result: %w", method, err)
		}
	}
	return nil
}

// HerdrWorkspace mirrors herdr's WorkspaceInfo.
//
// Deliberately no worktree field. A namespace's workspace is created from a
// directory rather than a checkout, so herdr reports worktree: null for it, and
// nothing here can rely on it. A workspace is matched back to a namespace by its
// pane's working directory instead.
type HerdrWorkspace struct {
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
	Focused     bool   `json:"focused"`
	ActiveTabID string `json:"active_tab_id"`
	AgentStatus string `json:"agent_status"`
}

// truncate shortens s to at most n bytes without splitting a rune.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// Ping verifies the socket is live and not older than the protocol floor.
func Ping(ctx context.Context, h Herdr) error {
	var result struct {
		Protocol int `json:"protocol"`
	}
	if err := h.Call(ctx, "ping", nil, &result); err != nil {
		return err
	}
	if result.Protocol != 0 && result.Protocol < herdrMinProtocol {
		return fmt.Errorf("herdr speaks protocol %d, herdr-pick needs at least %d: upgrade herdr", result.Protocol, herdrMinProtocol)
	}
	return nil
}

// WorkspaceCreate opens a directory as a workspace and returns it with its root
// pane, saving a pane.list on the create path.
//
// This is the only way herdr-pick opens a workspace, and it is why none of
// herdr's worktree.* methods are used at all. worktree.open refuses a checkout
// whose clone is not its neighbour ("New and open worktree actions start from the
// repo parent workspace", linked_worktree_source) — and it would be the wrong
// call anyway: a namespace is a directory of checkouts, not one checkout, and
// creating each member through herdr would open one workspace per member when
// the whole point is one workspace over all of them.
func WorkspaceCreate(ctx context.Context, h Herdr, cwd, label string) (HerdrWorkspace, HerdrPane, error) {
	params := map[string]any{
		"cwd":   cwd,
		"label": label,
		"focus": true,
	}
	var result struct {
		Workspace HerdrWorkspace `json:"workspace"`
		RootPane  HerdrPane      `json:"root_pane"`
	}
	if err := h.Call(ctx, "workspace.create", params, &result); err != nil {
		return HerdrWorkspace{}, HerdrPane{}, err
	}
	if result.Workspace.WorkspaceID == "" {
		return HerdrWorkspace{}, HerdrPane{}, fmt.Errorf("herdr created no workspace for %s", cwd)
	}
	return result.Workspace, result.RootPane, nil
}

// WorkspaceList returns every workspace herdr currently has open. It is the
// entry point for managing work already in flight — switching to it or
// cleaning it up.
func WorkspaceList(ctx context.Context, h Herdr) ([]HerdrWorkspace, error) {
	var result struct {
		Workspaces []HerdrWorkspace `json:"workspaces"`
	}
	if err := h.Call(ctx, "workspace.list", nil, &result); err != nil {
		return nil, err
	}
	return result.Workspaces, nil
}

// WorkspaceFocus brings a workspace to the foreground.
func WorkspaceFocus(ctx context.Context, h Herdr, workspaceID string) error {
	return h.Call(ctx, "workspace.focus", map[string]any{"workspace_id": workspaceID}, nil)
}

// HerdrPane mirrors the fields of herdr's PaneInfo that we use.
//
// Cwd is how a workspace is matched back to a namespace. A directory-backed
// workspace has no worktree for herdr to report, and WorkspaceInfo carries no
// path of its own, so the pane's working directory is the only thing tying a
// workspace to a place on disk.
// TabID is what a layout is applied to. A workspace can hold several tabs, so
// the workspace alone does not say where panes should go — naming the tab the
// agent's pane is already in is what puts the rest of the layout beside it.
type HerdrPane struct {
	PaneID        string `json:"pane_id"`
	WorkspaceID   string `json:"workspace_id"`
	TabID         string `json:"tab_id"`
	Focused       bool   `json:"focused"`
	Cwd           string `json:"cwd"`
	ForegroundCwd string `json:"foreground_cwd"`
}

// Dir returns the pane's working directory, preferring the shell's own over the
// foreground process's.
func (p HerdrPane) Dir() string {
	if p.Cwd != "" {
		return p.Cwd
	}
	return p.ForegroundCwd
}

// PaneList returns the panes belonging to a workspace, or every pane in the
// session when workspaceID is empty. Listing all of them in one call is what
// keeps matching workspaces to namespaces to a single round trip.
func PaneList(ctx context.Context, h Herdr, workspaceID string) ([]HerdrPane, error) {
	var result struct {
		Panes []HerdrPane `json:"panes"`
	}
	params := map[string]any{}
	if workspaceID != "" {
		params["workspace_id"] = workspaceID
	}
	if err := h.Call(ctx, "pane.list", params, &result); err != nil {
		return nil, err
	}
	return result.Panes, nil
}

// RootPane picks the pane to run the agent in: the focused one, else the first.
// workspace.create yields a single-pane workspace, and that pane is the one the
// layout is then built around, so it is the agent's.
//
// The whole pane rather than its id, because the layout needs its tab too.
func RootPane(panes []HerdrPane) (HerdrPane, error) {
	if len(panes) == 0 {
		return HerdrPane{}, errors.New("workspace has no panes")
	}
	for _, p := range panes {
		if p.Focused {
			return p, nil
		}
	}
	return panes[0], nil
}

// RunInPane types a command into an existing pane and submits it.
//
// Deliberately not agent.start: that has no pane_id parameter, so it cannot be
// pointed at a pane we already have — and its only split options are right and
// down, so it would add a pane of its own alongside whichever layout we built.
// Typing into a named pane leaves the layout alone, and herdr still tracks the
// agent because it detects them from the foreground process. send_input carries
// text and Enter in one call, which is how herdr's own `pane run` submits
// atomically under bracketed paste.
func RunInPane(ctx context.Context, h Herdr, paneID, command string) error {
	params := map[string]any{
		"pane_id": paneID,
		"text":    command,
		"keys":    []string{"enter"},
	}
	return h.Call(ctx, "pane.send_input", params, nil)
}

// PaneFocus puts the cursor in a pane. Applying a layout leaves focus wherever
// herdr chooses, so the pane the human types into is named explicitly.
func PaneFocus(ctx context.Context, h Herdr, paneID string) error {
	return h.Call(ctx, "pane.focus", map[string]any{"pane_id": paneID}, nil)
}

// Node types in herdr's layout tree.
const (
	layoutPane  = "pane"
	layoutSplit = "split"
)

// LayoutNode is one node of herdr's declarative pane tree: either a pane or a
// split of two children, discriminated by Type.
//
// One struct rather than two, because the tree is walked far more often than it
// is type-switched, and omitempty keeps the unused half out of the request. A
// PaneID on a pane node adopts an existing pane; without one herdr creates a
// pane, and fills the id in on the tree it returns.
type LayoutNode struct {
	Type string `json:"type"`

	// Pane nodes.
	PaneID string `json:"pane_id,omitempty"`
	Cwd    string `json:"cwd,omitempty"`

	// Split nodes. Ratio is First's share of the space; for direction "right"
	// First is the left child, for "down" it is the upper one.
	Direction string      `json:"direction,omitempty"`
	Ratio     float64     `json:"ratio,omitempty"`
	First     *LayoutNode `json:"first,omitempty"`
	Second    *LayoutNode `json:"second,omitempty"`
}

// LayoutApply reshapes a tab's panes to a declared tree, and returns the tree
// herdr actually built — the same shape, with a pane id filled in on every
// node, which is how the panes it created are identified.
//
// Declaring the whole tree in one call rather than walking it with pane.split:
// the split node carries an explicit first/second, so a ratio is unambiguously
// one named child's share rather than "the new pane's" or "the old pane's", and
// a three-pane workspace costs one round trip instead of two.
//
// A tab, not a workspace. Both parameters are optional and it is tempting to
// pass the workspace, but herdr then applies the layout to a *new* tab of it —
// leaving the agent alone in the old one and the new panes empty in the new.
func LayoutApply(ctx context.Context, h Herdr, tabID string, root LayoutNode) (LayoutNode, error) {
	params := map[string]any{
		"tab_id": tabID,
		"root":   root,
	}
	var result struct {
		Layout struct {
			Root LayoutNode `json:"root"`
		} `json:"layout"`
	}
	if err := h.Call(ctx, "layout.apply", params, &result); err != nil {
		return LayoutNode{}, err
	}
	return result.Layout.Root, nil
}
