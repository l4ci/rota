package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/jsonx"
)

// trackerProject is a project with a backlog and no git: enough for the verbs
// that do not look at git.
func trackerProject(t *testing.T, config string) string {
	t.Helper()
	root := t.TempDir()
	rota := filepath.Join(root, ".rota")
	if err := os.MkdirAll(rota, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"BACKLOG.md":    "# TODO\n\n## Bugs\n- **[B01] [P1] First.** x\n\n## Completed\n",
		"counters.json": "{}\n",
	}
	if config != "" {
		files["config.json"] = config
	}
	for n, c := range files {
		if err := os.WriteFile(filepath.Join(rota, n), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func rotaRun(t *testing.T, args ...string) (int, map[string]any, string) {
	t.Helper()
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	var out, errb bytes.Buffer
	code := mainWith(testDeps(), args, strings.NewReader(""), &out, &errb)
	var env map[string]any
	if out.Len() > 0 {
		v, err := jsonx.Decode(out.Bytes())
		if err != nil {
			t.Fatalf("stdout %q: %v", out.String(), err)
		}
		m := map[string]any{}
		if o, ok := v.(*jsonx.Object); ok {
			for _, k := range o.Keys() {
				m[k], _ = o.Get(k)
			}
		}
		env = m
	}
	return code, env, errb.String()
}

func TestItemIssueModeNeedsTracker(t *testing.T) {
	root := trackerProject(t, `{"backlog": {"backend": "issues"}}`)
	code, env, stderr := rotaRun(t, "--json", "-C", root, "item", "complete", "12", "--commit", "abc")
	if code != ExitUnavailable || env["ok"] != false || !strings.Contains(stderr, "cannot determine provider") {
		t.Fatalf("code=%d env=%v stderr=%s", code, env, stderr)
	}
	// File-only verbs are refused before the tracker is built.
	for _, argv := range [][]string{
		{"item", "rm", "B01"},
		{"id", "next", "--kind", "bugs"},
		{"item", "field", "set", "12", "--name", "detail", "--value", "x"},
	} {
		code, env, _ := rotaRun(t, append([]string{"--json", "-C", root}, argv...)...)
		data, _ := env["data"].(*jsonx.Object)
		if code != ExitRefused || data == nil {
			t.Fatalf("%v: code=%d env=%v", argv, code, env)
		}
		if v, _ := data.Get("blockedBy"); v != "backend" {
			t.Errorf("%v: blockedBy = %v", argv, v)
		}
	}
}

// With a tracker that knows no issue 12, every item verb answers 3, and
// nothing is written.
func TestItemIssueModeUnknownItem(t *testing.T) {
	root := trackerProject(t, `{"backlog": {"backend": "issues"}}`)
	fake := &trackertest.Fake{}
	deps := withTracker(t, fake)
	for _, argv := range [][]string{
		{"item", "complete", "12", "--commit", "abc"},
		{"item", "reopen", "12"},
		{"item", "ready", "12"},
		{"item", "show", "12"},
		{"item", "claim", "12", "--as", "a"},
		{"item", "release", "12", "--as", "a"},
		{"item", "state", "12", "--to", "none"},
		{"item", "note", "show", "12", "--kind", "plan"},
		{"item", "field", "set", "12", "--name", "milestone", "--value", "M1"},
	} {
		code, _, stderr := rotaRunWith(t, deps, append([]string{"--json", "-C", root}, argv...)...)
		if code != ExitResolution {
			t.Errorf("%v: code=%d stderr=%s", argv, code, stderr)
		}
	}
	for _, c := range fake.Calls {
		if c.Method != "get" {
			t.Errorf("unexpected write-side call %v", c)
		}
	}
}

func TestItemScope(t *testing.T) {
	root := trackerProject(t, "")
	if code, _, _ := rotaRun(t, "--json", "-C", root, "item", "reopen", "B01", "--repo", "web"); code != ExitResolution {
		t.Errorf("--repo outside umbrella: exit %d, want 3", code)
	}
	reg := `{"repos": [{"name": "web", "path": "web"}]}`
	os.WriteFile(filepath.Join(root, ".rota", "repos.json"), []byte(reg), 0o644)
	// A file-mode umbrella keeps one backlog at its root: the verbs work there,
	// with or without a registered --repo.
	if code, _, stderr := rotaRun(t, "--json", "-C", root, "item", "reopen", "B01"); code != 0 {
		t.Errorf("umbrella: exit %d, stderr %s", code, stderr)
	}
	if code, _, _ := rotaRun(t, "--json", "-C", root, "item", "reopen", "B01", "--repo", "web"); code != 0 {
		t.Errorf("registered --repo: exit %d, want 0", code)
	}
	if code, _, _ := rotaRun(t, "--json", "-C", root, "item", "reopen", "B01", "--repo", "api"); code != ExitResolution {
		t.Errorf("unregistered --repo: exit %d, want 3", code)
	}
}

type exitErr struct{ code int }

func (e exitErr) Error() string { return "tracker said no" }
func (e exitErr) Exit() int     { return e.code }

func TestItemFailMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		exit int
		data string // blockedBy, "" when no data
	}{
		{"not found", fmt.Errorf("x: %w", backlog.ErrNotFound), ExitResolution, ""},
		{"invalid", fmt.Errorf("x: %w", backlog.ErrInvalid), ExitUsage, ""},
		{"refused", &backlog.RefusedError{BlockedBy: "proof missing", Msg: "m", Err: backlog.ErrProofMissing}, ExitRefused, "proof missing"},
		{"active", &backlog.ActiveError{ID: "B01", Branch: "feat/x"}, ExitRefused, "active"},
		{"wrong backend", backlog.ErrWrongBackend, ExitRefused, "backend"},
		{"not ported", fmt.Errorf("x: %w", backlog.ErrNotPorted), ExitNotImplemented, ""},
		{"exit interface", exitErr{6}, ExitRetry, ""},
		{"wrapped exit interface", fmt.Errorf("w: %w", exitErr{5}), ExitUnavailable, ""},
		{"cli error passes through", Unavailable("x"), ExitUnavailable, ""},
	}
	for _, c := range cases {
		res, err := backlogFail(c.err)
		var e *Error
		if !errors.As(err, &e) || e.Exit != c.exit {
			t.Errorf("%s: err = %#v, want exit %d", c.name, err, c.exit)
			continue
		}
		data, _ := res.Data.(*jsonx.Object)
		if c.data == "" && data != nil {
			t.Errorf("%s: unexpected data", c.name)
		}
		if c.data != "" {
			if v, _ := data.Get("blockedBy"); v != c.data {
				t.Errorf("%s: blockedBy = %v", c.name, v)
			}
			if v, _ := data.Get("changed"); v != false {
				t.Errorf("%s: changed = %v", c.name, v)
			}
		}
	}
	if _, err := backlogFail(errors.New("boom")); asError(err).Exit != ExitInternal {
		t.Errorf("unclassified error should be internal, got %v", err)
	}
}
