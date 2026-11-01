package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/verdict"
)

// verdictRepo is a git repo on feat/x with a main base branch and .rota/.
func verdictRepo(t *testing.T) string {
	t.Helper()
	dir := gitRepo(t)
	gittest.Run(t, dir, "branch", "main")
	return dir
}

func writeBody(t *testing.T, dir, text string) string {
	t.Helper()
	p := filepath.Join(dir, "body.json")
	if err := os.WriteFile(p, []byte(text), 0o666); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestVerdictAddValidates(t *testing.T) {
	dir := verdictRepo(t)
	bad := writeBody(t, dir, `{"findings": [{"severity": "huge", "title": "t"}]}`)
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"no kind", []string{"--verdict", "PASS"}, 2},
		{"unknown kind", []string{"--kind", "lint", "--verdict", "PASS"}, 2},
		{"debug kind", []string{"--kind", "debug-fix", "--verdict", "PASS"}, 2},
		{"no verdict", []string{"--kind", "qa"}, 2},
		{"wrong verdict for kind", []string{"--kind", "review-spec", "--verdict", "INFRA-FAIL"}, 2},
		{"lowercase", []string{"--kind", "qa", "--verdict", "pass"}, 2},
		{"bad body", []string{"--kind", "qa", "--verdict", "FAIL", "--body-file", bad}, 2},
		{"missing body file", []string{"--kind", "qa", "--verdict", "FAIL", "--body-file", filepath.Join(dir, "nope")}, 2},
		{"body verdict differs", []string{"--kind", "qa", "--verdict", "FAIL", "--body-file", writeBody(t, t.TempDir(), `{"verdict": "PASS"}`)}, 2},
		{"unknown branch", []string{"nope", "--kind", "qa", "--verdict", "FAIL"}, 3},
	}
	for _, c := range cases {
		code, _, _ := rotaIn(t, dir, append([]string{"verdict", "add"}, c.args...)...)
		if code != c.want {
			t.Errorf("%s: exit %d, want %d", c.name, code, c.want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".rota", "verdicts.json")); err == nil {
		t.Error("a rejected verdict wrote the store")
	}
}

