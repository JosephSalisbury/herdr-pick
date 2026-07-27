package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

// repoFetchLimit bounds how many repositories are pulled per org. It is set
// well above the largest org in use so lists are never silently truncated.
const repoFetchLimit = 5000

// ghRepo is a single entry from `gh repo list --json nameWithOwner`.
type ghRepo struct {
	NameWithOwner string `json:"nameWithOwner"`
}

// FetchOrgRepos lists an org's repositories via the gh CLI.
func FetchOrgRepos(ctx context.Context, executor Executor, org string, limit int, includeArchived bool) ([]string, error) {
	args := []string{
		"repo", "list", org,
		"--limit", strconv.Itoa(limit),
		"--json", "nameWithOwner",
	}
	if !includeArchived {
		args = append(args, "--no-archived")
	}

	out, err := executor.Run(ctx, "gh", args...)
	if err != nil {
		return nil, fmt.Errorf("listing repos for %s: %w", org, err)
	}

	var repos []ghRepo
	if err := json.Unmarshal([]byte(out), &repos); err != nil {
		return nil, fmt.Errorf("parsing gh output for %s: %w", org, err)
	}

	names := make([]string, 0, len(repos))
	for _, r := range repos {
		if r.NameWithOwner != "" {
			names = append(names, r.NameWithOwner)
		}
	}
	sortFold(names)
	return names, nil
}
