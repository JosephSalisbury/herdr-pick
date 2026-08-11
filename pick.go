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
	Short: "Show the picker and open a namespace or a temp directory",
	Long: "Lists existing namespaces, existing temp directories, a line that makes a " +
		"new temp directory, and then every cached repository. Selecting a " +
		"namespace or temp directory opens it; marking one or more repositories " +
		"starts a new namespace over them.",
	RunE: runPick,
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
	// The fixed temp line means the list is never empty, so an empty one is a
	// warning rather than the error it used to be: a temp directory is exactly
	// what is still useful with nothing configured and nothing in flight.
	if len(candidates) == 1 {
		fmt.Fprintf(os.Stderr, "warning: nothing but a temp directory to offer: add an 'orgs' list to %s, then run 'herdr-pick refresh'\n", configHint())
	}

	lines, byLine := candidateLines(candidates)
	selected, err := runFzfMulti(cmd.Context(), "project> ", lines)
	if err != nil {
		return err
	}
	if len(selected) == 0 {
		return nil
	}

	chosen := make([]Candidate, 0, len(selected))
	for _, line := range selected {
		c, ok := byLine[line]
		if !ok {
			return fmt.Errorf("unknown selection %q", line)
		}
		chosen = append(chosen, c)
	}

	// Existing work needs no name and a set of repos does, so the selection
	// itself decides the verb and there is nothing extra to confirm.
	selection, err := ResolveSelection(chosen)
	if err != nil {
		return err
	}

	// herdr is checked before any prompt, clone or checkout. Building a namespace
	// only to find there is nowhere to open it wastes a round trip to GitHub and
	// the name you just typed.
	herdr, err := connectHerdr(cmd.Context())
	if err != nil {
		return err
	}

	switch {
	case selection.Namespace != nil:
		return openAndReport(cmd.Context(), herdr, cfg, *selection.Namespace)
	case selection.Temp != nil:
		return openTempAndReport(cmd.Context(), herdr, cfg, *selection.Temp)
	case selection.NewTemp:
		return createTempFromSelection(cmd.Context(), herdr, cfg, root)
	default:
		return createFromSelection(cmd.Context(), herdr, cfg, root, selection.Repos)
	}
}

// Selection is the verb a picker selection resolved to. Exactly one field is
// set.
type Selection struct {
	// Namespace is the namespace to resume.
	Namespace *Namespace
	// Temp is the temp directory to resume.
	Temp *Temp
	// NewTemp asks for a fresh temp directory.
	NewTemp bool
	// Repos are the repositories to start a new namespace over.
	Repos []RepoRef
}

// ResolveSelection interprets a picker selection as one of the verbs.
//
// Only repositories combine. Everything else is a single thing to open — one
// namespace, one temp directory, or one new temp directory — so a selection
// mixing them, or holding two of them, is a selection that does not name one
// verb.
func ResolveSelection(chosen []Candidate) (Selection, error) {
	if len(chosen) == 0 {
		return Selection{}, errors.New("nothing selected")
	}

	repos := make([]RepoRef, 0, len(chosen))
	for _, c := range chosen {
		if c.Kind != CandidateRepo {
			break
		}
		repos = append(repos, c.Repo)
	}
	if len(repos) == len(chosen) {
		return Selection{Repos: repos}, nil
	}

	if len(chosen) > 1 {
		return Selection{}, errors.New("select either one namespace or temp directory to open, or one or more repositories to start a new namespace")
	}

	switch c := chosen[0]; c.Kind {
	case CandidateNamespace:
		return Selection{Namespace: &c.Namespace}, nil
	case CandidateTemp:
		return Selection{Temp: &c.Temp}, nil
	case CandidateNewTemp:
		return Selection{NewTemp: true}, nil
	default:
		return Selection{}, fmt.Errorf("unknown candidate kind %q", c.Kind)
	}
}

