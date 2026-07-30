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

func TestBuildCandidatesPutsNamespacesFirst(t *testing.T) {
	root := t.TempDir()
	writeMember(t, root, "add-foo", "claudebox")
	if err := WriteCache(root, "giantswarm", []string{"giantswarm/aaa", "giantswarm/foo"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := BuildCandidates(root, []string{"giantswarm"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d candidates, want 3: %v", len(got), got)
	}
	if got[0].Kind != CandidateNamespace || got[0].Namespace.Name != "add-foo" {
		t.Fatalf("expected a namespace first, got %v", got[0])
	}
	if got[1].Kind != CandidateRepo || got[1].String() != "giantswarm/aaa" {
		t.Fatalf("got %v second", got[1])
	}
}

// Clones live under their own root, so nothing in the clone tree can be mistaken
// for a namespace however deep it goes.
func TestBuildCandidatesIgnoresClones(t *testing.T) {
	root := t.TempDir()
	writeBareClone(t, CloneDir(root, "giantswarm", "foo"))

	got, err := BuildCandidates(root, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want none", got)
	}
}

func TestBuildCandidatesEmptyRoot(t *testing.T) {
	got, err := BuildCandidates(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want none", got)
	}
}
