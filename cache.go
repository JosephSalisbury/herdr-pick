package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// lessFold orders two strings case-insensitively, falling back to a
// case-sensitive comparison so the result is deterministic. Without this,
// "JosephSalisbury/*" would sort ahead of every "giantswarm/*" on byte value.
func lessFold(a, b string) bool {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	if la != lb {
		return la < lb
	}
	return a < b
}

// sortFold sorts strings case-insensitively in place.
func sortFold(items []string) {
	sort.Slice(items, func(i, j int) bool { return lessFold(items[i], items[j]) })
}

// ReadCache returns the cached repository list for an org. A missing cache is
// not an error: it simply yields no repositories.
func ReadCache(root, org string) ([]string, error) {
	data, err := os.ReadFile(CacheFile(root, org))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading cache for %s: %w", org, err)
	}

	var repos []string
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			repos = append(repos, line)
		}
	}
	return repos, nil
}

// WriteCache atomically replaces the cached repository list for an org.
func WriteCache(root, org string, repos []string) error {
	dir := CacheDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating cache dir: %w", err)
	}

	tmp, err := os.CreateTemp(dir, org+".*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp cache file for %s: %w", org, err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()

	content := strings.Join(repos, "\n")
	if content != "" {
		content += "\n"
	}
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing cache for %s: %w", org, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing cache for %s: %w", org, err)
	}
	if err := os.Rename(name, CacheFile(root, org)); err != nil {
		return fmt.Errorf("replacing cache for %s: %w", org, err)
	}
	return nil
}

// CacheStale reports whether an org's cache is missing or older than ttl.
func CacheStale(root, org string, ttl time.Duration, now time.Time) (bool, error) {
	info, err := os.Stat(CacheFile(root, org))
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("stating cache for %s: %w", org, err)
	}
	return now.Sub(info.ModTime()) > ttl, nil
}

// StaleOrgs returns the orgs whose caches are missing or older than ttl.
func StaleOrgs(root string, orgs []string, ttl time.Duration, now time.Time) ([]string, error) {
	var stale []string
	for _, org := range orgs {
		s, err := CacheStale(root, org, ttl, now)
		if err != nil {
			return nil, err
		}
		if s {
			stale = append(stale, org)
		}
	}
	return stale, nil
}

// CachedRepos returns the merged, deduplicated repository list across orgs.
func CachedRepos(root string, orgs []string) ([]string, error) {
	seen := make(map[string]bool)
	var all []string
	for _, org := range orgs {
		repos, err := ReadCache(root, org)
		if err != nil {
			return nil, err
		}
		for _, r := range repos {
			if !seen[r] {
				seen[r] = true
				all = append(all, r)
			}
		}
	}
	sortFold(all)
	return all, nil
}

// RefreshOrgs fetches and caches each org's repository list concurrently.
// Every org is attempted even if some fail; all failures are returned together.
func RefreshOrgs(ctx context.Context, executor Executor, root string, orgs []string, includeArchived bool) error {
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

	for _, org := range orgs {
		wg.Add(1)
		go func(org string) {
			defer wg.Done()

			repos, err := FetchOrgRepos(ctx, executor, org, repoFetchLimit, includeArchived)
			if err == nil {
				err = WriteCache(root, org, repos)
			}
			if err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}(org)
	}

	wg.Wait()
	return errors.Join(errs...)
}
