package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/roundlease"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/golden"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/verdict"
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
	// lease, when set, is the round lease the world's env reads.
	lease func() roundlease.Env
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
	w.setConfig(`{"test":{"full":["true"]}}`)
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

// setConfig writes the project config. The gate tests exercise the github issue
// source (the closing check, claim labels, review depth) unless a case names a
// source itself.
func (w *world) setConfig(cfg string) {
	if !strings.Contains(cfg, `"source"`) && strings.HasPrefix(cfg, "{") && cfg != "{}" {
		cfg = `{"issues":{"source":"github"},` + cfg[1:]
	}
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
		Lease: w.lease,
		Sleep: func(time.Duration) {},
		Getenv: func(k string) string {
			return map[string]string{"ROTA_GATE_SHA_WAIT": "0", "PATH": os.Getenv("PATH"), "HOME": os.Getenv("HOME")}[k]
		},
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
		{name: "c: forge lags the push, then catches up", pr: ghURL, opts: GateOpts{CheckOnly: true}, verdict: GateFresh,
			setup: func(w *world) { w.forge("staleHeadFor", "3") }},
		{name: "c: forge never catches up", pr: ghURL, opts: GateOpts{CheckOnly: true}, verdict: GatePRMismatch,
			setup: func(w *world) { w.forge("staleHeadFor", "99") }},
		{name: "c: stacked PR", pr: ghURL, opts: GateOpts{CheckOnly: true}, verdict: GatePRMismatch,
			setup: func(w *world) { w.forge("base", "stack") }},
		{name: "c: wrong head branch", pr: ghURL, opts: GateOpts{CheckOnly: true}, verdict: GatePRMismatch,
			setup: func(w *world) { w.forge("head", "other") }},
		{name: "c: PR not open", pr: ghURL, opts: GateOpts{CheckOnly: true}, verdict: GatePRMismatch,
			setup: func(w *world) { w.forge("state", "MERGED") }},
		{name: "d: github merge, verified on the merged tree", pr: ghURL, verdict: GatePass, changed: true, files: []string{"work.txt"},
			setup: func(w *world) { w.setConfig(`{"test":{"full":["test -f work.txt"]}}`) }},
		{name: "d: no verify commands", pr: ghURL, verdict: GateNoVerify, noFiles: []string{"work.txt"},
			setup: func(w *world) { w.setConfig(`{"test":{"full":[]}}`) }},
		{name: "d: no verify commands with --no-verify", pr: ghURL, opts: GateOpts{NoVerify: true}, verdict: GatePass, changed: true, files: []string{"work.txt"},
			setup: func(w *world) { w.setConfig(`{"test":{"full":[]}}`) }},
		{name: "d: --no-verify", pr: ghURL, opts: GateOpts{NoVerify: true}, verdict: GatePass, changed: true, files: []string{"work.txt"},
			setup: func(w *world) { w.setConfig(`{"test":{"full":["false"]}}`) }},
		{name: "d: verify fails on the scratch merge, nothing lands", pr: ghURL, verdict: GateVerifyFailed, noFiles: []string{"work.txt"},
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

// The result names the issue the gate acted on: the slot's task, "" when the
// slot holds none. The issue is resolved before any step, so a refusal carries it.
func TestGateResultNamesTheSlotsIssue(t *testing.T) {
	for _, c := range []struct{ name, slot, want string }{
		{"task", `{"name":"w1","branch":"w1","task":"12"}`, "12"},
		{"neither", `{"name":"w1","branch":"w1","pr":"` + ghURL + `"}`, ""},
	} {
		w := newWorld(t, ghURL)
		os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"), []byte(`{"slots":[`+c.slot+`]}`), 0o644)
		res, err := w.gate(false, GateOpts{CheckOnly: true})
		if res.Issue != c.want {
			t.Errorf("%s: issue %q (%v), want %q", c.name, res.Issue, err, c.want)
		}
	}
}

func (e Env) withForge(f func(provider, dir string) Forge, brokenMergeBase bool) Env {
	e.Forge = func(provider, dir string, _ tracker.Settings) (Forge, error) { return f(provider, dir), nil }
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
			e.Forge = func(p, d string, r tracker.Settings) (Forge, error) {
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
	e.Forge = func(p, d string, r tracker.Settings) (Forge, error) {
		f, err := inner(p, d, r)
		return &flakyForge{Forge: f, from: 1, err: &tracker.Error{Kind: tracker.KindUnavailable, Code: 3, CLIMissing: true, Message: "gh is not installed"}}, err
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
	e := w.env(false)
	// Every git call is slow, as on a loaded machine: the verify command must
	// still be what the cancellation lands on, not the merge or the worktree.
	e.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		time.Sleep(200 * time.Millisecond)
		return git.Exec(ctx, dir, args...)
	}
	started := filepath.Join(t.TempDir(), "started")
	w.setConfig(`{"test":{"full":["touch ` + started + `; sleep 30"]}}`)
	ctx, cancel := context.WithCancel(bg)
	defer cancel()
	go func() { // cancel once the verify command is running, however long the setup took
		for ctx.Err() == nil {
			if _, err := os.Stat(started); err == nil {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	start := time.Now()
	res, err := e.Gate(ctx, w.dir, GateOpts{Slot: "w1", Base: "main"})
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
// clean, even when both sides changed a file; only a conflict goes back to the
// worker.
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
		noteHas string
	}{
		{name: "disjoint files merge clean", setup: func(w *world) { advanceMainOn(w, "more.txt") }, verdict: GatePass, changed: true, note: true},
		{name: "disjoint files are fresh under --check-only", setup: func(w *world) { advanceMainOn(w, "more.txt") }, opts: GateOpts{CheckOnly: true}, verdict: GateFresh, note: true},
		{name: "the merged tree is still verified", setup: func(w *world) { advanceMainOn(w, "more.txt") }, cfg: `{"test":{"full":["test -f more.txt && test -f work.txt"]}}`, verdict: GatePass, changed: true, note: true},
		{name: "a shared file with a clean merge lands after verify", setup: func(w *world) { sharedFile(t, w) }, verdict: GatePass, changed: true, note: true, noteHas: "both sides changed seed.txt"},
		{name: "a shared path under round.sharedPaths is ignored", setup: func(w *world) { sharedFile(t, w) }, cfg: `{"test":{"full":["true"]},"round":{"sharedPaths":["seed.txt"]}}`, verdict: GatePass, changed: true, note: true},
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
				if strings.HasPrefix(n, "STALE-MERGE") {
					noted = true
					if c.noteHas != "" && !strings.Contains(n, c.noteHas) {
						t.Errorf("note %q lacks %q", n, c.noteHas)
					}
				}
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
		if n, err := recordBounceVia(Env{}, root, "31", "", ""); err != nil || n != want {
			t.Fatalf("bounce %d: got %d, %v", want, n, err)
		}
	}
	if n, _ := recordBounceVia(Env{}, root, "32", "", ""); n != 1 {
		t.Errorf("counts are per item, got %d for another", n)
	}
	if err := ClearBounces(root, "31"); err != nil {
		t.Fatal(err)
	}
	if n, _ := recordBounceVia(Env{}, root, "31", "", ""); n != 1 {
		t.Errorf("cleared count restarts, got %d", n)
	}
}

// The cap and the increment share one lock: concurrent bounces never take the
// count past max, and exactly max of them count.
func TestRecordBounceCappedConcurrentNeverExceedsMax(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".rota"), 0o755)
	const max, callers = 3, 12
	var wg sync.WaitGroup
	var counted, capped atomic.Int32
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			n, _, c, err := recordBounceCappedVia(Env{}, nil, root, "31", "", fmt.Sprintf("head%d", i), max)
			if err != nil || n > max {
				t.Errorf("caller %d: n=%d err=%v", i, n, err)
			}
			if c {
				capped.Add(1)
			} else {
				counted.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if counted.Load() != max || capped.Load() != callers-max {
		t.Errorf("counted %d capped %d, want %d and %d", counted.Load(), capped.Load(), max, callers-max)
	}
	if n := LoadRegistryTolerant(root).Bounces("31"); n != max {
		t.Errorf("registry holds %d bounces, want %d", n, max)
	}
	if n, _, c, _ := recordBounceCappedVia(Env{}, nil, root, "31", "", "late", max); !c || n != max {
		t.Errorf("at the cap: n=%d capped=%v", n, c)
	}
	if n, _, c, _ := recordBounceCappedVia(Env{}, nil, root, "31", "", "late", 0); c || n != max+1 {
		t.Errorf("cap 0 is off: n=%d capped=%v", n, c)
	}
}

func TestBounceSameHeadCountsOnce(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".rota"), 0o755)
	for i := 0; i < 3; i++ {
		if n, _ := recordBounceVia(Env{}, root, "31", "", "aaa"); n != 1 {
			t.Fatalf("same head re-gated: got %d, want 1", n)
		}
	}
	if n, _ := recordBounceVia(Env{}, root, "31", "", "bbb"); n != 2 {
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
	if got := LoadRegistryTolerant(w.dir).Slot("w1").PR(); got != ghURL {
		t.Errorf("the slot still records no PR: %q", got)
	}
}

// Approve is handed the PR the gate adopted, so the approval thread is the
// PR's, not the issue's.
func TestGateApproveSeesTheAdoptedPR(t *testing.T) {
	w := newWorld(t, "")
	w.forge("listed", "1")
	var got GateTarget
	res, err := w.gate(false, GateOpts{Approve: func(t GateTarget, _ func() ([]string, error)) (*exitcode.Refusal, error) { got = t; return nil, nil }})
	if err != nil || res.Verdict != GatePass {
		t.Fatalf("%+v %v", res, err)
	}
	if got.PR != ghURL {
		t.Errorf("Approve saw PR %q, want the adopted %q", got.PR, ghURL)
	}
}

// An unrecorded external PR takes its issue from the head branch the forge
// reports, so the closes check still applies to it.
func TestGateExternalPRIssueComesFromItsHead(t *testing.T) {
	w := newWorld(t, "")
	gitq(t, w.worker, "push", "-q", "origin", "w1:dana/5-x")
	w.forge("head", "dana/5-x")
	w.forge("body", "Adds the thing.\n")
	res, _ := w.env(false).Gate(bg, w.dir, GateOpts{Slot: "7", Base: "main", CheckOnly: true})
	if res.Issue != "5" || res.Verdict != GateNotClosing {
		t.Errorf("issue %q verdict %q, want 5 and %s: %+v", res.Issue, res.Verdict, GateNotClosing, res)
	}
}

// #1598 AC-8 (T7): main's adopted slot for a support PR judges the head the
// support machine pushed since, not the one it was adopted at, so a fix that
// answers a bounce is gated as itself.
func TestGateAdoptedSupportSlotJudgesThePushedFixHead(t *testing.T) {
	w := newWorld(t, "")
	const branch = "mac-ben/5-x"
	gitq(t, w.worker, "push", "-q", "origin", "w1:"+branch)
	if err := RegisterExternal(w.dir, "ext-1", branch, "", "main", "5", ghURL); err != nil {
		t.Fatal(err)
	}
	w.forge("head", branch)
	w.forge("body", "Closes #5")
	w.forge("sha", gitq(t, w.worker, "rev-parse", "HEAD"))
	gate := func() string {
		t.Helper()
		res, err := w.env(false).Gate(bg, w.dir, GateOpts{Slot: "ext-1", Base: "main", CheckOnly: true})
		if err != nil || res.SHA == "" {
			t.Fatalf("gate = %+v, %v", res, err)
		}
		return res.SHA
	}
	first := gate()
	if err := os.WriteFile(filepath.Join(w.worker, "fix.txt"), []byte("fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitq(t, w.worker, "add", "fix.txt")
	gitq(t, w.worker, "commit", "-q", "-m", "answer the bounce")
	gitq(t, w.worker, "push", "-q", "origin", "w1:"+branch)
	w.forge("sha", gitq(t, w.worker, "rev-parse", "HEAD"))
	want := strings.TrimSpace(gitq(t, w.worker, "rev-parse", "--short=7", "HEAD"))
	if got := gate(); got != want || got == first {
		t.Errorf("the adopted slot judged %s, want the pushed fix %s (adopted at %s)", got, want, first)
	}
}

func TestGateCheckOnlyAdoptsTheOpenPRWithoutRecordingIt(t *testing.T) {
	w := newWorld(t, "")
	w.forge("listed", "1")
	res, err := w.gate(false, GateOpts{CheckOnly: true})
	if err != nil || res.Verdict != GateFresh || res.PR != ghURL {
		t.Fatalf("%+v %v", res, err)
	}
	if got := LoadRegistryTolerant(w.dir).Slot("w1").PR(); got != "" {
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

func TestGateRunsE2EAfterFull(t *testing.T) {
	w := newWorld(t, "")
	gitq(t, w.dir, "fetch", "-q", "origin", "w1:w1")
	w.setConfig(`{"test":{"full":["true"],"e2e":["test -f work.txt"]}}`)
	res, err := w.gate(false, GateOpts{})
	if err != nil || res.Verdict != GatePass || res.VerifySkipped {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestGateRunsE2EWhenFullIsEmpty(t *testing.T) {
	w := newWorld(t, "")
	gitq(t, w.dir, "fetch", "-q", "origin", "w1:w1")
	w.setConfig(`{"test":{"full":[],"e2e":["false"]}}`)
	res, _ := w.gate(false, GateOpts{})
	if res.Verdict != GateVerifyFailed || !strings.Contains(res.Err, "test.e2e") {
		t.Fatalf("e2e must gate even without test.full: %+v", res)
	}
}

func TestGateE2ERedFails(t *testing.T) {
	w := newWorld(t, "")
	gitq(t, w.dir, "fetch", "-q", "origin", "w1:w1")
	w.setConfig(`{"test":{"full":["true"],"e2e":["false"]}}`)
	res, err := w.gate(false, GateOpts{})
	if err != nil || res.Verdict != GateVerifyFailed || !strings.Contains(res.Err, "test.e2e") {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestGateSkipsE2EWhenFullRed(t *testing.T) {
	w := newWorld(t, "")
	gitq(t, w.dir, "fetch", "-q", "origin", "w1:w1")
	ran := filepath.Join(t.TempDir(), "e2e-ran.txt") // outside the scratch tree the verify runs in
	w.setConfig(fmt.Sprintf(`{"test":{"full":["false"],"e2e":["echo ran >> %s"]}}`, ran))
	res, _ := w.gate(false, GateOpts{})
	if res.Verdict != GateVerifyFailed || strings.Contains(res.Err, "test.e2e") {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("e2e ran after a red test.full")
	}
}

// A refused Approve reaches the caller as a typed refusal on the result, data
// intact, for the gate and (in train_test.go) the train.
func TestGateApprovalRefusalIsTyped(t *testing.T) {
	w := newWorld(t, ghURL)
	want := &exitcode.Refusal{Err: &exitcode.Error{Exit: exitcode.ExitRefused, Message: "gate"}, Data: BlockData{BlockedBy: "manual gate"}}
	res, err := w.gate(false, GateOpts{Approve: func(GateTarget, func() ([]string, error)) (*exitcode.Refusal, error) { return want, nil }})
	if res.Verdict != GateApprovalRequired {
		t.Fatalf("verdict %q", res.Verdict)
	}
	if res.Refusal != want || err != want.Err || !res.Refused(err) {
		t.Errorf("refusal = %+v, err %v, refused %v", res.Refusal, err, res.Refused(err))
	}
}

// A recorded FAIL (the Verdict port's error) refuses the gate before the merge,
// also under --check-only; a nil port, a PASS and an absent record pass.
func TestGateVerdictRefusal(t *testing.T) {
	block := fmt.Errorf("FAIL recorded")
	for _, check := range []bool{false, true} {
		w := newWorld(t, ghURL)
		var seen string
		res, err := w.gate(false, GateOpts{CheckOnly: check, Verdict: func(b string) error { seen = b; return block }})
		if err != block || res.Verdict != GateVerdictBlocked || res.Changed {
			t.Fatalf("check=%v: %+v %v", check, res, err)
		}
		if seen == "" {
			t.Errorf("check=%v: the port got no branch", check)
		}
		if w.onMain("work.txt") {
			t.Errorf("check=%v: a FAIL-verdict branch merged", check)
		}
	}
	w := newWorld(t, ghURL)
	if res, err := w.gate(false, GateOpts{CheckOnly: true, Verdict: func(string) error { return nil }}); err != nil || !res.OK() {
		t.Errorf("no blocking record: %+v %v", res, err)
	}
}

// An empty test.full refuses before the merge; --no-verify merges and says so.
func TestGateRefusesEmptyTestFull(t *testing.T) {
	w := newWorld(t, "")
	gitq(t, w.dir, "fetch", "-q", "origin", "w1:w1")
	w.setConfig(`{"test":{"full":[]}}`)
	res, err := w.gate(false, GateOpts{})
	if err != nil || res.Verdict != GateNoVerify || res.Changed || w.onMain("work.txt") {
		t.Fatalf("%+v %v", res, err)
	}
	if !strings.Contains(res.Err, "test.full") || !strings.Contains(res.Hint, "rota config set test.full") || !strings.Contains(res.Hint, "--no-verify") {
		t.Errorf("refusal must name the key and the way out: %q / %q", res.Err, res.Hint)
	}
	res, err = w.gate(false, GateOpts{NoVerify: true})
	if err != nil || res.Verdict != GatePass || !res.VerifySkipped || !strings.Contains(strings.Join(res.Notes, "\n"), "NO-VERIFY") {
		t.Fatalf("--no-verify: %+v %v", res, err)
	}
}

// A PR whose body does not close the slot's issue is refused before the merge,
// unless the issue is labelled a partial slice.
func TestGateRefusesAPRThatDoesNotCloseItsIssue(t *testing.T) {
	cases := []struct {
		name, body, labels string
		check              bool
		verdict            string
	}{
		{name: "no keyword", body: "Adds the thing.\n", verdict: GateNotClosing},
		{name: "refs is not closing", body: "Refs #5\n", verdict: GateNotClosing},
		{name: "another issue", body: "Closes #6\n", verdict: GateNotClosing},
		{name: "no keyword under check-only", body: "x", check: true, verdict: GateNotClosing},
		{name: "closes", body: "Closes #5\n", verdict: GatePass},
		{name: "fixes", body: "Fixes #5\n", verdict: GatePass},
		{name: "partial slice", body: "Refs #5\n", labels: "in-progress," + PartialSliceLabel, verdict: GatePass},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"),
				[]byte(fmt.Sprintf(`{"slots":[{"name":"w1","branch":"w1","task":"#5","pr":%q}]}`, ghURL)), 0o644)
			w.forge("body", c.body)
			w.forge("issueLabels", c.labels)
			res, err := w.gate(false, GateOpts{CheckOnly: c.check})
			want := c.verdict
			if c.check && want == GatePass {
				want = GateFresh
			}
			if err != nil || res.Verdict != want {
				t.Fatalf("verdict = %q (%v), want %q: %+v", res.Verdict, err, want, res)
			}
			if c.verdict == GateNotClosing {
				if res.Changed || w.onMain("work.txt") {
					t.Errorf("nothing may land: %+v", res)
				}
				if !strings.Contains(res.Hint, "Closes #5") || !strings.Contains(res.Hint, PartialSliceLabel) {
					t.Errorf("hint must show the line to add: %q", res.Hint)
				}
			}
		})
	}
}

// A partial-slice issue is closed by no slice but the last: a PR the forge says
// closes it (a keyword in prose counts) is refused unless FinalSlice is set, and
// a forge that cannot report closing references skips the check and says so.
func TestGateRefusesASlicePRTheForgeSaysClosesItsIssue(t *testing.T) {
	cases := []struct {
		name, refs string
		final      bool
		verdict    string
		note       bool
	}{
		{name: "forge says it closes", refs: "5", verdict: GateNotClosing},
		{name: "forge lists another issue", refs: "6", verdict: GatePass},
		{name: "forge lists none", refs: "none", verdict: GatePass},
		{name: "final slice may close", refs: "5", final: true, verdict: GatePass},
		{name: "forge cannot report", refs: "", verdict: GatePass, note: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"),
				[]byte(fmt.Sprintf(`{"slots":[{"name":"w1","branch":"w1","task":"#5","pr":%q}]}`, ghURL)), 0o644)
			w.forge("body", "Refs #5\n\nD=T8 learnings (Closes #5)\n")
			w.forge("issueLabels", "in-progress,"+PartialSliceLabel)
			w.forge("closingRefs", c.refs)
			res, err := w.gate(false, GateOpts{FinalSlice: c.final})
			if err != nil || res.Verdict != c.verdict {
				t.Fatalf("verdict = %q (%v), want %q: %+v", res.Verdict, err, c.verdict, res)
			}
			if c.verdict == GateNotClosing {
				if res.Changed || w.onMain("work.txt") {
					t.Errorf("nothing may land: %+v", res)
				}
				if !strings.Contains(res.Hint, "closing keyword") || !strings.Contains(res.Hint, "--final-slice") {
					t.Errorf("hint must name the keyword to drop and the flag: %q", res.Hint)
				}
			}
			if got := strings.Count(strings.Join(res.Notes, "\n"), "CLOSING-REFS-UNKNOWN"); got != map[bool]int{true: 1, false: 0}[c.note] {
				t.Errorf("CLOSING-REFS-UNKNOWN notes = %d, notes %q", got, res.Notes)
			}
		})
	}
}

// A PR no slot or review record owns (a fix-forward PR, a PR after wind-down)
// is gated by number like a round PR: it lands when fresh and is refused STALE
// when its merge with the base conflicts.
func TestGateAnUnrecordedPR(t *testing.T) {
	for _, c := range []struct {
		name    string
		arg     string
		setup   func(w *world)
		verdict string
	}{
		{name: "fresh PR lands", arg: "7", verdict: GatePass},
		{name: "hash ref lands", arg: "#7", verdict: GatePass},
		{name: "url lands", arg: ghURL, verdict: GatePass},
		{name: "conflicting PR is refused", arg: "7", setup: func(w *world) { advanceMainOn(w, "work.txt") }, verdict: GateStale},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, "")
			// the pool records no PR at all: nothing in it owns #7
			if c.setup != nil {
				c.setup(w)
			}
			res, err := w.env(false).Gate(bg, w.dir, GateOpts{Slot: c.arg, Base: "main"})
			if err != nil || res.Verdict != c.verdict {
				t.Fatalf("verdict = %q (%v), want %q: %+v", res.Verdict, err, c.verdict, res)
			}
			landed := c.verdict == GatePass
			if res.Changed != landed || w.onMain("work.txt") != landed {
				t.Errorf("landed = %v, want %v: %+v", res.Changed, landed, res)
			}
			if res.Branch != "w1" {
				t.Errorf("branch = %q, want the PR's head w1", res.Branch)
			}
		})
	}
}

// A fast-forward refused by a dirty file the merge touches is not divergence:
// the verdict names the file, and says the merged tree was not verified.
func TestGateMergedRemotelyCause(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(w *world)
		want  string
	}{
		{"dirty file the merge touches", func(w *world) {
			os.WriteFile(filepath.Join(w.dir, "work.txt"), []byte("local edit\n"), 0o644)
		}, "local changes would be overwritten: work.txt"},
		{"local-only commit", func(w *world) {
			os.WriteFile(filepath.Join(w.dir, "local.txt"), []byte("local only\n"), 0o644)
			gitq(t, w.dir, "add", "local.txt")
			gitq(t, w.dir, "commit", "-q", "-m", "local only commit")
		}, "diverged: 1 ahead, 2 behind origin/main"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			w.mode = "ok"
			c.setup(w)
			res, err := w.gate(false, GateOpts{})
			if err != nil {
				t.Fatal(err)
			}
			if res.Verdict != GateMergedRemotely || !res.Changed || !res.VerifySkipped {
				t.Fatalf("verdict=%s changed=%v verifySkipped=%v: %s", res.Verdict, res.Changed, res.VerifySkipped, res.Err)
			}
			if !strings.Contains(res.Err, c.want) || !strings.Contains(res.Hint, "NOT verified") {
				t.Errorf("err=%q hint=%q, want %q and a not-verified hint", res.Err, res.Hint, c.want)
			}
		})
	}
}

// An attempt of a best-of:2 issue is refused until a pick names its PR.
func TestGateRefusesAnUnpickedBestOfPR(t *testing.T) {
	cases := []struct {
		name    string
		bestOf  string // the registry's bestOf object, "" for none
		check   bool
		verdict string
	}{
		{name: "unpicked", bestOf: `{"5":{"attempts":[{"slot":"w1"},{"slot":"w2"}],"pick":"","round":1}}`, verdict: GateBestOfUnpicked},
		{name: "unpicked under check-only", bestOf: `{"5":{"attempts":[{"slot":"w1"},{"slot":"w2"}],"pick":"","round":1}}`, check: true, verdict: GateBestOfUnpicked},
		{name: "pick names another PR", bestOf: `{"5":{"attempts":[{"slot":"w1"},{"slot":"w2"}],"pick":"#8","round":1}}`, verdict: GateBestOfUnpicked},
		{name: "pick names this PR", bestOf: `{"5":{"attempts":[{"slot":"w1"},{"slot":"w2"}],"pick":"#7","round":1}}`, verdict: GatePass},
		{name: "picked by URL", bestOf: `{"5":{"attempts":[{"slot":"w1"},{"slot":"w2"}],"pick":"` + ghURL + `","round":1}}`, check: true, verdict: GateFresh},
		{name: "another issue is best-of", bestOf: `{"6":{"attempts":[],"pick":"","round":1}}`, verdict: GatePass},
		{name: "not best-of", verdict: GatePass},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			extra := ""
			if c.bestOf != "" {
				extra = `,"bestOf":` + c.bestOf
			}
			os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"),
				[]byte(fmt.Sprintf(`{"slots":[{"name":"w1","branch":"w1","task":"#5","pr":%q}]%s}`, ghURL, extra)), 0o644)
			w.forge("body", "Closes #5\n")
			res, err := w.gate(false, GateOpts{CheckOnly: c.check})
			if err != nil || res.Verdict != c.verdict {
				t.Fatalf("verdict = %q (%v), want %q: %+v", res.Verdict, err, c.verdict, res)
			}
			if c.verdict != GateBestOfUnpicked {
				return
			}
			if res.Changed || w.onMain("work.txt") {
				t.Errorf("nothing may land: %+v", res)
			}
			if !strings.Contains(res.Hint, "rota round pick 5 --pr") {
				t.Errorf("hint must name the pick verb: %q", res.Hint)
			}
			if strings.Contains(c.name, "another PR") != strings.Contains(res.Err, "this attempt lost") {
				t.Errorf("loser wording: %q", res.Err)
			}
		})
	}
}

