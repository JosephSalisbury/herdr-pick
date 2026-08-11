package main

import (
	"strings"
	"testing"
)

func TestCandidateString(t *testing.T) {
	repo := Candidate{Kind: CandidateRepo, Repo: RepoRef{"giantswarm", "foo"}}
	if got, want := repo.String(), "giantswarm/foo"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	// A namespace line carries its members, so typing a member's name surfaces
	// the namespace containing it as well as the repo itself.
	ns := Candidate{Kind: CandidateNamespace, Namespace: Namespace{
		Name:    "add-foo",
		Members: []string{"claudebox", "claudebox-image"},
	}}
	if got, want := ns.String(), "add-foo  (claudebox, claudebox-image)"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	temp := Candidate{Kind: CandidateTemp, Temp: Temp{Name: "jade-wyvern"}}
	if got, want := temp.String(), "jade-wyvern  [temp]"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := (Candidate{Kind: CandidateNewTemp}).String(); got != newTempLine {
		t.Fatalf("got %q, want %q", got, newTempLine)
	}
}

// The picker maps lines back to candidates, so two rendering alike would shadow
// each other silently. Brackets rather than a namespace's parentheses are what
// keep a namespace whose one member is called "temp" distinct from a temp
// directory of the same name.
func TestCandidateLinesDoNotCollide(t *testing.T) {
	lines, byLine := candidateLines([]Candidate{
		{Kind: CandidateNamespace, Namespace: Namespace{Name: "x", Members: []string{"temp"}}},
		{Kind: CandidateTemp, Temp: Temp{Name: "x"}},
		{Kind: CandidateNewTemp},
	})
	if len(byLine) != len(lines) {
		t.Fatalf("%d lines collapsed to %d candidates: %v", len(lines), len(byLine), lines)
	}
	if byLine[lines[0]].Kind != CandidateNamespace || byLine[lines[1]].Kind != CandidateTemp {
		t.Fatalf("lines did not map back: %v", lines)
	}
}

// A namespace name never contains a slash and a repo always contains exactly
// one, so the two kinds stay readable side by side without a sigil.
func TestCandidateKindsAreVisuallyDistinct(t *testing.T) {
	ns := Candidate{Kind: CandidateNamespace, Namespace: Namespace{
		Name: "add-foo", Members: []string{"claudebox"},
	}}
	if strings.Contains(strings.Fields(ns.String())[0], "/") {
		t.Fatalf("namespace line looks like a repo: %q", ns.String())
	}

	repo := Candidate{Kind: CandidateRepo, Repo: RepoRef{"giantswarm", "foo"}}
	if strings.Count(repo.String(), "/") != 1 {
		t.Fatalf("repo line should carry exactly one slash: %q", repo.String())
	}
}

// Namespaces lead because returning to work in flight is the commoner case —
// which is also what stops Enter on an empty query quietly making a directory.
// The fixed temp line sits with the temp directories, above the repositories.
func TestBuildCandidatesOrder(t *testing.T) {
	root := t.TempDir()
	writeMember(t, root, "add-foo", "claudebox")
	if _, err := CreateTemp(root, "jade-wyvern"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := WriteCache(root, "giantswarm", []string{"giantswarm/aaa", "giantswarm/foo"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := BuildCandidates(root, []string{"giantswarm"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d candidates, want 5: %v", len(got), got)
	}
	if got[0].Kind != CandidateNamespace || got[0].Namespace.Name != "add-foo" {
		t.Fatalf("expected a namespace first, got %v", got[0])
	}
	if got[1].Kind != CandidateTemp || got[1].Temp.Name != "jade-wyvern" {
		t.Fatalf("expected a temp directory second, got %v", got[1])
	}
	if got[2].Kind != CandidateNewTemp {
		t.Fatalf("expected the new temp line third, got %v", got[2])
	}
	if got[3].Kind != CandidateRepo || got[3].String() != "giantswarm/aaa" {
		t.Fatalf("got %v fourth", got[3])
	}
}

// Clones live under their own root, so nothing in the clone tree can be mistaken
// for a namespace or a temp directory however deep it goes.
func TestBuildCandidatesIgnoresClones(t *testing.T) {
	root := t.TempDir()
	writeBareClone(t, CloneDir(root, "giantswarm", "foo"))

	got, err := BuildCandidates(root, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Kind != CandidateNewTemp {
		t.Fatalf("got %v, want just the new temp line", got)
	}
}

// The picker is never empty: with nothing configured and nothing in flight,
// a temp directory is still something to open.
func TestBuildCandidatesEmptyRoot(t *testing.T) {
	got, err := BuildCandidates(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Kind != CandidateNewTemp {
		t.Fatalf("got %v, want just the new temp line", got)
	}
}
