package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// EnsureClone bare-clones a repository into dir unless it is already there,
// reporting whether it did the clone. A clone it just made is current; one that
// was already on disk is as old as the last time it was fetched.
//
// Bare is deliberate. The clone exists only as a source for worktrees, and a
// bare repo has no working tree — so herdr cannot open it as a stray "main"
// workspace when it creates the parent for a worktree group, and nothing can
// be accidentally committed into the worktree source.
func EnsureClone(ctx context.Context, executor Executor, org, repo, dir string) (bool, error) {
	if isBareRepo(dir) {
		return false, nil
	}
	// A non-bare checkout here is left over from an earlier layout. Say so,
	// rather than letting git fail with "already exists and is not empty".
	if _, err := os.Stat(dir); err == nil {
		return false, fmt.Errorf("%s exists but is not a bare clone: remove it and retry", dir)
	}

	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return false, fmt.Errorf("creating clone parent dir: %w", err)
	}

	url := fmt.Sprintf("git@github.com:%s/%s.git", org, repo)
	if _, err := executor.Run(ctx, "git", "clone", "--bare", url, dir); err != nil {
		return false, fmt.Errorf("cloning %s/%s: %w", org, repo, err)
	}
	return true, nil
}

// FetchDefaultBranch updates the bare clone's default branch from origin, so a
// new worktree branches off current main rather than main as of whenever the
// clone was first made.
//
// Only the branch HEAD points at is fetched. That is the one worktree.create
// bases a new checkout on — a bare clone has no remote-tracking refs, so HEAD
// is all it can branch from. Fetching every head instead (+refs/heads/*) would
// fail on any branch already checked out in a worktree, which here is most of
// them.
func FetchDefaultBranch(ctx context.Context, executor Executor, dir string) error {
	branch, err := headBranch(ctx, executor, dir)
	if err != nil {
		return err
	}
	// Forced: the clone is only ever a worktree source and is never committed
	// into, so overwriting its default branch cannot discard work. Without the
	// '+' a rewritten main (a force-push, a squashed merge) would be rejected
	// and leave the stale ref in place.
	refspec := fmt.Sprintf("+%s:%s", branch, branch)
	if _, err := executor.Run(ctx, "git", "-C", dir, "fetch", "--quiet", "origin", refspec); err != nil {
		return fmt.Errorf("fetching %s in %s: %w", branch, dir, err)
	}
	return nil
}

// headBranch reports the branch the clone's HEAD points at.
func headBranch(ctx context.Context, executor Executor, dir string) (string, error) {
	out, err := executor.Run(ctx, "git", "-C", dir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", fmt.Errorf("reading HEAD of %s: %w", dir, err)
	}
	if out == "" {
		return "", fmt.Errorf("%s has no branch at HEAD", dir)
	}
	return out, nil
}

// isBareRepo reports whether dir holds a bare clone. A bare repo keeps HEAD at
// its top level; a normal checkout keeps it inside .git.
func isBareRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "HEAD"))
	return err == nil
}
