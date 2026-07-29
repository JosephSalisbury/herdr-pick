package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// writeBareClone creates a directory that looks like a bare clone.
func writeBareClone(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating clone dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatalf("writing HEAD: %v", err)
	}
}

func TestEnsureCloneClonesBareWhenAbsent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "repos", "giantswarm", "foo")
	executor := &fakeExecutor{}

	cloned, err := EnsureClone(context.Background(), executor, "giantswarm", "foo", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cloned {
		t.Fatal("expected a fresh clone to be reported")
	}
	// Bare is what stops herdr opening the clone as a stray main workspace.
	if !executor.ran("git", "clone", "--bare", "git@github.com:giantswarm/foo.git", dir) {
		t.Fatalf("unexpected commands: %v", executor.calls)
	}
}

func TestEnsureCloneSkipsExistingBareClone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "clone")
	writeBareClone(t, dir)
	executor := &fakeExecutor{}

	cloned, err := EnsureClone(context.Background(), executor, "giantswarm", "foo", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cloned {
		t.Fatal("expected an existing clone not to be reported as fresh")
	}
	if len(executor.calls) != 0 {
		t.Fatalf("expected no commands, got %v", executor.calls)
	}
}

// A leftover non-bare checkout must produce a clear error rather than letting
// git fail with "already exists and is not an empty directory".
func TestEnsureCloneRejectsNonBareCheckout(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "clone")
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("creating .git: %v", err)
	}
	executor := &fakeExecutor{}

	_, err := EnsureClone(context.Background(), executor, "giantswarm", "foo", dir)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	requireContains(t, err.Error(), "not a bare clone")
	if len(executor.calls) != 0 {
		t.Fatalf("expected no commands, got %v", executor.calls)
	}
}

// Without this, worktree.create can only branch off main as of the first clone.
func TestFetchDefaultBranchFetchesTheBranchAtHead(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "clone")
	executor := &fakeExecutor{outputs: map[string]string{"git": "main"}}

	if err := FetchDefaultBranch(context.Background(), executor, dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !executor.ran("git", "-C", dir, "symbolic-ref", "--short", "HEAD") {
		t.Fatalf("expected HEAD to be resolved, got %v", executor.calls)
	}
	// Forced, so a rewritten main still lands. Only HEAD's branch is fetched:
	// +refs/heads/* would fail on branches checked out in a worktree.
	if !executor.ran("git", "-C", dir, "fetch", "--quiet", "origin", "+main:main") {
		t.Fatalf("expected a forced fetch of main, got %v", executor.calls)
	}
}

func TestFetchDefaultBranchErrorsWithoutABranchAtHead(t *testing.T) {
	executor := &fakeExecutor{}

	err := FetchDefaultBranch(context.Background(), executor, "/clone")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	requireContains(t, err.Error(), "no branch at HEAD")
	if executor.ran("git", "-C", "/clone", "fetch") {
		t.Fatalf("expected no fetch, got %v", executor.calls)
	}
}

func TestIsBareRepo(t *testing.T) {
	bare := filepath.Join(t.TempDir(), "bare")
	writeBareClone(t, bare)
	if !isBareRepo(bare) {
		t.Fatal("expected bare clone to be recognised")
	}
	if isBareRepo(filepath.Join(t.TempDir(), "absent")) {
		t.Fatal("expected missing dir not to be a bare clone")
	}
}
