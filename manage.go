package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// OwnsPath reports whether a working directory is one herdr-pick made, so
// switching only ever touches our own workspaces and never the user's other herdr
// work.
//
// Every namespace lives under one root, so this is a single containment check.
// Clones have their own root and are worktree sources rather than workspaces, so
// they fall outside without needing to be excluded by name.
func OwnsPath(root, dir string) bool {
	if dir == "" {
		return false
	}
	return withinDir(NamespaceRoot(root), filepath.Clean(dir))
}

// withinDir reports whether path is strictly inside dir, comparing whole path
// components so <root>-adjacent directories do not match on a string prefix.
func withinDir(dir, path string) bool {
	return strings.HasPrefix(filepath.Clean(path), filepath.Clean(dir)+string(filepath.Separator))
}

// OwnedWorkspaces filters a workspace list down to the ones sitting in a
// namespace, using the panes to locate each workspace on disk.
//
// Matched by pane working directory rather than by herdr's checkout_path: a
// namespace's workspace is created from a directory, so herdr reports no worktree
// for it and WorkspaceInfo carries no path of its own. It is still matched by
// where it is rather than by a state file.
func OwnedWorkspaces(root string, workspaces []HerdrWorkspace, panes []HerdrPane) []HerdrWorkspace {
	owned := make(map[string]bool, len(panes))
	for _, p := range panes {
		if OwnsPath(root, p.Dir()) {
			owned[p.WorkspaceID] = true
		}
	}

	var out []HerdrWorkspace
	for _, ws := range workspaces {
		if owned[ws.WorkspaceID] {
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

// workspaceLabel is the human name for a workspace. workspace.create is given the
// namespace name as its label, so this is normally just that.
func workspaceLabel(ws HerdrWorkspace) string {
	if ws.Label != "" {
		return ws.Label
	}
	return ws.WorkspaceID
}

// SwitchCandidates returns the namespaces worth switching to, ordered by urgency.
// By default only active work is listed; all includes finished and agent-less
// workspaces too.
func SwitchCandidates(root string, workspaces []HerdrWorkspace, panes []HerdrPane, all bool) []HerdrWorkspace {
	var out []HerdrWorkspace
	for _, ws := range OwnedWorkspaces(root, workspaces, panes) {
		if all || activeStatus(ws.AgentStatus) {
			out = append(out, ws)
		}
	}
	sortWorkspaces(out)
	return out
}

// StatusWorkspaces returns every open namespace, ordered by urgency, for a
// glanceable overview of what the fleet of agents is doing.
func StatusWorkspaces(root string, workspaces []HerdrWorkspace, panes []HerdrPane) []HerdrWorkspace {
	out := OwnedWorkspaces(root, workspaces, panes)
	sortWorkspaces(out)
	return out
}

// statusLine renders one workspace as a tab-separated status/label pair, status
// first so the output greps and sorts by state.
func statusLine(ws HerdrWorkspace) string {
	return fmt.Sprintf("%s\t%s", ws.AgentStatus, workspaceLabel(ws))
}

// There is deliberately no cleanup here. herdr's worktree.remove takes a
// workspace and removes the one checkout backing it, and a namespace's workspace
// is backed by a directory rather than a checkout — so there is nothing correct
// for it to remove. A teardown has to understand every member, and until it
// exists namespaces are removed by hand.
