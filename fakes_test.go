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
}

func (f *fakeExecutor) Run(_ context.Context, name string, args ...string) (string, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
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