// The gate applies ship.review: the resolved depth decides which recorded
// review verdicts it requires (full both, light the Spec one (Standards when there is no issue), none no
// verdict), and it refuses a branch that lacks them. The depth is noted either way.
func TestGateAppliesTheReviewDepth(t *testing.T) {
	policy := `{"ship":{"review":{"default":"full","lightBelow":1000,"labels":{"risk:high":"full","partial-slice":"none"}}},"test":{"full":["true"]}}`
	spec, quality := verdict.ReviewSpec, verdict.ReviewQuality
	for _, c := range []struct {
		name, cfg, labels, note string
		recorded                []string
		refused                 string // the missing kinds the refusal names; "" passes
	}{
		{"small diff is light, spec verdict passes", policy, "in-progress", "REVIEW-DEPTH w1 — light (1 changed lines, under lightBelow 1000)", []string{spec}, ""},
		{"light without a verdict is refused", policy, "in-progress", "light", nil, spec},
		{"light with only a standards verdict is refused", policy, "in-progress", "light", []string{quality}, spec},
		{"risk:high forces full, both verdicts pass", policy, "in-progress,risk:high", "REVIEW-DEPTH w1 — full (label risk:high)", []string{spec, quality}, ""},
		{"full with a standards verdict only is refused", policy, "in-progress,risk:high", "full", []string{quality}, spec},
		{"full with no verdict is refused", policy, "in-progress,risk:high", "full", nil, spec + " and " + quality},
		{"none needs no verdict", policy, "in-progress," + PartialSliceLabel, "REVIEW-DEPTH w1 — none (label partial-slice)", nil, ""},
		{"legacy false needs no verdict", `{"ship":{"review":false},"test":{"full":["true"]}}`, "in-progress", "REVIEW-DEPTH w1 — none (ship.review is false)", nil, ""},
		{"default full with no verdict is refused", `{"test":{"full":["true"]}}`, "in-progress", "full", nil, spec + " and " + quality},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			w.setConfig(c.cfg)
			os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"),
				[]byte(fmt.Sprintf(`{"slots":[{"name":"w1","branch":"w1","task":"#5","pr":%q}]}`, ghURL)), 0o644)
			w.forge("body", "Closes #5\n")
			w.forge("issueLabels", c.labels)
			w.record("w1", "", c.recorded...)
			res, err := w.gate(false, GateOpts{CheckOnly: true, CheckReviews: true})
			if err != nil {
				t.Fatal(err)
			}
			if c.refused == "" {
				if res.Verdict != GateFresh {
					t.Fatalf("%+v", res)
				}
			} else if res.Verdict != GateReviewMissing || !strings.Contains(res.Err, "needs a recorded "+c.refused+" verdict") || res.Changed {
				t.Fatalf("verdict %s (%s), want review-missing naming %s", res.Verdict, res.Err, c.refused)
			}
			if got := strings.Join(res.Notes, "\n"); !strings.Contains(got, c.note) {
				t.Errorf("notes %q lack %q", got, c.note)
			}
		})
	}
}

