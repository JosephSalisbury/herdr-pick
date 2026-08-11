package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
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

var listTemp bool

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List namespaces and their members, one per line",
	Long: "Prints one line per namespace as `name<TAB>member,member`. Read-only " +
		"and pipeable, and unlike status it does not need herdr to be running. " +
		"--temp lists temp directories instead, one name per line.",
	RunE: func(_ *cobra.Command, _ []string) error {
		_, root, err := loadConfig()
		if err != nil {
			return err
		}

		// A separate flag rather than more lines in the same output: this prints
		// `name<TAB>member,member` and a temp directory has no members, so it has
		// nothing to say in that shape. It is also the only way to enumerate temp
		// directories for deletion, which nothing else does for you.
		if listTemp {
			temps, err := DiscoverTemps(root)
			if err != nil {
				return err
			}
			if len(temps) == 0 {
				fmt.Fprintln(os.Stderr, "no temp directories")
				return nil
			}
			for _, t := range temps {
				fmt.Println(t.Name)
			}
			return nil
		}

		namespaces, err := DiscoverNamespaces(root)
		if err != nil {
			return err
		}
		if len(namespaces) == 0 {
			fmt.Fprintln(os.Stderr, "no namespaces")
			return nil
		}
		for _, ns := range namespaces {
			fmt.Printf("%s\t%s\n", ns.Name, strings.Join(ns.Members, ","))
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

// newCmd and openCmd are the non-interactive halves of pick: the same two verbs
// without fzf or a prompt, for scripting.

var newCmd = &cobra.Command{
	Use:   "new <name> <org/repo>...",
	Short: "Create a namespace over one or more repositories and start an agent",
	Long: "Checks out every named repository on a branch named after the namespace, " +
		"opens it as one herdr workspace and starts the agent over all of them.",
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, root, err := loadConfig()
		if err != nil {
			return err
		}

		name := args[0]
		members := make([]RepoRef, 0, len(args)-1)
		for _, arg := range args[1:] {
			ref, err := ParseRepo(arg)
			if err != nil {
				return err
			}
			members = append(members, ref)
		}

		// Checked before any clone or checkout, so a dead socket costs nothing.
		herdr, err := connectHerdr(cmd.Context())
		if err != nil {
			return err
		}

		ns, err := CreateNamespace(cmd.Context(), &DefaultExecutor{}, root, name, members)
		if err != nil {
			return err
		}
		return openAndReport(cmd.Context(), herdr, cfg, ns)
	},
}

var openCmd = &cobra.Command{
	Use:   "open <name>",
	Short: "Resume an existing namespace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, root, err := loadConfig()
		if err != nil {
			return err
		}

		ns, err := LoadNamespace(root, args[0])
		if err != nil {
			return err
		}
		herdr, err := connectHerdr(cmd.Context())
		if err != nil {
			return err
		}
		return openAndReport(cmd.Context(), herdr, cfg, ns)
	},
}

var tempCmd = &cobra.Command{
	Use:   "temp [name]",
	Short: "Open a temp directory with an agent in it",
	Long: "Makes an empty directory under <root>/tmp, opens it as a herdr workspace " +
		"and starts the agent in it — for trying something out that has no " +
		"repositories yet. With no name, one is generated.",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, root, err := loadConfig()
		if err != nil {
			return err
		}

		name := GenerateName()
		if len(args) == 1 {
			name = args[0]
		}

		// Checked before the directory is made, so a dead socket leaves nothing
		// behind.
		herdr, err := connectHerdr(cmd.Context())
		if err != nil {
			return err
		}

		// An existing name resumes rather than failing, unlike `new`: a temp
		// directory has no branch to collide with, so the only thing the name can
		// already mean is the directory you asked for.
		t, err := LoadTemp(root, name)
		if err != nil {
			if t, err = CreateTemp(root, name); err != nil {
				return err
			}
		}
		return openTempAndReport(cmd.Context(), herdr, cfg, t)
	},
}

func init() {
	listCmd.Flags().BoolVar(&listTemp, "temp", false, "list temp directories instead of namespaces")
	rootCmd.AddCommand(listCmd, refreshCmd, newCmd, openCmd, pickCmd, pingCmd, tempCmd)
}