// createFromSelection prompts for a name and builds a namespace over the chosen
// repositories.
//
// The name is asked for after the repositories, which is what lets the branch
// check run against them — and it keeps the one-repo case in the order it has
// always been: pick the project, then name the work.
func createFromSelection(ctx context.Context, herdr Herdr, cfg Config, root string, members []RepoRef) error {
	name := promptName()
	ns, err := CreateNamespace(ctx, &DefaultExecutor{}, root, name, members)
	if err != nil {
		return err
	}
	return openAndReport(ctx, herdr, cfg, ns)
}

// createTempFromSelection makes a temp directory and opens it.
//
// The name is prompted for as a namespace's is, generated default and all, even
// though nothing but the directory and the workspace label depends on it: a
// directory called "jade-wyvern" is no help finding the one experiment you want
// back, and the prompt is one Enter away from that same generated name anyway.
func createTempFromSelection(ctx context.Context, herdr Herdr, cfg Config, root string) error {
	t, err := CreateTemp(root, promptName())
	if err != nil {
		return err
	}
	return openTempAndReport(ctx, herdr, cfg, t)
}

// openAndReport opens a namespace and prints its path, which is what the picker
// hands back to the caller.
func openAndReport(ctx context.Context, herdr Herdr, cfg Config, ns Namespace) error {
	if err := OpenNamespace(ctx, herdr, cfg, ns); err != nil {
		return err
	}
	fmt.Println(ns.Path)
	return nil
}

// openTempAndReport opens a temp directory and prints its path.
func openTempAndReport(ctx context.Context, herdr Herdr, cfg Config, t Temp) error {
	if err := OpenTemp(ctx, herdr, cfg, t); err != nil {
		return err
	}
	fmt.Println(t.Path)
	return nil
}

// candidateLines renders candidates as picker lines, alongside a map back to the
// candidate fzf cannot carry. Mapping rather than parsing is what frees the
// rendering to be readable — a namespace line can list its members.
func candidateLines(candidates []Candidate) ([]string, map[string]Candidate) {
	lines := make([]string, 0, len(candidates))
	byLine := make(map[string]Candidate, len(candidates))
	for _, c := range candidates {
		line := c.String()
		lines = append(lines, line)
		byLine[line] = c
	}
	return lines, byLine
}

// runFzfLines pipes lines through fzf and returns the chosen one, or an empty
// string if the user cancelled.
func runFzfLines(ctx context.Context, prompt string, lines []string) (string, error) {
	chosen, err := runFzf(ctx, prompt, lines, false)
	if err != nil || len(chosen) == 0 {
		return "", err
	}
	return chosen[0], nil
}

// runFzfMulti pipes lines through fzf with multi-select and returns every marked
// line, or nothing if the user cancelled.
//
// Multi-select rather than asking repeatedly until done: marks are visible
// inline, unmarking works, and it is one screen and one Enter rather than one
// invocation per repository.
func runFzfMulti(ctx context.Context, prompt string, lines []string) ([]string, error) {
	return runFzf(ctx, prompt, lines, true)
}

func runFzf(ctx context.Context, prompt string, lines []string, multi bool) ([]string, error) {
	var in bytes.Buffer
	for _, line := range lines {
		in.WriteString(line)
		in.WriteByte('\n')
	}

	args := []string{"--prompt", prompt}
	if multi {
		args = append(args, "--multi")
	}

	cmd := exec.CommandContext(ctx, "fzf", args...)
	cmd.Stdin = &in
	cmd.Stderr = os.Stderr

	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && (exitErr.ExitCode() == 1 || exitErr.ExitCode() == fzfCancelled) {
			return nil, nil
		}
		return nil, fmt.Errorf("running fzf: %w", err)
	}

	var chosen []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			chosen = append(chosen, line)
		}
	}
	return chosen, nil
}

// promptName asks for a namespace name, falling back to a generated one when the
// user just presses enter. The name becomes the directory and the branch in every
// member.
func promptName() string {
	generated := GenerateName()
	fmt.Fprintf(os.Stderr, "name [%s]: ", generated)

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return generated
	}
	if line = strings.TrimSpace(line); line != "" {
		return line
	}
	return generated
}