// The gate lifts a light review on the PR's Door line and on a diff outside the
// issue's declared files, and a label mapped to none beats both. Deleting the
// PR-body or issue wiring in stepReviewDepth fails these.
func TestGateReviewDepthSignals(t *testing.T) {
	light := `{"ship":{"review":{"default":"full","lightBelow":1000,"labels":{"partial-slice":"none"}}},"test":{"full":["true"]}}`
	spec, quality := verdict.ReviewSpec, verdict.ReviewQuality
	const door = "Closes #5\n\nDoor: one-way. Blast radius: the schema.\n"
	for _, c := range []struct {
		name, cfg, body, labels, issueBody, note string
		refused                                  string // "" passes
	}{
		{"light with no signal passes", light, "Closes #5\n", "in-progress", "", "light", ""},
		{"one-way door lifts light to full", light, door, "in-progress", "", "REVIEW-DEPTH w1 — full (PR body says Door: one-way)", quality},
		{"two-way door stays light", light, "Closes #5\n\nDoor: two-way.\n", "in-progress", "", "light", ""},
		{"door lifts a default of none", `{"ship":{"review":"none"},"test":{"full":["true"]}}`, door, "in-progress", "", "full", quality},
		{"label none beats the door", light, door, "in-progress," + PartialSliceLabel, "", "none", ""},
		{"diff outside the issue's files lifts to full", light, "Closes #5\n", "in-progress", "## Files\n\n- other.txt\n", "files the issue never named: work.txt", quality},
		{"declared file stays light", light, "Closes #5\n", "in-progress", "## Files\n\n- work.txt\n", "light", ""},
		{"scopeDrift false stays light", `{"ship":{"review":{"default":"full","lightBelow":1000,"scopeDrift":false}},"test":{"full":["true"]}}`, "Closes #5\n", "in-progress", "## Files\n\n- other.txt\n", "light", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			w.setConfig(c.cfg)
			os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"),
				[]byte(fmt.Sprintf(`{"slots":[{"name":"w1","branch":"w1","task":"#5","pr":%q}]}`, ghURL)), 0o644)
			w.forge("body", c.body)
			w.forge("issueLabels", c.labels)
			w.forge("issueBody", c.issueBody)
			w.record("w1", "", spec)
			res, err := w.gate(false, GateOpts{CheckOnly: true, CheckReviews: true})
			if err != nil {
				t.Fatal(err)
			}
			if c.refused == "" {
				if res.Verdict != GateFresh {
					t.Fatalf("%+v", res)
				}
			} else if res.Verdict != GateReviewMissing || !strings.Contains(res.Err, "needs a recorded "+c.refused+" verdict") {
				t.Fatalf("verdict %s (%s), want review-missing naming %s", res.Verdict, res.Err, c.refused)
			}
			if got := strings.Join(res.Notes, "\n"); !strings.Contains(got, c.note) {
				t.Errorf("notes %q lack %q", got, c.note)
			}
		})
	}
}

