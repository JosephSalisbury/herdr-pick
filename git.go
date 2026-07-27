package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// EnsureClone bare-clones a repository into dir unless it is already there.
//
// Bare is deliberate. The clone exists only as a source for worktrees, and a
// bare repo has no working tree — so herdr cannot open it as a stray "main"
// workspace when it creates the parent for a worktree group, and nothing can
// be accidentally committed into the worktree source.
func EnsureClone(ctx context.Context, executor Executor, org, repo, dir string) error {
	if isBareRepo(dir) {
		return nil
	}
	// A non-bare checkout here is left over from an earlier layout. Say so,
	// rather than letting git fail with "already exists and is not empty".
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("%s exists but is not a bare clone: remove it and retry", dir)
	}

	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return fmt.Errorf("creating clone parent dir: %w", err)
	}

	url := fmt.Sprintf("git@github.com:%s/%s.git", org, repo)
	if _, err := executor.Run(ctx, "git", "clone", "--bare", url, dir); err != nil {
		return fmt.Errorf("cloning %s/%s: %w", org, repo, err)
	}
	return nil
}

// isBareRepo reports whether dir holds a bare clone. A bare repo keeps HEAD at
// its top level; a normal checkout keeps it inside .git.
func isBareRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "HEAD"))
	return err == nil
}
