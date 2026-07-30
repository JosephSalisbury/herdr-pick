package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// fakeExecutor records the commands it is asked to run and replays canned
// output keyed by the command name.
type fakeExecutor struct {
	calls   [][]string
	outputs map[string]string
	err     error

	// matches are consulted before outputs and err, keyed by a substring of the
	// whole argv. Several git subcommands share the name "git", so this is how a
	// single fake answers `git branch --list` and `git symbolic-ref`
	// differently.
	matches []fakeMatch
}

// fakeMatch is a canned reply for any argv containing contains.
type fakeMatch struct {
	contains string
	out      string
	err      error
}

func (f *fakeExecutor) Run(_ context.Context, name string, args ...string) (string, error) {
	argv := append([]string{name}, args...)
	f.calls = append(f.calls, argv)

	line := strings.Join(argv, " ")
	for _, m := range f.matches {
		if strings.Contains(line, m.contains) {
			return m.out, m.err
		}
	}

	if f.err != nil {
		return "", f.err
	}
	return f.outputs[name], nil
}

// ran reports whether a command starting with the given words was run.
func (f *fakeExecutor) ran(prefix ...string) bool {
	for _, call := range f.calls {
		if len(call) < len(prefix) {
			continue
		}
		match := true
		for i, want := range prefix {
			if call[i] != want {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// fakeHerdr records socket calls and replays canned JSON results per method.
type fakeHerdr struct {
	methods []string
	params  []map[string]any
	results map[string]string
	err     error
}

func (f *fakeHerdr) Call(_ context.Context, method string, params, out any) error {
	f.methods = append(f.methods, method)

	// Round-trip the params so assertions can inspect them generically.
	var decoded map[string]any
	if params != nil {
		if raw, err := json.Marshal(params); err == nil {
			_ = json.Unmarshal(raw, &decoded)
		}
	}
	f.params = append(f.params, decoded)

	if f.err != nil {
		return f.err
	}
	raw, ok := f.results[method]
	if !ok || out == nil {
		return nil
	}
	return json.Unmarshal([]byte(raw), out)
}

// paramsFor returns the params of the first call to method.
func (f *fakeHerdr) paramsFor(method string) map[string]any {
	for i, m := range f.methods {
		if m == method {
			return f.params[i]
		}
	}
	return nil
}

// called reports whether method was invoked.
func (f *fakeHerdr) called(method string) bool {
	for _, m := range f.methods {
		if m == method {
			return true
		}
	}
	return false
}

// requireContains fails the test unless got contains want.
func requireContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("expected %q to contain %q", got, want)
	}
}