// An external PR no slot owns takes its issue from the head branch the forge
// reports (#795); the depth step must read that recomputed target, for the
// issue's files and for the PR body's Door line.
func TestGateReviewDepthOnAnExternalPR(t *testing.T) {
	spec, quality := verdict.ReviewSpec, verdict.ReviewQuality
	for _, c := range []struct{ name, body, issueBody, note string }{
		{"door", "Closes #5\n\nDoor: one-way. Blast radius: x.\n", "", "PR body says Door: one-way"},
		{"scope drift", "Closes #5\n", "## Files\n\n- other.txt\n", "files the issue never named"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, "")
			w.setConfig(`{"ship":{"review":{"default":"full","lightBelow":1000}},"test":{"full":["true"]}}`)
			gitq(t, w.worker, "push", "-q", "origin", "w1:dana/5-x")
			w.forge("head", "dana/5-x")
			w.forge("body", c.body)
			w.forge("issueLabels", "in-progress")
			w.forge("issueBody", c.issueBody)
			w.record("dana/5-x", "", spec)
			res, _ := w.env(false).Gate(bg, w.dir, GateOpts{Slot: "7", Base: "main", CheckOnly: true, CheckReviews: true})
			if res.Verdict != GateReviewMissing || !strings.Contains(res.Err, "needs a recorded "+quality+" verdict") {
				t.Fatalf("verdict %s (%s), want review-missing naming %s: %+v", res.Verdict, res.Err, quality, res)
			}
			if got := strings.Join(res.Notes, "\n"); !strings.Contains(got, c.note) {
				t.Errorf("notes %q lack %q", got, c.note)
			}
		})
	}
}