func TestVerdictReviewFlow(t *testing.T) {
	dir := verdictRepo(t)
	body := writeBody(t, dir, `{"summary": "s", "findings": [{"severity": "major", "title": "naming", "file": "a.go", "line": 4}]}`)
	code, out, _ := rotaIn(t, dir, "verdict", "add", "--kind", "review-spec", "--verdict", "CONCERNS", "--json")
	if d := data(t, out); code != 0 || d["next"] != "quality" || d["branch"] != "feat/x" || d["changed"] != true {
		t.Fatalf("spec: %d %v", code, d)
	}
	code, out, _ = rotaIn(t, dir, "verdict", "add", "--kind", "review-quality", "--verdict", "PASS", "--body-file", body, "--json")
	d := data(t, out)
	if code != 0 || d["combined"] != "CONCERNS" || d["next"] != "report" || d["summary"] != "s" {
		t.Fatalf("quality: %d %v", code, d)
	}
	if f := d["findings"].([]any)[0].(map[string]any); f["line"] != float64(4) || f["file"] != "a.go" {
		t.Errorf("findings: %v", f)
	}
	code, out, _ = rotaIn(t, dir, "verdict", "route", "--for", "ship-review", "--json")
	if d := data(t, out); code != 0 || d["verdict"] != "CONCERNS" || d["next"] != "ask" || d["stale"] != false || d["kind"] != "review-quality" {
		t.Fatalf("route: %d %v", code, d)
	}
	// The retired loop level routes like off: ask, never address.
	if err := os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{"autonomy": {"level": "loop"}}`), 0o666); err != nil {
		t.Fatal(err)
	}
	if _, out, _ = rotaIn(t, dir, "verdict", "route", "--for", "ship-review", "--json"); data(t, out)["next"] != "ask" {
		t.Errorf("loop route: %v", data(t, out))
	}
	// A new commit makes the verdict stale without changing the route.
	gittest.Run(t, dir, "commit", "-q", "--allow-empty", "-m", "more")
	_, out, _ = rotaIn(t, dir, "verdict", "route", "--for", "ship-review", "--json")
	if d := data(t, out); d["stale"] != true || d["next"] != "ask" {
		t.Errorf("stale route: %v", d)
	}
	_, out, _ = rotaIn(t, dir, "verdict", "show", "--json")
	if d := data(t, out); len(d["records"].([]any)) != 2 || d["head"] == "" {
		t.Errorf("show: %v", d)
	}
}

// A light review records review-spec alone: that record is the branch's
// combined verdict, in `verdict add` and in `verdict show`.
func TestVerdictSpecOnlyIsCombined(t *testing.T) {
	dir := verdictRepo(t)
	_, out, _ := rotaIn(t, dir, "verdict", "add", "--kind", "review-spec", "--verdict", "CONCERNS", "--json")
	if d := data(t, out); d["combined"] != "CONCERNS" {
		t.Errorf("add: %v", d)
	}
	_, out, _ = rotaIn(t, dir, "verdict", "show", "--json")
	recs := data(t, out)["records"].([]any)
	if len(recs) != 1 || recs[0].(map[string]any)["combined"] != "CONCERNS" {
		t.Errorf("show: %v", recs)
	}
}

func TestVerdictAddBehaviourTranscript(t *testing.T) {
	dir := verdictRepo(t)
	code, out, _ := rotaIn(t, dir, "verdict", "add", "--kind", "behaviour", "--verdict", "FAIL", "--transcript", ".rota/qa-runs/behaviour/x.txt", "--json")
	if d := data(t, out); code != 0 || d["kind"] != "behaviour" || d["transcript"] != ".rota/qa-runs/behaviour/x.txt" {
		t.Fatalf("behaviour: %d %v", code, d)
	}
	_, out, _ = rotaIn(t, dir, "verdict", "show", "--json")
	if d := data(t, out); d["records"].([]any)[0].(map[string]any)["transcript"] != ".rota/qa-runs/behaviour/x.txt" {
		t.Errorf("show: %v", d)
	}
	if code, _, _ := rotaIn(t, dir, "verdict", "add", "--kind", "behaviour", "--verdict", "CONCERNS"); code != 2 {
		t.Errorf("behaviour CONCERNS: exit %d", code)
	}
	code, _, errs := rotaIn(t, dir, "verdict", "add", "--kind", "qa", "--verdict", "PASS", "--transcript", "t.txt")
	if code != 2 || !strings.Contains(errs, "--transcript") {
		t.Errorf("transcript on qa: exit %d %q", code, errs)
	}
}

func TestVerdictRouteMissingAndUsage(t *testing.T) {
	dir := verdictRepo(t)
	if code, _, _ := rotaIn(t, dir, "verdict", "route", "--for", "ship-qa"); code != 3 {
		t.Errorf("no verdict: exit %d, want 3", code)
	}
	if code, _, _ := rotaIn(t, dir, "verdict", "route"); code != 2 {
		t.Errorf("no --for: exit %d, want 2", code)
	}
	if code, _, _ := rotaIn(t, dir, "verdict", "route", "--for", "ship"); code != 2 {
		t.Errorf("unknown --for: exit %d, want 2", code)
	}
	code, out, _ := rotaIn(t, dir, "verdict", "show", "--json")
	if d := data(t, out); code != 0 || len(d["records"].([]any)) != 0 {
		t.Errorf("empty show: %d %v", code, d)
	}
	// A verdict on another branch is not this branch's.
	gittest.Run(t, dir, "branch", "other")
	rotaIn(t, dir, "verdict", "add", "other", "--kind", "qa", "--verdict", "FAIL")
	if code, _, _ := rotaIn(t, dir, "verdict", "route", "--for", "ship-qa"); code != 3 {
		t.Errorf("other branch's verdict was read: exit %d", code)
	}
	if _, out, _ := rotaIn(t, dir, "verdict", "route", "other", "--for", "ship-qa", "--json"); data(t, out)["next"] != "surface" {
		t.Errorf("advisory qa route: %v", data(t, out))
	}
}

func TestDebugVerdictIronLaw(t *testing.T) {
	dir := verdictRepo(t)
	if code, _, _ := rotaIn(t, dir, "debug", "verdict", "B07", "--verdict", "CONCERNS"); code != 2 {
		t.Errorf("CONCERNS: exit %d, want 2", code)
	}
	if code, _, _ := rotaIn(t, dir, "debug", "verdict", "B07"); code != 2 {
		t.Errorf("no verdict: exit %d, want 2", code)
	}
	if code, _, _ := rotaIn(t, dir, "debug", "verdict", " ", "--verdict", "FAIL"); code != 2 {
		t.Errorf("blank id: exit %d, want 2", code)
	}
	rotaIn(t, dir, "debug", "counter", "init", "B07")
	rotaIn(t, dir, "debug", "counter", "record-attempt", "--hypothesis", "h1", "--commit", "abc")
	want := []string{"hypothesize", "hypothesize", "halt"}
	for i, next := range want {
		code, out, _ := rotaIn(t, dir, "debug", "verdict", "B07", "--verdict", "FAIL", "--json")
		d := data(t, out)
		if code != 0 || d["next"] != next || d["failedFixes"] != float64(i+1) || d["bugId"] != "B07" || d["kind"] != "debug-fix" {
			t.Fatalf("fail %d: %d %v", i+1, code, d)
		}
		// Only the first call had a pending counter attempt to close.
		if _, has := d["attempt"]; has != (i == 0) {
			t.Errorf("fail %d: attempt present=%v", i+1, has)
		}
	}
	_, out, _ := rotaIn(t, dir, "debug", "counter", "show", "--json")
	if d := data(t, out); d["failedFixes"] != float64(1) {
		t.Errorf("counter not closed: %v", d)
	}
	// At 3 failed fixes the Iron Law refuses a 4th attempt until a reset (B3).
	if code, _, _ := rotaIn(t, dir, "debug", "counter", "record-attempt", "--hypothesis", "h2", "--commit", "def"); code != 4 {
		t.Fatalf("4th attempt: exit %d, want 4", code)
	}
	rotaIn(t, dir, "debug", "reset", "B07", "--reason", "new angle", "--confirm", "--confirm-note", "yes")
	rotaIn(t, dir, "debug", "counter", "record-attempt", "--hypothesis", "h2", "--commit", "def")
	code, out, _ := rotaIn(t, dir, "debug", "verdict", "B07", "--verdict", "PASS", "--json")
	if d := data(t, out); code != 0 || d["next"] != "complete" || d["attempt"] != float64(2) || d["failedFixes"] != float64(0) {
		t.Fatalf("pass: %d %v", code, d)
	}
}

// pushedVerdictRepo is verdictRepo with an origin that already holds feat/x.
func pushedVerdictRepo(t *testing.T) string {
	t.Helper()
	dir := verdictRepo(t)
	origin := filepath.Join(t.TempDir(), "origin.git")
	gittest.Run(t, dir, "init", "-q", "--bare", origin)
	gittest.Run(t, dir, "remote", "add", "origin", origin)
	gittest.Run(t, dir, "push", "-q", "origin", "feat/x")
	gittest.Run(t, dir, "fetch", "-q", "origin")
	return dir
}

func headSha(t *testing.T, dir, ref string) string {
	t.Helper()
	return gittest.Run(t, dir, "rev-parse", ref)
}

func TestVerdictAddStripsRemotePrefix(t *testing.T) {
	dir := pushedVerdictRepo(t)
	if code, out, errs := rotaIn(t, dir, "verdict", "add", "origin/feat/x", "--kind", "qa", "--verdict", "PASS", "--json"); code != 0 {
		t.Fatalf("add origin/feat/x: %d %s %s", code, out, errs)
	}
	_, out, _ := rotaIn(t, dir, "verdict", "show", "feat/x", "--json")
	if d := data(t, out); d["branch"] != "feat/x" || len(d["records"].([]any)) != 1 {
		t.Errorf("bare key holds no record: %v", d)
	}
	_, out, _ = rotaIn(t, dir, "verdict", "show", "origin/feat/x", "--json")
	if d := data(t, out); len(d["records"].([]any)) != 1 {
		t.Errorf("origin/feat/x reads another key: %v", d)
	}
}

// A binding refusal must leave the store untouched; an explicit reviewed SHA
// must reach the persisted record through the CLI adapter.
func TestVerdictAddHeadBindingIntegration(t *testing.T) {
	dir := pushedVerdictRepo(t)
	reviewed := headSha(t, dir, "origin/feat/x")
	gittest.Run(t, dir, "commit", "-q", "--allow-empty", "-m", "unpushed")
	args := []string{"verdict", "add", "--kind", "qa", "--verdict", "PASS", "--json"}
	if code, _, _ := rotaIn(t, dir, args...); code != 4 {
		t.Errorf("local ahead of origin, no --sha: exit %d, want 4", code)
	}
	if _, err := os.Stat(filepath.Join(dir, ".rota", "verdicts.json")); err == nil {
		t.Error("a refused verdict wrote the store")
	}
	code, out, _ := rotaIn(t, dir, append(args, "--sha", reviewed)...)
	if d := data(t, out); code != 0 || !strings.HasPrefix(reviewed, d["sha"].(string)) {
		t.Errorf("--sha at origin head: %d %v", code, d)
	}
}

func TestVerdictHeadErrorTranslation(t *testing.T) {
	cases := []struct {
		name          string
		err           error
		code          int
		message, hint string
	}{
		{"refused", &verdict.HeadError{Message: "stale", Hint: "fetch"}, 4, "stale", "fetch"},
		{"missing commit", &verdict.HeadError{Message: "unknown", NotFound: true}, 3, "unknown", ""},
		{"git stderr", &git.ExitError{Args: "rev-parse feat/x", ExitCode: 128, Stderr: " unknown revision\n"}, 5, "git rev-parse failed: unknown revision", ""},
		{"git exit only", &git.ExitError{Args: "rev-parse feat/x", ExitCode: 128}, 5, "git rev-parse failed: exit 128", ""},
		{"git unavailable", git.ErrNoGit, 5, "git is not installed", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got *Error
			if err := verdictHeadError(tc.err); !errors.As(err, &got) || got.Exit != tc.code || got.Message != tc.message || got.Hint != tc.hint {
				t.Fatalf("got %#v; want exit %d, message %q, hint %q", err, tc.code, tc.message, tc.hint)
			}
		})
	}
	if err := verdictHeadError(nil); err != nil {
		t.Fatalf("success became %v", err)
	}
}

// A local branch literally named origin/feat/x wins over the remote split:
// its verdicts live under their own key, not feat/x's.
func TestVerdictAddLocalBranchNamedLikeRemoteStaysWhole(t *testing.T) {
	dir := pushedVerdictRepo(t)
	gittest.Run(t, dir, "branch", "origin/feat/x", "feat/x")
	if code, out, errs := rotaIn(t, dir, "verdict", "add", "origin/feat/x", "--kind", "qa", "--verdict", "PASS", "--json"); code != 0 {
		t.Fatalf("add origin/feat/x: %d %s %s", code, out, errs)
	}
	_, out, _ := rotaIn(t, dir, "verdict", "show", "origin/feat/x", "--json")
	if d := data(t, out); d["branch"] != "origin/feat/x" || len(d["records"].([]any)) != 1 {
		t.Errorf("local branch split at the remote: %v", d)
	}
	_, out, _ = rotaIn(t, dir, "verdict", "show", "feat/x", "--json")
	if d := data(t, out); len(d["records"].([]any)) != 0 {
		t.Errorf("origin/feat/x leaked into feat/x: %v", d)
	}
}

// AC-1: a verdict recorded while the branch's depth needs more says which
// verdict is still missing at head, so the orchestrator records it before gating.
func TestVerdictAddNamesMissingVerdictOfRaisedDepth(t *testing.T) {
	dir := verdictRepo(t)
	if err := os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{"ship":{"review":{"default":"full"}}}`), 0o666); err != nil {
		t.Fatal(err)
	}
	code, out, _ := rotaIn(t, dir, "verdict", "add", "--kind", "review-quality", "--verdict", "PASS", "--json")
	d := data(t, out)
	note, _ := d["depthNote"].(string)
	if code != 0 || !strings.Contains(note, "light -> full") || !strings.Contains(note, "review-spec") {
		t.Fatalf("depthNote %q (exit %d), want light -> full naming review-spec", note, code)
	}
	code, out, _ = rotaIn(t, dir, "verdict", "add", "--kind", "review-spec", "--verdict", "PASS", "--json")
	if d := data(t, out); code != 0 || d["depthNote"] != nil {
		t.Errorf("complete set still noted: %v", d["depthNote"])
	}
}

