package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CandidateKind distinguishes where a picker entry came from.
type CandidateKind string

const (
	// CandidateWorktree is an existing checkout on disk.
	CandidateWorktree CandidateKind = "worktree"
	// CandidateRepo is a repository known only from the GitHub cache.
	CandidateRepo CandidateKind = "repo"
)

// Candidate is a single selectable entry in the picker.
type Candidate struct {
	Kind   CandidateKind
	Org    string
	Repo   string
	Branch string
	Path   string
}

// String renders the candidate as one picker line. The rendering is the only
// thing that survives the trip through fzf, so it must round-trip via
// ParseSelection.
func (c Candidate) String() string {
	if c.Kind == CandidateWorktree {
		return fmt.Sprintf("%s/%s@%s", c.Org, c.Repo, c.Branch)
	}
	return fmt.Sprintf("%s/%s", c.Org, c.Repo)
}

// ParseSelection parses a picker line back into its parts. An empty branch
// means the line referred to a repository rather than an existing worktree.
func ParseSelection(line string) (org, repo, branch string, err error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", "", errors.New("empty selection")
	}

	spec, branch, hasBranch := strings.Cut(line, "@")
	if hasBranch && branch == "" {
		return "", "", "", fmt.Errorf("malformed selection %q", line)
	}

	org, repo, hasSlash := strings.Cut(spec, "/")
	if !hasSlash || org == "" || repo == "" || strings.Contains(repo, "/") {
		return "", "", "", fmt.Errorf("malformed selection %q", line)
	}
	return org, repo, branch, nil
}

// DiscoverWorktrees walks the worktree root and returns the checkouts found.
// The filesystem is the source of truth; herdr does not need to be running.
func DiscoverWorktrees(root string) ([]Candidate, error) {
	base := filepath.Join(root, "worktrees")

	orgs, err := readDirNames(base)
	if err != nil {
		return nil, err
	}

	var out []Candidate
	for _, org := range orgs {
		repos, err := readDirNames(filepath.Join(base, org))
		if err != nil {
			return nil, err
		}
		for _, repo := range repos {
			branches, err := readDirNames(filepath.Join(base, org, repo))
			if err != nil {
				return nil, err
			}
			for _, branch := range branches {
				path := filepath.Join(base, org, repo, branch)
				// A linked worktree has a .git file, not a directory.
				if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
					continue
				}
				out = append(out, Candidate{
					Kind:   CandidateWorktree,
					Org:    org,
					Repo:   repo,
					Branch: branch,
					Path:   path,
				})
			}
		}
	}

	sort.Slice(out, func(i, j int) bool { return lessFold(out[i].String(), out[j].String()) })
	return out, nil
}

// BuildCandidates returns picker entries: live worktrees first, then every
// cached repository. Worktrees lead because switching to work already in
// flight is the more common case.
func BuildCandidates(root string, orgs []string) ([]Candidate, error) {
	out, err := DiscoverWorktrees(root)
	if err != nil {
		return nil, err
	}

	repos, err := CachedRepos(root, orgs)
	if err != nil {
		return nil, err
	}
	for _, full := range repos {
		org, repo, found := strings.Cut(full, "/")
		if !found || org == "" || repo == "" {
			continue
		}
		out = append(out, Candidate{Kind: CandidateRepo, Org: org, Repo: repo})
	}
	return out, nil
}

// readDirNames returns the subdirectory names of dir, treating a missing
// directory as empty.
func readDirNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}
