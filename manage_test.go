package main

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOwnsWorktree(t *testing.T) {
	root := "/data/herdr-pick"

	owned := filepath.Join(root, "giantswarm", "foo", "iron-lich")
	if !OwnsWorktree(root, owned) {
		t.Fatalf("expected %q to be owned", owned)
	}

	for _, foreign := range []string{
		"",
		"/somewhere/else",
		// The clone is a worktree source, never a workspace.
		filepath.Join(root, "giantswarm", "foo", ".bare"),
		"/data/herdr-pick-evil/giantswarm/foo/x", // prefix but not a child
		root,
	} {
		if OwnsWorktree(root, foreign) {
			t.Fatalf("expected %q not to be owned", foreign)
		}
	}
}

// ws is a compact workspace builder for the management tests.
func ws(id, checkout, status string) HerdrWorkspace {
	w := HerdrWorkspace{WorkspaceID: id, Label: id, AgentStatus: status}
	if checkout != "" {
		w.Worktree = &HerdrWorkspaceWorktree{CheckoutPath: checkout}
	}
	return w
}

func TestDoneWorkspacesFiltersToOwnedAndDone(t *testing.T) {
	root := "/r"
	base := filepath.Join(root, "o", "r")
	workspaces := []HerdrWorkspace{
		ws("a", filepath.Join(base, "a"), AgentDone),
		ws("b", filepath.Join(base, "b"), AgentWorking), // not done
		ws("c", "/elsewhere/c", AgentDone),              // done but not ours
		ws("d", "", AgentDone),                          // no worktree
		ws("e", filepath.Join(base, "e"), AgentDone),
	}

	got := DoneWorkspaces(root, workspaces)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2: %+v", len(got), got)
	}
	ids := map[string]bool{got[0].WorkspaceID: true, got[1].WorkspaceID: true}
	if !ids["a"] || !ids["e"] {
		t.Fatalf("expected a and e, got %v", ids)
	}
}

func TestSwitchCandidatesActiveOnlyByDefault(t *testing.T) {
	root := "/r"
	base := filepath.Join(root, "o", "r")
	workspaces := []HerdrWorkspace{
		ws("idle", filepath.Join(base, "idle"), AgentIdle),
		ws("working", filepath.Join(base, "working"), AgentWorking),
		ws("done", filepath.Join(base, "done"), AgentDone),
		ws("blocked", filepath.Join(base, "blocked"), AgentBlocked),
		ws("foreign", "/elsewhere", AgentWorking),
	}

	got := SwitchCandidates(root, workspaces, false)
	if len(got) != 3 {
		t.Fatalf("got %d active, want 3: %+v", len(got), got)
	}
	// blocked sorts first, then working, then idle.
	if got[0].WorkspaceID != "blocked" || got[1].WorkspaceID != "working" || got[2].WorkspaceID != "idle" {
		t.Fatalf("wrong order: %s %s %s", got[0].WorkspaceID, got[1].WorkspaceID, got[2].WorkspaceID)
	}

	all := SwitchCandidates(root, workspaces, true)
	if len(all) != 4 {
		t.Fatalf("got %d with --all, want 4 (owned): %+v", len(all), all)
	}
	if all[3].WorkspaceID != "done" {
		t.Fatalf("expected done to sort last, got %s", all[3].WorkspaceID)
	}
}

func TestCleanDoneRemovesOnlyOwnedDone(t *testing.T) {
	root := "/r"
	base := filepath.Join(root, "o", "r")
	herdr := &fakeHerdr{results: map[string]string{
		"workspace.list": `{"type":"workspace_list","workspaces":[
			{"workspace_id":"a","label":"a","agent_status":"done","worktree":{"checkout_path":"` + filepath.Join(base, "a") + `"}},
			{"workspace_id":"b","label":"b","agent_status":"working","worktree":{"checkout_path":"` + filepath.Join(base, "b") + `"}},
			{"workspace_id":"c","label":"c","agent_status":"done","worktree":{"checkout_path":"/elsewhere/c"}}
		]}`,
		"worktree.remove": `{"type":"worktree_removed","path":"` + filepath.Join(base, "a") + `","workspace_id":"a","forced":false}`,
	}}

	removed, err := CleanDone(context.Background(), herdr, root, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(removed) != 1 {
		t.Fatalf("got %d removed, want 1: %v", len(removed), removed)
	}
	// Only workspace "a" is both ours and done.
	if got, _ := herdr.paramsFor("worktree.remove")["workspace_id"].(string); got != "a" {
		t.Fatalf("removed workspace %q, want a", got)
	}
}

func TestStatusWorkspacesIncludesDoneAndOrdersByUrgency(t *testing.T) {
	root := "/r"
	base := filepath.Join(root, "o", "r")
	workspaces := []HerdrWorkspace{
		ws("done", filepath.Join(base, "done"), AgentDone),
		ws("idle", filepath.Join(base, "idle"), AgentIdle),
		ws("blocked", filepath.Join(base, "blocked"), AgentBlocked),
		ws("foreign", "/elsewhere", AgentWorking),
	}

	got := StatusWorkspaces(root, workspaces)
	if len(got) != 3 {
		t.Fatalf("got %d, want 3 (owned): %+v", len(got), got)
	}
	if got[0].WorkspaceID != "blocked" || got[2].WorkspaceID != "done" {
		t.Fatalf("wrong order: %s ... %s", got[0].WorkspaceID, got[2].WorkspaceID)
	}
}

func TestStatusLineIsStatusThenLabel(t *testing.T) {
	line := statusLine(ws("w1", "/wt/a", AgentBlocked))
	if line != "blocked\tw1" {
		t.Fatalf("got %q, want %q", line, "blocked\tw1")
	}
}

func TestSwitchLinesRoundTripToWorkspaceID(t *testing.T) {
	workspaces := []HerdrWorkspace{
		ws("w1", "/wt/a", AgentWorking),
		ws("w2", "/wt/b", AgentIdle),
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
