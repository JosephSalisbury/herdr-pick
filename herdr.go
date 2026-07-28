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
)

// herdrProtocol is the socket protocol version this client was written
// against. It is asserted on connect so a herdr upgrade fails loudly.
const herdrProtocol = 17

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
type HerdrWorkspace struct {
	WorkspaceID string                  `json:"workspace_id"`
	Label       string                  `json:"label"`
	Focused     bool                    `json:"focused"`
	ActiveTabID string                  `json:"active_tab_id"`
	AgentStatus string                  `json:"agent_status"`
	Worktree    *HerdrWorkspaceWorktree `json:"worktree"`
}

// HerdrWorkspaceWorktree mirrors herdr's WorkspaceWorktreeInfo. CheckoutPath is
// how a workspace is matched back to a herdr-pick worktree on disk.
type HerdrWorkspaceWorktree struct {
	CheckoutPath string `json:"checkout_path"`
	RepoName     string `json:"repo_name"`
}

// HerdrWorktree mirrors herdr's WorktreeInfo.
type HerdrWorktree struct {
	Path             string  `json:"path"`
	Label            string  `json:"label"`
	Branch           *string `json:"branch"`
	IsBare           bool    `json:"is_bare"`
	IsDetached       bool    `json:"is_detached"`
	IsPrunable       bool    `json:"is_prunable"`
	IsLinkedWorktree bool    `json:"is_linked_worktree"`
	OpenWorkspaceID  *string `json:"open_workspace_id"`
}

// worktreeResult covers the worktree_created and worktree_opened replies,
// which share a workspace/worktree pair.
type worktreeResult struct {
	Workspace   HerdrWorkspace `json:"workspace"`
	Worktree    HerdrWorktree  `json:"worktree"`
	AlreadyOpen bool           `json:"already_open"`
}

// Ping verifies the socket is live and speaking the expected protocol.
func Ping(ctx context.Context, h Herdr) error {
	var result struct {
		Protocol int `json:"protocol"`
	}
	if err := h.Call(ctx, "ping", nil, &result); err != nil {
		return err
	}
	if result.Protocol != 0 && result.Protocol != herdrProtocol {
		return fmt.Errorf("herdr speaks protocol %d, herdr-pick expects %d", result.Protocol, herdrProtocol)
	}
	return nil
}

// WorktreeCreate creates a checkout and returns the workspace herdr opened.
func WorktreeCreate(ctx context.Context, h Herdr, cwd, branch, path, label string) (HerdrWorkspace, error) {
	params := map[string]any{
		"cwd":    cwd,
		"branch": branch,
		"path":   path,
		"label":  label,
		"focus":  true,
	}
	var result worktreeResult
	if err := h.Call(ctx, "worktree.create", params, &result); err != nil {
		return HerdrWorkspace{}, err
	}
	if result.Workspace.WorkspaceID == "" {
		return HerdrWorkspace{}, fmt.Errorf("herdr created worktree %s without a workspace", path)
	}
	return result.Workspace, nil
}

// WorktreeOpen opens an existing checkout, focusing it. It is idempotent:
// herdr reports an already-open worktree rather than failing.
func WorktreeOpen(ctx context.Context, h Herdr, path string) (HerdrWorkspace, error) {
	params := map[string]any{
		"path":  path,
		"focus": true,
	}
	var result worktreeResult
	if err := h.Call(ctx, "worktree.open", params, &result); err != nil {
		return HerdrWorkspace{}, err
	}
	if result.Workspace.WorkspaceID == "" {
		return HerdrWorkspace{}, fmt.Errorf("herdr opened worktree %s without a workspace", path)
	}
	return result.Workspace, nil
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

// WorktreeRemove removes the worktree backing a workspace and closes it,
// returning the path herdr removed. Without force, herdr refuses a checkout
// with uncommitted changes rather than discarding work.
func WorktreeRemove(ctx context.Context, h Herdr, workspaceID string, force bool) (string, error) {
	params := map[string]any{
		"workspace_id": workspaceID,
		"force":        force,
	}
	var result struct {
		Path string `json:"path"`
	}
	if err := h.Call(ctx, "worktree.remove", params, &result); err != nil {
		return "", err
	}
	return result.Path, nil
}

// HerdrPane mirrors the fields of herdr's PaneInfo that we use.
type HerdrPane struct {
	PaneID  string `json:"pane_id"`
	Focused bool   `json:"focused"`
}

// PaneList returns the panes belonging to a workspace.
func PaneList(ctx context.Context, h Herdr, workspaceID string) ([]HerdrPane, error) {
	var result struct {
		Panes []HerdrPane `json:"panes"`
	}
	params := map[string]any{"workspace_id": workspaceID}
	if err := h.Call(ctx, "pane.list", params, &result); err != nil {
		return nil, err
	}
	return result.Panes, nil
}

// RootPane picks the pane to run the agent in: the focused one, else the only
// one. worktree.create yields a single-pane workspace and we deliberately do
// not add a second.
func RootPane(panes []HerdrPane) (string, error) {
	if len(panes) == 0 {
		return "", errors.New("workspace has no panes")
	}
	for _, p := range panes {
		if p.Focused {
			return p.PaneID, nil
		}
	}
	return panes[0].PaneID, nil
}

// RunInPane types a command into an existing pane and submits it.
//
// Deliberately not agent.start: that has no pane_id parameter and its only
// split options are right/down, so it always adds a second pane. Typing into
// the pane worktree.create already made keeps the workspace to one pane, and
// herdr still tracks the agent because it detects them from the foreground
// process. send_input carries text and Enter in one call, which is how herdr's
// own `pane run` submits atomically under bracketed paste.
func RunInPane(ctx context.Context, h Herdr, paneID, command string) error {
	params := map[string]any{
		"pane_id": paneID,
		"text":    command,
		"keys":    []string{"enter"},
	}
	return h.Call(ctx, "pane.send_input", params, nil)
}
