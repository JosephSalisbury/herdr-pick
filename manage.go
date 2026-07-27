package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// OwnsWorktree reports whether a checkout path is one herdr-pick created — it
// lives under <root>/worktrees. Cleanup and switching only ever touch our own
// workspaces, never the user's other herdr work.
func OwnsWorktree(root, checkoutPath string) bool {
	if checkoutPath == "" {
		return false
	}
	base := filepath.Clean(filepath.Join(root, "worktrees")) + string(filepath.Separator)
	return strings.HasPrefix(filepath.Clean(checkoutPath)+string(filepath.Separator), base)
}

// OwnedWorkspaces filters a workspace list down to the ones backed by a
// herdr-pick worktree.
func OwnedWorkspaces(root string, workspaces []HerdrWorkspace) []HerdrWorkspace {
	var out []HerdrWorkspace
	for _, ws := range workspaces {
		if ws.Worktree != nil && OwnsWorktree(root, ws.Worktree.CheckoutPath) {
			out = append(out, ws)
		}
	}
	return out
}

// activeStatus reports whether an agent status counts as work in flight —
// busy, waiting on input, or between turns. A finished ("done") agent is not
// active; clean removes those. "unknown" means herdr saw no agent at all.
func activeStatus(status string) bool {
	switch status {
	case AgentWorking, AgentBlocked, AgentIdle:
		return true
	default:
		return false
	}
}

// statusRank orders statuses so the most demanding work sorts first: blocked
// (needs you) before working before idle, and everything else last.
func statusRank(status string) int {
	switch status {
	case AgentBlocked:
		return 0
	case AgentWorking:
		return 1
	case AgentIdle:
		return 2
	case AgentDone:
		return 3
	default:
		return 4
	}
}

// sortWorkspaces orders workspaces by status urgency, then label, so the
// picker leads with what most wants attention.
func sortWorkspaces(workspaces []HerdrWorkspace) {
	sort.SliceStable(workspaces, func(i, j int) bool {
		ri, rj := statusRank(workspaces[i].AgentStatus), statusRank(workspaces[j].AgentStatus)
		if ri != rj {
			return ri < rj
		}
		return lessFold(workspaceLabel(workspaces[i]), workspaceLabel(workspaces[j]))
	})
}

// workspaceLabel is the human name for a workspace, falling back to the
// checkout path when herdr has no label for it.
func workspaceLabel(ws HerdrWorkspace) string {
	if ws.Label != "" {
		return ws.Label
	}
	if ws.Worktree != nil {
		return ws.Worktree.CheckoutPath
	}
	return ws.WorkspaceID
}

// DoneWorkspaces returns the herdr-pick workspaces whose agent has finished.
func DoneWorkspaces(root string, workspaces []HerdrWorkspace) []HerdrWorkspace {
	var out []HerdrWorkspace
	for _, ws := range OwnedWorkspaces(root, workspaces) {
		if ws.AgentStatus == AgentDone {
			out = append(out, ws)
		}
	}
	sortWorkspaces(out)
	return out
}

// SwitchCandidates returns the herdr-pick workspaces worth switching to,
// ordered by urgency. By default only active work is listed; all includes
// finished and agent-less workspaces too.
func SwitchCandidates(root string, workspaces []HerdrWorkspace, all bool) []HerdrWorkspace {
	var out []HerdrWorkspace
	for _, ws := range OwnedWorkspaces(root, workspaces) {
		if all || activeStatus(ws.AgentStatus) {
			out = append(out, ws)
		}
	}
	sortWorkspaces(out)
	return out
}

// StatusWorkspaces returns every herdr-pick worktree herdr has open, ordered
// by urgency, for a glanceable overview of what the fleet of agents is doing.
func StatusWorkspaces(root string, workspaces []HerdrWorkspace) []HerdrWorkspace {
	out := OwnedWorkspaces(root, workspaces)
	sortWorkspaces(out)
	return out
}

// statusLine renders one workspace as a tab-separated status/label pair, status
// first so the output greps and sorts by state.
func statusLine(ws HerdrWorkspace) string {
	return fmt.Sprintf("%s\t%s", ws.AgentStatus, workspaceLabel(ws))
}

// CleanDone removes every herdr-pick worktree whose agent has finished,
// returning the paths removed. It never touches workspaces herdr-pick does not
// own, and — unless force is set — lets herdr refuse a checkout with
// uncommitted changes so unfinished work survives a stray "done".
func CleanDone(ctx context.Context, h Herdr, root string, force bool) ([]string, error) {
	workspaces, err := WorkspaceList(ctx, h)
	if err != nil {
		return nil, err
	}

	var (
		removed []string
		errs    []error
	)
	for _, ws := range DoneWorkspaces(root, workspaces) {
		path, err := WorktreeRemove(ctx, h, ws.WorkspaceID, force)
		if err != nil {
			errs = append(errs, fmt.Errorf("removing %s: %w", workspaceLabel(ws), err))
			continue
		}
		removed = append(removed, path)
	}
	return removed, errors.Join(errs...)
}
