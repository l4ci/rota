package worker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker"
)

const ciCfg = `{"test":{"full":["false"],"fullWhere":"ci","ciChecks":["ci/test"]}}` // "false" proves the local tier did not run

// ciEnv is w.env with the CI waits ended on the first poll.
func (w *world) ciEnv() Env {
	return Env{
		Sleep: func(time.Duration) {},
		Getenv: func(k string) string {
			return map[string]string{"ROTA_GATE_SHA_WAIT": "0", "ROTA_CI_POLL": "0", "ROTA_CI_START_WAIT": "0", "ROTA_CI_TIMEOUT": "0"}[k]
		},
	}.withForge(func(provider, dir string) Forge { return &fakeForge{w: w, provider: provider} }, false)
}

func (w *world) ciGate(o GateOpts) (GateResult, error) {
	o.Slot, o.Base = "w1", "main"
	return w.ciEnv().Gate(bg, w.dir, o)
}

func (w *world) ciBranches() string {
	return gitq(w.t, w.origin, "for-each-ref", "refs/heads/rota/ci/")
}

// onOriginMain reports whether file is in origin/main's tree.
func (w *world) onOriginMain(file string) bool {
	return exec.Command("git", "-C", w.origin, "cat-file", "-e", "main:"+file).Run() == nil
}

func TestGateCIPR(t *testing.T) {
	cases := []struct {
		name, ci, fail string
		verdict        string
		merged         bool
		errHas         []string
		hintHas        string
	}{
		{name: "green", verdict: GatePass, merged: true},
		{name: "red", fail: "work.txt", verdict: GateVerifyFailed, errHas: []string{"nothing landed", "ci/test"}},
		{name: "none", ci: "none", verdict: GateCINotRun, hintHas: "rota/ci/"},
		{name: "pending", ci: "pending", verdict: GateVerifyTimeout},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			w.setConfig(ciCfg)
			w.forge("ci", c.ci)
			w.forge("ciFail", c.fail)
			res, err := w.ciGate(GateOpts{})
			if err != nil || res.Verdict != c.verdict {
				t.Fatalf("%+v %v", res, err)
			}
			if got := w.forgeWord("state"); (got == "MERGED") != c.merged {
				t.Errorf("PR state %s, merged want %v", got, c.merged)
			}
			if w.onOriginMain("work.txt") != c.merged {
				t.Errorf("work.txt on origin/main = %v, want %v", !c.merged, c.merged)
			}
			for _, s := range c.errHas {
				if !strings.Contains(res.Err, s) {
					t.Errorf("Err %q lacks %q", res.Err, s)
				}
			}
			if !strings.Contains(res.Hint, c.hintHas) {
				t.Errorf("Hint %q lacks %q", res.Hint, c.hintHas)
			}
			if res.Changed != c.merged {
				t.Errorf("Changed = %v", res.Changed)
			}
			if c.merged {
				if len(res.Verified) != 1 || res.Verified[0] != "ci/test" {
					t.Errorf("Verified = %v", res.Verified)
				}
				if !strings.Contains(w.logText(), "CommitChecks") {
					t.Errorf("no CommitChecks in forge log:\n%s", w.logText())
				}
			}
			if b := w.ciBranches(); b != "" {
				t.Errorf("rota/ci branch left behind:\n%s", b)
			}
		})
	}
}

func TestGateCINoPR(t *testing.T) {
	w := newWorld(t, "")
	gitq(t, w.dir, "fetch", "-q", "origin", "w1:w1")
	w.setConfig(ciCfg)
	res, err := w.ciGate(GateOpts{})
	if err != nil || res.Verdict != GatePass || !w.onMain("work.txt") {
		t.Fatalf("%+v %v", res, err)
	}
	if len(res.Verified) != 1 || res.Verified[0] != "ci/test" {
		t.Errorf("Verified = %v", res.Verified)
	}
}

func TestGateCINoVerifySkipsCI(t *testing.T) {
	w := newWorld(t, ghURL)
	w.setConfig(ciCfg)
	res, err := w.ciGate(GateOpts{NoVerify: true})
	if err != nil || res.Verdict != GatePass {
		t.Fatalf("%+v %v", res, err)
	}
	if strings.Contains(w.logText(), "CommitChecks") {
		t.Errorf("CI was consulted under --no-verify:\n%s", w.logText())
	}
}

