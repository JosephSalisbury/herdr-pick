package main

import (
	"context"
	"fmt"
	"strings"
)

// Open resolves a picker selection and opens it as a herdr workspace.
//
// A selection carrying a branch (org/repo@branch) refers to a checkout that
// already exists, so it is opened and focused. A bare org/repo starts new
// work: clone if needed, create a worktree, then launch the agent in the
// workspace herdr hands back.
func Open(ctx context.Context, executor Executor, herdr Herdr, cfg Config, root, selection, branch string) (string, error) {
	return open(ctx, executor, herdr, cfg, root, selection, branch, "")
}

// open is Open with an optional prompt fed to the agent as its opening
// message. New work (a bare org/repo) launches the agent; opening an existing
// worktree ignores the prompt, since an agent is presumably already there.
func open(ctx context.Context, executor Executor, herdr Herdr, cfg Config, root, selection, branch, prompt string) (string, error) {
	org, repo, selBranch, err := ParseSelection(selection)
	if err != nil {
		return "", err
	}

	if selBranch != "" {
		path := WorktreeDir(root, org, repo, selBranch)
		if _, err := WorktreeOpen(ctx, herdr, path); err != nil {
			return "", err
		}
		return path, nil
	}

	if branch == "" {
		branch = GenerateName()
	}
	if err := ValidateBranchName(branch); err != nil {
		return "", err
	}

	cloneDir := RepoDir(root, org, repo)
	if err := EnsureClone(ctx, executor, org, repo, cloneDir); err != nil {
		return "", err
	}

	path := WorktreeDir(root, org, repo, branch)
	label := fmt.Sprintf("%s/%s@%s", org, repo, branch)
	workspace, err := WorktreeCreate(ctx, herdr, cloneDir, branch, path, label)
	if err != nil {
		return "", err
	}

	panes, err := PaneList(ctx, herdr, workspace.WorkspaceID)
	if err != nil {
		return "", err
	}
	pane, err := RootPane(panes)
	if err != nil {
		return "", fmt.Errorf("finding pane for %s: %w", label, err)
	}
	if err := RunInPane(ctx, herdr, pane, agentCommand(cfg.AgentArgv(), prompt)); err != nil {
		return "", fmt.Errorf("starting agent in %s: %w", label, err)
	}
	return path, nil
}

// agentCommand builds the shell line typed into the workspace pane: the
// configured agent, plus a shell-quoted opening prompt when one is given.
func agentCommand(argv []string, prompt string) string {
	cmd := strings.Join(argv, " ")
	if prompt != "" {
		cmd += " " + shellQuote(prompt)
	}
	return cmd
}

// shellQuote single-quotes a string so it survives as one argument when typed
// into a shell, escaping any embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
