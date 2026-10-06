package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/git"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/golden"
	"github.com/l4ci/rota/internal/tracker"
)

// The gate tests rebuild smoke section 68's world: a bare origin, a gate
// checkout on main, a worker clone with a pushed branch w1, and a fake Forge
// (fakeforge_test.go) that can lie the ways a real one does. Each case
// runs the Go port and compares the outcome with what the retired shell gate
// produced, frozen under testdata/golden. The fake forge only talks to the
// local bare origin.

const (
	ghURL = "https://github.com/o/r/pull/7"
	glURL = "https://gitlab.com/o/r/-/merge_requests/7"
)

var shaRe = regexp.MustCompile(`[0-9a-f]{40}`)

type world struct {
	t       *testing.T
	dir     string // the gate checkout
	origin  string
	worker  string
	forgeDB string
	log     string
	mode    string
}

func gitq(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return gittest.Run(t, dir, append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
}

// newWorld is gt_case: pr is the recorded PR (URL), "" for none.
func newWorld(t *testing.T, pr string) *world {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, dir: filepath.Join(base, "gate"), origin: filepath.Join(base, "origin.git"), worker: filepath.Join(base, "worker"),
		forgeDB: filepath.Join(base, "forge.json"), log: filepath.Join(base, "forge.log"), mode: "ok"}
	gittest.Run(t, base, "init", "-q", "--bare", "-b", "main", w.origin)
	gittest.Run(t, base, "clone", "-q", w.origin, w.dir)
	gitq(t, w.dir, "checkout", "-q", "-B", "main")
	os.WriteFile(filepath.Join(w.dir, "seed.txt"), []byte("seed\n"), 0o644)
	gitq(t, w.dir, "add", "seed.txt")
	gitq(t, w.dir, "commit", "-q", "-m", "seed")
	gitq(t, w.dir, "push", "-q", "origin", "main")
	gittest.Run(t, base, "clone", "-q", w.origin, w.worker)
	gitq(t, w.worker, "checkout", "-q", "-b", "w1")
	os.WriteFile(filepath.Join(w.worker, "work.txt"), []byte("work\n"), 0o644)
	gitq(t, w.worker, "add", "work.txt")
	gitq(t, w.worker, "commit", "-q", "-m", "work")
	gitq(t, w.worker, "push", "-q", "origin", "w1")
	os.MkdirAll(filepath.Join(w.dir, ".rota"), 0o755)
	w.setConfig(`{"test":{"full":[]}}`)
	w.setSlot(pr, "")
	os.WriteFile(w.log, nil, 0o644)
	w.forge("origin", w.origin)
	w.forge("head", "w1")
	w.forge("sha", gitq(t, w.worker, "rev-parse", "HEAD"))
	w.forge("base", "main")
	w.forge("state", "OPEN")
	w.forge("merge", "")
	w.forge("body", "")
	return w
}

func (w *world) setConfig(cfg string) {
	os.WriteFile(filepath.Join(w.dir, ".rota", "config.json"), []byte(cfg), 0o644)
}

// setSlot writes workers.json with slot w1 on branch w1 and the given PR and
// relays (a JSON array, "" for none).
func (w *world) setSlot(pr, relays string) {
	slot := fmt.Sprintf(`{"name":"w1","branch":"w1","pr":%q`, pr)
	if pr == "" {
		slot = `{"name":"w1","branch":"w1"`
	}
	if relays != "" {
		slot += `,"relays":` + relays
	}
	os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"), []byte(`{"slots":[`+slot+`}]}`), 0o644)
}

// forge sets one key of the fake forge's state (a string value; "merge" and
// "body" may be empty).
func (w *world) forge(key, val string) {
	st := map[string]any{}
	if b, err := os.ReadFile(w.forgeDB); err == nil {
		jsonUnmarshal(b, &st)
	}
	st[key] = val
	b, _ := jsonMarshal(st)
	os.WriteFile(w.forgeDB, b, 0o644)
}

func (w *world) forgeWord(key string) string {
	st := map[string]any{}
	b, _ := os.ReadFile(w.forgeDB)
	jsonUnmarshal(b, &st)
	s, _ := st[key].(string)
	return s
}

// env builds the Go Env around a fake Forge over the world's bare origin.
func (w *world) env(brokenMergeBase bool) Env {
	return Env{
		Sleep:  func(time.Duration) {},
		Getenv: func(k string) string { return map[string]string{"ROTA_GATE_SHA_WAIT": "0"}[k] },
	}.withForge(func(provider, dir string) Forge { return &fakeForge{w: w, provider: provider} }, brokenMergeBase)
}

