package main

import (
	"fmt"
	"strings"
)

// CandidateKind distinguishes the things the picker offers, which are also the
// verbs it can carry out: resume an existing namespace or temp directory, start
// a new namespace from repositories, or make a fresh temp directory.
type CandidateKind string

const (
	// CandidateNamespace is a namespace already on disk.
	CandidateNamespace CandidateKind = "namespace"
	// CandidateTemp is a temp directory already on disk.
	CandidateTemp CandidateKind = "temp"
	// CandidateNewTemp is the one fixed line in the picker: it stands for a temp
	// directory that does not exist yet rather than for anything on disk.
	CandidateNewTemp CandidateKind = "new-temp"
	// CandidateRepo is a repository known from the GitHub cache.
	CandidateRepo CandidateKind = "repo"
)

// newTempLine is the fixed line's rendering. Nothing else can render as it:
// ValidateName rejects both spaces and "+", so no namespace or temp directory
// can be named into a collision, and a repo line always holds a slash.
const newTempLine = "+ new temp directory"

// Candidate is a single selectable entry in the picker.
type Candidate struct {
	Kind CandidateKind
	// Namespace is set when Kind is CandidateNamespace.
	Namespace Namespace
	// Temp is set when Kind is CandidateTemp.
	Temp Temp
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
//
// A temp directory is bracketed where a namespace is parenthesised, because the
// map means two candidates rendering alike shadow each other silently — and a
// namespace whose one member happened to be called "temp" would otherwise read
// exactly as a temp directory does.
func (c Candidate) String() string {
	switch c.Kind {
	case CandidateNamespace:
		return fmt.Sprintf("%s  (%s)", c.Namespace.Name, strings.Join(c.Namespace.Members, ", "))
	case CandidateTemp:
		return fmt.Sprintf("%s  [temp]", c.Temp.Name)
	case CandidateNewTemp:
		return newTempLine
	default:
		return c.Repo.String()
	}
}

// BuildCandidates returns picker entries: existing namespaces, then existing
// temp directories, then the one fixed line that makes a new one, then every
// cached repository.
//
// Namespaces lead because returning to work already in flight is the more common
// case — and keeping them there is also what stops Enter on an empty query
// quietly making a directory when it used to resume something. The fixed line
// sits with the temp directories it belongs to, so typing "temp" finds all of
// them at once.
func BuildCandidates(root string, orgs []string) ([]Candidate, error) {
	namespaces, err := DiscoverNamespaces(root)
	if err != nil {
		return nil, err
	}

	var out []Candidate
	for _, ns := range namespaces {
		out = append(out, Candidate{Kind: CandidateNamespace, Namespace: ns})
	}

	temps, err := DiscoverTemps(root)
	if err != nil {
		return nil, err
	}
	for _, t := range temps {
		out = append(out, Candidate{Kind: CandidateTemp, Temp: t})
	}
	out = append(out, Candidate{Kind: CandidateNewTemp})

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
