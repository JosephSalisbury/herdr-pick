package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// issueBranchMaxSlug bounds the slug portion of an issue branch so a long
// title does not produce an unwieldy directory name.
const issueBranchMaxSlug = 40

// Issue is the subset of a GitHub issue herdr-pick needs to name a branch and
// brief the agent.
type Issue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	URL    string `json:"url"`
}

// ParseIssueRef resolves an issue reference into its parts. It accepts the
// shorthand "org/repo#123" and a full GitHub issue URL.
func ParseIssueRef(ref string) (org, repo string, number int, err error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", "", 0, fmt.Errorf("empty issue reference")
	}

	if strings.Contains(ref, "github.com") {
		return parseIssueURL(ref)
	}

	spec, num, ok := strings.Cut(ref, "#")
	if !ok {
		return "", "", 0, fmt.Errorf("issue reference %q must be org/repo#number or a GitHub URL", ref)
	}
	org, repo, ok = strings.Cut(spec, "/")
	if !ok || org == "" || repo == "" || strings.Contains(repo, "/") {
		return "", "", 0, fmt.Errorf("issue reference %q must name org/repo", ref)
	}
	number, err = parseIssueNumber(num)
	if err != nil {
		return "", "", 0, err
	}
	return org, repo, number, nil
}

// parseIssueURL handles https://github.com/org/repo/issues/123 and its /pull/
// sibling, tolerating a trailing #comment fragment or query string.
func parseIssueURL(ref string) (org, repo string, number int, err error) {
	trimmed := ref
	if i := strings.IndexAny(trimmed, "?#"); i >= 0 {
		trimmed = trimmed[:i]
	}
	_, path, ok := strings.Cut(trimmed, "github.com/")
	if !ok {
		return "", "", 0, fmt.Errorf("cannot parse issue URL %q", ref)
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 4 || (parts[2] != "issues" && parts[2] != "pull") {
		return "", "", 0, fmt.Errorf("cannot parse issue URL %q", ref)
	}
	number, err = parseIssueNumber(parts[3])
	if err != nil {
		return "", "", 0, err
	}
	return parts[0], parts[1], number, nil
}

func parseIssueNumber(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid issue number %q", s)
	}
	return n, nil
}

// FetchIssue reads an issue's number, title, body and URL via the gh CLI.
func FetchIssue(ctx context.Context, executor Executor, org, repo string, number int) (Issue, error) {
	out, err := executor.Run(ctx, "gh", "issue", "view", strconv.Itoa(number),
		"--repo", org+"/"+repo,
		"--json", "number,title,body,url")
	if err != nil {
		return Issue{}, fmt.Errorf("fetching %s/%s#%d: %w", org, repo, number, err)
	}

	var issue Issue
	if err := json.Unmarshal([]byte(out), &issue); err != nil {
		return Issue{}, fmt.Errorf("parsing gh output for %s/%s#%d: %w", org, repo, number, err)
	}
	if issue.Number == 0 {
		issue.Number = number
	}
	return issue, nil
}

// IssueBranch derives a branch name from an issue: its number, then a slug of
// the title. The number leads so the branch sorts and reads by issue, and so a
// titleless issue still yields a valid name.
func IssueBranch(number int, title string) string {
	slug := slugify(title)
	if len(slug) > issueBranchMaxSlug {
		slug = strings.Trim(slug[:issueBranchMaxSlug], "-")
	}
	if slug == "" {
		return strconv.Itoa(number)
	}
	return fmt.Sprintf("%d-%s", number, slug)
}

// slugify lowercases text and collapses every run of non-alphanumeric
// characters into a single dash, trimming dashes from the ends. The result
// contains only characters ValidateBranchName accepts and never "..".
func slugify(s string) string {
	var b strings.Builder
	lastDash := true // leading dashes are trimmed by suppressing them
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// IssuePrompt is the opening message handed to the agent. It points at the
// issue and tells the agent how to read the whole thing itself, rather than
// stuffing a whole issue body through the shell.
func IssuePrompt(org, repo string, issue Issue) string {
	title := strings.Join(strings.Fields(issue.Title), " ")
	return fmt.Sprintf(
		"Start working on GitHub issue %s/%s#%d (%s). "+
			"Run `gh issue view %d --repo %s/%s --comments` to read the full issue and its comments, then get started.",
		org, repo, issue.Number, title, issue.Number, org, repo)
}

var issueCmd = &cobra.Command{
	Use:   "issue <org/repo#number | url>",
	Short: "Create a worktree for a GitHub issue and start an agent on it",
	Long: "Resolves a GitHub issue, opens a worktree on a branch named for it, " +
		"and launches the agent briefed to start work on that issue.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, root, err := loadConfig()
		if err != nil {
			return err
		}

		org, repo, number, err := ParseIssueRef(args[0])
		if err != nil {
			return err
		}

		executor := &DefaultExecutor{}
		issue, err := FetchIssue(cmd.Context(), executor, org, repo, number)
		if err != nil {
			return err
		}

		branch := IssueBranch(issue.Number, issue.Title)
		if err := ValidateBranchName(branch); err != nil {
			return fmt.Errorf("derived branch %q from issue is invalid: %w", branch, err)
		}

		herdr, err := connectHerdr(cmd.Context())
		if err != nil {
			return err
		}

		path, err := open(cmd.Context(), executor, herdr, cfg, root,
			org+"/"+repo, branch, IssuePrompt(org, repo, issue))
		if err != nil {
			return err
		}
		fmt.Println(path)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(issueCmd)
}