func TestGateCIBadFullWhere(t *testing.T) {
	w := newWorld(t, "")
	gitq(t, w.dir, "fetch", "-q", "origin", "w1:w1")
	w.setConfig(`{"test":{"fullWhere":"bogus"}}`)
	_, err := w.ciGate(GateOpts{})
	we, _ := err.(*exitcode.Error)
	if we == nil || we.Exit != exitcode.ExitInternal {
		t.Fatalf("want exit %d, got %v", exitcode.ExitInternal, err)
	}
}

func TestGateCINoOrigin(t *testing.T) {
	w := newWorld(t, "")
	gitq(t, w.dir, "fetch", "-q", "origin", "w1:w1")
	gitq(t, w.dir, "remote", "remove", "origin")
	w.setConfig(ciCfg)
	res, err := w.ciGate(GateOpts{})
	if err != nil || res.Verdict != GateCheckBroke || !strings.Contains(res.Err, "origin") {
		t.Fatalf("%+v %v", res, err)
	}
	if w.onMain("work.txt") {
		t.Error("work landed")
	}
}

func ciTrain(t *testing.T, ci, fail string, branches ...string) (*world, TrainResult) {
	t.Helper()
	w := trainWorld(t, "false", branches...)
	w.setConfig(ciCfg)
	w.forge("ci", ci)
	w.forge("ciFail", fail)
	res, err := w.ciEnv().Train(bg, w.dir, TrainOpts{Base: "main", Targets: branches})
	if err != nil {
		t.Fatal(err)
	}
	return w, res
}

func TestTrainCIRed(t *testing.T) {
	w, res := ciTrain(t, "", "b3.txt", "b1", "b2", "b3", "b4")
	if res.Verdict != GateVerifyFailed || res.Culprit != "b3" || !strings.Contains(res.Err, "ci/test") || len(res.Landed) != 0 {
		t.Fatalf("%+v", res)
	}
	for _, f := range []string{"b1.txt", "b2.txt", "b3.txt", "b4.txt"} {
		if w.onMain(f) {
			t.Errorf("%s landed", f)
		}
	}
}

func TestTrainCIGreen(t *testing.T) {
	w, res := ciTrain(t, "", "", "b1", "b2")
	if res.Verdict != GatePass || strings.Join(res.Landed, ",") != "b1,b2" || !w.onMain("b1.txt") || !w.onMain("b2.txt") {
		t.Fatalf("%+v", res)
	}
	// One CI run: green, then the same checks green again to settle.
	if n := strings.Count(w.logText(), "CommitChecks"); n != 2 {
		t.Errorf("CommitChecks calls = %d, want 2:\n%s", n, w.logText())
	}
}

// ciScriptGate runs the gate against scripted checks (see fakeForge.CommitChecks)
// with the start window and timeout left open, so only the script ends it.
func ciScriptGate(t *testing.T, cfg, script string) (*world, GateResult, error) {
	t.Helper()
	w := newWorld(t, ghURL)
	w.setConfig(cfg)
	w.forge("ciScript", script)
	e := w.ciEnv()
	e.Getenv = func(k string) string {
		return map[string]string{"ROTA_GATE_SHA_WAIT": "0", "ROTA_CI_POLL": "0", "ROTA_CI_START_WAIT": "5", "ROTA_CI_TIMEOUT": "5"}[k]
	}
	res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
	return w, res, err
}

// A fast unrelated check passing is not green: the gate waits until every
// listed check has appeared and passed, then settles for one more poll.
func TestGateCIWaitsForListedChecks(t *testing.T) {
	cfg := `{"test":{"full":["false"],"fullWhere":"ci","ciChecks":["ci/test","ci/e2e"]}}`
	w, res, err := ciScriptGate(t, cfg, "lint=success|lint=success|lint=success,ci/test=success,ci/e2e=pending|lint=success,ci/test=success,ci/e2e=pending|lint=success,ci/test=success,ci/e2e=success")
	if err != nil || res.Verdict != GatePass || !w.onOriginMain("work.txt") {
		t.Fatalf("%+v %v", res, err)
	}
	if n := strings.Count(w.logText(), "CommitChecks"); n != 6 {
		t.Errorf("CommitChecks calls = %d, want 6 (green on the 5th, settled on the 6th):\n%s", n, w.logText())
	}
	if strings.Join(res.Verified, ",") != "ci/test,ci/e2e" {
		t.Errorf("Verified = %v", res.Verified)
	}
}

