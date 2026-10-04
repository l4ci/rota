package worker

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		if res, err := gateAs(w, arg, GateOpts{CheckOnly: true}); err != nil || res.Verdict != GateFresh || res.Branch != "w1" || res.PR != ghURL {
			t.Fatalf("%s check-only: %+v %v", arg, res, err)
		}
		if n := len(LoadRegistry(w.dir).PRs()); n != 1 {
			t.Fatalf("%s: a check keeps the record, have %d", arg, n)
		}
		res, err := gateAs(w, arg, GateOpts{})
		if err != nil || res.Verdict != GatePass {
			t.Fatalf("%s: %+v %v", arg, res, err)
		}
		if _, err := os.Stat(filepath.Join(w.dir, "work.txt")); err != nil {
			t.Errorf("%s: the PR's work is not on main", arg)
		}
		if n := len(LoadRegistry(w.dir).PRs()); n != 0 {
			t.Errorf("%s: a passing gate drops the record, have %d", arg, n)
		}
	}

	w := newWorld(t, "")
	w.setQueued()
	_, err := gateAs(w, "w2", GateOpts{CheckOnly: true})
	var we *Error
	if !errors.As(err, &we) || we.Exit != ExitUsage || !strings.Contains(we.Hint, "rota worker gate #7") {
		t.Errorf("a slot whose PR is queued is refused with a hint: %v", err)
	}
	if _, err := gateAs(w, "#99", GateOpts{CheckOnly: true}); exitOf(err) != ExitResolution {
		t.Errorf("unknown PR: %v", err)
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
	reg := LoadRegistry(root)
	for ref, issue := range map[string]string{"#7": "5", "7": "5", "https://github.com/o/r/pull/7": "5", "9": "6", "https://gitlab.com/o/r/-/merge_requests/9": "6"} {
		if q := reg.QueuedPR(ref); q == nil || Str(q, "issue") != issue {
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
	if got := LoadRegistry(root).PRs(); len(got) != 1 || Str(got[0], "issue") != "6" {
		t.Errorf("after remove: %v", got)
	}
}
