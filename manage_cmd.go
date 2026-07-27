package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// connectHerdr opens the herdr socket and verifies it is live and speaking the
// expected protocol. The management commands all begin this way.
func connectHerdr(ctx context.Context) (Herdr, error) {
	herdr, err := NewSocketHerdr()
	if err != nil {
		return nil, err
	}
	if err := Ping(ctx, herdr); err != nil {
		return nil, err
	}
	return herdr, nil
}

var (
	cleanForce  bool
	cleanDryRun bool
)

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove worktrees whose agent has finished",
	Long: "Removes every herdr-pick worktree whose agent reports it is done, " +
		"closing the workspace and deleting the checkout. Only worktrees " +
		"herdr-pick created are touched. A checkout with uncommitted changes " +
		"is kept unless --force is given.",
	RunE: func(cmd *cobra.Command, _ []string) error {
		_, root, err := loadConfig()
		if err != nil {
			return err
		}
		herdr, err := connectHerdr(cmd.Context())
		if err != nil {
			return err
		}

		if cleanDryRun {
			workspaces, err := WorkspaceList(cmd.Context(), herdr)
			if err != nil {
				return err
			}
			done := DoneWorkspaces(root, workspaces)
			if len(done) == 0 {
				fmt.Fprintln(os.Stderr, "no done worktrees")
				return nil
			}
			for _, ws := range done {
				fmt.Println(ws.Worktree.CheckoutPath)
			}
			return nil
		}

		removed, err := CleanDone(cmd.Context(), herdr, root, cleanForce)
		for _, path := range removed {
			fmt.Println(path)
		}
		if err != nil {
			return err
		}
		if len(removed) == 0 {
			fmt.Fprintln(os.Stderr, "no done worktrees")
		}
		return nil
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show every herdr-pick worktree and its agent status",
	Long: "Prints one line per open herdr-pick worktree as `status<TAB>label`, " +
		"ordered by urgency (blocked, working, idle, done). Read-only and " +
		"pipeable — a glance at what every agent is doing.",
	RunE: func(cmd *cobra.Command, _ []string) error {
		_, root, err := loadConfig()
		if err != nil {
			return err
		}
		herdr, err := connectHerdr(cmd.Context())
		if err != nil {
			return err
		}

		workspaces, err := WorkspaceList(cmd.Context(), herdr)
		if err != nil {
			return err
		}
		owned := StatusWorkspaces(root, workspaces)
		if len(owned) == 0 {
			fmt.Fprintln(os.Stderr, "no open worktrees")
			return nil
		}
		for _, ws := range owned {
			fmt.Println(statusLine(ws))
		}
		return nil
	},
}

var switchAll bool

var switchCmd = &cobra.Command{
	Use:     "switch",
	Aliases: []string{"active"},
	Short:   "Switch to a worktree with a running agent",
	Long: "Lists herdr-pick worktrees with an agent still in flight and focuses " +
		"the one you pick. --all includes finished and agent-less worktrees too.",
	RunE: func(cmd *cobra.Command, _ []string) error {
		_, root, err := loadConfig()
		if err != nil {
			return err
		}
		herdr, err := connectHerdr(cmd.Context())
		if err != nil {
			return err
		}

		workspaces, err := WorkspaceList(cmd.Context(), herdr)
		if err != nil {
			return err
		}
		candidates := SwitchCandidates(root, workspaces, switchAll)
		if len(candidates) == 0 {
			fmt.Fprintln(os.Stderr, "no active worktrees")
			return nil
		}

		lines, byLine := switchLines(candidates)
		selection, err := runFzfLines(cmd.Context(), "switch> ", lines)
		if err != nil {
			return err
		}
		if selection == "" {
			return nil
		}
		id, ok := byLine[selection]
		if !ok {
			return fmt.Errorf("unknown selection %q", selection)
		}
		if err := WorkspaceFocus(cmd.Context(), herdr, id); err != nil {
			return err
		}
		fmt.Println(selection)
		return nil
	},
}

// switchLines renders workspaces as picker lines annotated with agent status,
// alongside a map back to the workspace id fzf cannot carry.
func switchLines(workspaces []HerdrWorkspace) ([]string, map[string]string) {
	lines := make([]string, 0, len(workspaces))
	byLine := make(map[string]string, len(workspaces))
	for _, ws := range workspaces {
		line := fmt.Sprintf("%s  [%s]", workspaceLabel(ws), ws.AgentStatus)
		lines = append(lines, line)
		byLine[line] = ws.WorkspaceID
	}
	return lines, byLine
}

func init() {
	cleanCmd.Flags().BoolVar(&cleanForce, "force", false, "remove even with uncommitted changes")
	cleanCmd.Flags().BoolVar(&cleanDryRun, "dry-run", false, "list what would be removed without removing it")
	switchCmd.Flags().BoolVar(&switchAll, "all", false, "include finished and agent-less worktrees")
	rootCmd.AddCommand(cleanCmd, switchCmd, statusCmd)
}
