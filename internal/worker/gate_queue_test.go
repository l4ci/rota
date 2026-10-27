package worker

import (
	"errors"
	"github.com/l4ci/rota/internal/exitcode"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/verdict"
)

// setQueued writes a registry whose only slot, w2, moved on from PR 7: the PR
// waits in `prs` with the branch w1 the fixture pushed.
func (w *world) setQueued() {
	doc := `{"slots":[{"name":"w2","branch":"park/w2"}],"prs":[{"issue":"5","branch":"w1","pr":"` + ghURL + `","base":"main","from":"w2","claimId":"w2@1","round":1,"relays":[]}]}`
	os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"), []byte(doc), 0o644)
}

func TestGateQueuedPR(t *testing.T) {
	gateAs := func(w *world, arg string, o GateOpts) (GateResult, error) {
		o.Slot, o.Base = arg, "main"
		return w.env(false).Gate(bg, w.dir, o)
	}
	for _, arg := range []string{"#7", "7", ghURL} {
		w := newWorld(t, "")
		w.setQueued()
		w.setPRBody("Closes #5\n")
		if res, err := gateAs(w, arg, GateOpts{CheckOnly: true}); err != nil || res.Verdict != GateFresh || res.Branch != "w1" || res.PR != ghURL {
			t.Fatalf("%s check-only: %+v %v", arg, res, err)
		}
		if n := len(LoadRegistryTolerant(w.dir).PRs()); n != 1 {
			t.Fatalf("%s: a check keeps the record, have %d", arg, n)
		}
		res, err := gateAs(w, arg, GateOpts{})
		if err != nil || res.Verdict != GatePass {
			t.Fatalf("%s: %+v %v", arg, res, err)
		}
		if res.Issue != "5" {
			t.Errorf("%s: result issue = %q, want the queued record's 5", arg, res.Issue)
		}
		if _, err := os.Stat(filepath.Join(w.dir, "work.txt")); err != nil {
			t.Errorf("%s: the PR's work is not on main", arg)
		}
		if n := len(LoadRegistryTolerant(w.dir).PRs()); n != 0 {
			t.Errorf("%s: a passing gate drops the record, have %d", arg, n)
		}
	}

	w := newWorld(t, "")
	w.setQueued()
	_, err := gateAs(w, "w2", GateOpts{CheckOnly: true})
	var we *exitcode.Error
	if !errors.As(err, &we) || we.Exit != exitcode.ExitUsage || !strings.Contains(we.Hint, "rota worker gate 7`") {
		t.Errorf("a slot whose PR is queued is refused with a hint: %v", err)
	}
	// Strict record lookup refuses an unknown PR; the gate resolves it as external.
	if _, err := LoadRegistryTolerant(w.dir).GateTarget("#99"); exitOf(err) != exitcode.ExitResolution {
		t.Errorf("unknown PR: %v", err)
	}
	if res, err := gateAs(w, "#99", GateOpts{CheckOnly: true}); err != nil || res.Verdict != GateFresh {
		t.Errorf("unrecorded PR gated by number: %+v %v", res, err)
	}
	// A slot recording the PR is found by its number when nothing is queued.
	w2 := newWorld(t, ghURL)
	if res, err := gateAs(w2, "#7", GateOpts{CheckOnly: true}); err != nil || res.Verdict != GateFresh {
		t.Errorf("slot by PR: %+v %v", res, err)
	}
}

func TestQueuedPRHelpers(t *testing.T) {
	doc := `{"slots":[],"prs":[{"issue":"5","pr":"https://github.com/o/r/pull/7"},{"issue":"6","pr":"https://gitlab.com/o/r/-/merge_requests/9"}]}`
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".rota"), 0o755)
	os.WriteFile(RegistryPath(root), []byte(doc), 0o644)
	reg := LoadRegistryTolerant(root)
	for ref, issue := range map[string]string{"#7": "5", "7": "5", "https://github.com/o/r/pull/7": "5", "9": "6", "https://gitlab.com/o/r/-/merge_requests/9": "6"} {
		if q := reg.QueuedPR(ref); q == nil || q.Issue != issue {
			t.Errorf("%s: %v", ref, q)
		}
	}
	if reg.QueuedPR("8") != nil || reg.QueuedPR("x") != nil {
		t.Error("no such PR")
	}
	if reg.QueuedIssue("#6") == nil || reg.QueuedIssue("7") != nil {
		t.Error("QueuedIssue")
	}
	if err := RemoveQueuedPR(root, "#7"); err != nil {
		t.Fatal(err)
	}
	if got := LoadRegistryTolerant(root).PRs(); len(got) != 1 || got[0].Issue != "6" {
		t.Errorf("after remove: %v", got)
	}
}

