package worker

import (
	"context"
	"github.com/l4ci/rota/internal/landing"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/postgate"
)

func TestLandingAdapterEndsLandedItem(t *testing.T) {
	w := newWorld(t, "")
	gittest.Run(t, w.dir, "fetch", "-q", "origin", "w1:w1")
	// A local branch is enough to exercise the real gate and registry bookkeeping.
	if _, err := UpdateSlot(w.dir, "w1", func(s *Slot) { s.SetTask("12") }); err != nil {
		t.Fatal(err)
	}
	if err := RecordItemStart(w.dir, "12", time.Now()); err != nil {
		t.Fatal(err)
	}
	var released string
	env := w.env(false).Landing(func(_, issue, slot string) error { released = issue + "@" + slot; return nil })
	run := env.Run(context.Background(), w.dir, postgate.Request{Gate: &postgate.GateOpts{Slot: "w1", Base: "main"}}).Gate
	if run.Err != nil || run.BookErr != nil || run.Refused || run.Result.Verdict != landing.GatePass {
		t.Fatalf("run: %+v", run)
	}
	if _, ok := LoadRegistryTolerant(w.dir).ItemStart("12"); ok {
		t.Fatal("landed item clock remains")
	}
	if released != "12@w1" {
		t.Fatalf("released %q, want 12@w1", released)
	}
}

type fixedRunner struct{ result landing.GateResult }

func (r fixedRunner) Gate(context.Context, string, postgate.GateOpts) (landing.GateResult, error) {
	return r.result, nil
}

func (r fixedRunner) Train(context.Context, string, postgate.TrainOpts) (landing.TrainResult, error) {
	return landing.TrainResult{}, nil
}

// Post-gate reporting follows git.remote, the rule the gate itself uses: the
// "is there a remote" check and the ref of a branch with no local head.
func TestLandingReportsThroughConfiguredRemote(t *testing.T) {
	for _, tc := range []struct {
		name    string
		remotes []string
	}{
		{"only the configured remote", []string{"upstream"}},
		{"a stale origin beside it", []string{"upstream", "origin"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			gittest.Run(t, dir, "init", "-q", "-b", "main")
			gittest.Run(t, dir, "commit", "-q", "--allow-empty", "-m", "stale")
			stale := strings.TrimSpace(string(gittest.Run(t, dir, "rev-parse", "HEAD")))
			gittest.Run(t, dir, "commit", "-q", "--allow-empty", "-m", "head")
			head := strings.TrimSpace(string(gittest.Run(t, dir, "rev-parse", "--short", "HEAD")))
			for _, name := range tc.remotes {
				gittest.Run(t, dir, "remote", "add", name, dir)
				sha := stale
				if name == "upstream" {
					sha = "HEAD"
				}
				gittest.Run(t, dir, "update-ref", "refs/remotes/"+name+"/feature", sha)
			}
			if err := os.MkdirAll(filepath.Join(dir, ".rota"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{"git":{"remote":"upstream"}}`), 0o644); err != nil {
				t.Fatal(err)
			}
			env := Env{Config: diskConfig}.WithDefaults().Landing(nil)
			env.Runner = fixedRunner{landing.GateResult{Branch: "feature", PR: "20", Verdict: landing.GateCheckBroke}}
			var reports []postgate.Report
			env.Report = func(_ context.Context, _ string, r postgate.Report) error {
				reports = append(reports, r)
				return nil
			}
			run := env.Run(context.Background(), dir, postgate.Request{Gate: &postgate.GateOpts{}}).Gate
			if run.Err != nil || len(run.Warnings) != 0 {
				t.Fatalf("run: %+v", run)
			}
			if len(reports) != 1 || reports[0].Ref != "upstream/feature" || reports[0].Gate.SHA != head {
				t.Fatalf("reports: %+v, want one for upstream/feature at %s", reports, head)
			}
		})
	}
}
