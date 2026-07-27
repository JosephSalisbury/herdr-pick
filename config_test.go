package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfigMissingFileReturnsDefaults(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Root != defaultRoot {
		t.Fatalf("got root %q, want %q", cfg.Root, defaultRoot)
	}
	if got := cfg.AgentArgv(); len(got) != 1 || got[0] != defaultAgent {
		t.Fatalf("got agent %v, want [%s]", got, defaultAgent)
	}
}

func TestLoadConfigParsesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "orgs:\n  - giantswarm\n  - JosephSalisbury\nroot: /tmp/hp\ncache_ttl: 2h\nagent: [claude, --resume]\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Orgs) != 2 || cfg.Orgs[0] != "giantswarm" {
		t.Fatalf("got orgs %v", cfg.Orgs)
	}

	root, err := cfg.RootDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if root != "/tmp/hp" {
		t.Fatalf("got root %q", root)
	}

	ttl, err := cfg.TTL()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ttl != 2*time.Hour {
		t.Fatalf("got ttl %v, want 2h", ttl)
	}
	if got := cfg.AgentArgv(); len(got) != 2 || got[1] != "--resume" {
		t.Fatalf("got agent %v", got)
	}
}

func TestLoadConfigPartialFileKeepsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("orgs:\n  - giantswarm\n"), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.CacheTTL != defaultCacheTTL {
		t.Fatalf("got cache_ttl %q, want %q", cfg.CacheTTL, defaultCacheTTL)
	}
}

func TestLoadConfigRejectsBadValues(t *testing.T) {
	tests := map[string]string{
		"bad ttl":        "cache_ttl: not-a-duration\n",
		"negative ttl":   "cache_ttl: -5m\n",
		"org with slash": "orgs:\n  - giant/swarm\n",
		"empty org":      "orgs:\n  - \"\"\n",
		"malformed yaml": "orgs: [unclosed\n",
	}

	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatalf("writing config: %v", err)
			}
			if _, err := LoadConfig(path); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

// herdr keeps its config and socket under ~/.config on macOS as well as
// Linux, so this must not follow os.UserConfigDir onto ~/Library.
func TestDefaultConfigPathFollowsXDGNotPlatform(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")

	got, err := DefaultConfigPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "/xdg/herdr-pick/config.yaml"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDefaultConfigPathFallsBackToDotConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}

	got, err := DefaultConfigPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := filepath.Join(home, ".config", "herdr-pick", "config.yaml"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}

	got, err := ExpandHome("~/foo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := filepath.Join(home, "foo"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	if got, err := ExpandHome("/absolute"); err != nil || got != "/absolute" {
		t.Fatalf("got %q, %v", got, err)
	}
	// A bare ~ prefix without a separator is not a home reference.
	if got, err := ExpandHome("~user/foo"); err != nil || got != "~user/foo" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestPathLayout(t *testing.T) {
	root := "/r"
	if got, want := RepoDir(root, "o", "p"), "/r/repos/o/p"; got != want {
		t.Fatalf("RepoDir got %q, want %q", got, want)
	}
	if got, want := WorktreeDir(root, "o", "p", "b"), "/r/worktrees/o/p/b"; got != want {
		t.Fatalf("WorktreeDir got %q, want %q", got, want)
	}
	if got, want := CacheFile(root, "o"), "/r/cache/o.txt"; got != want {
		t.Fatalf("CacheFile got %q, want %q", got, want)
	}
}

// Two orgs sharing a repo name must not collide on disk, which is why the org
// is part of every path.
func TestWorktreeDirIsOrgScoped(t *testing.T) {
	a := WorktreeDir("/r", "giantswarm", "cluster-api", "main")
	b := WorktreeDir("/r", "kubernetes-sigs", "cluster-api", "main")
	if a == b {
		t.Fatalf("expected distinct paths, both were %q", a)
	}
}