// A branch that references no issue has no spec to review (#683): light keeps
// asking for the Standards verdict only.
func TestGateLightWithoutAnIssueNeedsStandards(t *testing.T) {
	spec, quality := verdict.ReviewSpec, verdict.ReviewQuality
	for _, c := range []struct {
		name     string
		recorded []string
		refused  string
	}{
		{"standards verdict passes", []string{quality}, ""},
		{"spec verdict alone is refused", []string{spec}, quality},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			w.setConfig(`{"ship":{"review":"light"},"test":{"full":["true"]}}`)
			w.forge("body", "no issue here\n")
			w.record("w1", "", c.recorded...)
			res, err := w.gate(false, GateOpts{CheckOnly: true, CheckReviews: true})
			if err != nil {
				t.Fatal(err)
			}
			if c.refused == "" {
				if res.Verdict != GateFresh {
					t.Fatalf("%+v", res)
				}
			} else if res.Verdict != GateReviewMissing || !strings.Contains(res.Err, "needs a recorded "+c.refused+" verdict") {
				t.Fatalf("verdict %s (%s), want review-missing naming %s", res.Verdict, res.Err, c.refused)
			}
		})
	}
}

// Without CheckReviews the gate only reports the depth, as the file
// backend and the tests that never set it expect; a failed label read or a
// malformed policy is a check-broke, never a guessed depth.
func TestGateReviewDepthUnresolvable(t *testing.T) {
	w := newWorld(t, ghURL)
	w.setConfig(`{"ship":{"review":{"default":"full","labels":{"risk:high":"full"}}},"test":{"full":["true"]}}`)
	os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"),
		[]byte(fmt.Sprintf(`{"slots":[{"name":"w1","branch":"w1","task":"#5","pr":%q}]}`, ghURL)), 0o644)
	w.forge("body", "Closes #5\n")
	if res, err := w.gate(false, GateOpts{CheckOnly: true}); err != nil || res.Verdict != GateFresh {
		t.Fatalf("no port: %+v %v", res, err)
	}
	w.setConfig(`{"ship":{"review":{"default":"deep"}},"test":{"full":["true"]}}`)
	if res, err := w.gate(false, GateOpts{CheckOnly: true}); err != nil || res.Verdict != GateCheckBroke {
		t.Errorf("malformed policy: %+v %v", res, err)
	}
}

