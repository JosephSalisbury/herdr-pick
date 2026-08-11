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

// namePattern matches names usable as both a git branch and a single path
// segment. Slashes are excluded so a branch maps 1:1 to a directory name.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// GenerateName returns a random adjective-monster name.
func GenerateName() string {
	adj := adjectives[rand.Intn(len(adjectives))]
	mon := monsters[rand.Intn(len(monsters))]
	return fmt.Sprintf("%s-%s", adj, mon)
}

// ValidateName checks that name is safe as a single path segment and as a git
// branch.
//
// One function for both, because the rules are the same and the caller is what
// says which it is: a namespace name is also the branch in every member, a temp
// directory's is only ever a directory. Hence the neutral wording — the caller
// wraps it with whichever it was asking about.
func ValidateName(name string) error {
	if name == "" {
		return errors.New("name is empty")
	}
	if !namePattern.MatchString(name) {
		return fmt.Errorf("name %q may only contain letters, digits, dot, dash and underscore", name)
	}
	if strings.HasPrefix(name, "-") || strings.HasPrefix(name, ".") {
		return fmt.Errorf("name %q must not start with a dash or dot", name)
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("name %q must not contain consecutive dots", name)
	}
	return nil
}
