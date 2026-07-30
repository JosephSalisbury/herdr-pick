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

// listWorkspacesAndPanes fetches what the management commands both need: the
// workspaces, and every pane in the session so each workspace can be located on
// disk. Two calls, because a directory-backed workspace carries no path of its
// own for herdr to report.
func listWorkspacesAndPanes(ctx context.Context, h Herdr) ([]HerdrWorkspace, []HerdrPane, error) {
	workspaces, err := WorkspaceList(ctx, h)
	if err != nil {
		return nil, nil, err
	}
	panes, err := PaneList(ctx, h, "")
	if err != nil {
		return nil, nil, err
	}
	return workspaces, panes, nil
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show every open namespace and its agent status",
	Long: "Prints one line per open namespace as `status<TAB>label`, " +
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

		workspaces, panes, err := listWorkspacesAndPanes(cmd.Context(), herdr)
		if err != nil {
			return err
		}
		owned := StatusWorkspaces(root, workspaces, panes)
		if len(owned) == 0 {
			fmt.Fprintln(os.Stderr, "no open namespaces")
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
	Short:   "Switch to a namespace with a running agent",
	Long: "Lists namespaces with an agent still in flight and focuses " +
		"the one you pick. --all includes finished and agent-less namespaces too.",
	RunE: func(cmd *cobra.Command, _ []string) error {
		_, root, err := loadConfig()
		if err != nil {
			return err
		}
		herdr, err := connectHerdr(cmd.Context())
		if err != nil {
			return err
		}

		workspaces, panes, err := listWorkspacesAndPanes(cmd.Context(), herdr)
		if err != nil {
			return err
		}
		candidates := SwitchCandidates(root, workspaces, panes, switchAll)
		if len(candidates) == 0 {
			fmt.Fprintln(os.Stderr, "no active namespaces")
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
	switchCmd.Flags().BoolVar(&switchAll, "all", false, "include finished and agent-less namespaces")
	rootCmd.AddCommand(switchCmd, statusCmd)
}