// A label-read failure is a check-broke, never a guessed depth: guessing could
// pass a risk:high branch as light. The recorded verdicts are passed for every
// kind, so only the failed read can refuse.
func TestGateReviewDepthLabelReadFailureIsCheckBroke(t *testing.T) {
	w := newWorld(t, ghURL)
	w.setConfig(`{"ship":{"review":{"default":"full","labels":{"risk:high":"full"}}},"test":{"full":["true"]}}`)
	os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"),
		[]byte(fmt.Sprintf(`{"slots":[{"name":"w1","branch":"w1","task":"#5","pr":%q}]}`, ghURL)), 0o644)
	w.forge("body", "Closes #5\n")
	w.forge("issueErr", "HTTP 500: server error")
	w.record("w1", "", verdict.ReviewSpec, verdict.ReviewQuality)
	res, err := w.gate(false, GateOpts{CheckOnly: true, CheckReviews: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != GateCheckBroke || !strings.Contains(res.Err, "could not read #5 for its labels") {
		t.Errorf("label read failure: %s (%s)", res.Verdict, res.Err)
	}
}

// An unreadable diff is a check-broke too: the size rule needs it.
func TestGateReviewDepthDiffReadFailureIsCheckBroke(t *testing.T) {
	w := newWorld(t, ghURL)
	w.setConfig(`{"ship":{"review":{"default":"full","lightBelow":100}},"test":{"full":["true"]}}`)
	os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"),
		[]byte(fmt.Sprintf(`{"slots":[{"name":"w1","branch":"w1","task":"#5","pr":%q}]}`, ghURL)), 0o644)
	w.forge("body", "Closes #5\n")
	e := w.env(false)
	realGit := e.Git
	if realGit == nil {
		realGit = git.Exec
	}
	e.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		if len(args) > 1 && args[0] == "diff" && args[1] == "--numstat" {
			return git.Result{ExitCode: 128}, nil
		}
		return realGit(ctx, dir, args...)
	}
	w.record("w1", "", verdict.ReviewSpec, verdict.ReviewQuality)
	res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main", CheckOnly: true, CheckReviews: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != GateCheckBroke || !strings.Contains(res.Err, "could not diff") {
		t.Errorf("diff read failure: %s (%s)", res.Verdict, res.Err)
	}
}

