package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// verdictRepo is a git repo on feat/x with a main base branch and .rota/.
func verdictRepo(t *testing.T) string {
	t.Helper()
	dir := gitRepo(t)
	gitIn(t, dir, "branch", "main")
	return dir
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", append([]string{"-c", "user.email=a@b", "-c", "user.name=n"}, args...)...)
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
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
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "more")
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
	gitIn(t, dir, "branch", "other")
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
	gitIn(t, dir, "init", "-q", "--bare", origin)
	gitIn(t, dir, "remote", "add", "origin", origin)
	gitIn(t, dir, "push", "-q", "origin", "feat/x")
	gitIn(t, dir, "fetch", "-q", "origin")
	return dir
}

func headSha(t *testing.T, dir, ref string) string {
	t.Helper()
	c := exec.Command("git", "rev-parse", ref)
	c.Dir = dir
	out, err := c.Output()
	if err != nil {
		t.Fatalf("rev-parse %s: %v", ref, err)
	}
	return strings.TrimSpace(string(out))
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

func TestVerdictAddRefusesLocalAheadOfOrigin(t *testing.T) {
	dir := pushedVerdictRepo(t)
	reviewed := headSha(t, dir, "origin/feat/x")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "unpushed")
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

func TestVerdictAddShaMustBeOriginHead(t *testing.T) {
	dir := pushedVerdictRepo(t)
	old := headSha(t, dir, "feat/x")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "worker push")
	gitIn(t, dir, "push", "-q", "origin", "feat/x")
	code, _, errs := rotaIn(t, dir, "verdict", "add", "--kind", "qa", "--verdict", "PASS", "--sha", old)
	if code != 4 || !strings.Contains(errs, "worker push") {
		t.Errorf("stale --sha: exit %d, stderr %q (want 4 naming the extra commit)", code, errs)
	}
	if code, _, _ := rotaIn(t, dir, "verdict", "add", "--kind", "qa", "--verdict", "PASS", "--sha", "deadbeef"); code != 3 {
		t.Errorf("unknown --sha: exit %d, want 3", code)
	}
	if code, _, _ := rotaIn(t, dir, "verdict", "add", "--kind", "qa", "--verdict", "PASS", "--sha", headSha(t, dir, "origin/feat/x")); code != 0 {
		t.Errorf("--sha at origin head: exit %d, want 0", code)
	}
}

// With a stale slot worktree, --sha still binds the verdict to the pushed head.
func TestVerdictAddShaBindsStaleLocalRef(t *testing.T) {
	dir := pushedVerdictRepo(t)
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "orchestrator push")
	gitIn(t, dir, "push", "-q", "origin", "feat/x")
	pushed := headSha(t, dir, "origin/feat/x")
	gitIn(t, dir, "reset", "-q", "--hard", "HEAD~1") // slot worktree one behind origin
	code, out, errs := rotaIn(t, dir, "verdict", "add", "--kind", "qa", "--verdict", "PASS", "--sha", pushed, "--json")
	if d := data(t, out); code != 0 || !strings.HasPrefix(pushed, d["sha"].(string)) {
		t.Fatalf("--sha on stale ref: %d %v %s", code, d, errs)
	}
}

// A tracking ref that lags the pushed head is a fetch problem, not a stale --sha.
func TestVerdictAddShaAheadOfTrackingHintsFetch(t *testing.T) {
	dir := pushedVerdictRepo(t)
	behind := headSha(t, dir, "origin/feat/x")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "worker push")
	pushed := headSha(t, dir, "feat/x")
	gitIn(t, dir, "update-ref", "refs/remotes/origin/feat/x", behind) // origin/feat/x not fetched
	code, _, errs := rotaIn(t, dir, "verdict", "add", "--kind", "qa", "--verdict", "PASS", "--sha", pushed)
	if code != 4 || !strings.Contains(errs, "behind") || !strings.Contains(errs, "git fetch") || strings.Contains(errs, "review the current head") {
		t.Errorf("--sha ahead of tracking ref: exit %d, stderr %q (want 4, behind, git fetch hint)", code, errs)
	}
}

// A local branch literally named origin/feat/x wins over the remote split:
// its verdicts live under their own key, not feat/x's.
func TestVerdictAddLocalBranchNamedLikeRemoteStaysWhole(t *testing.T) {
	dir := pushedVerdictRepo(t)
	gitIn(t, dir, "branch", "origin/feat/x", "feat/x")
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

// A remote other than origin is stripped and --sha is checked against that
// remote's head, not origin's.
func TestVerdictAddShaUsesNamedRemote(t *testing.T) {
	dir := pushedVerdictRepo(t)
	up := filepath.Join(t.TempDir(), "up.git")
	gitIn(t, dir, "init", "-q", "--bare", up)
	gitIn(t, dir, "remote", "add", "up", up)
	old := headSha(t, dir, "feat/x")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "up push")
	gitIn(t, dir, "push", "-q", "up", "feat/x")
	gitIn(t, dir, "fetch", "-q", "up")
	pushed := headSha(t, dir, "up/feat/x")
	args := []string{"verdict", "add", "up/feat/x", "--kind", "qa", "--verdict", "PASS", "--json"}
	// origin still holds the old head, so only the named remote accepts the new sha.
	code, out, errs := rotaIn(t, dir, append(args, "--sha", pushed)...)
	if d := data(t, out); code != 0 || !strings.HasPrefix(pushed, d["sha"].(string)) {
		t.Fatalf("--sha at up head: %d %v %s", code, d, errs)
	}
	if code, _, errs := rotaIn(t, dir, append(args, "--sha", old)...); code != 4 || !strings.Contains(errs, "up/feat/x") {
		t.Errorf("--sha behind up head: exit %d, stderr %q (want 4 naming up/feat/x)", code, errs)
	}
	_, out, _ = rotaIn(t, dir, "verdict", "show", "feat/x", "--json")
	if d := data(t, out); len(d["records"].([]any)) != 1 {
		t.Errorf("up/feat/x not keyed as feat/x: %v", d)
	}
}

// origin/<b> without --sha records the local tip and skips the local-vs-origin
// check, which only guards a bare branch argument.
func TestVerdictAddRemoteArgWithoutShaRecordsTip(t *testing.T) {
	dir := pushedVerdictRepo(t)
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "unpushed")
	tip := headSha(t, dir, "origin/feat/x")
	code, out, errs := rotaIn(t, dir, "verdict", "add", "origin/feat/x", "--kind", "qa", "--verdict", "PASS", "--json")
	if d := data(t, out); code != 0 || !strings.HasPrefix(tip, d["sha"].(string)) {
		t.Errorf("origin/feat/x without --sha: %d %v %s (want origin head %s)", code, d, errs, tip[:7])
	}
}

// With nothing pushed, --sha is checked against the local branch.
func TestVerdictAddShaOnUnpushedBranch(t *testing.T) {
	dir := verdictRepo(t)
	old := headSha(t, dir, "feat/x")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "second")
	args := []string{"verdict", "add", "--kind", "qa", "--verdict", "PASS", "--json"}
	if code, _, errs := rotaIn(t, dir, append(args, "--sha", old)...); code != 4 || !strings.Contains(errs, "feat/x") {
		t.Errorf("stale --sha, unpushed: exit %d, stderr %q (want 4)", code, errs)
	}
	head := headSha(t, dir, "feat/x")
	code, out, errs := rotaIn(t, dir, append(args, "--sha", head)...)
	if d := data(t, out); code != 0 || !strings.HasPrefix(head, d["sha"].(string)) {
		t.Errorf("--sha at local head: %d %v %s", code, d, errs)
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
