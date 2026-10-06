package worker

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/jsonx"
)

const ciCfg = `{"test":{"full":["false"],"fullWhere":"ci"}}` // "false" proves the local tier did not run

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

func TestTrainCINotRun(t *testing.T) {
	w, res := ciTrain(t, "none", "", "b1", "b2")
	if res.Verdict != GateCINotRun || len(res.Landed) != 0 || len(res.Members) != 2 || w.onMain("b1.txt") || w.onMain("b2.txt") {
		t.Fatalf("%+v", res)
	}
}

func TestFullWhere(t *testing.T) {
	for in, want := range map[string]string{`{}`: "local", `{"test":{"fullWhere":"ci"}}`: "ci", `{"test":{"fullWhere":"local"}}`: "local"} {
		if got, err := FullWhere(parseCfg(in)); err != nil || got != want {
			t.Errorf("%s: %q %v", in, got, err)
		}
	}
	if _, err := FullWhere(parseCfg(`{"test":{"fullWhere":"x"}}`)); err == nil {
		t.Error("x must be an error")
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
		s, err := ciSettingsFrom(parseCfg(c.cfg), c.env)
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