func (w *world) gate(brokenMergeBase bool, o GateOpts) (GateResult, error) {
	o.Slot = "w1"
	if o.Base == "" {
		o.Base = "main"
	}
	return w.env(brokenMergeBase).Gate(bg, w.dir, o)
}

// oldExitVerdicts maps the retired gate's exit code to the verdict(s) the Go
// port may report: contract rule, old 3 is split by message, old 4, 5 and 6 are
// verdicts.
var oldExitVerdicts = map[int][]string{
	0: {GateFresh, GatePass},
	3: {GateStale, GatePRMismatch, GateMergeFailed},
	4: {GateProvenanceFail, GateNotMerged, GateNotOnBase, GateVerifyFailed},
	5: {GateCheckBroke},
	6: {GateMergedRemotely},
}

func inList(v string, l []string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

// gateCase is one gate scenario; the Go verdict must be the one expected and
// the retired gate's recorded exit code must map onto it.
type gateCase struct {
	name    string
	pr      string
	setup   func(w *world)
	opts    GateOpts
	mode    string
	broken  bool
	verdict string
	changed bool
	files   []string // files the gate checkout must hold afterwards
	noFiles []string
	body    string // forge PR body (provenance cases)
	relays  string // relays recorded on the slot (provenance cases)
}

// gateOutcome is what the retired gate left behind for a case: its exit code,
// the sorted commit subjects on origin/main and on the local base, and the
// forge calls it made (shas normalised).
type gateOutcome struct {
	Exit                 int
	Origin, Local, Forge string
}

// gateExit is the retired gate's exit code for a verdict, the inverse of
// oldExitVerdicts.
func gateExit(verdict string) int {
	for exit, vs := range oldExitVerdicts {
		if inList(verdict, vs) {
			return exit
		}
	}
	return -1
}

// runGateCases runs every case and checks the collected outcomes, keyed by
// case name, against the golden. The golden's inputs describe every case, so a
// changed case fails loudly.
func runGateCases(t *testing.T, cases []gateCase) {
	t.Helper()
	var descs []map[string]any
	got := map[string]gateOutcome{}
	for _, c := range cases {
		descs = append(descs, map[string]any{"name": c.name, "pr": c.pr, "mode": c.mode, "broken": c.broken,
			"checkOnly": c.opts.CheckOnly, "noVerify": c.opts.NoVerify, "verdict": c.verdict, "changed": c.changed,
			"files": c.files, "noFiles": c.noFiles, "body": c.body, "relays": c.relays})
		t.Run(c.name, func(t *testing.T) { got[c.name] = runGateCase(t, c) })
	}
	golden.Check(t, map[string]any{"cases": descs}, got)
}

func runGateCase(t *testing.T, c gateCase) gateOutcome {
	t.Helper()
	w := newWorld(t, c.pr)
	w.mode = c.mode
	if c.mode == "" {
		w.mode = "ok"
	}
	if c.setup != nil {
		c.setup(w)
	}
	res, err := w.gate(c.broken, c.opts)
	if err != nil {
		t.Fatalf("go: %v", err)
	}
	if res.Verdict != c.verdict {
		t.Errorf("go verdict = %s (%s), want %s", res.Verdict, res.Err, c.verdict)
	}
	if res.Changed != c.changed {
		t.Errorf("changed = %v, want %v", res.Changed, c.changed)
	}
	for _, f := range c.files {
		if _, err := os.Stat(filepath.Join(w.dir, f)); err != nil {
			t.Errorf("%s: %s missing after the gate", w.dir, f)
		}
	}
	for _, f := range c.noFiles {
		if _, err := os.Stat(filepath.Join(w.dir, f)); err == nil {
			t.Errorf("%s: %s must not exist", w.dir, f)
		}
	}
	// the same commits end up on origin/main and on the local base (the order
	// of equal-second commits is not stable, so compare sorted subjects)
	subjects := func(dir string) string {
		l := strings.Split(gitq(t, dir, "log", "--format=%s", "main"), "\n")
		sort.Strings(l)
		return strings.Join(l, "\n")
	}
	// forge calls are recorded argument for argument (shas differ per world)
	gl, _ := os.ReadFile(w.log)
	return gateOutcome{Exit: gateExit(res.Verdict), Origin: subjects(w.origin), Local: subjects(w.dir),
		Forge: shaRe.ReplaceAllString(string(gl), "SHA")}
}

// advanceMainOn lands a commit on origin/main that writes file.
func advanceMainOn(w *world, file string) {
	gitq(w.t, w.worker, "checkout", "-q", "main")
	os.WriteFile(filepath.Join(w.worker, file), []byte("main\n"), 0o644)
	gitq(w.t, w.worker, "add", file)
	gitq(w.t, w.worker, "commit", "-q", "-m", "main moves")
	gitq(w.t, w.worker, "push", "-q", "origin", "main")
}

func TestGate(t *testing.T) {
	cases := []gateCase{
		{name: "a: stale on pushed refs even though local w1 merged main", pr: ghURL, opts: GateOpts{CheckOnly: true}, verdict: GateStale,
			setup: func(w *world) {
				advanceMainOn(w, "work.txt") // conflicts with w1's work.txt, so it is bounced
				gitq(t, w.dir, "fetch", "-q", "origin")
				gitq(t, w.dir, "checkout", "-q", "-b", "w1", "origin/w1")
				gitq(t, w.dir, "merge", "-q", "-X", "ours", "origin/main", "-m", "sync")
				gitq(t, w.dir, "checkout", "-q", "main")
			}},
		{name: "b: a merge-base that dies is check-broke, not stale", pr: ghURL, opts: GateOpts{CheckOnly: true}, broken: true, verdict: GateCheckBroke},
		{name: "b2: a failed fetch is check-broke", pr: ghURL, opts: GateOpts{CheckOnly: true}, verdict: GateCheckBroke,
			setup: func(w *world) {
				gitq(t, w.dir, "remote", "set-url", "origin", filepath.Join(filepath.Dir(w.dir), "nope.git"))
			}},
		{name: "c: matching PR is fresh", pr: ghURL, opts: GateOpts{CheckOnly: true}, verdict: GateFresh},
		{name: "c: head sha moved", pr: ghURL, opts: GateOpts{CheckOnly: true}, verdict: GatePRMismatch,
			setup: func(w *world) { w.forge("sha", strings.Repeat("0", 40)) }},
		{name: "c: stacked PR", pr: ghURL, opts: GateOpts{CheckOnly: true}, verdict: GatePRMismatch,
			setup: func(w *world) { w.forge("base", "stack") }},
		{name: "c: wrong head branch", pr: ghURL, opts: GateOpts{CheckOnly: true}, verdict: GatePRMismatch,
			setup: func(w *world) { w.forge("head", "other") }},
		{name: "c: PR not open", pr: ghURL, opts: GateOpts{CheckOnly: true}, verdict: GatePRMismatch,
			setup: func(w *world) { w.forge("state", "MERGED") }},
		{name: "d: github merge, verified on the merged tree", pr: ghURL, verdict: GatePass, changed: true, files: []string{"work.txt"},
			setup: func(w *world) { w.setConfig(`{"test":{"full":["test -f work.txt"]}}`) }},
		{name: "d: no verify commands", pr: ghURL, verdict: GatePass, changed: true, files: []string{"work.txt"}},
		{name: "d: --no-verify", pr: ghURL, opts: GateOpts{NoVerify: true}, verdict: GatePass, changed: true, files: []string{"work.txt"},
			setup: func(w *world) { w.setConfig(`{"test":{"full":["false"]}}`) }},
		{name: "d: verify fails after the merge landed", pr: ghURL, verdict: GateVerifyFailed, changed: true, files: []string{"work.txt"},
			setup: func(w *world) { w.setConfig(`{"test":{"full":["true","false"]}}`) }},
		{name: "e: merge that merged nothing", pr: glURL, mode: "noop", verdict: GateNotMerged, noFiles: []string{"work.txt"}},
		{name: "e: merge into another branch", pr: ghURL, mode: "elsewhere", verdict: GateNotOnBase, noFiles: []string{"work.txt"}},
		{name: "e: refused merge shows the CLI output", pr: ghURL, mode: "fail", verdict: GateMergeFailed, noFiles: []string{"work.txt"}},
		{name: "e: a push after the check is refused by the pin", pr: ghURL, mode: "race", verdict: GateMergeFailed, noFiles: []string{"work.txt"}},
		{name: "f: gitlab squash falls back to squash_commit_sha", pr: glURL, mode: "squash", verdict: GatePass, changed: true, files: []string{"work.txt"}},
		{name: "f: gitlab fast-forward merge has no merge commit", pr: glURL, mode: "ff", verdict: GatePass, changed: true, files: []string{"work.txt"}},
		{name: "f2: gitlab provenance reads the MR description", pr: glURL, opts: GateOpts{CheckOnly: true}, verdict: GateProvenanceFail,
			setup: func(w *world) {
				w.forge("body", "## Approvals\n- x: orchestrator relay round 2\n")
				w.setSlot(glURL, "[]")
			}},
		{name: "g: PR without an origin remote is refused, never merged locally", pr: ghURL, verdict: GateCheckBroke, noFiles: []string{"work.txt"},
			setup: func(w *world) { gitq(t, w.dir, "remote", "remove", "origin") }},
		{name: "h: local merge without a PR", pr: "", verdict: GatePass, changed: true, files: []string{"work.txt"},
			setup: func(w *world) { gitq(t, w.dir, "fetch", "-q", "origin", "w1:w1") }},
		{name: "h: no PR means no provenance to read", pr: "", opts: GateOpts{CheckOnly: true}, verdict: GateFresh,
			setup: func(w *world) { gitq(t, w.dir, "fetch", "-q", "origin", "w1:w1") }},
		{name: "i: merged on origin but local base diverged", pr: ghURL, verdict: GateMergedRemotely, changed: true,
			setup: func(w *world) {
				os.WriteFile(filepath.Join(w.dir, "local.txt"), []byte("local only\n"), 0o644)
				gitq(t, w.dir, "add", "local.txt")
				gitq(t, w.dir, "commit", "-q", "-m", "local only commit")
			}},
	}
	runGateCases(t, cases)
}

func TestGateProvenance(t *testing.T) {
	relay := `[{"round":2,"ts":"2026-10-02T10:00:00Z","summary":"use the shared cache for the lookup"}]`
	var cases []gateCase
	for _, c := range []struct {
		name, body, relays string
		verdict            string
	}{
		{"no section, no relays", "just a body", "[]", GateFresh},
		{"no section while relays exist", "just a body", relay, GateProvenanceFail},
		{"relay cited and logged", "## Approvals\n- x: orchestrator relay round 2\n", relay, GateFresh},
		{"relay round never sent", "## Approvals\n- x: orchestrator relay round 5\n", relay, GateProvenanceFail},
		{"relay cited with no number but none logged", "## Approvals\n- orchestrator relay said so\n", "[]", GateProvenanceFail},
		{"relay cited with no number, one logged", "## Approvals\n- orchestrator relay said so\n", relay, GateFresh},
		{"relayed text cited as the maintainer", "## Approvals\n- the maintainer: use the shared cache for the lookup\n", relay, GateProvenanceFail},
		{"maintainer line with different text", "## Approvals\n- the maintainer approved the naming in my pane\n", relay, GateFresh},
		{"short summary is not matched", "## Approvals\n- the maintainer: ok\n", `[{"round":1,"summary":"ok"}]`, GateFresh},
		{"section ends at the next heading", "## Approvals\n- orchestrator relay round 2\n## Notes\n- the maintainer: use the shared cache for the lookup\n", relay, GateFresh},
		{"heading is case-insensitive", "## approvals\n- x: orchestrator relay round 9\n", relay, GateProvenanceFail},
		{"only one section counts: ### is not a new heading", "## Approvals\n### detail\n- orchestrator relay round 9\n", relay, GateProvenanceFail},
	} {
		c := c
		cases = append(cases, gateCase{name: c.name, pr: ghURL, opts: GateOpts{CheckOnly: true}, verdict: c.verdict, body: c.body, relays: c.relays,
			setup: func(w *world) {
				w.forge("body", c.body)
				w.setSlot(ghURL, c.relays)
			}})
	}
	runGateCases(t, cases)
}

func TestGateResolutionFailures(t *testing.T) {
	w := newWorld(t, ghURL)
	exit := func(err error) int { return exitOf(err) }
	if _, err := w.gate(false, GateOpts{Base: "nope"}); exit(err) != exitcode.ExitResolution || !strings.Contains(err.Error(), "base branch 'nope' does not exist") {
		t.Errorf("missing base: %v", err)
	}
	// the worker branch is only looked up locally when there is no PR
	w.setSlot("", "")
	w.setSlot("", "")
	if _, err := w.gate(false, GateOpts{}); exit(err) != exitcode.ExitResolution || !strings.Contains(err.Error(), "worker branch 'w1' does not exist") {
		t.Errorf("missing worker branch: %v", err)
	}
	os.Remove(RegistryPath(w.dir))
	if _, err := w.gate(false, GateOpts{}); exit(err) != exitcode.ExitResolution || !strings.Contains(err.Error(), "no worker pool") {
		t.Errorf("no registry: %v", err)
	}
	w.setSlot(ghURL, "")
	e := w.env(false)
	if _, err := e.Gate(bg, w.dir, GateOpts{Slot: "w9", Base: "main"}); exit(err) != exitcode.ExitResolution || !strings.Contains(err.Error(), "slot 'w9' is not in the pool") {
		t.Errorf("unknown slot: %v", err)
	}
	// merging needs the base checked out
	gitq(t, w.dir, "checkout", "-q", "-b", "elsewhere")
	if _, err := w.gate(false, GateOpts{}); exit(err) != exitcode.ExitResolution || !strings.Contains(err.Error(), "gate must run with main checked out (currently on elsewhere)") {
		t.Errorf("wrong checkout: %v", err)
	}
	// --check-only does not need it
	if res, err := w.gate(false, GateOpts{CheckOnly: true}); err != nil || res.Verdict != GateFresh {
		t.Errorf("check-only off-base: %+v %v", res, err)
	}
}

func TestGateResultShape(t *testing.T) {
	w := newWorld(t, ghURL)
	w.setConfig(`{"test":{"full":["true"," ","echo hi"]}}`)
	res, err := w.gate(false, GateOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != GatePass || strings.Join(res.Verified, "|") != "true|echo hi" || res.VerifySkipped || !res.Changed ||
		res.PR != ghURL || res.Branch != "w1" || res.Base != "main" || len(res.SHA) < 7 {
		t.Errorf("%+v", res)
	}
	if want := gitq(t, w.dir, "rev-parse", "--short", "HEAD"); res.SHA != want {
		t.Errorf("sha = %s, want %s", res.SHA, want)
	}
}

func (e Env) withForge(f func(provider, dir string) Forge, brokenMergeBase bool) Env {
	e.Forge = func(provider, dir string, _ any) (Forge, error) { return f(provider, dir), nil }
	if brokenMergeBase {
		realGit := e.Git
		if realGit == nil {
			realGit = git.Exec
		}
		e.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
			if len(args) > 0 && args[0] == "merge-base" {
				return git.Result{ExitCode: 128}, nil
			}
			return realGit(ctx, dir, args...)
		}
	}
	return e
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
func jsonMarshal(v any) ([]byte, error)   { return json.Marshal(v) }

// A local merge that conflicts is aborted and reported, not left half done.
// (A fresh branch cannot conflict through the CLI, so the failure is injected.)
func TestGateLocalMergeFailureIsAbortedAndReported(t *testing.T) {
	w := newWorld(t, "")
	gitq(t, w.dir, "fetch", "-q", "origin", "w1:w1")
	var seen []string
	e := w.env(false)
	inner := e.Git
	if inner == nil {
		inner = git.Exec
	}
	e.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		seen = append(seen, strings.Join(args, " "))
		if len(args) > 0 && args[0] == "merge" && args[1] == "--no-ff" {
			return git.Result{Stdout: "CONFLICT (content): Merge conflict in work.txt", ExitCode: 1}, nil
		}
		if strings.Join(args, " ") == "merge --abort" {
			return git.Result{}, nil
		}
		return inner(ctx, dir, args...)
	}
	res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
	if err != nil || res.Verdict != GateMergeFailed || !strings.Contains(res.Err, "merge of w1 into main conflicted") || res.Changed {
		t.Fatalf("%+v %v", res, err)
	}
	if !strings.Contains(strings.Join(seen, "\n"), "merge --abort") {
		t.Errorf("the half-done merge was not aborted: %v", seen)
	}
}

// The Python gate read an unreadable PR body as PROVENANCE-SKIP and merged
// anyway (fail open). Only a missing forge CLI may skip now; any other read
// error is check-broke and nothing merges.
func TestGateProvenanceFailsClosedWhenTheBodyCannotBeRead(t *testing.T) {
	// failFrom makes every PRView from the nth call on fail: the first reads
	// the PR (identity check), the second its body (provenance).
	failFrom := func(n int, err error) func(Forge) Forge {
		return func(f Forge) Forge { return &flakyForge{Forge: f, from: n, err: err} }
	}
	for name, tc := range map[string]struct {
		wrap func(Forge) Forge
	}{
		"body read fails":   {failFrom(2, &tracker.Error{Kind: tracker.KindFailed, Code: 1, Message: "HTTP 502"})},
		"not authenticated": {failFrom(2, &tracker.Error{Kind: tracker.KindUnavailable, Code: 3, Message: "gh auth login"})},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			e := w.env(false)
			inner := e.Forge
			e.Forge = func(p, d string, r any) (Forge, error) {
				f, err := inner(p, d, r)
				return tc.wrap(f), err
			}
			res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
			if err != nil || res.Verdict != GateCheckBroke || res.Changed {
				t.Fatalf("%+v %v", res, err)
			}
			if _, err := os.Stat(filepath.Join(w.dir, "work.txt")); err == nil {
				t.Error("the gate merged without reading the approvals")
			}
			if strings.Contains(strings.Join(res.Notes, "\n"), "PROVENANCE-SKIP") {
				t.Errorf("must not skip: %v", res.Notes)
			}
		})
	}
	// a missing CLI still skips (the retired helper's one legitimate skip) and then
	// the PR read fails, so the gate stops there instead
	w := newWorld(t, ghURL)
	e := w.env(false)
	inner := e.Forge
	e.Forge = func(p, d string, r any) (Forge, error) {
		f, err := inner(p, d, r)
		return &flakyForge{Forge: f, from: 1, err: &tracker.Error{Kind: tracker.KindUnavailable, Code: 3, Message: "gh is not installed"}}, err
	}
	res, _ := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main", CheckOnly: true})
	if res.Verdict != GateCheckBroke {
		t.Errorf("PR info needs the CLI too: %+v", res)
	}
}

