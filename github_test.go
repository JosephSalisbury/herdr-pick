package main

import (
	"context"
	"testing"
)

func TestFetchOrgReposParsesAndSorts(t *testing.T) {
	executor := &fakeExecutor{outputs: map[string]string{
		"gh": `[{"nameWithOwner":"org/zebra"},{"nameWithOwner":"org/apple"},{"nameWithOwner":""}]`,
	}}

	got, err := FetchOrgRepos(context.Background(), executor, "org", 5000, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0] != "org/apple" || got[1] != "org/zebra" {
		t.Fatalf("got %v", got)
	}
	if !executor.ran("gh", "repo", "list", "org") {
		t.Fatalf("unexpected commands: %v", executor.calls)
	}
}

// The limit must be passed through so large orgs are not silently truncated.
func TestFetchOrgReposPassesLimit(t *testing.T) {
	executor := &fakeExecutor{outputs: map[string]string{"gh": `[]`}}
	if _, err := FetchOrgRepos(context.Background(), executor, "org", 5000, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var sawLimit bool
	for _, call := range executor.calls {
		for i, arg := range call {
			if arg == "--limit" && i+1 < len(call) && call[i+1] == "5000" {
				sawLimit = true
			}
		}
	}
	if !sawLimit {
		t.Fatalf("expected --limit 5000, got %v", executor.calls)
	}
}

func TestFetchOrgReposRejectsBadJSON(t *testing.T) {
	executor := &fakeExecutor{outputs: map[string]string{"gh": `not json`}}
	if _, err := FetchOrgRepos(context.Background(), executor, "org", 10, false); err == nil {
		t.Fatal("expected error, got nil")
	}
}

// Archived repos are the majority of some orgs, so the filter must actually
// track the config rather than being hardcoded either way.
func TestFetchOrgReposArchivedFilter(t *testing.T) {
	for _, includeArchived := range []bool{true, false} {
		executor := &fakeExecutor{outputs: map[string]string{"gh": `[]`}}
		if _, err := FetchOrgRepos(context.Background(), executor, "org", 10, includeArchived); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var sawFlag bool
		for _, call := range executor.calls {
			for _, arg := range call {
				if arg == "--no-archived" {
					sawFlag = true
				}
			}
		}
		if sawFlag == includeArchived {
			t.Fatalf("includeArchived=%v: --no-archived present=%v", includeArchived, sawFlag)
		}
	}
}

func TestFetchOrgReposSortsCaseInsensitively(t *testing.T) {
	executor := &fakeExecutor{outputs: map[string]string{
		"gh": `[{"nameWithOwner":"org/Zebra"},{"nameWithOwner":"org/apple"}]`,
	}}

	got, err := FetchOrgRepos(context.Background(), executor, "org", 10, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0] != "org/apple" {
		t.Fatalf("got %v, want apple before Zebra", got)
	}
}
