package main

import (
	"context"
	"strings"
	"testing"
)

func TestParseIssueRef(t *testing.T) {
	cases := []struct {
		in      string
		org     string
		repo    string
		number  int
		wantErr bool
	}{
		{in: "giantswarm/foo#123", org: "giantswarm", repo: "foo", number: 123},
		{in: "https://github.com/giantswarm/foo/issues/7", org: "giantswarm", repo: "foo", number: 7},
		{in: "https://github.com/giantswarm/foo/issues/7#issuecomment-99", org: "giantswarm", repo: "foo", number: 7},
		{in: "https://github.com/giantswarm/foo/pull/42", org: "giantswarm", repo: "foo", number: 42},
		{in: "", wantErr: true},
		{in: "giantswarm/foo", wantErr: true},
		{in: "foo#1", wantErr: true},
		{in: "giantswarm/foo#0", wantErr: true},
		{in: "giantswarm/foo#abc", wantErr: true},
		{in: "https://github.com/giantswarm/foo", wantErr: true},
	}
	for _, c := range cases {
		org, repo, number, err := ParseIssueRef(c.in)
		if c.wantErr {
			if err == nil {
				t.Fatalf("%q: expected error, got %s/%s#%d", c.in, org, repo, number)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q: unexpected error: %v", c.in, err)
		}
		if org != c.org || repo != c.repo || number != c.number {
			t.Fatalf("%q: got %s/%s#%d, want %s/%s#%d", c.in, org, repo, number, c.org, c.repo, c.number)
		}
	}
}

func TestIssueBranchIsValid(t *testing.T) {
	cases := map[string]struct {
		number int
		title  string
	}{
		"123-fix-the-login-bug":   {123, "Fix the login bug!"},
		"7-add-retries-to-client": {7, "Add retries to  client"},
		"42":                      {42, "!!!"},
		"9":                       {9, ""},
	}
	for want, c := range cases {
		got := IssueBranch(c.number, c.title)
		if got != want {
			t.Fatalf("IssueBranch(%d,%q) = %q, want %q", c.number, c.title, got, want)
		}
		if err := ValidateBranchName(got); err != nil {
			t.Fatalf("branch %q is invalid: %v", got, err)
		}
	}
}

func TestIssueBranchTruncatesLongTitle(t *testing.T) {
	long := strings.Repeat("word ", 50)
	got := IssueBranch(1, long)
	if err := ValidateBranchName(got); err != nil {
		t.Fatalf("branch %q is invalid: %v", got, err)
	}
	// number + dash + up to issueBranchMaxSlug of slug.
	if len(got) > len("1-")+issueBranchMaxSlug {
		t.Fatalf("branch %q too long (%d)", got, len(got))
	}
}

func TestFetchIssueParsesJSON(t *testing.T) {
	executor := &fakeExecutor{outputs: map[string]string{
		"gh": `{"number":5,"title":"Do a thing","body":"details","url":"https://github.com/o/r/issues/5"}`,
	}}
	issue, err := FetchIssue(context.Background(), executor, "o", "r", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issue.Number != 5 || issue.Title != "Do a thing" {
		t.Fatalf("got %+v", issue)
	}
	if !executor.ran("gh", "issue", "view", "5") {
		t.Fatalf("unexpected commands: %v", executor.calls)
	}
}

// The prompt is fed to the agent through the shell, so it must survive
// single-quoting intact and point the agent at the full issue.
func TestIssuePromptReferencesIssue(t *testing.T) {
	prompt := IssuePrompt("o", "r", Issue{Number: 5, Title: "Fix\nthe thing"})
	requireContains(t, prompt, "o/r#5")
	requireContains(t, prompt, "gh issue view 5")
	// Newlines in the title must be collapsed so the shell line stays single.
	if strings.Contains(prompt, "\n") {
		t.Fatalf("prompt contains a newline: %q", prompt)
	}
}

func TestShellQuoteEscapesSingleQuotes(t *testing.T) {
	got := shellQuote("it's a `test`")
	if got != `'it'\''s a `+"`test`"+`'` {
		t.Fatalf("got %q", got)
	}
}