func TestGateCIListedChecks(t *testing.T) {
	cases := []struct {
		name, script, verdict string
		errHas                []string
	}{
		{name: "listed fails", script: "lint=success,ci/test=failure", verdict: GateVerifyFailed, errHas: []string{"failure ci/test"}},
		{name: "unlisted fails", script: "lint=failure,ci/test=success", verdict: GateVerifyFailed, errHas: []string{"failure lint"}},
		{name: "listed skipped", script: "ci/test=skipped", verdict: GateVerifyFailed, errHas: []string{"ci/test skipped"}},
		{name: "unlisted pending", script: "slow=pending,ci/test=success", verdict: GatePass},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, res, err := ciScriptGate(t, ciCfg, c.script)
			if err != nil || res.Verdict != c.verdict {
				t.Fatalf("%+v %v", res, err)
			}
			if w.onOriginMain("work.txt") != (c.verdict == GatePass) {
				t.Errorf("work.txt on origin/main = %v", !(c.verdict == GatePass))
			}
			for _, s := range c.errHas {
				if !strings.Contains(res.Err, s) {
					t.Errorf("Err %q lacks %q", res.Err, s)
				}
			}
		})
	}
}

// A listed check that never shows up is ci-not-run naming it, once the start
// window is over and nothing on the commit is still running.
func TestGateCIMissingListedCheck(t *testing.T) {
	w := newWorld(t, ghURL)
	w.setConfig(`{"test":{"full":["false"],"fullWhere":"ci","ciChecks":["ci/test","ci/typo"]}}`)
	w.forge("ciScript", "lint=success,ci/test=success")
	res, err := w.ciGate(GateOpts{})
	if err != nil || res.Verdict != GateCINotRun || !strings.Contains(res.Err, "ci/typo") || strings.Contains(res.Err, "ci/test,") {
		t.Fatalf("%+v %v", res, err)
	}
	if !strings.Contains(res.Hint, "test.ciChecks") || w.onOriginMain("work.txt") {
		t.Fatalf("hint %q, landed %v", res.Hint, w.onOriginMain("work.txt"))
	}
}

// While CI still runs, a missing listed check may yet appear (a job behind
// needs:), so the start window alone does not end the wait.
func TestGateCIMissingWhileRunning(t *testing.T) {
	w := newWorld(t, ghURL)
	w.setConfig(ciCfg)
	w.forge("ciScript", "suite GitHub Actions=pending,lint=success")
	res, err := w.ciGate(GateOpts{})
	if err != nil || res.Verdict != GateVerifyTimeout || !strings.Contains(res.Err, "ci/test") {
		t.Fatalf("%+v %v", res, err)
	}
}

// test.fullWhere ci with no test.ciChecks is refused before anything runs.
func TestCIChecksRequired(t *testing.T) {
	cfg := `{"test":{"full":["false"],"fullWhere":"ci"}}`
	check := func(t *testing.T, w *world, err error) {
		t.Helper()
		we, _ := err.(*exitcode.Error)
		if we == nil || we.Exit != exitcode.ExitInternal || !strings.Contains(we.Message+we.Hint, "test.ciChecks") {
			t.Fatalf("want exit %d naming test.ciChecks, got %#v", exitcode.ExitInternal, err)
		}
		if strings.Contains(w.logText(), "CommitChecks") || w.ciBranches() != "" {
			t.Errorf("CI was consulted:\n%s", w.logText())
		}
	}
	t.Run("gate", func(t *testing.T) {
		w := newWorld(t, ghURL)
		w.setConfig(cfg)
		_, err := w.ciGate(GateOpts{})
		check(t, w, err)
		if w.onOriginMain("work.txt") {
			t.Error("work landed")
		}
	})
	t.Run("train", func(t *testing.T) {
		w := trainWorld(t, "false", "b1")
		w.setConfig(cfg)
		_, err := w.ciEnv().Train(bg, w.dir, TrainOpts{Base: "main", Targets: []string{"b1"}})
		check(t, w, err)
		if w.onMain("b1.txt") {
			t.Error("b1 landed")
		}
	})
}