// flakyForge fails every PRView from the from-th call on with err.
type flakyForge struct {
	Forge
	from, calls int
	err         error
}

func (f *flakyForge) PRView(ctx context.Context, pr int) (tracker.PRInfo, error) {
	if f.calls++; f.calls >= f.from {
		return tracker.PRInfo{}, f.err
	}
	return f.Forge.PRView(ctx, pr)
}

func TestGateRevParseFailureIsCheckBroke(t *testing.T) {
	w := newWorld(t, ghURL)
	e := w.env(false)
	e.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		if len(args) == 2 && args[0] == "rev-parse" && args[1] == "origin/w1" {
			return git.Result{Stderr: "fatal", ExitCode: 128}, nil
		}
		return git.Exec(ctx, dir, args...)
	}
	res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main", CheckOnly: true})
	if err != nil || res.Verdict != GateCheckBroke || !strings.Contains(res.Err, "git rev-parse origin/w1 failed") {
		t.Errorf("%+v %v", res, err)
	}
	if strings.Contains(w.logText(), "PRRequestMerge") {
		t.Error("nothing may be merged")
	}
}

func (w *world) logText() string { b, _ := os.ReadFile(w.log); return string(b) }

func TestGateShellRunsUnderTheContext(t *testing.T) {
	w := newWorld(t, "")
	gitq(t, w.dir, "fetch", "-q", "origin", "w1:w1")
	w.setConfig(`{"test":{"full":["sleep 30"]}}`)
	ctx, cancel := context.WithTimeout(bg, 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	res, err := w.env(false).Gate(ctx, w.dir, GateOpts{Slot: "w1", Base: "main"})
	if err != nil || res.Verdict != GateVerifyFailed || time.Since(start) > 10*time.Second {
		t.Errorf("a cancelled context must stop the verify command: %+v %v after %v", res, err, time.Since(start))
	}
}

// A merge that fails for a reason other than a conflict (no committer identity
// was the CI case) is reported with git's own stderr, not called a conflict.
func TestGateLocalMergeNonConflictFailureShowsGitsWords(t *testing.T) {
	w := newWorld(t, "")
	gitq(t, w.dir, "fetch", "-q", "origin", "w1:w1")
	e := w.env(false)
	inner := git.Exec
	e.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		if len(args) > 1 && args[0] == "merge" && args[1] == "--no-ff" {
			return git.Result{Stderr: "fatal: unable to auto-detect email address", ExitCode: 128}, nil
		}
		return inner(ctx, dir, args...)
	}
	res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
	if err != nil || res.Verdict != GateMergeFailed || strings.Contains(res.Err, "conflicted") ||
		!strings.Contains(res.Err, "fatal: unable to auto-detect email address") ||
		!strings.Contains(res.Err, "git merge --abort failed") {
		t.Errorf("%+v %v", res, err)
	}
}

