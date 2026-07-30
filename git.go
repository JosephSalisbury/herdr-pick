package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// originRefspec is the remote-tracking refspec a normal clone gets and a bare
// clone does not. Without it there is no refs/remotes/origin/*, so inside a
// worktree `git merge origin/main` cannot resolve its argument and git status
// has nothing to count ahead/behind against.
const originRefspec = "+refs/heads/*:refs/remotes/origin/*"

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

// SyncClone brings the bare clone up to date and gives it the remote-tracking
// refs a worktree needs. It answers two problems at once:
//
//   - herdr branches a new worktree off the clone's HEAD, so an unfetched clone
//     silently starts the work from whatever main was when the clone was made.
//   - `git clone --bare` copies remote heads straight into refs/heads/* and
//     writes no remote.origin.fetch, so refs/remotes/origin/* never exists and
//     merging or rebasing onto origin/main inside a worktree fails outright.
//
// Configuring the refspec is what makes the *user's* later `git fetch` and
// `git merge origin/main` behave normally; passing the refspecs explicitly here
// is what makes this fetch update both namespaces in one round trip, since an
// explicit refspec overrides the configured one.
//
// Fresh clones are synced too. `clone --bare` leaves no remote-tracking refs
// behind, so without this a brand new clone is exactly the repo that cannot
// merge main.
func SyncClone(ctx context.Context, executor Executor, dir string) error {
	if err := ensureOriginRefspec(ctx, executor, dir); err != nil {
		return err
	}

	branch, err := defaultBranch(ctx, executor, dir)
	if err != nil {
		return err
	}

	// The default branch is also mirrored into refs/heads because that is what
	// HEAD resolves to and what herdr branches from. Only the default branch:
	// +refs/heads/*:refs/heads/* would fail on any branch a worktree has checked
	// out, which here is every branch we ever create.
	head := fmt.Sprintf("+refs/heads/%s:refs/heads/%s", branch, branch)
	if _, err := executor.Run(ctx, "git", "-C", dir, "fetch", "origin", originRefspec, head); err != nil {
		return fmt.Errorf("fetching %s in %s: %w", branch, dir, err)
	}
	return nil
}

// ensureOriginRefspec gives the clone the remote-tracking refspec, replacing
// whatever is there so a clone made before this existed converges on the next
// open rather than staying broken forever.
func ensureOriginRefspec(ctx context.Context, executor Executor, dir string) error {
	if _, err := executor.Run(ctx, "git", "-C", dir, "config", "--replace-all", "remote.origin.fetch", originRefspec); err != nil {
		return fmt.Errorf("configuring origin refspec in %s: %w", dir, err)
	}
	return nil
}

// defaultBranch reads the branch the clone's HEAD points at, which for a bare
// clone is the remote's default branch as it stood at clone time.
func defaultBranch(ctx context.Context, executor Executor, dir string) (string, error) {
	out, err := executor.Run(ctx, "git", "-C", dir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolving default branch in %s: %w", dir, err)
	}
	branch := strings.TrimSpace(out)
	if branch == "" {
		return "", fmt.Errorf("no default branch in %s", dir)
	}
	return branch, nil
}

// isBareRepo reports whether dir holds a bare clone. A bare repo keeps HEAD at
// its top level; a normal checkout keeps it inside .git.
func isBareRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "HEAD"))
	return err == nil
}