func TestTrainCINotRun(t *testing.T) {
	w, res := ciTrain(t, "none", "", "b1", "b2")
	if res.Verdict != GateCINotRun || len(res.Landed) != 0 || len(res.Members) != 2 || w.onMain("b1.txt") || w.onMain("b2.txt") {
		t.Fatalf("%+v", res)
	}
}

func TestGateConfigWhere(t *testing.T) {
	for in, want := range map[string]string{`{}`: "local", `{"test":{"fullWhere":"ci","ciChecks":["t"]}}`: "ci", `{"test":{"fullWhere":"local"}}`: "local"} {
		if got, err := ParseGateConfig(parseCfg(in)).Where(); err != nil || got != want {
			t.Errorf("%s: %q %v", in, got, err)
		}
	}
	if _, err := ParseGateConfig(parseCfg(`{"test":{"fullWhere":"x"}}`)).Where(); err == nil {
		t.Error("x must be an error")
	}
	if _, err := ParseGateConfig(parseCfg(`{"test":{"fullWhere":"ci","ciChecks":[" "]}}`)).Where(); err == nil {
		t.Error("ci with no check names must be an error")
	}
}

func TestCISettings(t *testing.T) {
	none := func(string) string { return "" }
	for _, c := range []struct {
		cfg  string
		env  func(string) string
		want time.Duration
	}{
		{`{}`, none, 60 * time.Minute},
		{`{"test":{"ciTimeoutMinutes":5}}`, none, 5 * time.Minute},
		{`{}`, func(k string) string {
			if k == "ROTA_CI_TIMEOUT" {
				return "7"
			}
			return ""
		}, 7 * time.Second},
	} {
		s, err := ParseGateConfig(parseCfg(c.cfg)).CISettings(c.env)
		if err != nil || s.timeout != c.want {
			t.Errorf("%s: %v %v", c.cfg, s, err)
		}
	}
}

func parseCfg(s string) any {
	v, err := jsonx.Decode([]byte(s))
	if err != nil {
		panic(err)
	}
	return v
}

// Green is not final until it holds for a second poll: a check that appears
// after the first (a later workflow, a job created when it starts) counts.
func TestGateCILateCheck(t *testing.T) {
	w := newWorld(t, ghURL)
	w.setConfig(ciCfg)
	w.forge("ci", "late")
	res, err := w.ciGate(GateOpts{})
	if err != nil || res.Verdict != GateVerifyFailed || w.forgeWord("state") != "OPEN" || !strings.Contains(res.Err, "ci/late") {
		t.Fatalf("%+v %v", res, err)
	}
}

// Checks that all skipped tested nothing, so they pass nothing.
func TestGateCIAllSkipped(t *testing.T) {
	w := newWorld(t, ghURL)
	w.setConfig(ciCfg)
	w.forge("ci", "skipped")
	res, err := w.ciGate(GateOpts{})
	if err != nil || res.Verdict != GateVerifyFailed || w.forgeWord("state") != "OPEN" || !strings.Contains(res.Err, "nothing landed") {
		t.Fatalf("%+v %v", res, err)
	}
}

// A merge that edits the CI definition would choose its own verification.
func TestGateCIConfigChanged(t *testing.T) {
	w := newWorld(t, ghURL)
	w.setConfig(ciCfg)
	os.MkdirAll(filepath.Join(w.worker, ".github", "workflows"), 0o755)
	os.WriteFile(filepath.Join(w.worker, ".github", "workflows", "ci.yml"), []byte("on: push\n"), 0o644)
	gitq(t, w.worker, "add", ".github")
	gitq(t, w.worker, "commit", "-q", "-m", "ci")
	gitq(t, w.worker, "push", "-q", "origin", "w1")
	w.forge("sha", gitq(t, w.worker, "rev-parse", "HEAD"))
	res, err := w.ciGate(GateOpts{})
	if err != nil || res.Verdict != GateCIConfigChanged || w.forgeWord("state") != "OPEN" || !strings.Contains(res.Err, ".github/workflows/ci.yml") {
		t.Fatalf("%+v %v", res, err)
	}
	if strings.Contains(w.logText(), "CommitChecks") {
		t.Error("CI must not run on a merge that changes it")
	}
}

