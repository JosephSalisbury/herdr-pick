package main

import (
	"testing"
)

func TestOwnsPath(t *testing.T) {
	root := "/data/herdr-pick"

	for _, owned := range []string{
		NamespaceDir(root, "add-foo"),
		MemberDir(root, "add-foo", "claudebox"),
	} {
		if !OwnsPath(root, owned) {
			t.Fatalf("expected %q to be owned", owned)
		}
	}

	for _, foreign := range []string{
		"",
		"/somewhere/else",
		// Clones have their own root: a worktree source is never a workspace.
		CloneDir(root, "giantswarm", "foo"),
		"/data/herdr-pick-evil/ns/x/y", // prefix but not a child
		root,
		NamespaceRoot(root),
	} {
		if OwnsPath(root, foreign) {
			t.Fatalf("expected %q not to be owned", foreign)
		}
	}
}

// ws and pane are compact builders for the management tests. A workspace is
// located through its pane's working directory, so the two go together.
func ws(id, status string) HerdrWorkspace {
	return HerdrWorkspace{WorkspaceID: id, Label: id, AgentStatus: status}
}

func pane(workspaceID, dir string) HerdrPane {
	return HerdrPane{PaneID: "p-" + workspaceID, WorkspaceID: workspaceID, Cwd: dir}
}

// A workspace with no pane anywhere near a namespace is somebody else's work.
func TestOwnedWorkspacesMatchesByPaneDirectory(t *testing.T) {
	root := "/r"
	workspaces := []HerdrWorkspace{ws("a", AgentWorking), ws("b", AgentIdle), ws("c", AgentDone)}
	panes := []HerdrPane{
		pane("a", NamespaceDir(root, "add-foo")),
		pane("b", "/elsewhere"),
		// c has no pane at all.
	}

	got := OwnedWorkspaces(root, workspaces, panes)
	if len(got) != 1 || got[0].WorkspaceID != "a" {
		t.Fatalf("got %+v, want just a", got)
	}
}

// fleet builds a set of namespace workspaces plus one foreign workspace, each
// located by its pane.
func fleet(root string, statuses map[string]string) ([]HerdrWorkspace, []HerdrPane) {
	var workspaces []HerdrWorkspace
	var panes []HerdrPane
	for id, status := range statuses {
		workspaces = append(workspaces, ws(id, status))
		panes = append(panes, pane(id, NamespaceDir(root, id)))
	}
	workspaces = append(workspaces, ws("foreign", AgentWorking))
	panes = append(panes, pane("foreign", "/elsewhere"))
	return workspaces, panes
}

func TestSwitchCandidatesActiveOnlyByDefault(t *testing.T) {
	root := "/r"
	workspaces, panes := fleet(root, map[string]string{
		"idle": AgentIdle, "working": AgentWorking, "done": AgentDone, "blocked": AgentBlocked,
	})

	got := SwitchCandidates(root, workspaces, panes, false)
	if len(got) != 3 {
		t.Fatalf("got %d active, want 3: %+v", len(got), got)
	}
	// blocked sorts first, then working, then idle.
	if got[0].WorkspaceID != "blocked" || got[1].WorkspaceID != "working" || got[2].WorkspaceID != "idle" {
		t.Fatalf("wrong order: %s %s %s", got[0].WorkspaceID, got[1].WorkspaceID, got[2].WorkspaceID)
	}

	all := SwitchCandidates(root, workspaces, panes, true)
	if len(all) != 4 {
		t.Fatalf("got %d with --all, want 4 (owned): %+v", len(all), all)
	}
	if all[3].WorkspaceID != "done" {
		t.Fatalf("expected done to sort last, got %s", all[3].WorkspaceID)
	}
}

func TestStatusWorkspacesIncludesDoneAndOrdersByUrgency(t *testing.T) {
	root := "/r"
	workspaces, panes := fleet(root, map[string]string{
		"done": AgentDone, "idle": AgentIdle, "blocked": AgentBlocked,
	})

	got := StatusWorkspaces(root, workspaces, panes)
	if len(got) != 3 {
		t.Fatalf("got %d, want 3 (owned): %+v", len(got), got)
	}
	if got[0].WorkspaceID != "blocked" || got[2].WorkspaceID != "done" {
		t.Fatalf("wrong order: %s ... %s", got[0].WorkspaceID, got[2].WorkspaceID)
	}
}

func TestStatusLineIsStatusThenLabel(t *testing.T) {
	line := statusLine(ws("w1", AgentBlocked))
	if line != "blocked\tw1" {
		t.Fatalf("got %q, want %q", line, "blocked\tw1")
	}
}

func TestSwitchLinesRoundTripToWorkspaceID(t *testing.T) {
	workspaces := []HerdrWorkspace{
		ws("w1", AgentWorking),
		ws("w2", AgentIdle),
	}
	lines, byLine := switchLines(workspaces)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	if byLine[lines[0]] != "w1" || byLine[lines[1]] != "w2" {
		t.Fatalf("line->id map wrong: %v", byLine)
	}
	// The status must be visible in the line.
	requireContains(t, lines[0], "working")
}