// A review-cross verdict (#1612) records the reviewer's harness and model, and
// verdict show prints them; review-cross refuses a record without both.
func TestVerdictAddCrossHarness(t *testing.T) {
	dir := verdictRepo(t)
	for _, args := range [][]string{
		{"--kind", "review-cross", "--verdict", "PASS"},
		{"--kind", "review-cross", "--verdict", "PASS", "--harness", "codex"},
		{"--kind", "review-cross", "--verdict", "PASS", "--model", "gpt-5"},
		{"--kind", "review-cross", "--verdict", "PASS", "--harness", "gemini", "--model", "g"},
	} {
		if code, _, _ := rotaIn(t, dir, append([]string{"verdict", "add"}, args...)...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	code, out, _ := rotaIn(t, dir, "verdict", "add", "--kind", "review-cross", "--verdict", "CONCERNS", "--harness", "codex", "--model", "gpt-5.5", "--json")
	if d := data(t, out); code != 0 || d["kind"] != "review-cross" || d["harness"] != harness.Codex || d["model"] != "gpt-5.5" || d["next"] != "report" {
		t.Fatalf("add: %d %v", code, d)
	}
	code, out, _ = rotaIn(t, dir, "verdict", "add", "--kind", "review-spec", "--verdict", "PASS", "--harness", "claude", "--model", "opus")
	if code != 0 {
		t.Fatalf("spec with harness: %d %s", code, out)
	}
	code, out, _ = rotaIn(t, dir, "verdict", "show")
	if code != 0 || !strings.Contains(out, "review-cross CONCERNS @ ") || !strings.Contains(out, "(codex gpt-5.5)") || !strings.Contains(out, "(claude opus)") {
		t.Fatalf("show: %d %q", code, out)
	}
	code, out, _ = rotaIn(t, dir, "verdict", "show", "--json")
	recs := data(t, out)["records"].([]any)
	var cross map[string]any
	for _, r := range recs {
		if m := r.(map[string]any); m["kind"] == "review-cross" {
			cross = m
		}
	}
	if code != 0 || cross == nil || cross["harness"] != harness.Codex || cross["model"] != "gpt-5.5" {
		t.Fatalf("show --json: %d %v", code, recs)
	}
}

// A review-cross FAIL is relayed like a review FAIL (#1612): its findings
// reach the bounce reason and the relay text names the reviewer.
func TestRelayCarriesACrossFail(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".rota"), 0o777)
	pass := verdict.Record{Kind: verdict.ReviewQuality, Verdict: verdict.Pass, Sha: "abc1234", RecordedAt: "2026-10-22T10:00:00Z", Findings: []verdict.Finding{}}
	cross := verdict.Record{Kind: verdict.ReviewCross, Verdict: verdict.Fail, Sha: "abc1234", RecordedAt: "2026-10-22T10:05:00Z", Harness: "codex", Model: "gpt-5.5",
		Findings: []verdict.Finding{{Severity: "major", Title: "exit code changed", File: "a.go", Line: 3}}}
	for _, r := range []verdict.Record{pass, cross} {
		if _, err := verdict.AddBranch(root, "nia/6-x", r); err != nil {
			t.Fatal(err)
		}
	}
	if got := openFindings(root, "nia/6-x"); len(got) != 1 || got[0] != "[major] exit code changed (a.go:3)" {
		t.Errorf("open findings: %v", got)
	}
	v := failVerdict(root, "nia/6-x")
	if v == nil || !strings.Contains(v.Text, "review-cross FAIL at abc1234 (codex gpt-5.5)") || !strings.Contains(v.Text, "exit code changed") {
		t.Fatalf("relay: %+v", v)
	}
}