func TestTrainCIConfigChanged(t *testing.T) {
	w := trainWorld(t, "true", "b1", "b2")
	gitq(t, w.dir, "checkout", "-q", "b2")
	trainWrite(t, w, ".gitlab-ci.yml", "test: {script: [true]}")
	gitq(t, w.dir, "checkout", "-q", "main")
	w.setConfig(ciCfg)
	res, err := w.ciEnv().Train(bg, w.dir, TrainOpts{Targets: []string{"b1", "b2"}, Base: "main"})
	if err != nil || res.Verdict != GateCIConfigChanged || len(res.Landed) != 0 || !strings.Contains(res.Err, ".gitlab-ci.yml") {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestCIConfigChanges(t *testing.T) {
	got := ciConfigChanges([]string{"a.go", ".github/workflows/x.yml", ".github/CODEOWNERS", ".gitlab-ci.yml", ".gitlab/ci/t.yml", "sub/.gitlab-ci.yml"})
	if strings.Join(got, ",") != ".github/workflows/x.yml,.gitlab-ci.yml,.gitlab/ci/t.yml" {
		t.Fatalf("%v", got)
	}
}

// A quoted path or a rename must not hide a CI file from the check.
func TestGateCIConfigRenamedOrQuoted(t *testing.T) {
	for _, c := range []struct{ name, setup, want string }{
		{"rename", "rename", ".github/workflows/ci.yml"},
		{"quoted", "quoted", ".github/workflows/\u00e9.yml"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			wf := filepath.Join(w.dir, ".github", "workflows")
			os.MkdirAll(wf, 0o755)
			os.WriteFile(filepath.Join(wf, "ci.yml"), []byte("on: push\n"), 0o644)
			gitq(t, w.dir, "add", ".github")
			gitq(t, w.dir, "commit", "-q", "-m", "ci")
			gitq(t, w.dir, "push", "-q", "origin", "main")
			gitq(t, w.worker, "pull", "-q", "--no-rebase", "origin", "main")
			if c.setup == "rename" {
				gitq(t, w.worker, "mv", ".github/workflows/ci.yml", "disabled.yml")
			} else {
				os.WriteFile(filepath.Join(w.worker, ".github", "workflows", "\u00e9.yml"), []byte("on: push\n"), 0o644)
				gitq(t, w.worker, "add", ".github")
			}
			gitq(t, w.worker, "commit", "-q", "-m", c.name)
			gitq(t, w.worker, "push", "-q", "origin", "w1")
			w.forge("sha", gitq(t, w.worker, "rev-parse", "HEAD"))
			w.setConfig(ciCfg)
			res, err := w.ciGate(GateOpts{})
			if err != nil || res.Verdict != GateCIConfigChanged || !strings.Contains(res.Err, c.want) {
				t.Fatalf("%+v %v", res, err)
			}
		})
	}
}

// originCommit pushes a commit adding file to the origin's main, as another
// merge would.
func (w *world) originCommit(file string) {
	w.t.Helper()
	d := w.t.TempDir()
	gitq(w.t, d, "clone", "-q", w.origin, ".")
	os.WriteFile(filepath.Join(d, file), []byte(file+"\n"), 0o644)
	gitq(w.t, d, "add", file)
	gitq(w.t, d, "commit", "-q", "-m", "add "+file)
	gitq(w.t, d, "push", "-q", "origin", "HEAD:main")
}

// scratchTrees lists the worktrees of the gate checkout other than itself.
func (w *world) scratchTrees() []string {
	var extra []string
	for _, l := range strings.Split(gitq(w.t, w.dir, "worktree", "list", "--porcelain"), "\n") {
		if p, ok := strings.CutPrefix(l, "worktree "); ok && p != w.dir {
			extra = append(extra, p)
		}
	}
	return extra
}

// CI verified the merge on a base that moved before the merge: nothing lands.
func TestGateCIBaseMovedAfterCI(t *testing.T) {
	w := newWorld(t, ghURL)
	w.setConfig(ciCfg)
	w.forge("ciMoveBase", "other.txt")
	res, err := w.ciGate(GateOpts{})
	if err != nil || res.Verdict != GateBaseMoved || !strings.Contains(res.Err, "while CI verified; nothing landed") {
		t.Fatalf("%+v %v", res, err)
	}
	if w.forgeWord("state") != "OPEN" || w.onOriginMain("work.txt") || res.Changed {
		t.Errorf("work landed: state %s", w.forgeWord("state"))
	}
	if b := w.ciBranches(); b != "" {
		t.Errorf("rota/ci branch left behind:\n%s", b)
	}
}

// The forge merged onto a base that moved after the gate's check, so what
// landed is not what CI verified.
func TestGateCILandedTreeMismatch(t *testing.T) {
	w := newWorld(t, ghURL)
	w.setConfig(ciCfg)
	w.forge("pushBeforeMerge", "other.txt")
	res, err := w.ciGate(GateOpts{})
	if err != nil || res.Verdict != GateBaseMoved || !strings.Contains(res.Err, "differs from the tree CI verified") {
		t.Fatalf("%+v %v", res, err)
	}
	if !res.Changed || !w.onOriginMain("work.txt") {
		t.Errorf("Changed = %v; the PR did land", res.Changed)
	}
}

// A push to the base after the merge is not the merge differing from CI: the
// landed merge commit's tree is compared, not the base's new tip.
func TestGateCIPushAfterMerge(t *testing.T) {
	w := newWorld(t, ghURL)
	w.setConfig(ciCfg)
	w.forge("pushAfterMerge", "later.txt")
	res, err := w.ciGate(GateOpts{})
	if err != nil || res.Verdict != GatePass {
		t.Fatalf("%+v %v", res, err)
	}
	if !w.onOriginMain("later.txt") || !w.onOriginMain("work.txt") {
		t.Error("origin/main lacks the merge or the later push")
	}
}

// cancelForge cancels the run while CI is still pending.
type cancelForge struct {
	*fakeForge
	cancel context.CancelFunc
}

func (f cancelForge) CommitChecks(ctx context.Context, sha string) ([]tracker.CheckRun, error) {
	f.cancel()
	return []tracker.CheckRun{{Name: "ci/test", State: tracker.CheckPending}}, nil
}

// An interrupted gate still deletes its rota/ci branch and scratch worktree.
func TestGateCICancelCleansUp(t *testing.T) {
	w := newWorld(t, ghURL)
	w.setConfig(ciCfg)
	ctx, cancel := context.WithCancel(bg)
	defer cancel()
	e := w.ciEnv().withForge(func(provider, dir string) Forge {
		return cancelForge{fakeForge: &fakeForge{w: w, provider: provider}, cancel: cancel}
	}, false)
	e.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		if err := ctx.Err(); err != nil { // git.Exec would be killed; fail it outright
			return git.Result{ExitCode: -1}, err
		}
		return git.Exec(ctx, dir, args...)
	}
	e.Ctx = ctx
	e.Gate(ctx, w.dir, GateOpts{Slot: "w1", Base: "main"})
	if b := w.ciBranches(); b != "" {
		t.Errorf("rota/ci branch left behind:\n%s", b)
	}
	if extra := w.scratchTrees(); len(extra) != 0 {
		t.Errorf("scratch worktree left behind: %v", extra)
	}
	if w.forgeWord("state") != "OPEN" {
		t.Errorf("state %s", w.forgeWord("state"))
	}
}

