package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCandidateStringRoundTrips(t *testing.T) {
	tests := []Candidate{
		{Kind: CandidateRepo, Org: "giantswarm", Repo: "foo"},
		{Kind: CandidateWorktree, Org: "giantswarm", Repo: "foo", Branch: "swift-owlbear"},
	}

	for _, c := range tests {
		org, repo, branch, err := ParseSelection(c.String())
		if err != nil {
			t.Fatalf("%q: unexpected error: %v", c.String(), err)
		}
		if org != c.Org || repo != c.Repo || branch != c.Branch {
			t.Fatalf("%q: got %s/%s@%s", c.String(), org, repo, branch)
		}
	}
}

func TestParseSelectionRejectsMalformed(t *testing.T) {
	for _, in := range []string{"", "   ", "noslash", "a/b/c", "/b", "a/", "a/b@"} {
		if _, _, _, err := ParseSelection(in); err == nil {
			t.Fatalf("expected error for %q", in)
		}
	}
}

// writeWorktree creates a directory that looks like a linked git worktree.
func writeWorktree(t *testing.T, root, org, repo, branch string) {
	t.Helper()
	dir := WorktreeDir(root, org, repo, branch)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating worktree dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatalf("writing .git: %v", err)
	}
}

func TestDiscoverWorktrees(t *testing.T) {
	root := t.TempDir()
	writeWorktree(t, root, "giantswarm", "foo", "swift-owlbear")
	writeWorktree(t, root, "giantswarm", "bar", "iron-lich")

	// A directory with no .git is not a worktree.
	if err := os.MkdirAll(WorktreeDir(root, "giantswarm", "foo", "not-a-worktree"), 0o755); err != nil {
		t.Fatalf("creating dir: %v", err)
	}

	got, err := DiscoverWorktrees(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2: %v", len(got), got)
	}
	// Sorted by rendered name, so bar precedes foo.
	if got[0].String() != "giantswarm/bar@iron-lich" {
		t.Fatalf("got %q first", got[0].String())
	}
	if got[0].Path != WorktreeDir(root, "giantswarm", "bar", "iron-lich") {
		t.Fatalf("got path %q", got[0].Path)
	}
}

func TestDiscoverWorktreesMissingRootIsEmpty(t *testing.T) {
	got, err := DiscoverWorktrees(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

func TestBuildCandidatesPutsWorktreesFirst(t *testing.T) {
	root := t.TempDir()
	writeWorktree(t, root, "giantswarm", "foo", "swift-owlbear")
	if err := WriteCache(root, "giantswarm", []string{"giantswarm/aaa", "giantswarm/foo"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := BuildCandidates(root, []string{"giantswarm"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d candidates, want 3: %v", len(got), got)
	}
	if got[0].Kind != CandidateWorktree {
		t.Fatalf("expected a worktree first, got %v", got[0])
	}
	if got[1].Kind != CandidateRepo || got[1].String() != "giantswarm/aaa" {
		t.Fatalf("got %v second", got[1])
	}
}