// A lens verdict (#2064) is recorded by kind with PASS/CONCERNS/FAIL and listed
// by verdict show; a lens FAIL is relayed like a review FAIL.
func TestVerdictAddLens(t *testing.T) {
	dir := verdictRepo(t)
	for _, kind := range []string{"review-security", "review-perf", "review-ux"} {
		code, out, _ := rotaIn(t, dir, "verdict", "add", "--kind", kind, "--verdict", "CONCERNS", "--json")
		if d := data(t, out); code != 0 || d["kind"] != kind || d["next"] != "report" {
			t.Fatalf("%s add: %d %v", kind, code, d)
		}
	}
	if code, _, _ := rotaIn(t, dir, "verdict", "add", "--kind", "review-ux", "--verdict", "INFRA-FAIL"); code != 2 {
		t.Errorf("a lens refuses INFRA-FAIL: exit %d, want 2", code)
	}
	code, out, _ := rotaIn(t, dir, "verdict", "show")
	if code != 0 {
		t.Fatalf("show: %d %q", code, out)
	}
	for _, kind := range []string{"review-security", "review-perf", "review-ux"} {
		if !strings.Contains(out, kind+" CONCERNS @ ") {
			t.Errorf("show lacks %s: %q", kind, out)
		}
	}
}

func TestRelayCarriesALensFail(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".rota"), 0o777)
	sec := verdict.Record{Kind: verdict.ReviewSecurity, Verdict: verdict.Fail, Sha: "abc1234", RecordedAt: "2026-10-22T10:05:00Z",
		Findings: []verdict.Finding{{Severity: "major", Title: "argument injection", File: "run.go", Line: 9}}}
	ux := verdict.Record{Kind: verdict.ReviewUX, Verdict: verdict.Pass, Sha: "abc1234", RecordedAt: "2026-10-22T10:06:00Z", Findings: []verdict.Finding{}}
	for _, r := range []verdict.Record{sec, ux} {
		if _, err := verdict.AddBranch(root, "nia/6-x", r); err != nil {
			t.Fatal(err)
		}
	}
	if got := openFindings(root, "nia/6-x"); len(got) != 1 || got[0] != "[major] argument injection (run.go:9)" {
		t.Errorf("open findings: %v", got)
	}
	if v := failVerdict(root, "nia/6-x"); v == nil || !strings.Contains(v.Text, "review-security FAIL at abc1234") {
		t.Fatalf("relay: %+v", v)
	}
}

func TestVerdictAddRefusesCorruptStore(t *testing.T) {
	dir := verdictRepo(t)
	store := filepath.Join(dir, ".rota", "verdicts.json")
	if err := os.WriteFile(store, []byte("not json"), 0o666); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := rotaIn(t, dir, "verdict", "add", "--kind", "qa", "--verdict", "PASS")
	if code != 4 {
		t.Errorf("exit %d, want 4: %s %s", code, out, errOut)
	}
	all := out + errOut
	if !strings.Contains(all, "verdicts.json") || !strings.Contains(all, "move it aside") || strings.Contains(all, "bug in rota") {
		t.Errorf("output lacks path or repair hint: %s", all)
	}
	if raw, _ := os.ReadFile(store); string(raw) != "not json" {
		t.Errorf("store rewritten: %q", raw)
	}
}
