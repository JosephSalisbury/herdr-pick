package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Temp is a directory to try something out in: a named, empty directory opened
// as a herdr workspace with the agent running in it.
//
// It is what a namespace is for work that has no repositories yet — no members,
// no clone, no fetch and no branch, so creating one is pure filesystem and
// cannot fail on the network. That is the whole of the difference; everything
// downstream of the directory (opening it, focusing it, `status`, `switch`) is
// the namespace path unchanged.
type Temp struct {
	// Name is the directory name, and the workspace's label.
	Name string
	// Path is the directory itself, which is the agent's working directory and
	// the one path claudebox mounts.
	Path string
}

// DiscoverTemps returns every temp directory on disk, sorted. The filesystem is
// the source of truth here as it is for namespaces.
//
// Unlike a namespace there is nothing to filter on: an empty directory is
// exactly what a temp directory is, so every subdirectory of <root>/tmp counts.
// A missing root is empty rather than an error, so the picker works before one
// has ever been made.
func DiscoverTemps(root string) ([]Temp, error) {
	tmpRootDir := TempRoot(root)
	names, err := readDirNames(tmpRootDir)
	if err != nil {
		return nil, err
	}
	sortFold(names)

	out := make([]Temp, 0, len(names))
	for _, name := range names {
		out = append(out, Temp{Name: name, Path: filepath.Join(tmpRootDir, name)})
	}
	return out, nil
}

// LoadTemp reads one temp directory by name.
func LoadTemp(root, name string) (Temp, error) {
	path := TempDir(root, name)
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return Temp{}, fmt.Errorf("no temp directory %q in %s", name, TempRoot(root))
	}
	return Temp{Name: name, Path: path}, nil
}

// CreateTemp makes a temp directory. An existing name is refused rather than
// reused, so the caller decides whether it meant to resume one.
func CreateTemp(root, name string) (Temp, error) {
	if err := ValidateName(name); err != nil {
		return Temp{}, fmt.Errorf("temp directory name: %w", err)
	}

	path := TempDir(root, name)
	if _, err := os.Stat(path); err == nil {
		return Temp{}, fmt.Errorf("temp directory %q already exists at %s", name, path)
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return Temp{}, fmt.Errorf("creating temp directory %s: %w", path, err)
	}
	return Temp{Name: name, Path: path}, nil
}

// OpenTemp opens a temp directory as a herdr workspace and starts the agent in
// it, or focuses the workspace if one is already open on it.
func OpenTemp(ctx context.Context, h Herdr, cfg Config, t Temp) error {
	return openWorkspace(ctx, h, cfg, t.Path, t.Name, layoutTemp)
}

// layoutTemp gives a temp directory's workspace the shape it is worked in:
//
//	┌───────────┬───────────┐
//	│           │           │
//	│   agent   │   shell   │
//	│           │           │
//	└───────────┴───────────┘
//
// Two panes rather than a namespace's three: the third watches one `git status`
// per member, and a temp directory has no members for it to report on. It
// returns the pane the agent is to start in.
//
// The rules are the namespace layout's, for the same reasons (see
// layoutWorkspace): the layout goes to the pane's *tab*, never its workspace,
// and every pane id — the agent's included — is read back out of the reply
// rather than carried over from the request.
func layoutTemp(ctx context.Context, h Herdr, pane HerdrPane, cwd string) (string, error) {
	if pane.TabID == "" {
		return "", fmt.Errorf("herdr reported no tab for pane %s: leaving %s as one pane", pane.PaneID, cwd)
	}

	applied, err := LayoutApply(ctx, h, pane.TabID, tempLayout(pane.PaneID, cwd))
	if err != nil {
		return "", fmt.Errorf("laying out the workspace for %s: %w", cwd, err)
	}
	if applied.First == nil || applied.Second == nil {
		return "", errors.New("herdr applied a layout of a different shape")
	}

	agent, shell := applied.First.PaneID, applied.Second.PaneID
	if agent == "" || shell == "" {
		return "", errors.New("herdr's layout left a pane unnamed")
	}

	// cd'd as well as opened in the directory, for the reason agentCommand is: a
	// shell rc that changes directory on startup would otherwise leave the human's
	// pane somewhere else entirely.
	if err := RunInPane(ctx, h, shell, cdCommand(cwd)); err != nil {
		return "", fmt.Errorf("preparing the shell pane: %w", err)
	}

	// Applying a layout leaves focus wherever herdr puts it, and the pane worth
	// typing into is the agent's.
	if err := PaneFocus(ctx, h, agent); err != nil {
		return "", fmt.Errorf("focusing the agent pane: %w", err)
	}
	return agent, nil
}

// tempLayout builds the tree layoutTemp applies.
func tempLayout(agentPane, cwd string) LayoutNode {
	return LayoutNode{
		Type:      layoutSplit,
		Direction: "right",
		Ratio:     agentPaneRatio,
		First:     &LayoutNode{Type: layoutPane, PaneID: agentPane},
		Second:    &LayoutNode{Type: layoutPane, Cwd: cwd},
	}
}