// A verdict recorded on an older commit does not satisfy the depth check: the
// gate asks about the checked tip, so a record on another sha leaves the
// branch review-missing.
func TestGateReviewDepthAsksForTheCheckedHead(t *testing.T) {
	w := newWorld(t, ghURL)
	w.setConfig(`{"ship":{"review":{"default":"light"}},"test":{"full":["true"]}}`)
	os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"),
		[]byte(fmt.Sprintf(`{"slots":[{"name":"w1","branch":"w1","task":"#5","pr":%q}]}`, ghURL)), 0o644)
	w.forge("body", "Closes #5\n")
	w.record("w1", "oldsha", verdict.ReviewSpec)
	res, err := w.gate(false, GateOpts{CheckOnly: true, CheckReviews: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != GateReviewMissing {
		t.Errorf("a stale verdict must not pass: %+v", res)
	}
}

// record stores passing verdicts of kinds for branch at sha, or at the
// worker's tip when sha is empty, in the world's verdict store.
func (w *world) record(branch, sha string, kinds ...string) {
	w.t.Helper()
	if sha == "" {
		sha = gitq(w.t, w.worker, "rev-parse", "HEAD")
	}
	for _, k := range kinds {
		if _, err := verdict.AddBranch(w.dir, branch, verdict.Record{Kind: k, Verdict: verdict.Pass, Sha: sha}); err != nil {
			w.t.Fatal(err)
		}
	}
}

// secondPR sets up a split issue: the slot still records the branch of its
// first (partial-slice) PR, gone from origin, while PR 7 is headed by the
// branch of the second slice. head is that branch.
func secondPR(t *testing.T, head string) *world {
	t.Helper()
	w := newWorld(t, ghURL)
	gitq(t, w.worker, "push", "-q", "origin", "w1:refs/heads/"+head)
	w.forge("head", head)
	w.forge("body", "Closes #5\n\n## Approvals\n- use the shared cache: orchestrator relay round 2\n")
	relays := `[{"round":2,"ts":"2026-10-02T10:00:00Z","summary":"use the shared cache for the lookup"}]`
	os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"),
		[]byte(fmt.Sprintf(`{"slots":[{"name":"w1","branch":"dana/5-first-slice","task":"#5","pr":%q,"relays":%s}]}`, ghURL, relays)), 0o644)
	return w
}

