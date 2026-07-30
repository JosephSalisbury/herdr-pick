package main

import (
	"testing"
)

func nsCandidate(name string, members ...string) Candidate {
	return Candidate{Kind: CandidateNamespace, Namespace: Namespace{
		Name:    name,
		Path:    NamespaceDir("/r", name),
		Members: members,
	}}
}

func repoCandidate(org, repo string) Candidate {
	return Candidate{Kind: CandidateRepo, Repo: RepoRef{org, repo}}
}

// What was selected is what decides the verb, so there is nothing extra to
// confirm: a namespace can only be resumed, and repositories can only start one.
func TestResolveSelectionChoosesTheVerb(t *testing.T) {
	// One namespace resumes it.
	ns, err := ResolveSelection([]Candidate{nsCandidate("add-foo", "claudebox")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ns == nil || ns.Name != "add-foo" {
		t.Fatalf("got %+v, want the add-foo namespace", ns)
	}

	// A single unmarked repo starts a one-member namespace — the replacement for
	// the old single-repo flow.
	ns, err = ResolveSelection([]Candidate{repoCandidate("JosephSalisbury", "claudebox")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ns != nil {
		t.Fatalf("expected a create, got namespace %+v", ns)
	}

	// Several marked repos start a namespace over all of them.
	ns, err = ResolveSelection([]Candidate{
		repoCandidate("JosephSalisbury", "claudebox"),
		repoCandidate("JosephSalisbury", "claudebox-image"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ns != nil {
		t.Fatalf("expected a create, got namespace %+v", ns)
	}
}

func TestResolveSelectionRejectsUncombinableVerbs(t *testing.T) {
	// Resuming and starting are different verbs, not a set operation.
	if _, err := ResolveSelection([]Candidate{
		nsCandidate("add-foo", "claudebox"),
		repoCandidate("JosephSalisbury", "herdr-pick"),
	}); err == nil {
		t.Fatal("expected an error for a namespace mixed with repositories")
	}

	// Only one namespace can be resumed at a time: there is one agent per
	// workspace.
	if _, err := ResolveSelection([]Candidate{
		nsCandidate("add-foo", "claudebox"),
		nsCandidate("add-bar", "herdr-pick"),
	}); err == nil {
		t.Fatal("expected an error for two namespaces")
	}

	if _, err := ResolveSelection(nil); err == nil {
		t.Fatal("expected an error for an empty selection")
	}
}

// fzf can only hand back the line it was given, so the mapping is what carries
// the candidate. It is also what frees the line to be readable rather than
// parseable.
func TestCandidateLinesMapBackToCandidates(t *testing.T) {
	candidates := []Candidate{
		nsCandidate("add-foo", "claudebox", "claudebox-image"),
		repoCandidate("JosephSalisbury", "claudebox"),
	}

	lines, byLine := candidateLines(candidates)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}

	got, ok := byLine[lines[0]]
	if !ok || got.Kind != CandidateNamespace || got.Namespace.Name != "add-foo" {
		t.Fatalf("first line did not map back: %q -> %+v", lines[0], got)
	}
	got, ok = byLine[lines[1]]
	if !ok || got.Kind != CandidateRepo || got.Repo.Repo != "claudebox" {
		t.Fatalf("second line did not map back: %q -> %+v", lines[1], got)
	}

	// The members are in the namespace's line, so typing one finds it.
	requireContains(t, lines[0], "claudebox-image")
}
