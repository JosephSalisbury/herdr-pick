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
	defaultAgent    = "claudebox"
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

// Everything lives under four fixed roots, and nothing user-named sits at
// <root> itself:
//
//	<root>/clones/<org>/<repo>/  bare clones, only ever worktree sources
//	<root>/ns/<name>/<repo>/     checkouts, one per namespace member
//	<root>/tmp/<name>/           temp directories, no members and no git
//	<root>/cache/<org>.txt       derived data, safe to delete
//
// Keeping orgs and namespace names a level below the roots is what removes the
// reserved-name problem altogether: no org, namespace or repo can shadow a root,
// so no walk needs a skip list. The alternative — orgs directly at <root> —
// costs one less level but shadows any org sharing a root's name.
//
// A clone needs no ".bare" suffix because a checkout is never its sibling, so
// there is nothing for it to collide with. It stays bare so the worktree source
// has no working tree to be committed into.
//
// A temp directory gets a root of its own rather than sitting in <root>/ns as a
// member-less namespace, because its *location* is then what says which it is.
// Sharing the namespace root would mean telling a deliberately empty directory
// from a create that failed partway — which is precisely what
// DiscoverNamespaces reads "no members" as — and that takes a marker file, the
// one state file this tool does not have.
const (
	cloneRoot = "clones"
	nsRoot    = "ns"
	tmpRoot   = "tmp"
	cacheRoot = "cache"
)

// CloneDir returns the bare clone directory for a repository.
func CloneDir(root, org, repo string) string {
	return filepath.Join(root, cloneRoot, org, repo)
}

// NamespaceRoot returns the directory holding every namespace.
func NamespaceRoot(root string) string {
	return filepath.Join(root, nsRoot)
}

// NamespaceDir returns a namespace's directory, which is the agent's working
// directory and the one path claudebox mounts.
func NamespaceDir(root, name string) string {
	return filepath.Join(root, nsRoot, name)
}

// MemberDir returns the checkout directory for one member of a namespace. The
// org is dropped: the agent's view of its own cwd is a flat list of repo names,
// which is the point of a namespace. Two orgs sharing a repo name therefore
// cannot both be members, and CreateNamespace rejects that at creation.
func MemberDir(root, name, repo string) string {
	return filepath.Join(root, nsRoot, name, repo)
}

// TempRoot returns the directory holding every temp directory.
func TempRoot(root string) string {
	return filepath.Join(root, tmpRoot)
}

// TempDir returns one temp directory: the agent's working directory and the one
// path claudebox mounts, as a namespace's directory is — but with nothing
// checked out inside it.
func TempDir(root, name string) string {
	return filepath.Join(root, tmpRoot, name)
}

// CacheDir returns the directory holding per-org repository caches.
func CacheDir(root string) string {
	return filepath.Join(root, cacheRoot)
}

// CacheFile returns the cache file path for an org.
func CacheFile(root, org string) string {
	return filepath.Join(CacheDir(root), org+".txt")
}
