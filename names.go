package main

import (
	"errors"
	"fmt"
	"math/rand"
	"regexp"
	"strings"
)

var adjectives = []string{
	"swift", "fuzzy", "fierce", "ancient", "shadow",
	"crimson", "golden", "silent", "wild", "arcane",
	"brave", "cunning", "dire", "elder", "frost",
	"grim", "iron", "jade", "keen", "lunar",
	"mystic", "noble", "pale", "rune", "storm",
}

var monsters = []string{
	"owlbear", "beholder", "mimic", "basilisk", "dragon",
	"griffon", "hydra", "kobold", "lich", "manticore",
	"naga", "ogre", "phoenix", "quasit", "roc",
	"sphinx", "troll", "umber-hulk", "vampire", "wyvern",
	"aboleth", "bugbear", "chimera", "djinni", "ettin",
}

// branchNamePattern matches names usable as both a git branch and a single
// path segment. Slashes are excluded so a branch maps 1:1 to a directory name.
var branchNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// GenerateName returns a random adjective-monster name.
func GenerateName() string {
	adj := adjectives[rand.Intn(len(adjectives))]
	mon := monsters[rand.Intn(len(monsters))]
	return fmt.Sprintf("%s-%s", adj, mon)
}

// ValidateBranchName checks that name is safe as a branch and a path segment.
func ValidateBranchName(name string) error {
	if name == "" {
		return errors.New("branch name is empty")
	}
	if !branchNamePattern.MatchString(name) {
		return fmt.Errorf("branch name %q may only contain letters, digits, dot, dash and underscore", name)
	}
	if strings.HasPrefix(name, "-") || strings.HasPrefix(name, ".") {
		return fmt.Errorf("branch name %q must not start with a dash or dot", name)
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("branch name %q must not contain consecutive dots", name)
	}
	return nil
}
