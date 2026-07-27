package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultRoot     = "~/.local/share/herdr-pick"
	defaultCacheTTL = "6h"
	defaultAgent    = "claude"
)

// Config holds herdr-pick configuration.
type Config struct {
	Orgs     []string `yaml:"orgs"`
	Root     string   `yaml:"root"`
	CacheTTL string   `yaml:"cache_ttl"`
	Agent    []string `yaml:"agent"`

	// IncludeArchived pulls archived repositories into the picker. Off by
	// default: archived repos are the majority of some orgs and are rarely
	// what you want to start work on.
	IncludeArchived bool `yaml:"include_archived"`
}

// DefaultConfig returns the configuration used when no config file exists.
func DefaultConfig() Config {
	return Config{
		Root:     defaultRoot,
		CacheTTL: defaultCacheTTL,
		Agent:    []string{defaultAgent},
	}
}

// configHome returns the XDG-style config directory.
//
// Deliberately not os.UserConfigDir: on macOS that yields
// ~/Library/Application Support, but herdr keeps its own config and socket
// under ~/.config on macOS as well as Linux. Following herdr matters more here
// than following the platform convention, since we have to find its socket.
func configHome() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".config"), nil
}

// DefaultConfigPath returns the default configuration file location.
func DefaultConfigPath() (string, error) {
	dir, err := configHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "herdr-pick", "config.yaml"), nil
}

// LoadConfig reads configuration from path, returning defaults if it is absent.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("reading config: %w", err)
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate checks that the configuration is usable.
func (c Config) Validate() error {
	for _, org := range c.Orgs {
		if org == "" {
			return errors.New("config lists an empty org")
		}
		if strings.ContainsAny(org, "/\\") {
			return fmt.Errorf("org %q must not contain a path separator", org)
		}
	}
	if _, err := c.TTL(); err != nil {
		return err
	}
	return nil
}

// TTL returns the parsed cache staleness threshold.
func (c Config) TTL() (time.Duration, error) {
	raw := c.CacheTTL
	if raw == "" {
		raw = defaultCacheTTL
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parsing cache_ttl %q: %w", raw, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("cache_ttl must be positive, got %q", raw)
	}
	return d, nil
}

// RootDir returns the expanded root directory for clones, worktrees and caches.
func (c Config) RootDir() (string, error) {
	raw := c.Root
	if raw == "" {
		raw = defaultRoot
	}
	return ExpandHome(raw)
}

// AgentArgv returns the command used to launch the agent in a new workspace.
func (c Config) AgentArgv() []string {
	if len(c.Agent) == 0 {
		return []string{defaultAgent}
	}
	return c.Agent
}

// ExpandHome expands a leading ~ in path to the user's home directory.
func ExpandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, path[2:]), nil
}

// RepoDir returns the parent clone directory for a repository.
func RepoDir(root, org, repo string) string {
	return filepath.Join(root, "repos", org, repo)
}

// WorktreeDir returns the worktree checkout directory for a branch.
func WorktreeDir(root, org, repo, branch string) string {
	return filepath.Join(root, "worktrees", org, repo, branch)
}

// CacheDir returns the directory holding per-org repository caches.
func CacheDir(root string) string {
	return filepath.Join(root, "cache")
}

// CacheFile returns the cache file path for an org.
func CacheFile(root, org string) string {
	return filepath.Join(CacheDir(root), org+".txt")
}