// A slot that merged a partial-slice PR and reports a second PR from a new
// branch gates that PR with the slot's relays, by slot name and by PR number,
// and records the new branch on the slot.
func TestGateSecondPROfASplitIssueUsesTheSlotsRelays(t *testing.T) {
	for _, arg := range []string{"w1", "7"} {
		t.Run(arg, func(t *testing.T) {
			w := secondPR(t, "dana/5-second-slice")
			res, err := w.env(false).Gate(bg, w.dir, GateOpts{Slot: arg, Base: "main"})
			if err != nil || res.Verdict != GatePass {
				t.Fatalf("verdict = %q (%v): %+v", res.Verdict, err, res)
			}
			if res.Branch != "dana/5-second-slice" || !w.onMain("work.txt") {
				t.Errorf("branch = %q, landed = %v", res.Branch, w.onMain("work.txt"))
			}
		})
	}
}

func TestGateRecordsTheHeadBranchOfTheSecondPR(t *testing.T) {
	w := secondPR(t, "dana/5-second-slice")
	if res, err := w.gate(false, GateOpts{CheckOnly: true}); err != nil || res.Verdict != GateFresh {
		t.Fatalf("%+v %v", res, err)
	}
	if got := LoadRegistryTolerant(w.dir).Slot("w1").Branch(); got != "dana/5-first-slice" {
		t.Errorf("check-only recorded %q", got)
	}
	if res, err := w.gate(false, GateOpts{}); err != nil || res.Verdict != GatePass {
		t.Fatalf("%+v %v", res, err)
	}
	if got := LoadRegistryTolerant(w.dir).Slot("w1").Branch(); got != "dana/5-second-slice" {
		t.Errorf("slot branch = %q, want the second PR's head", got)
	}
}

// A recorded PR headed by a branch of another issue is still a mismatch: only
// the slot's own issue's branches are adopted.
func TestGateKeepsRefusingAPRFromAnotherIssuesBranch(t *testing.T) {
	w := secondPR(t, "dana/6-other-issue")
	os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"),
		[]byte(fmt.Sprintf(`{"slots":[{"name":"w1","branch":"w1","task":"#5","pr":%q}]}`, ghURL)), 0o644)
	res, err := w.gate(false, GateOpts{CheckOnly: true})
	if err != nil || res.Verdict != GatePRMismatch {
		t.Fatalf("verdict = %q (%v): %+v", res.Verdict, err, res)
	}
}

// A PR head that never catches up is refused with a hint that names the re-run:
// the same --request-id would replay the stored refusal, not look again.
func TestGatePRHeadLagHintNamesTheRerun(t *testing.T) {
	w := newWorld(t, ghURL)
	w.forge("staleHeadFor", "99")
	res, err := w.gate(false, GateOpts{CheckOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != GatePRMismatch {
		t.Fatalf("verdict = %s, want %s", res.Verdict, GatePRMismatch)
	}
	if want := "rota worker gate " + res.Slot; !strings.Contains(res.Hint, want) {
		t.Errorf("hint = %q, want it to name %q", res.Hint, want)
	}
	if !strings.Contains(res.Hint, "without --request-id") {
		t.Errorf("hint = %q, want it to say the re-run takes no --request-id", res.Hint)
	}
}
