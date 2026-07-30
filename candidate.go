package main

import (
	"fmt"
	"strings"
)

// CandidateKind distinguishes the two things the picker offers, which are also
// the two verbs it can carry out: resume an existing namespace, or start a new
// one from repositories.
type CandidateKind string

const (
	// CandidateNamespace is a namespace already on disk.
	CandidateNamespace CandidateKind = "namespace"
	// CandidateRepo is a repository known from the GitHub cache.
	CandidateRepo CandidateKind = "repo"
)

// Candidate is a single selectable entry in the picker.
type Candidate struct {
	Kind CandidateKind
	// Namespace is set when Kind is CandidateNamespace.
	Namespace Namespace
	// Repo is set when Kind is CandidateRepo.
	Repo RepoRef
}

// String renders the candidate as one picker line.
//
// Unlike the old grammar, this does not need to round-trip: the picker keeps a
// map from line back to candidate, so a namespace line can also list its members
// without anything having to parse them back out. Listing them means typing a
// member's name finds the namespace already containing it as well as the repo
// itself.
func (c Candidate) String() string {
	if c.Kind == CandidateNamespace {
		return fmt.Sprintf("%s  (%s)", c.Namespace.Name, strings.Join(c.Namespace.Members, ", "))
	}
	return c.Repo.String()
}

// BuildCandidates returns picker entries: existing namespaces first, then every
// cached repository. Namespaces lead because returning to work already in flight
// is the more common case.
func BuildCandidates(root string, orgs []string) ([]Candidate, error) {
	namespaces, err := DiscoverNamespaces(root)
	if err != nil {
		return nil, err
	}

	var out []Candidate
	for _, ns := range namespaces {
		out = append(out, Candidate{Kind: CandidateNamespace, Namespace: ns})
	}

	repos, err := CachedRepos(root, orgs)
	if err != nil {
		return nil, err
	}
	for _, full := range repos {
		ref, err := ParseRepo(full)
		if err != nil {
			// A malformed cache line is skipped rather than fatal; the cache is
			// derived data and is refilled on the next refresh.
			continue
		}
		out = append(out, Candidate{Kind: CandidateRepo, Repo: ref})
	}
	return out, nil
}
