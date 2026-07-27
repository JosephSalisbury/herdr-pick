package main

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestReadCacheMissingIsEmpty(t *testing.T) {
	repos, err := ReadCache(t.TempDir(), "giantswarm")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repos) != 0 {
		t.Fatalf("got %v, want empty", repos)
	}
}

func TestWriteThenReadCache(t *testing.T) {
	root := t.TempDir()
	want := []string{"giantswarm/a", "giantswarm/b"}
	if err := WriteCache(root, "giantswarm", want); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := ReadCache(root, "giantswarm")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestWriteCacheReplacesPrevious(t *testing.T) {
	root := t.TempDir()
	if err := WriteCache(root, "org", []string{"org/old"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := WriteCache(root, "org", []string{"org/new"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := ReadCache(root, "org")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != "org/new" {
		t.Fatalf("got %v, want [org/new]", got)
	}
}

// A refresh that fails must not leave a partial cache behind.
func TestWriteCacheLeavesNoTempFiles(t *testing.T) {
	root := t.TempDir()
	if err := WriteCache(root, "org", []string{"org/a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entries, err := os.ReadDir(CacheDir(root))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "org.txt" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("got %v, want [org.txt]", names)
	}
}

func TestCacheStale(t *testing.T) {
	root := t.TempDir()
	now := time.Now()

	stale, err := CacheStale(root, "org", time.Hour, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !stale {
		t.Fatal("expected a missing cache to be stale")
	}

	if err := WriteCache(root, "org", []string{"org/a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stale, err = CacheStale(root, "org", time.Hour, now); err != nil || stale {
		t.Fatalf("expected fresh cache, got stale=%v err=%v", stale, err)
	}
	if stale, err = CacheStale(root, "org", time.Hour, now.Add(2*time.Hour)); err != nil || !stale {
		t.Fatalf("expected stale cache, got stale=%v err=%v", stale, err)
	}
}

func TestStaleOrgs(t *testing.T) {
	root := t.TempDir()
	if err := WriteCache(root, "fresh", []string{"fresh/a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	stale, err := StaleOrgs(root, []string{"fresh", "missing"}, time.Hour, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stale) != 1 || stale[0] != "missing" {
		t.Fatalf("got %v, want [missing]", stale)
	}
}

func TestCachedReposMergesAndDeduplicates(t *testing.T) {
	root := t.TempDir()
	if err := WriteCache(root, "a", []string{"a/two", "shared/repo"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := WriteCache(root, "b", []string{"b/one", "shared/repo"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := CachedRepos(root, []string{"a", "b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"a/two", "b/one", "shared/repo"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// A byte-value sort puts every "Zebra/*" ahead of every "apple/*", because
// 'Z' (0x5A) sorts below 'a' (0x61). That is the bug that stacked all
// JosephSalisbury repos ahead of all giantswarm ones.
func TestCachedReposSortsCaseInsensitively(t *testing.T) {
	root := t.TempDir()
	if err := WriteCache(root, "Zebra", []string{"Zebra/one"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := WriteCache(root, "apple", []string{"apple/one"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := CachedRepos(root, []string{"Zebra", "apple"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"apple/one", "Zebra/one"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// Within one org, repos order case-insensitively too.
func TestCachedReposFoldsWithinOrg(t *testing.T) {
	root := t.TempDir()
	if err := WriteCache(root, "org", []string{"org/Zebra", "org/apple"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := CachedRepos(root, []string{"org"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0] != "org/apple" {
		t.Fatalf("got %v, want apple before Zebra", got)
	}
}

func TestRefreshOrgsWritesEveryCache(t *testing.T) {
	root := t.TempDir()
	executor := &fakeExecutor{outputs: map[string]string{
		"gh": `[{"nameWithOwner":"x/one"},{"nameWithOwner":"x/two"}]`,
	}}

	if err := RefreshOrgs(context.Background(), executor, root, []string{"a", "b"}, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, org := range []string{"a", "b"} {
		repos, err := ReadCache(root, org)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(repos) != 2 {
			t.Fatalf("org %s: got %v, want 2 repos", org, repos)
		}
	}
}

// One failing org must not stop the others from refreshing.
func TestRefreshOrgsReportsFailures(t *testing.T) {
	root := t.TempDir()
	executor := &fakeExecutor{err: errors.New("gh exploded")}

	err := RefreshOrgs(context.Background(), executor, root, []string{"a", "b"}, false)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	requireContains(t, err.Error(), "gh exploded")
}