// A branch behind the base is merged by the gate itself when the merge is
// clean and the two sides share no changed file; a conflict or a shared file
// goes back to the worker.
func TestGateStaleMerge(t *testing.T) {
	for _, c := range []struct {
		name    string
		setup   func(w *world)
		cfg     string
		opts    GateOpts
		verdict string
		changed bool
		errHas  string
		note    bool
	}{
		{name: "disjoint files merge clean", setup: func(w *world) { advanceMainOn(w, "more.txt") }, verdict: GatePass, changed: true, note: true},
		{name: "disjoint files are fresh under --check-only", setup: func(w *world) { advanceMainOn(w, "more.txt") }, opts: GateOpts{CheckOnly: true}, verdict: GateFresh, note: true},
		{name: "the merged tree is still verified", setup: func(w *world) { advanceMainOn(w, "more.txt") }, cfg: `{"test":{"full":["test -f more.txt && test -f work.txt"]}}`, verdict: GatePass, changed: true, note: true},
		{name: "a shared file goes back", setup: func(w *world) { sharedFile(t, w) }, verdict: GateStale, errHas: "both sides changed seed.txt"},
		{name: "a shared path under round.sharedPaths is ignored", setup: func(w *world) { sharedFile(t, w) }, cfg: `{"round":{"sharedPaths":["seed.txt"]}}`, verdict: GatePass, changed: true, note: true},
		{name: "a conflict goes back", setup: func(w *world) { advanceMainOn(w, "work.txt") }, verdict: GateStale, errHas: "the merge conflicts"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			c.setup(w)
			if c.cfg != "" {
				w.setConfig(c.cfg)
			}
			res, err := w.gate(false, c.opts)
			if err != nil {
				t.Fatal(err)
			}
			if res.Verdict != c.verdict || res.Changed != c.changed {
				t.Fatalf("verdict %s changed %v (%s), want %s %v", res.Verdict, res.Changed, res.Err, c.verdict, c.changed)
			}
			if c.errHas != "" && !strings.Contains(res.Err, c.errHas) {
				t.Errorf("err %q lacks %q", res.Err, c.errHas)
			}
			noted := false
			for _, n := range res.Notes {
				noted = noted || strings.HasPrefix(n, "STALE-MERGE")
			}
			if noted != c.note {
				t.Errorf("STALE-MERGE note = %v, want %v: %v", noted, c.note, res.Notes)
			}
		})
	}
}

