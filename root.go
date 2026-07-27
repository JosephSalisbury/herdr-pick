package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

var configPath string

var rootCmd = &cobra.Command{
	Use:           "herdr-pick",
	Short:         "Pick a project and open it as a herdr workspace",
	Long:          "Discovers projects across configured GitHub orgs and existing worktrees, then hands the chosen one to herdr as a workspace running an agent.",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "", "path to config file")
}

// resolveConfigPath returns the config file to read: the --config flag if
// given, otherwise the platform default.
func resolveConfigPath() (string, error) {
	if configPath != "" {
		return configPath, nil
	}
	return DefaultConfigPath()
}

// configHint names the config file in an error, so a missing or empty config
// says where to fix it rather than just what is wrong.
func configHint() string {
	path, err := resolveConfigPath()
	if err != nil {
		return "the config file"
	}
	return path
}

// loadConfig resolves the config file and the expanded root directory.
func loadConfig() (Config, string, error) {
	path, err := resolveConfigPath()
	if err != nil {
		return Config{}, "", err
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		return Config{}, "", err
	}
	root, err := cfg.RootDir()
	if err != nil {
		return Config{}, "", err
	}
	return cfg, root, nil
}

// maybeRefresh starts a detached cache refresh when any org cache is stale.
// The picker never waits on it: a stale list this time becomes a fresh list
// next time.
func maybeRefresh(cfg Config, root string) error {
	if len(cfg.Orgs) == 0 {
		return nil
	}

	ttl, err := cfg.TTL()
	if err != nil {
		return err
	}
	stale, err := StaleOrgs(root, cfg.Orgs, ttl, time.Now())
	if err != nil {
		return err
	}
	if len(stale) == 0 {
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving executable: %w", err)
	}

	args := []string{"refresh"}
	if configPath != "" {
		args = append(args, "--config", configPath)
	}

	// Deliberately not exec.CommandContext: the refresh must outlive the
	// command that spawned it.
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting background refresh: %w", err)
	}
	return nil
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List picker candidates, one per line",
	RunE: func(_ *cobra.Command, _ []string) error {
		cfg, root, err := loadConfig()
		if err != nil {
			return err
		}
		if err := maybeRefresh(cfg, root); err != nil {
			fmt.Fprintf(os.Stderr, "warning: %v\n", err)
		}

		candidates, err := BuildCandidates(root, cfg.Orgs)
		if err != nil {
			return err
		}
		for _, c := range candidates {
			fmt.Println(c.String())
		}
		return nil
	},
}

var refreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "Refresh the cached repository list for every configured org",
	RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, root, err := loadConfig()
		if err != nil {
			return err
		}
		if len(cfg.Orgs) == 0 {
			return fmt.Errorf("no orgs configured: add an 'orgs' list to %s", configHint())
		}
		return RefreshOrgs(cmd.Context(), &DefaultExecutor{}, root, cfg.Orgs, cfg.IncludeArchived)
	},
}

var pingCmd = &cobra.Command{
	Use:   "ping",
	Short: "Check the herdr socket is reachable and speaks the expected protocol",
	RunE: func(cmd *cobra.Command, _ []string) error {
		herdr, err := NewSocketHerdr()
		if err != nil {
			return err
		}
		if err := Ping(cmd.Context(), herdr); err != nil {
			return err
		}
		fmt.Printf("ok: %s\n", herdr.Path)
		return nil
	},
}

var openBranch string

var openCmd = &cobra.Command{
	Use:   "open <org/repo[@branch]>",
	Short: "Open a selection as a herdr workspace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, root, err := loadConfig()
		if err != nil {
			return err
		}
		herdr, err := NewSocketHerdr()
		if err != nil {
			return err
		}
		if err := Ping(cmd.Context(), herdr); err != nil {
			return err
		}

		path, err := Open(cmd.Context(), &DefaultExecutor{}, herdr, cfg, root, args[0], openBranch)
		if err != nil {
			return err
		}
		fmt.Println(path)
		return nil
	},
}

func init() {
	openCmd.Flags().StringVar(&openBranch, "branch", "", "branch name (generated if not given)")
	rootCmd.AddCommand(listCmd, refreshCmd, openCmd, pickCmd, pingCmd)
}