// A PR the gate merged leaves the review list whatever the verdict says about
// the verify or the local base, and however the target resolved (#1028).
func TestGateDropsMergedQueuedPR(t *testing.T) {
	for _, arg := range []string{"#7", "w2"} {
		t.Run(arg, func(t *testing.T) {
			w := newWorld(t, "")
			w.setQueued()
			w.setPRBody("Closes #5\n")
			if arg == "w2" {
				// the slot records the PR the queue also holds
				doc := `{"slots":[{"name":"w2","branch":"w1","pr":"` + ghURL + `","base":"main"}],"prs":[{"issue":"5","branch":"w1","pr":"` + ghURL + `","base":"main","from":"w2","round":1,"relays":[]}]}`
				os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"), []byte(doc), 0o644)
			}
			os.WriteFile(filepath.Join(w.dir, "local.txt"), []byte("local only\n"), 0o644)
			gitq(t, w.dir, "add", "local.txt")
			gitq(t, w.dir, "commit", "-q", "-m", "local only commit")
			res, err := w.env(false).Gate(bg, w.dir, GateOpts{Slot: arg, Base: "main"})
			if err != nil {
				t.Fatal(err)
			}
			if res.Verdict != GateMergedRemotely || !res.Changed {
				t.Fatalf("verdict=%s changed=%v: %s", res.Verdict, res.Changed, res.Err)
			}
			if n := len(LoadRegistryTolerant(w.dir).PRs()); n != 0 {
				t.Errorf("a merged PR stays in review: %d record(s)", n)
			}
		})
	}
}

// A queued PR's slot moved on, so the author kind comes from the issue's latest
// assign ledger entry: a Codex-built PR needs a claude cross review, not codex.
func TestGateQueuedPRAuthorFromAssignLedger(t *testing.T) {
	run := func(t *testing.T, entries ...ledger.Entry) GateResult {
		t.Helper()
		w := newWorld(t, "")
		w.setConfig(`{"round":{"harnesses":["claude","codex"]},"test":{"full":["true"]}}`)
		w.setQueued()
		w.setPRBody("Closes #5\n")
		w.forge("issueLabels", "in-progress,size:Major")
		w.record("w1", "", verdict.ReviewSpec, verdict.ReviewQuality)
		for _, e := range entries {
			e.TS = time.Now()
			if err := ledger.Append(w.dir, e); err != nil {
				t.Fatal(err)
			}
		}
		res, err := w.env(false).Gate(bg, w.dir, GateOpts{Slot: "#7", Base: "main", CheckOnly: true, CheckReviews: true})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	t.Run("latest assign entry names the author", func(t *testing.T) {
		res := run(t,
			ledger.Entry{Kind: ledger.KindAssign, Issue: "5", Slot: "ben", Harness: "claude"},
			ledger.Entry{Kind: ledger.KindAssign, Issue: "9", Slot: "kit", Harness: "claude"},
			ledger.Entry{Kind: ledger.KindAssign, Issue: "5", Slot: "w2", Harness: "codex"})
		if res.Verdict != GateReviewMissing || !strings.Contains(res.Err, "review-cross verdict from claude") {
			t.Fatalf("%s (%s), want review-missing naming claude", res.Verdict, res.Err)
		}
	})
	t.Run("no entry falls back to the default order", func(t *testing.T) {
		res := run(t)
		if res.Verdict != GateReviewMissing || !strings.Contains(res.Err, "review-cross verdict from codex") {
			t.Fatalf("%s (%s), want review-missing naming codex", res.Verdict, res.Err)
		}
	})
}
