package worker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #494: a local gate verifies a scratch merge of the PR into the base before it
// asks the forge to merge, so a red verify lands nothing and the PR can go back
// to its worker instead of being fixed forward on the base.

// A red test.full on the scratch merge leaves the PR open, the base untouched
// and the slot as it was, with no scratch tree behind.
func TestGateLocalRedVerifyLandsNothing(t *testing.T) {
	w := newWorld(t, ghURL)
	w.setConfig(`{"test":{"full":["true","false"]}}`)
	res, err := w.gate(false, GateOpts{})
	if err != nil || res.Verdict != GateVerifyFailed || !strings.Contains(res.Err, "nothing landed") {
		t.Fatalf("%+v %v", res, err)
	}
	if res.Changed || w.forgeWord("state") != "OPEN" || w.onOriginMain("work.txt") {
		t.Errorf("work landed: changed %v, state %s", res.Changed, w.forgeWord("state"))
	}
	if strings.Contains(w.logText(), "PRRequestMerge") {
		t.Errorf("the forge was asked to merge:\n%s", w.logText())
	}
	if _, err := os.Stat(filepath.Join(w.dir, "work.txt")); err == nil {
		t.Error("work.txt is on the local base")
	}
	if !strings.Contains(res.Hint, "send w1 back") {
		t.Errorf("hint %q does not bounce the slot", res.Hint)
	}
	if s := LoadRegistryTolerant(w.dir).Slot("w1"); s == nil || s.PR() != ghURL {
		t.Errorf("slot w1 lost its PR: %+v", s)
	}
	if extra := w.scratchTrees(); len(extra) != 0 {
		t.Errorf("scratch worktree left behind: %v", extra)
	}
}

// A queued PR record survives a red scratch verify.
func TestGateLocalRedVerifyKeepsTheReviewRecord(t *testing.T) {
	w := newWorld(t, ghURL)
	w.setConfig(`{"test":{"full":["false"]}}`)
	w.forge("body", "Closes #7")
	os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"),
		[]byte(fmt.Sprintf(`{"slots":[{"name":"w1","branch":"next"}],"prs":[{"pr":%q,"branch":"w1","base":"main","from":"w1","issue":"7"}]}`, ghURL)), 0o644)
	res, err := w.env(false).Gate(bg, w.dir, GateOpts{Slot: "#7", Base: "main"})
	if err != nil || res.Verdict != GateVerifyFailed || res.Changed {
		t.Fatalf("%+v %v", res, err)
	}
	if LoadRegistryTolerant(w.dir).QueuedPR(ghURL) == nil {
		t.Error("the review record was dropped")
	}
}

// test.full and test.e2e run in a scratch tree that holds the merge, not in the
// gate checkout, and the scratch tree is one the worktree guard ignores.
func TestGateLocalVerifiesInAScratchMerge(t *testing.T) {
	w := newWorld(t, ghURL)
	out := filepath.Join(t.TempDir(), "where")
	w.setConfig(fmt.Sprintf(`{"test":{"full":["test -f work.txt && pwd >> %[1]s"],"e2e":["test -f work.txt && pwd >> %[1]s"]}}`, out))
	res, err := w.gate(false, GateOpts{})
	if err != nil || res.Verdict != GatePass || !res.Changed || len(res.Verified) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	b, _ := os.ReadFile(out)
	dirs := strings.Fields(string(b))
	if len(dirs) != 2 {
		t.Fatalf("want test.full and test.e2e to run once each, ran in %v", dirs)
	}
	for _, d := range dirs {
		if d == w.dir || !strings.Contains(d, "/rota-train-") {
			t.Errorf("verify ran in %s, want a rota-train-*/tree scratch tree", d)
		}
	}
	if extra := w.scratchTrees(); len(extra) != 0 {
		t.Errorf("scratch worktree left behind: %v", extra)
	}
}

// A red test.e2e on the scratch merge lands nothing either.
func TestGateLocalRedE2ELandsNothing(t *testing.T) {
	w := newWorld(t, ghURL)
	w.setConfig(`{"test":{"full":["true"],"e2e":["false"]}}`)
	res, err := w.gate(false, GateOpts{})
	if err != nil || res.Verdict != GateVerifyFailed || !strings.Contains(res.Err, "test.e2e") || res.Changed {
		t.Fatalf("%+v %v", res, err)
	}
	if w.forgeWord("state") != "OPEN" || w.onOriginMain("work.txt") {
		t.Errorf("work landed: state %s", w.forgeWord("state"))
	}
}

// The base moving while the scratch tree verifies leaves the verify stale:
// nothing lands.
func TestGateLocalBaseMovedWhileVerifying(t *testing.T) {
	w := newWorld(t, ghURL)
	mover := t.TempDir()
	gitq(t, mover, "clone", "-q", w.origin, ".")
	os.WriteFile(filepath.Join(mover, "other.txt"), []byte("other\n"), 0o644)
	gitq(t, mover, "add", "other.txt")
	gitq(t, mover, "commit", "-q", "-m", "lands while the gate verifies")
	w.setConfig(fmt.Sprintf(`{"test":{"full":["git -C %s push -q origin HEAD:main"]}}`, mover))
	res, err := w.gate(false, GateOpts{})
	if err != nil || res.Verdict != GateBaseMoved || !strings.Contains(res.Err, "nothing landed") {
		t.Fatalf("%+v %v", res, err)
	}
	if res.Changed || w.forgeWord("state") != "OPEN" || w.onOriginMain("work.txt") {
		t.Errorf("work landed: changed %v, state %s", res.Changed, w.forgeWord("state"))
	}
}

// The base moving between the last check and the forge merge lands a tree the
// scratch verify never saw: the gate verifies what landed and says so.
func TestGateLocalLandedTreeDiffersIsVerifiedAgain(t *testing.T) {
	for _, c := range []struct {
		name, full, verdict string
	}{
		{"green", `test -f work.txt`, GatePass},
		{"red", `test ! -f other.txt`, GateVerifyFailed}, // green on the scratch merge, red once other.txt landed too
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			w.setConfig(fmt.Sprintf(`{"test":{"full":[%q]}}`, c.full))
			w.forge("pushBeforeMerge", "other.txt")
			res, err := w.gate(false, GateOpts{})
			if err != nil || res.Verdict != c.verdict || !res.Changed {
				t.Fatalf("%+v %v", res, err)
			}
			if !strings.Contains(strings.Join(res.Notes, "\n"), "VERIFY-AGAIN") {
				t.Errorf("no VERIFY-AGAIN note: %v", res.Notes)
			}
			if c.verdict == GateVerifyFailed && !strings.Contains(res.Hint, "fix forward") {
				t.Errorf("a red verify of the landed tree must say it landed: %q", res.Hint)
			}
		})
	}
}

// --no-verify still merges without a scratch tree or a verify.
func TestGateLocalNoVerifyMakesNoScratchTree(t *testing.T) {
	w := newWorld(t, ghURL)
	out := filepath.Join(t.TempDir(), "ran")
	w.setConfig(fmt.Sprintf(`{"test":{"full":["touch %s"]}}`, out))
	res, err := w.gate(false, GateOpts{NoVerify: true})
	if err != nil || res.Verdict != GatePass || !res.VerifySkipped {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("test.full ran under --no-verify")
	}
}
