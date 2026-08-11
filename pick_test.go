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

func tempCandidate(name string) Candidate {
	return Candidate{Kind: CandidateTemp, Temp: Temp{Name: name, Path: TempDir("/r", name)}}
}

// What was selected is what decides the verb, so there is nothing extra to
// confirm: existing work can only be opened, and repositories can only start a
// new namespace.
func TestResolveSelectionChoosesTheVerb(t *testing.T) {
	// One namespace resumes it.
	got, err := ResolveSelection([]Candidate{nsCandidate("add-foo", "claudebox")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Namespace == nil || got.Namespace.Name != "add-foo" {
		t.Fatalf("got %+v, want the add-foo namespace", got)
	}

	// One temp directory resumes it, and is not confused for a namespace.
	got, err = ResolveSelection([]Candidate{tempCandidate("jade-wyvern")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Namespace != nil || got.Temp == nil || got.Temp.Name != "jade-wyvern" {
		t.Fatalf("got %+v, want the jade-wyvern temp directory", got)
	}

	// The fixed line asks for a temp directory that does not exist yet.
	got, err = ResolveSelection([]Candidate{{Kind: CandidateNewTemp}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.NewTemp || got.Temp != nil {
		t.Fatalf("got %+v, want a new temp directory", got)
	}

	// A single unmarked repo starts a one-member namespace — the replacement for
	// the old single-repo flow.
	got, err = ResolveSelection([]Candidate{repoCandidate("JosephSalisbury", "claudebox")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Namespace != nil || got.Temp != nil || got.NewTemp || len(got.Repos) != 1 {
		t.Fatalf("expected a create over one repo, got %+v", got)
	}

	// Several marked repos start a namespace over all of them.
	got, err = ResolveSelection([]Candidate{
		repoCandidate("JosephSalisbury", "claudebox"),
		repoCandidate("JosephSalisbury", "claudebox-image"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Repos) != 2 || got.Repos[1].Repo != "claudebox-image" {
		t.Fatalf("expected a create over both repos, got %+v", got)
	}
}

// Only repositories combine. Everything else names one thing to open, so a
// selection holding two of them names no verb at all.
func TestResolveSelectionRejectsUncombinableVerbs(t *testing.T) {
	for _, chosen := range [][]Candidate{
		// Opening and starting are different verbs, not a set operation.
		{nsCandidate("add-foo", "claudebox"), repoCandidate("JosephSalisbury", "herdr-pick")},
		{tempCandidate("jade-wyvern"), repoCandidate("JosephSalisbury", "herdr-pick")},
		{{Kind: CandidateNewTemp}, repoCandidate("JosephSalisbury", "herdr-pick")},
		// There is one agent per workspace, so only one can be opened at a time.
		{nsCandidate("add-foo", "claudebox"), nsCandidate("add-bar", "herdr-pick")},
		{tempCandidate("jade-wyvern"), tempCandidate("iron-lich")},
		{nsCandidate("add-foo", "claudebox"), tempCandidate("jade-wyvern")},
		{{Kind: CandidateNewTemp}, {Kind: CandidateNewTemp}},
		nil,
	} {
		if _, err := ResolveSelection(chosen); err == nil {
			t.Fatalf("expected an error for %+v", chosen)
		}
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
