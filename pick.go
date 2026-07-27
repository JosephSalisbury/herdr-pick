package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

// fzfCancelled is fzf's exit code for an interrupted selection. Exit code 1
// means the query matched nothing; both are user cancellation, not failure.
const fzfCancelled = 130

var pickCmd = &cobra.Command{
	Use:   "pick",
	Short: "Show the fzf picker and open the selection",
	RunE:  runPick,
}

func runPick(cmd *cobra.Command, _ []string) error {
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
	if len(candidates) == 0 {
		return fmt.Errorf("no candidates: add an 'orgs' list to %s, then run 'herdr-pick refresh'", configHint())
	}

	selection, err := runFzf(cmd.Context(), candidates)
	if err != nil {
		return err
	}
	if selection == "" {
		return nil
	}

	// An existing worktree already names its branch; a bare repo needs one.
	branch := ""
	if !strings.Contains(selection, "@") {
		branch = promptBranch()
	}

	herdr, err := NewSocketHerdr()
	if err != nil {
		return err
	}
	if err := Ping(cmd.Context(), herdr); err != nil {
		return err
	}

	path, err := Open(cmd.Context(), &DefaultExecutor{}, herdr, cfg, root, selection, branch)
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

// runFzf pipes the candidates through fzf and returns the chosen line, or an
// empty string if the user cancelled.
func runFzf(ctx context.Context, candidates []Candidate) (string, error) {
	lines := make([]string, len(candidates))
	for i, c := range candidates {
		lines[i] = c.String()
	}
	return runFzfLines(ctx, "project> ", lines)
}

// runFzfLines pipes lines through fzf and returns the chosen one, or an empty
// string if the user cancelled.
func runFzfLines(ctx context.Context, prompt string, lines []string) (string, error) {
	var in bytes.Buffer
	for _, line := range lines {
		in.WriteString(line)
		in.WriteByte('\n')
	}

	cmd := exec.CommandContext(ctx, "fzf", "--prompt", prompt)
	cmd.Stdin = &in
	cmd.Stderr = os.Stderr

	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && (exitErr.ExitCode() == 1 || exitErr.ExitCode() == fzfCancelled) {
			return "", nil
		}
		return "", fmt.Errorf("running fzf: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// promptBranch asks for a branch name, falling back to a generated one when
// the user just presses enter.
func promptBranch() string {
	generated := GenerateName()
	fmt.Fprintf(os.Stderr, "branch [%s]: ", generated)

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return generated
	}
	if line = strings.TrimSpace(line); line != "" {
		return line
	}
	return generated
}