// A red or unfinished CI train deletes every branch it pushed, bisect
// included, and its scratch worktree.
func TestTrainCICleansUp(t *testing.T) {
	for _, c := range []struct{ name, ci, fail, verdict string }{
		{"red", "", "b2.txt", GateVerifyFailed},
		{"timeout", "pending", "", GateVerifyTimeout},
	} {
		t.Run(c.name, func(t *testing.T) {
			w, res := ciTrain(t, c.ci, c.fail, "b1", "b2", "b3")
			if res.Verdict != c.verdict || len(res.Landed) != 0 {
				t.Fatalf("%+v", res)
			}
			if b := w.ciBranches(); b != "" {
				t.Errorf("rota/ci branch left behind:\n%s", b)
			}
			if extra := w.scratchTrees(); len(extra) != 0 {
				t.Errorf("scratch worktree left behind: %v", extra)
			}
		})
	}
}

// Under test.fullWhere ci, test.e2e still runs here, on the landed tree.
func TestGateCIRunsE2ELocally(t *testing.T) {
	w := newWorld(t, ghURL)
	w.setConfig(`{"test":{"full":["false"],"fullWhere":"ci","ciChecks":["ci/test"],"e2e":["false"]}}`)
	res, err := w.ciGate(GateOpts{})
	if err != nil || res.Verdict != GateVerifyFailed || !strings.Contains(res.Err, "test.e2e") || !res.Changed {
		t.Fatalf("%+v %v", res, err)
	}
}
