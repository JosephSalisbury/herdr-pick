package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func testConfig() Config {
	return Config{Orgs: []string{"giantswarm"}, Agent: []string{"claude"}}
}

// openHerdr returns a fake wired for the happy path of creating new work.
func openHerdr() *fakeHerdr {
	return &fakeHerdr{results: map[string]string{
		"worktree.create": `{"type":"worktree_created","workspace":{"workspace_id":"w1"},"worktree":{"path":"/wt","label":"x"}}`,
		"pane.list":       `{"type":"pane_list","panes":[{"pane_id":"p1","focused":true}]}`,
	}}
}

func TestOpenNewWorkCreatesWorktreeAndRunsAgent(t *testing.T) {
	root := t.TempDir()
	executor := &fakeExecutor{}
	herdr := openHerdr()

	path, err := Open(context.Background(), executor, herdr, testConfig(), root, "giantswarm/foo", "iron-lich")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := WorktreeDir(root, "giantswarm", "foo", "iron-lich"); path != want {
		t.Fatalf("got %q, want %q", path, want)
	}

	// The clone must be bare, so herdr cannot open it as a stray workspace.
	if !executor.ran("git", "clone", "--bare", "git@github.com:giantswarm/foo.git", RepoDir(root, "giantswarm", "foo")) {
		t.Fatalf("expected a bare clone, got %v", executor.calls)
	}
	if !herdr.called("worktree.create") || !herdr.called("pane.send_input") {
		t.Fatalf("got methods %v", herdr.methods)
	}
}

// One pane only: no agent.start, no split.
func TestOpenCreatesNoExtraPanes(t *testing.T) {
	herdr := openHerdr()

	if _, err := Open(context.Background(), &fakeExecutor{}, herdr, testConfig(), t.TempDir(), "giantswarm/foo", "iron-lich"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, method := range []string{"agent.start", "pane.split", "tab.create", "workspace.create"} {
		if herdr.called(method) {
			t.Fatalf("expected no %s, got %v", method, herdr.methods)
		}
	}
}

func TestOpenGeneratesBranchWhenEmpty(t *testing.T) {
	root := t.TempDir()

	path, err := Open(context.Background(), &fakeExecutor{}, openHerdr(), testConfig(), root, "giantswarm/foo", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	branch := filepath.Base(path)
	if branch == "" || branch == "foo" {
		t.Fatalf("expected a generated branch, got %q", path)
	}
	if err := ValidateBranchName(branch); err != nil {
		t.Fatalf("generated branch %q is invalid: %v", branch, err)
	}
}

// Selecting an existing worktree must reopen it, never clone or create.
func TestOpenExistingWorktreeOnlyOpens(t *testing.T) {
	root := t.TempDir()
	executor := &fakeExecutor{}
	herdr := &fakeHerdr{results: map[string]string{
		"worktree.open": `{"type":"worktree_opened","already_open":false,"workspace":{"workspace_id":"w2"},"worktree":{"path":"/wt","label":"x"}}`,
	}}

	path, err := Open(context.Background(), executor, herdr, testConfig(), root, "giantswarm/foo@swift-owlbear", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := WorktreeDir(root, "giantswarm", "foo", "swift-owlbear"); path != want {
		t.Fatalf("got %q, want %q", path, want)
	}

	if len(executor.calls) != 0 {
		t.Fatalf("expected no git commands, got %v", executor.calls)
	}
	if herdr.called("worktree.create") || herdr.called("pane.send_input") {
		t.Fatalf("expected open only, got %v", herdr.methods)
	}
}

func TestOpenRejectsInvalidBranch(t *testing.T) {
	herdr := &fakeHerdr{}

	_, err := Open(context.Background(), &fakeExecutor{}, herdr, testConfig(), t.TempDir(), "giantswarm/foo", "bad branch")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if len(herdr.methods) != 0 {
		t.Fatalf("expected no herdr calls, got %v", herdr.methods)
	}
}

func TestOpenSkipsCloneWhenBareClonePresent(t *testing.T) {
	root := t.TempDir()
	cloneDir := RepoDir(root, "giantswarm", "foo")
	writeBareClone(t, cloneDir)
	executor := &fakeExecutor{outputs: map[string]string{"git": "main"}}

	if _, err := Open(context.Background(), executor, openHerdr(), testConfig(), root, "giantswarm/foo", "iron-lich"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if executor.ran("git", "clone") {
		t.Fatalf("expected no clone, got %v", executor.calls)
	}
	// The whole point: the existing clone is refreshed, so the new worktree
	// branches off current main rather than main as of the original clone.
	if !executor.ran("git", "-C", cloneDir, "fetch", "--quiet", "origin", "+main:main") {
		t.Fatalf("expected the clone to be fetched, got %v", executor.calls)
	}
}

// A clone we just made is already current — fetching it again is a wasted
// round trip on the slowest path there is.
func TestOpenSkipsFetchAfterFreshClone(t *testing.T) {
	executor := &fakeExecutor{outputs: map[string]string{"git": "main"}}

	if _, err := Open(context.Background(), executor, openHerdr(), testConfig(), t.TempDir(), "giantswarm/foo", "iron-lich"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !executor.ran("git", "clone", "--bare") {
		t.Fatalf("expected a clone, got %v", executor.calls)
	}
	for _, call := range executor.calls {
		for _, arg := range call {
			if arg == "fetch" {
				t.Fatalf("expected no fetch, got %v", executor.calls)
			}
		}
	}
}

// Offline or VPN down must not stop work starting; a stale main is better than
// no worktree.
func TestOpenContinuesWhenFetchFails(t *testing.T) {
	root := t.TempDir()
	writeBareClone(t, RepoDir(root, "giantswarm", "foo"))
	executor := &fakeExecutor{err: errors.New("could not read from remote")}
	herdr := openHerdr()

	if _, err := Open(context.Background(), executor, herdr, testConfig(), root, "giantswarm/foo", "iron-lich"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !herdr.called("worktree.create") || !herdr.called("pane.send_input") {
		t.Fatalf("expected the worktree to be created anyway, got %v", herdr.methods)
	}
}

func TestOpenUsesConfiguredAgent(t *testing.T) {
	cfg := Config{Agent: []string{"claude", "--resume"}}
	herdr := openHerdr()

	if _, err := Open(context.Background(), &fakeExecutor{}, herdr, cfg, t.TempDir(), "giantswarm/foo", "iron-lich"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text, _ := herdr.paramsFor("pane.send_input")["text"].(string)
	if text != "claude --resume" {
		t.Fatalf("got text %q, want %q", text, "claude --resume")
	}
}

func TestAgentCommand(t *testing.T) {
	// No prompt: the bare agent command, unquoted, as callers relied on before.
	if got := agentCommand([]string{"claude", "--resume"}, ""); got != "claude --resume" {
		t.Fatalf("got %q, want %q", got, "claude --resume")
	}
	// With a prompt: the prompt is appended shell-quoted as one argument.
	got := agentCommand([]string{"claude"}, "do a thing")
	if got != "claude 'do a thing'" {
		t.Fatalf("got %q", got)
	}
}

func TestOpenLabelsWorkspace(t *testing.T) {
	herdr := openHerdr()

	if _, err := Open(context.Background(), &fakeExecutor{}, herdr, testConfig(), t.TempDir(), "giantswarm/foo", "iron-lich"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	label, _ := herdr.paramsFor("worktree.create")["label"].(string)
	if !strings.Contains(label, "giantswarm/foo") || !strings.Contains(label, "iron-lich") {
		t.Fatalf("got label %q", label)
	}
}
