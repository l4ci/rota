package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker"
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
	root := trackerProject(t, `{"backlog": {"backend": "issues"}}`) // legacy, tracker unresolved
	code, env, stderr := rotaRun(t, "--json", "-C", root, "item", "complete", "12", "--commit", "abc")
	if code != ExitUnavailable || env["ok"] != false || !strings.Contains(stderr, "cannot determine the issue tracker provider") {
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
	root := trackerProject(t, `{"forge": {"provider": "github"}, "issues": {"source": "github"}}`)
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

// item complete stamps the clock's day, so a pinned Deps.Today (what
// ROTA_TEST_TODAY feeds at the edge) fixes the date it writes end to end.
func TestItemCompleteStampsTheDepsDay(t *testing.T) {
	root := trackerProject(t, "")
	if code, _, stderr := rotaRun(t, "--json", "-C", root, "item", "create", "--kind", "bugs", "--title", "Dated bug"); code != 0 {
		t.Fatalf("create: exit %d: %s", code, stderr)
	}
	d := testDeps()
	d.Today = func() time.Time { return time.Date(2031, 2, 3, 12, 0, 0, 0, time.UTC) }
	if code, _, stderr := rotaRunWith(t, d, "--json", "-C", root, "item", "complete", "B01", "--commit", "abc1234", "--no-proof"); code != 0 {
		t.Fatalf("complete: exit %d: %s", code, stderr)
	}
	b, _ := os.ReadFile(filepath.Join(root, ".rota", "BACKLOG.md"))
	if !strings.Contains(string(b), "2031-02-03") {
		t.Errorf("BACKLOG.md lacks the pinned day:\n%s", b)
	}
}

// A batch files one record per item, in order, and each filed item keeps its
// own title and body (#838): no parallel lists to fall out of step.
func TestItemCreateBatchPairsTitleAndBody(t *testing.T) {
	root := trackerProject(t, "")
	bl := filepath.Join(root, ".rota", "BACKLOG.md")
	if err := os.WriteFile(bl, []byte("# TODO\n\n## Bugs\n\n## Tasks\n\n## Completed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	batch := filepath.Join(t.TempDir(), "batch.json")
	recs := `[
 {"kind":"tasks","title":"Alpha seam extraction","desc":"alpha summary","body":"## Acceptance\n- [ ] alpha"},
 {"kind":"bugs","title":"Beta crash on empty","tag":"P2","desc":"beta summary","body":"## Acceptance\n- [ ] beta"},
 {"kind":"tasks","title":"Gamma helper cleanup","desc":"gamma summary","body":"## Acceptance\n- [ ] gamma"}]`
	if err := os.WriteFile(batch, []byte(recs), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := rotaRun(t, "--json", "-C", root, "item", "create", "--batch-file", batch)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	body, _ := os.ReadFile(bl)
	for title, desc := range map[string]string{"Alpha seam extraction": "alpha summary",
		"Beta crash on empty": "beta summary", "Gamma helper cleanup": "gamma summary"} {
		var line string
		for _, l := range strings.Split(string(body), "\n") {
			if strings.Contains(l, title) {
				line = l
			}
		}
		if !strings.Contains(line, desc) {
			t.Errorf("%q is not paired with %q:\n%s", title, desc, body)
		}
	}
	// The detail file behind each item carries that item's own Acceptance box.
	for title, box := range map[string]string{"Alpha seam extraction": "- [ ] alpha", "Gamma helper cleanup": "- [ ] gamma"} {
		var id string
		for _, l := range strings.Split(string(body), "\n") {
			if strings.Contains(l, title) {
				id = strings.Trim(strings.Fields(l)[1], "*[]")
			}
		}
		detail, err := os.ReadFile(filepath.Join(root, ".rota", "tasks", id+".md"))
		if err != nil || !strings.Contains(string(detail), box) {
			t.Errorf("%q (%s): body lacks %q: %v\n%s", title, id, box, err, detail)
		}
	}
	// A weak title anywhere refuses the whole batch before anything is filed.
	bad := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(bad, []byte(`[{"kind":"tasks","title":"Fine long title"},{"kind":"tasks","title":"x"}]`), 0o644)
	if code, _, _ := rotaRun(t, "--json", "-C", root, "item", "create", "--batch-file", bad); code != 2 {
		t.Errorf("weak title in batch: exit %d, want 2", code)
	}
}

// failSecond fails its second Create the way a tracker does.
type failSecond struct {
	backlog.Backend
	n   int
	err error // the second Create's error; a tracker outage when nil
}

func (f *failSecond) Create(in backlog.CreateInput) (backlog.CreateResult, error) {
	if f.n++; f.n == 2 {
		if f.err != nil {
			return backlog.CreateResult{}, f.err
		}
		return backlog.CreateResult{}, &tracker.Error{Kind: tracker.KindUnavailable, Code: 1, Message: "network is down"}
	}
	return backlog.CreateResult{ID: "T0" + fmt.Sprint(f.n), Type: "T"}, nil
}

// A tracker failure on record 2 of 3 still reports record 1's id, in the data
// and in the message, and files nothing after it.
func TestItemCreateBatchKeepsFiledIDsOnFailure(t *testing.T) {
	ins := []backlog.CreateInput{{Kind: "tasks", Title: "First filed record"}, {Kind: "tasks", Title: "Second fails here"},
		{Kind: "tasks", Title: "Third never runs"}}
	be := &failSecond{}
	res, err := fileBatch(be, ins)
	var e *Error
	if !errors.As(err, &e) || e.Exit != ExitUnavailable {
		t.Fatalf("err = %#v, want exit %d", err, ExitUnavailable)
	}
	if !strings.Contains(e.Message, "T01") || !strings.Contains(e.Message, "network is down") {
		t.Errorf("message lacks the filed id or the cause: %q", e.Message)
	}
	data, _ := res.Data.(*jsonx.Object)
	if data == nil {
		t.Fatal("no data on a partial batch")
	}
	if v, _ := data.Get("failedAt"); v != 2 {
		t.Errorf("failedAt = %v, want 2", v)
	}
	items, _ := data.Get("items")
	enc, _ := jsonx.MarshalCompact(items)
	if got := string(enc); !strings.Contains(got, "T01") || strings.Contains(got, "T03") {
		t.Errorf("items = %v, want only record 1", items)
	}
	if be.n != 2 {
		t.Errorf("Create called %d times, want 2", be.n)
	}
}

// A plain error (file I/O, a lock) on record 2 of 3 is reported the same way:
// record 1's id in the message and in `items`, and the data survives withBackend.
func TestItemCreateBatchKeepsFiledIDsOnPlainError(t *testing.T) {
	ins := []backlog.CreateInput{{Kind: "tasks", Title: "First filed record"}, {Kind: "tasks", Title: "Second fails here"},
		{Kind: "tasks", Title: "Third never runs"}}
	be := &failSecond{err: errors.New("disk full")}
	res, err := fileBatch(be, ins)
	// An *Error is what withBackend passes through with its Result intact.
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("err = %#v, want an *Error so withBackend keeps the data", err)
	}
	if !strings.Contains(e.Message, "record 2") || !strings.Contains(e.Message, "T01") || !strings.Contains(e.Message, "disk full") {
		t.Errorf("message lacks the record, the filed id or the cause: %q", e.Message)
	}
	data, _ := res.Data.(*jsonx.Object)
	if data == nil {
		t.Fatal("no data on a partial batch")
	}
	if v, _ := data.Get("failedAt"); v != 2 {
		t.Errorf("failedAt = %v, want 2", v)
	}
	items, _ := data.Get("items")
	enc, _ := jsonx.MarshalCompact(items)
	if got := string(enc); !strings.Contains(got, "T01") || strings.Contains(got, "T03") {
		t.Errorf("items = %s, want only record 1", enc)
	}
}