// sharedFile leaves w1 behind main with both having changed seed.txt, in
// different places, so the merge is textually clean: main grows seed.txt, w1
// merges that and edits the first line, then main edits the last. The forge is
// told w1's new head.
func sharedFile(t *testing.T, w *world) {
	t.Helper()
	lines := "a\n" + strings.Repeat("-\n", 19) + "z\n"
	gitq(t, w.worker, "checkout", "-q", "main")
	os.WriteFile(filepath.Join(w.worker, "seed.txt"), []byte(lines), 0o644)
	gitq(t, w.worker, "commit", "-q", "-am", "seed grows")
	gitq(t, w.worker, "push", "-q", "origin", "main")
	gitq(t, w.worker, "checkout", "-q", "w1")
	gitq(t, w.worker, "merge", "-q", "origin/main", "-m", "sync")
	os.WriteFile(filepath.Join(w.worker, "seed.txt"), []byte("A\n"+lines[2:]), 0o644)
	gitq(t, w.worker, "commit", "-q", "-am", "w1 edits the top")
	gitq(t, w.worker, "push", "-q", "origin", "w1")
	w.forge("sha", gitq(t, w.worker, "rev-parse", "HEAD"))
	gitq(t, w.worker, "checkout", "-q", "main")
	os.WriteFile(filepath.Join(w.worker, "seed.txt"), []byte(lines[:len(lines)-2]+"Z\n"), 0o644)
	gitq(t, w.worker, "commit", "-q", "-am", "main edits the bottom")
	gitq(t, w.worker, "push", "-q", "origin", "main")
}

