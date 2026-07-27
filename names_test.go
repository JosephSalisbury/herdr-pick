package main

import (
	"strings"
	"testing"
)

func TestGenerateNameFormat(t *testing.T) {
	name := GenerateName()
	parts := strings.SplitN(name, "-", 2)
	if len(parts) < 2 {
		t.Fatalf("expected adjective-monster format, got %q", name)
	}
	if parts[0] == "" || parts[1] == "" {
		t.Fatalf("expected non-empty parts, got %q", name)
	}
}

func TestGenerateNameVariety(t *testing.T) {
	seen := make(map[string]bool)
	for range 100 {
		seen[GenerateName()] = true
	}
	// With 25x25=625 combos, 100 calls should give at least 50 unique names.
	if len(seen) < 50 {
		t.Fatalf("expected at least 50 unique names from 100 calls, got %d", len(seen))
	}
}

// Every generated name must survive the same validation a typed name faces,
// since both become a branch and a directory.
func TestGenerateNameIsAlwaysValid(t *testing.T) {
	for range 200 {
		name := GenerateName()
		if err := ValidateBranchName(name); err != nil {
			t.Fatalf("generated name %q is invalid: %v", name, err)
		}
	}
}

func TestValidateBranchNameAccepts(t *testing.T) {
	for _, name := range []string{"swift-owlbear", "fix_thing", "v1.2.3", "abc123", "a"} {
		if err := ValidateBranchName(name); err != nil {
			t.Fatalf("%q: unexpected error: %v", name, err)
		}
	}
}

func TestValidateBranchNameRejects(t *testing.T) {
	// Slashes are rejected so a branch is always exactly one path segment.
	for _, name := range []string{"", "feature/thing", "has space", "-leading", ".hidden", "a..b", "semi;colon", "quote'd", "../escape"} {
		if err := ValidateBranchName(name); err == nil {
			t.Fatalf("expected error for %q", name)
		}
	}
}