func TestBounceCount(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".rota"), 0o755)
	for want := 1; want <= 3; want++ {
		if n, err := RecordBounce(root, "31", ""); err != nil || n != want {
			t.Fatalf("bounce %d: got %d, %v", want, n, err)
		}
	}
	if n, _ := RecordBounce(root, "32", ""); n != 1 {
		t.Errorf("counts are per item, got %d for another", n)
	}
	if err := ClearBounces(root, "31"); err != nil {
		t.Fatal(err)
	}
	if n, _ := RecordBounce(root, "31", ""); n != 1 {
		t.Errorf("cleared count restarts, got %d", n)
	}
}

func TestBounceSameHeadCountsOnce(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".rota"), 0o755)
	for i := 0; i < 3; i++ {
		if n, _ := RecordBounce(root, "31", "aaa"); n != 1 {
			t.Fatalf("same head re-gated: got %d, want 1", n)
		}
	}
	if n, _ := RecordBounce(root, "31", "bbb"); n != 2 {
		t.Errorf("new head counts, got %d", n)
	}
}

// The branch lands in the gate checkout before it is verified; a branch that
// empties test.full in its own .rota/config.json must not
// switch its own verification off.
func TestGateVerifyCommandsComeFromBeforeTheMerge(t *testing.T) {
	w := newWorld(t, "")
	w.setConfig(`{"test":{"full":["false"]}}`)
	gitq(t, w.dir, "add", "-f", ".rota/config.json")
	gitq(t, w.dir, "commit", "-q", "-m", "config")
	gitq(t, w.dir, "push", "-q", "origin", "main")
	gitq(t, w.worker, "pull", "-q", "--no-rebase", "origin", "main")
	if err := os.WriteFile(filepath.Join(w.worker, ".rota", "config.json"), []byte(`{"test":{"full":[]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	gitq(t, w.worker, "commit", "-q", "-am", "disable verify")
	gitq(t, w.worker, "push", "-q", "origin", "w1")
	gitq(t, w.dir, "fetch", "-q", "origin", "w1:w1")
	res, err := w.env(false).Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
	if err != nil || res.Verdict != GateVerifyFailed {
		t.Errorf("verify must use the base's commands, got %+v %v", res, err)
	}
}

// A slot that records no PR while its branch heads an open PR must take the PR
// path: the old local merge landed on the local base only, left the PR open and
// reported a pass (#210).
func TestGateFindsTheOpenPROfAnUnrecordedSlot(t *testing.T) {
	w := newWorld(t, "")
	w.forge("listed", "1")
	res, err := w.gate(false, GateOpts{})
	if err != nil || res.Verdict != GatePass || res.PR != ghURL {
		t.Fatalf("%+v %v", res, err)
	}
	if !strings.Contains(w.logText(), "PRRequestMerge 7") {
		t.Errorf("the PR was not merged through the forge:\n%s", w.logText())
	}
	if got := gitq(t, w.origin, "log", "--format=%s", "main"); !strings.Contains(got, "merge pr") {
		t.Errorf("nothing reached origin/main:\n%s", got)
	}
	if got := LoadRegistry(w.dir).Slot("w1").PR(); got != ghURL {
		t.Errorf("the slot still records no PR: %q", got)
	}
}

func TestGateCheckOnlyAdoptsTheOpenPRWithoutRecordingIt(t *testing.T) {
	w := newWorld(t, "")
	w.forge("listed", "1")
	res, err := w.gate(false, GateOpts{CheckOnly: true})
	if err != nil || res.Verdict != GateFresh || res.PR != ghURL {
		t.Fatalf("%+v %v", res, err)
	}
	if got := LoadRegistry(w.dir).Slot("w1").PR(); got != "" {
		t.Errorf("a check-only gate recorded %q", got)
	}
}

// When the forge cannot say whether a PR is open, a local merge is not safe.
func TestGateWithoutARecordedPRIsCheckBrokeWhenThePRsCannotBeListed(t *testing.T) {
	w := newWorld(t, "")
	gitq(t, w.dir, "fetch", "-q", "origin", "w1:w1")
	w.forge("listError", "simulated: rate limited")
	res, err := w.gate(false, GateOpts{})
	if err != nil || res.Verdict != GateCheckBroke || res.Changed {
		t.Fatalf("%+v %v", res, err)
	}
	if _, serr := os.Stat(filepath.Join(w.dir, "work.txt")); serr == nil {
		t.Error("the branch was merged locally")
	}
}

// A merged PR releases the claim label on the closed issues it names, and only
// there: an issue the forge left open keeps its claim.
func TestGateReleasesTheClaimLabelOfClosedIssues(t *testing.T) {
	cases := []struct {
		name, state, remErr, getErr string
		removed                     bool
		note                        string
	}{
		{name: "closed", state: "closed", removed: true},
		{name: "still open", state: "open"},
		{name: "remove fails", state: "closed", remErr: "boom", note: "cannot remove in-progress from #5"},
		{name: "read fails", state: "closed", getErr: "boom", note: "cannot read #5"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			w.forge("body", "Closes #5\n")
			w.forge("issueState", c.state)
			w.forge("issueLabels", "in-progress,type:bug")
			w.forge("removeErr", c.remErr)
			w.forge("issueErr", c.getErr)
			res, err := w.gate(false, GateOpts{})
			if err != nil || res.Verdict != GatePass {
				t.Fatalf("a failed release must not fail the merged gate: %+v %v", res, err)
			}
			if got := strings.Contains(w.logText(), "RemoveLabels 5 in-progress"); got != c.removed && c.remErr == "" {
				t.Errorf("label removed = %v, want %v\n%s", got, c.removed, w.logText())
			}
			if c.removed && w.forgeWord("issueLabels") != "type:bug" {
				t.Errorf("labels = %q", w.forgeWord("issueLabels"))
			}
			if c.note != "" && !strings.Contains(strings.Join(res.Notes, "\n"), c.note) {
				t.Errorf("notes = %v, want %q", res.Notes, c.note)
			}
		})
	}
}

func TestGateLocalMergeReportsFailedRecovery(t *testing.T) {
	w := newWorld(t, "")
	gitq(t, w.dir, "fetch", "-q", "origin", "w1:w1")
	e := w.env(false)
	e.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		if len(args) > 1 && args[0] == "merge" {
			if args[1] == "--no-ff" {
				return git.Result{ExitCode: 1, Stdout: "CONFLICT in work.txt"}, nil
			}
			if args[1] == "--abort" {
				return git.Result{ExitCode: 128, Stderr: "index.lock exists"}, nil
			}
		}
		return git.Exec(ctx, dir, args...)
	}
	res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
	if err != nil || res.Verdict != GateMergeFailed || res.Changed {
		t.Fatalf("%+v %v", res, err)
	}
	for _, want := range []string{"CONFLICT", "index.lock exists", "git merge --abort", "git status"} {
		if !strings.Contains(res.Err, want) {
			t.Errorf("result missing %q: %s", want, res.Err)
		}
	}
}
