package worker

import (
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/git"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// trainWorld is a gate checkout on main with local branches b1..bN, each adding
// its own file, and a slot per branch: no PRs, so the train merges locally.
func trainWorld(t *testing.T, verify string, branches ...string) *world {
	t.Helper()
	w := newWorld(t, "")
	var slots []string
	for _, b := range branches {
		gitq(t, w.dir, "checkout", "-q", "-b", b, "main")
		trainWrite(t, w, b+".txt", b)
		gitq(t, w.dir, "checkout", "-q", "main")
		slots = append(slots, fmt.Sprintf(`{"name":"%s","branch":"%s"}`, b, b))
	}
	os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"), []byte(`{"slots":[`+strings.Join(slots, ",")+`]}`), 0o644)
	w.setConfig(fmt.Sprintf(`{"test":{"full":[%q]}}`, verify))
	return w
}

// trainWrite commits file=body on the checked-out branch.
func trainWrite(t *testing.T, w *world, file, body string) {
	t.Helper()
	os.WriteFile(filepath.Join(w.dir, file), []byte(body+"\n"), 0o644)
	gitq(t, w.dir, "add", file)
	gitq(t, w.dir, "commit", "-q", "-m", "add "+file)
}

func (w *world) onMain(file string) bool {
	_, err := os.Stat(filepath.Join(w.dir, file))
	return err == nil
}

func (w *world) train(o TrainOpts) (TrainResult, error) {
	o.Base = "main"
	return w.env(false).Train(bg, w.dir, o)
}

func TestTrainLandsAll(t *testing.T) {
	w := trainWorld(t, "test -f b1.txt && test -f b2.txt && test -f b3.txt", "b1", "b2", "b3")
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2", "b3"}})
	if err != nil || res.Verdict != GatePass || strings.Join(res.Landed, ",") != "b1,b2,b3" || !res.Changed {
		t.Fatalf("%+v %v", res, err)
	}
	if len(res.Verified) != 1 {
		t.Errorf("verified once, got %v", res.Verified)
	}
	// Members 2 and 3 are behind the base once the first lands, and the landing
	// gates run no verify of their own: they are LandVerified, not --no-verify.
	for _, n := range res.Notes {
		if strings.HasPrefix(n, "NO-VERIFY") || strings.HasPrefix(n, "STALE-MERGE") {
			t.Errorf("a train's landing gate must stay quiet, noted %q", n)
		}
	}
	for _, f := range []string{"b1.txt", "b2.txt", "b3.txt"} {
		if !w.onMain(f) {
			t.Errorf("%s is not on main", f)
		}
	}
	if out := gitq(t, w.dir, "worktree", "list"); strings.Count(out, "\n") != 0 {
		t.Errorf("scratch worktree left behind:\n%s", out)
	}
}

func TestTrainBisectsToTheCulprit(t *testing.T) {
	w := trainWorld(t, "test ! -f b2.txt", "b1", "b2", "b3", "b4")
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2", "b3", "b4"}})
	if err != nil || res.Verdict != GateVerifyFailed || res.Culprit != "b2" || res.Changed || len(res.Landed) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if !strings.Contains(res.Err, "the first 1 member(s) pass") {
		t.Errorf("message: %s", res.Err)
	}
	if w.onMain("b1.txt") {
		t.Error("nothing lands on a red train")
	}
	if !res.Members[1].Culprit || res.Members[0].Culprit {
		t.Errorf("members: %+v", res.Members)
	}
}

func TestTrainNamesTheInteraction(t *testing.T) {
	// b1 and b3 are each fine; together they break the tree.
	w := trainWorld(t, "! (test -f b1.txt && test -f b3.txt)", "b1", "b2", "b3")
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2", "b3"}})
	if err != nil || res.Verdict != GateVerifyFailed || res.Culprit != "b3" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestTrainLandGreen(t *testing.T) {
	w := trainWorld(t, "test ! -f b3.txt", "b1", "b2", "b3")
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2", "b3"}, LandGreen: true})
	if err != nil || res.Verdict != GateVerifyFailed || res.Culprit != "b3" || strings.Join(res.Landed, ",") != "b1,b2" || !res.Changed {
		t.Fatalf("%+v %v", res, err)
	}
	if !w.onMain("b1.txt") || !w.onMain("b2.txt") || w.onMain("b3.txt") {
		t.Error("main should hold the verified prefix only")
	}
}

func TestTrainConflict(t *testing.T) {
	w := trainWorld(t, "true", "b1", "b2")
	for _, b := range []string{"b1", "b2"} { // both rewrite the same new file differently
		gitq(t, w.dir, "checkout", "-q", b)
		trainWrite(t, w, "clash.txt", b)
		gitq(t, w.dir, "checkout", "-q", "main")
	}
	// An order the caller chose is not retried: the conflict names the member.
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2"}, Order: []string{"b1", "b2"}})
	if err != nil || res.Verdict != GateMergeFailed || res.Culprit != "b2" || w.onMain("b1.txt") {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestTrainRefusesBeforeMerging(t *testing.T) {
	w := trainWorld(t, "true", "b1", "b2")
	trainWrite(t, w, "b2.txt", "main") // b2 and main both add b2.txt: the branch is stale on a conflict
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2"}})
	if err != nil || res.Verdict != GateStale || res.Culprit != "b2" || w.onMain("b1.txt") {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestTrainSharedFileCleanMerge(t *testing.T) {
	// b2 edits the top of a file main edits at the bottom: both sides changed it,
	// the merge is clean, so the train lands it after verifying the merged tree.
	w := trainWorld(t, "true", "b1", "b2")
	lines := "a\n" + strings.Repeat("-\n", 19) + "z\n"
	trainWrite(t, w, "shared.txt", strings.TrimSuffix(lines, "\n"))
	gitq(t, w.dir, "checkout", "-q", "b2")
	gitq(t, w.dir, "merge", "-q", "main", "-m", "sync")
	trainWrite(t, w, "shared.txt", strings.TrimSuffix("A\n"+lines[2:], "\n"))
	gitq(t, w.dir, "checkout", "-q", "main")
	trainWrite(t, w, "shared.txt", strings.TrimSuffix(lines[:len(lines)-2]+"Z\n", "\n"))
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2"}})
	if err != nil || res.Verdict != GatePass || strings.Join(res.Landed, ",") != "b1,b2" || !w.onMain("b2.txt") {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestTrainBaseMoved(t *testing.T) {
	// the base moves while the train verifies: the verified tree is not what would land
	cmd := fmt.Sprintf("git -c user.name=t -c user.email=t@t -C %s commit -q --allow-empty -m moved", "WDIR")
	w := trainWorld(t, "true", "b1", "b2")
	w.setConfig(fmt.Sprintf(`{"test":{"full":[%q]}}`, strings.Replace(cmd, "WDIR", w.dir, 1)))
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2"}})
	if err != nil || res.Verdict != GateBaseMoved || len(res.Landed) != 0 || w.onMain("b1.txt") {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestTrainApprovalFailsBeforeVerify(t *testing.T) {
	w := trainWorld(t, "touch ran-verify", "b1", "b2")
	want := fmt.Errorf("needs a human")
	var files []string
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2"}, Approve: func(f func() ([]string, error)) (*exitcode.Refusal, error) {
		files, _ = f()
		return nil, want
	}})
	if err != want || res.Verdict != GateApprovalRequired || strings.Join(files, ",") != "b1.txt,b2.txt" {
		t.Fatalf("%+v %v files=%v", res, err, files)
	}
	if w.onMain("b1.txt") {
		t.Error("an unapproved train merges nothing")
	}
}

func TestTrainUsage(t *testing.T) {
	w := trainWorld(t, "true", "b1")
	if _, err := w.train(TrainOpts{}); err == nil {
		t.Error("an empty train is refused")
	}
	if _, err := w.train(TrainOpts{Targets: []string{"b1", "b1"}}); err == nil {
		t.Error("a repeated member is refused")
	}
}

// hook installs a git hook in the gate checkout.
func (w *world) hook(t *testing.T, name, body string) {
	t.Helper()
	p := filepath.Join(w.dir, ".git", "hooks", name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestTrainSecondLandingFails(t *testing.T) {
	w := trainWorld(t, "true", "b1", "b2", "b3")
	// hooks are shared with the train's scratch worktree: act in the checkout only
	w.hook(t, "pre-merge-commit", `[ "$(git rev-parse --show-toplevel)" = `+w.dir+` ] && [ -f b2.txt ] && [ ! -f b3.txt ] && exit 1; exit 0`)
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2", "b3"}})
	if err != nil || res.Verdict != GateMergeFailed || res.Culprit != "b2" || strings.Join(res.Landed, ",") != "b1" || !res.Changed {
		t.Fatalf("%+v %v", res, err)
	}
	if !strings.Contains(res.Hint, "landed 1 of 3") {
		t.Errorf("hint: %s", res.Hint)
	}
	if !res.Members[0].Landed || res.Members[1].Landed || res.Members[2].Landed || !w.onMain("b1.txt") || w.onMain("b3.txt") {
		t.Errorf("members: %+v", res.Members)
	}
}

func TestTrainForeignCommitBetweenLandings(t *testing.T) {
	w := trainWorld(t, "true", "b1", "b2", "b3")
	// a foreign commit lands on main right after the first landing
	w.hook(t, "post-merge", `[ "$(git rev-parse --show-toplevel)" = `+w.dir+` ] && [ ! -f foreign.txt ] && { echo x > foreign.txt && git add foreign.txt && git -c user.name=t -c user.email=t@t commit -q -m foreign; }; exit 0`)
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2", "b3"}})
	if err != nil || res.Verdict != GateBaseMoved || strings.Join(res.Landed, ",") != "b1" || res.Culprit != "b2" {
		t.Fatalf("%+v %v", res, err)
	}
	if !strings.Contains(res.Hint, "landed 1 of 3") || w.onMain("b2.txt") {
		t.Errorf("hint %q, b2 on main %v", res.Hint, w.onMain("b2.txt"))
	}
}

func TestTrainRedBase(t *testing.T) {
	// the base itself fails verification: no member is to blame
	w := trainWorld(t, "false", "b1", "b2")
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2"}})
	if err != nil || res.Verdict != GateVerifyFailed || res.Culprit != "" || !strings.Contains(res.Err, "fails verification on its own") {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestTrainRefusesMixedMembers(t *testing.T) {
	w := trainWorld(t, "true", "b1", "b2")
	os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"), []byte(`{"slots":[{"name":"b1","branch":"b1"},{"name":"b2","branch":"b2","pr":"https://example.test/o/r/pull/7"}]}`), 0o644)
	if _, err := w.train(TrainOpts{Targets: []string{"b1", "b2"}}); err == nil || !strings.Contains(err.Error(), "all PRs or all slots") {
		t.Fatalf("a mixed train is refused: %v", err)
	}
}

func TestTrainReportsFailedRecovery(t *testing.T) {
	w := trainWorld(t, "true", "b1")
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
	res, err := e.Train(bg, w.dir, TrainOpts{Targets: []string{"b1"}, Base: "main"})
	if err != nil || res.Verdict != GateMergeFailed || res.Changed {
		t.Fatalf("%+v %v", res, err)
	}
	for _, want := range []string{"CONFLICT", "index.lock exists", "git merge --abort", "git status"} {
		if !strings.Contains(res.Err, want) {
			t.Errorf("result missing %q: %s", want, res.Err)
		}
	}
}

// e2eWorld is trainWorld with a test.e2e tier next to test.full.
func e2eWorld(t *testing.T, full, e2e string, branches ...string) *world {
	t.Helper()
	w := trainWorld(t, full, branches...)
	w.setConfig(fmt.Sprintf(`{"test":{"full":[%q],"e2e":[%q]}}`, full, e2e))
	return w
}

func TestTrainRunsE2EAfterFull(t *testing.T) {
	w := e2eWorld(t, "true", "test -f b1.txt && test -f b2.txt", "b1", "b2")
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2"}})
	if err != nil || res.Verdict != GatePass || strings.Join(res.Landed, ",") != "b1,b2" {
		t.Fatalf("%+v %v", res, err)
	}
	if len(res.Verified) != 1 || len(res.E2EVerified) != 1 {
		t.Errorf("full %v, e2e %v", res.Verified, res.E2EVerified)
	}
}

func TestTrainBisectsE2E(t *testing.T) {
	w := e2eWorld(t, "true", "test ! -f b2.txt", "b1", "b2", "b3")
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2", "b3"}})
	if err != nil || res.Verdict != GateVerifyFailed || res.Culprit != "b2" || len(res.Landed) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if !strings.Contains(res.Err, "test.e2e") || !strings.Contains(res.Err, "the first 1 member(s) pass") {
		t.Errorf("message: %s", res.Err)
	}
	if w.onMain("b1.txt") {
		t.Error("nothing lands on a red e2e")
	}
}

func TestTrainE2ELandGreen(t *testing.T) {
	w := e2eWorld(t, "true", "test ! -f b3.txt", "b1", "b2", "b3")
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2", "b3"}, LandGreen: true})
	if err != nil || res.Culprit != "b3" || strings.Join(res.Landed, ",") != "b1,b2" {
		t.Fatalf("%+v %v", res, err)
	}
	notes := strings.Join(res.Notes, "\n")
	if !strings.Contains(notes, "e2e FAILED: test ! -f b3.txt") || strings.Contains(notes, "NO-E2E") {
		t.Errorf("a red e2e is not a skipped one; notes:\n%s", notes)
	}
}

func TestTrainSkipsE2EWhenFullFails(t *testing.T) {
	w := e2eWorld(t, "test ! -f b2.txt", "exit 1", "b1", "b2")
	res, _ := w.train(TrainOpts{Targets: []string{"b1", "b2"}})
	if res.Culprit != "b2" || strings.Contains(res.Err, "test.e2e") || len(res.E2EVerified) != 0 {
		t.Fatalf("e2e must not run on a red full: %+v", res)
	}
}

// In local mode a verify run that cannot start is an error, not a verdict:
// nothing about the train is wrong. TMPDIR turns read-only once the scratch
// tree exists, so the verify log cannot be created.
func TestTrainLocalVerifyErrorIsError(t *testing.T) {
	w := trainWorld(t, "true", "b1")
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Cleanup(func() { os.Chmod(tmp, 0o700) })
	e := w.env(false)
	e.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		r, err := git.Exec(ctx, dir, args...)
		if len(args) > 1 && args[0] == "worktree" && args[1] == "add" {
			os.Chmod(tmp, 0o500)
		}
		return r, err
	}
	res, err := e.Train(bg, w.dir, TrainOpts{Base: "main", Targets: []string{"b1"}})
	if err == nil || res.Verdict == GateCheckBroke {
		t.Fatalf("want an error, got %+v %v", res, err)
	}
	if w.onMain("b1.txt") {
		t.Error("b1 landed")
	}
}

func TestTrainApprovalRefusalIsTyped(t *testing.T) {
	w := trainWorld(t, "true", "b1", "b2")
	want := &exitcode.Refusal{Err: &exitcode.Error{Exit: exitcode.ExitRefused, Message: "gate"}, Data: BlockData{BlockedBy: "manual gate"}}
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2"}, Approve: func(func() ([]string, error)) (*exitcode.Refusal, error) { return want, nil }})
	if res.Verdict != GateApprovalRequired {
		t.Fatalf("verdict %q", res.Verdict)
	}
	if res.Refusal != want || err != want.Err || !res.Refused(err) {
		t.Errorf("refusal = %+v, err %v, refused %v", res.Refusal, err, res.Refused(err))
	}
}

// A train and a gate started together queue on the repo's land lock: their
// verifies never overlap (the train's scratch worktree is never visible to the
// gate's verify) and neither lands inside the other's verify-to-land window, so
// the train never reports base-moved (#427).
func TestTrainAndGateSerialize(t *testing.T) {
	log := filepath.Join(t.TempDir(), "verify.log")
	verify := fmt.Sprintf(`echo start >> %[1]s; n=$(git worktree list | wc -l); echo "worktrees $n" >> %[1]s; sleep 1; echo end >> %[1]s`, log)
	w := trainWorld(t, verify, "b1", "b2", "b3")
	var tres TrainResult
	var gres GateResult
	var terr, gerr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		tres, terr = w.train(TrainOpts{Targets: []string{"b1", "b2"}})
	}()
	go func() {
		defer wg.Done()
		gres, gerr = w.env(false).Gate(bg, w.dir, GateOpts{Slot: "b3", Base: "main"})
	}()
	wg.Wait()
	if terr != nil || tres.Verdict != GatePass {
		t.Errorf("train: %+v %v", tres, terr)
	}
	if gerr != nil || gres.Verdict != GatePass {
		t.Errorf("gate: %+v %v", gres, gerr)
	}
	data, _ := os.ReadFile(log)
	var seq []string
	for _, l := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if !strings.HasPrefix(l, "worktrees") {
			seq = append(seq, l)
		}
	}
	if got := strings.Join(seq, ","); got != "start,end,start,end" {
		t.Errorf("verifies overlapped: %s\n%s", got, data)
	}
	for _, f := range []string{"b1.txt", "b2.txt", "b3.txt"} {
		if !w.onMain(f) {
			t.Errorf("%s is not on main", f)
		}
	}
}

// countingVerify is a test.full command that appends a line to a counter file
// outside the repo each time it runs, then applies cond.
func countingVerify(t *testing.T, cond string) (cmd, counter string) {
	t.Helper()
	counter = filepath.Join(t.TempDir(), "runs")
	return fmt.Sprintf("echo x >> %s; %s", counter, cond), counter
}

func runs(t *testing.T, counter string) int {
	t.Helper()
	b, _ := os.ReadFile(counter)
	return strings.Count(string(b), "x")
}

func TestTrainCachesPassingVerdicts(t *testing.T) {
	cmd, counter := countingVerify(t, "true")
	w := trainWorld(t, cmd, "b1", "b2")
	targets := []string{"b1", "b2"}
	// a red train lands nothing, so base and heads stay put for the re-run
	w.setConfig(fmt.Sprintf(`{"test":{"full":[%q]}}`, cmd+"; test ! -f b2.txt"))
	res, err := w.train(TrainOpts{Targets: targets})
	if err != nil || res.Verdict != GateVerifyFailed || res.Culprit != "b2" || len(res.CacheHits) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	first := runs(t, counter)
	res, err = w.train(TrainOpts{Targets: targets})
	if err != nil || res.Verdict != GateVerifyFailed || res.Culprit != "b2" {
		t.Fatalf("re-run: %+v %v", res, err)
	}
	// the red full tree and the red bisect step run again; only the green prefix hits
	if got := runs(t, counter); got != first+1 {
		t.Errorf("a red re-verifies, a pass is reused: %d runs, then %d", first, got)
	}
	if len(res.CacheHits) != 1 || !strings.Contains(res.CacheHits[0], "first 1 member") {
		t.Errorf("hits: %v", res.CacheHits)
	}
}

func TestTrainCacheMissesOnChangedHeadOrBase(t *testing.T) {
	cmd, counter := countingVerify(t, "true")
	w := trainWorld(t, cmd, "b1", "b2", "b3")
	targets := []string{"b1", "b2", "b3"}
	w.setConfig(fmt.Sprintf(`{"test":{"full":[%q]}}`, cmd+"; test ! -f b3.txt"))
	if res, _ := w.train(TrainOpts{Targets: targets}); res.Verdict != GateVerifyFailed {
		t.Fatalf("%+v", res)
	}
	before := runs(t, counter)
	if res, _ := w.train(TrainOpts{Targets: targets}); len(res.CacheHits) == 0 {
		t.Fatalf("same key should hit its passing prefix: %+v", res)
	}
	same := runs(t, counter)
	gitq(t, w.dir, "checkout", "-q", "b1") // a changed member head
	trainWrite(t, w, "b1-more.txt", "more")
	gitq(t, w.dir, "checkout", "-q", "main")
	res, _ := w.train(TrainOpts{Targets: targets})
	if len(res.CacheHits) != 0 {
		t.Errorf("a changed head must miss: %v", res.CacheHits)
	}
	afterHead := runs(t, counter)
	if afterHead-same < same-before {
		t.Errorf("a changed head re-verifies everything: %d runs vs %d", afterHead-same, same-before)
	}
	trainWrite(t, w, "other.txt", "x") // a changed base
	if res, _ := w.train(TrainOpts{Targets: targets}); len(res.CacheHits) != 0 {
		t.Errorf("a changed base must miss: %v", res.CacheHits)
	}
}

func TestTrainNotesFlakyCulpritAsTransient(t *testing.T) {
	seen := filepath.Join(t.TempDir(), "seen")
	// b2 fails the first time it is verified in a tree, then passes: a flaky test
	cmd := fmt.Sprintf("test ! -f b2.txt || test -f %[1]s || { touch %[1]s; exit 1; }", seen)
	w := trainWorld(t, cmd, "b1", "b2", "b3")
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2"}})
	if err != nil || res.Verdict != GateVerifyFailed || res.Culprit != "b2" {
		t.Fatalf("first train: %+v %v", res, err)
	}
	res, err = w.train(TrainOpts{Targets: []string{"b2", "b3"}}) // different key, contains b2
	if err != nil || res.Verdict != GatePass || res.Culprit != "" {
		t.Fatalf("second train: %+v %v", res, err)
	}
	if strings.Join(res.Transient, ",") != "b2" {
		t.Errorf("b2 should be noted transient: %+v", res)
	}
	if !w.onMain("b2.txt") || !w.onMain("b3.txt") {
		t.Error("the second train should land")
	}
	if c := loadTrainCache(w.dir); len(c.f.Culprits) != 0 {
		t.Errorf("the culprit record should be cleared: %+v", c.f.Culprits)
	}
}

// An empty test.full verified nothing, so a train carrying an earlier named
// culprit must not forgive it: no TRANSIENT note, and the culprit record stays.
func TestTrainNoVerifyDoesNotForgiveCulprit(t *testing.T) {
	seen := filepath.Join(t.TempDir(), "seen")
	cmd := fmt.Sprintf("test ! -f b2.txt || test -f %[1]s || { touch %[1]s; exit 1; }", seen)
	w := trainWorld(t, cmd, "b1", "b2", "b3")
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2"}})
	if err != nil || res.Verdict != GateVerifyFailed || res.Culprit != "b2" {
		t.Fatalf("first train: %+v %v", res, err)
	}
	w.setConfig(`{"test":{"full":[]}}`)
	res, err = w.train(TrainOpts{Targets: []string{"b2", "b3"}, NoVerify: true})
	if err != nil || res.Verdict != GatePass {
		t.Fatalf("no-verify train: %+v %v", res, err)
	}
	if len(res.Transient) != 0 || strings.Contains(strings.Join(res.Notes, "\n"), "TRANSIENT") {
		t.Errorf("an unverified train must not forgive a culprit: %+v", res)
	}
	if c := loadTrainCache(w.dir); len(c.f.Culprits) == 0 {
		t.Error("the culprit record must survive an unverified train")
	}
}

func TestTrainVerdictRefusal(t *testing.T) {
	w := trainWorld(t, "touch ran-verify", "b1", "b2")
	block := fmt.Errorf("FAIL recorded")
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2"}, Verdict: func(b string) error {
		if b == "b2" {
			return block
		}
		return nil
	}})
	if err != block || res.Verdict != GateVerdictBlocked || res.Culprit != "b2" {
		t.Fatalf("%+v %v", res, err)
	}
	if w.onMain("b1.txt") || w.onMain("b2.txt") {
		t.Error("a train with a FAIL-verdict member merged something")
	}
	if res, err := w.train(TrainOpts{Targets: []string{"b1", "b2"}, Verdict: func(string) error { return nil }}); err != nil || !res.OK() {
		t.Fatalf("no blocking record: %+v %v", res, err)
	}
}

// An empty test.full lands nothing: the train is refused before the scratch
// merge unless --no-verify says so.
func TestTrainRefusesEmptyTestFull(t *testing.T) {
	w := trainWorld(t, "true", "b1", "b2")
	w.setConfig(`{"test":{"full":[]}}`)
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2"}})
	if err != nil || res.Verdict != GateNoVerify || res.Changed || len(res.Landed) != 0 || w.onMain("b1.txt") {
		t.Fatalf("%+v %v", res, err)
	}
	if !strings.Contains(res.Err, "test.full") || !strings.Contains(res.Hint, "rota config set test.full") || !strings.Contains(res.Hint, "--no-verify") {
		t.Errorf("refusal must name the key and the way out: %q / %q", res.Err, res.Hint)
	}
	res, err = w.train(TrainOpts{Targets: []string{"b1", "b2"}, NoVerify: true})
	if err != nil || res.Verdict != GatePass || strings.Join(res.Landed, ",") != "b1,b2" {
		t.Fatalf("--no-verify: %+v %v", res, err)
	}
	if !strings.Contains(strings.Join(res.Notes, "\n"), "NO-VERIFY") {
		t.Errorf("result must say NO-VERIFY: %v", res.Notes)
	}
}

// A train cancelled mid-merge still deletes its scratch worktree: cleanup runs
// on a context cut loose from the cancelled one.
func TestTrainCancelledLeavesNoScratchTree(t *testing.T) {
	w := trainWorld(t, "true", "b1", "b2")
	env := w.env(false)
	real := env.WithDefaults().Git
	ctx, cancel := context.WithCancel(bg)
	defer cancel()
	env.Ctx = ctx // production wires the signal context here; the old cleanup ran on it
	env.Git = func(c context.Context, dir string, args ...string) (git.Result, error) {
		if strings.Contains(dir, "rota-train-") && len(args) > 0 && args[0] == "merge" {
			cancel()
		}
		return real(c, dir, args...)
	}
	res, err := env.Train(ctx, w.dir, TrainOpts{Base: "main", Targets: []string{"b1", "b2"}})
	if ctx.Err() == nil {
		t.Fatalf("the train never reached the scratch merge: %+v %v", res, err)
	}
	if res.Verdict == GatePass {
		t.Fatalf("a cancelled train passed: %+v", res)
	}
	if extra := w.scratchTrees(); len(extra) != 0 {
		t.Errorf("scratch worktree left behind: %v", extra)
	}
}

// orderWorld adds a 20-line shared.txt to main and gives b1 a large edit at its
// top and b2 a one-line edit at its bottom (clean merge either way); b3 touches
// only its own file.
func orderWorld(t *testing.T) *world {
	t.Helper()
	w := trainWorld(t, "true", "b1", "b2", "b3")
	trainWrite(t, w, "shared.txt", strings.TrimSuffix(strings.Repeat("-\n", 20), "\n"))
	edit := func(b, body string) {
		gitq(t, w.dir, "checkout", "-q", b)
		gitq(t, w.dir, "merge", "-q", "main", "-m", "sync")
		trainWrite(t, w, "shared.txt", body)
		gitq(t, w.dir, "checkout", "-q", "main")
	}
	edit("b1", strings.Repeat("A\n", 5)+strings.TrimSuffix(strings.Repeat("-\n", 15), "\n"))
	edit("b2", strings.TrimSuffix(strings.Repeat("-\n", 19)+"Z", "\n"))
	return w
}

func memberOrder(res TrainResult) string {
	var ts []string
	for _, m := range res.Members {
		ts = append(ts, m.Target)
	}
	return strings.Join(ts, ",")
}

func TestTrainOrdersIsolatedFirstThenSmallestDiff(t *testing.T) {
	w := orderWorld(t)
	var said []string
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2", "b3"}, Say: func(s string) { said = append(said, s) }})
	if err != nil || res.Verdict != GatePass || memberOrder(res) != "b3,b2,b1" || strings.Join(res.Landed, ",") != "b3,b2,b1" {
		t.Fatalf("%+v %v", res, err)
	}
	all := strings.Join(said, "\n")
	for _, want := range []string{"b3: no shared paths", "b2: shares 1 paths with b1", "b1: shares 1 paths with b2"} {
		if !strings.Contains(all, want) {
			t.Errorf("order report lacks %q:\n%s", want, all)
		}
	}
	if len(res.Order) != 3 {
		t.Errorf("result should carry the order lines: %v", res.Order)
	}
}

func TestTrainOrderFlagOverrides(t *testing.T) {
	w := orderWorld(t)
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2", "b3"}, Order: []string{"b1", "b3", "b2"}})
	if err != nil || res.Verdict != GatePass || strings.Join(res.Landed, ",") != "b1,b3,b2" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestTrainOrderFlagMustNameEveryMemberOnce(t *testing.T) {
	w := orderWorld(t)
	for _, order := range [][]string{{"b1", "b2"}, {"b1", "b2", "b2"}, {"b1", "b2", "b3", "b3"}, {"b1", "b2", "b9"}} {
		_, err := w.train(TrainOpts{Targets: []string{"b1", "b2", "b3"}, Order: order})
		var ee *exitcode.Error
		if !errors.As(err, &ee) || ee.Exit != exitcode.ExitUsage {
			t.Errorf("--order %v should be a usage error, got %v", order, err)
		}
		if w.onMain("b1.txt") {
			t.Errorf("--order %v landed something", order)
		}
	}
}

func TestTrainSharedPathConflictStopsBeforeLanding(t *testing.T) {
	w := trainWorld(t, "true", "b1", "b2")
	for _, b := range []string{"b1", "b2"} {
		gitq(t, w.dir, "checkout", "-q", b)
		trainWrite(t, w, "clash.txt", b)
		gitq(t, w.dir, "checkout", "-q", "main")
	}
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2"}})
	if err != nil || res.Verdict != GateOrder || res.Changed || w.onMain("b1.txt") || !strings.Contains(res.Err, "b1") || !strings.Contains(res.Err, "b2") {
		t.Fatalf("%+v %v", res, err)
	}
}

func prMembers() ([]TrainMember, []trainFoot) {
	ms := []TrainMember{
		{Target: "s9", Branch: "x/9", PR: "https://github.com/o/r/pull/9"},
		{Target: "s12", Branch: "x/12", PR: "https://github.com/o/r/pull/12"},
		{Target: "s15", Branch: "x/15", PR: "https://github.com/o/r/pull/15"},
		{Target: "s3", Branch: "x/3", PR: "https://github.com/o/r/pull/3"},
		{Target: "s7", Branch: "x/7", PR: "https://github.com/o/r/pull/7"},
	}
	foots := []trainFoot{
		{paths: []string{"a"}, lines: 10},      // #9 shares a with #12
		{paths: []string{"a"}, lines: 10},      // #12: same size as #9
		{paths: []string{"b"}, lines: 1},       // #15 isolated
		{paths: []string{"c"}, lines: 99},      // #3 isolated
		{paths: []string{"a", "z"}, lines: 40}, // #7 shares a with #9, #12
	}
	return ms, foots
}

func orderedLabels(ms []TrainMember, perm []int) string {
	var ls []string
	for _, i := range perm {
		ls = append(ls, memberLabel(ms[i]))
	}
	return strings.Join(ls, ",")
}

func TestOrderTrainSortsByPRNumberThenDiffSize(t *testing.T) {
	ms, foots := prMembers()
	perm, lines, nIso, err := orderTrain(ms, foots, nil)
	if err != nil || nIso != 2 {
		t.Fatalf("nIso=%d err=%v", nIso, err)
	}
	// isolated by PR number (#3, #15), then shared by size with the tie on #9/#12 broken by PR number.
	if got := orderedLabels(ms, perm); got != "#3,#15,#9,#12,#7" {
		t.Errorf("order = %s", got)
	}
	if lines[0] != "#9: shares 1 paths with #12, #7" {
		t.Errorf("report line: %q", lines[0])
	}
	if lines[3] != "#3: no shared paths" {
		t.Errorf("report line: %q", lines[3])
	}
}

func TestOrderTrainOverrideAcceptsPRTokens(t *testing.T) {
	ms, foots := prMembers()
	for _, tok := range [][]string{
		{"#7", "#3", "#15", "#12", "#9"},
		{"7", "3", "15", "12", "9"},
		{"s7", "s3", "s15", "s12", "s9"},
		{"x/7", "https://github.com/o/r/pull/3", "15", "#12", "s9"},
	} {
		perm, lines, nIso, err := orderTrain(ms, foots, tok)
		if err != nil || nIso != 0 {
			t.Fatalf("%v: nIso=%d err=%v", tok, nIso, err)
		}
		if got := orderedLabels(ms, perm); got != "#7,#3,#15,#12,#9" {
			t.Errorf("%v: order = %s", tok, got)
		}
		if !strings.HasSuffix(lines[0], "(order given)") {
			t.Errorf("report line lacks the override mark: %q", lines[0])
		}
	}
}

func TestOrderTrainOverrideRejectsBadTokens(t *testing.T) {
	ms, foots := prMembers()
	for _, tok := range [][]string{
		{"#7", "#3", "#15", "#12", "#99"},
		{"#7", "#3", "#15", "#12", "#7"},
		{"#7", "7", "#15", "#12", "#9"},
		{"#7", "#3"},
	} {
		if _, _, _, err := orderTrain(ms, foots, tok); err == nil {
			t.Errorf("--order %v should be rejected", tok)
		}
	}
}

// A PR a train lands leaves the review list, like one the gate merged (#1526).
func TestTrainDropsLandedQueuedPR(t *testing.T) {
	w := newWorld(t, "")
	w.setQueued()
	w.forge("body", "Closes #5\n")
	if n := len(LoadRegistryTolerant(w.dir).PRs()); n != 1 {
		t.Fatalf("setup: %d queued record(s)", n)
	}
	res, err := w.train(TrainOpts{Targets: []string{"#7"}})
	if err != nil || res.Verdict != GatePass || len(res.Landed) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if n := len(LoadRegistryTolerant(w.dir).PRs()); n != 0 {
		t.Errorf("a train-landed PR stays in review: %d record(s)", n)
	}
}

// slicePRTrain is two partial-slice PRs (#7 on w1 for issue #5, #8 on w2 for
// issue #6) the forge says close their issues, one slot per PR.
func slicePRTrain(t *testing.T) *world {
	t.Helper()
	w := newWorld(t, "")
	gitq(t, w.worker, "checkout", "-q", "-b", "w2", "main")
	os.WriteFile(filepath.Join(w.worker, "work2.txt"), []byte("work2\n"), 0o644)
	gitq(t, w.worker, "add", "work2.txt")
	gitq(t, w.worker, "commit", "-q", "-m", "work2")
	gitq(t, w.worker, "push", "-q", "origin", "w2")
	w.forge("head#8", "w2")
	w.forge("sha#8", gitq(t, w.worker, "rev-parse", "HEAD"))
	pr := func(n int) string { return fmt.Sprintf("https://github.com/o/r/pull/%d", n) }
	os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"), []byte(fmt.Sprintf(
		`{"slots":[{"name":"w1","branch":"w1","task":"#5","pr":%q},{"name":"w2","branch":"w2","task":"#6","pr":%q}]}`, pr(7), pr(8))), 0o644)
	w.forge("body", "Refs #5 #6\n")
	w.forge("issueLabels", "in-progress,"+PartialSliceLabel)
	w.forge("closingRefs", "5,6")
	return w
}

// --final-slice exempts only the members it names, by the token the train was
// given: a slot name or a PR.
func TestTrainFinalSliceExemptsOnlyTheNamedMember(t *testing.T) {
	for _, c := range []struct {
		name    string
		targets []string
		final   []string
		culprit string
	}{
		{"none named", []string{"#7", "#8"}, nil, "#7"},
		{"first by PR", []string{"#7", "#8"}, []string{"#7"}, "#8"},
		{"second by PR", []string{"#7", "#8"}, []string{"#8"}, "#7"},
		{"first by slot", []string{"w1", "w2"}, []string{"w1"}, "w2"},
		{"second by slot", []string{"w1", "w2"}, []string{"w2"}, "w1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := slicePRTrain(t)
			res, err := w.train(TrainOpts{Targets: c.targets, FinalSlice: c.final})
			if err != nil || res.Verdict != GateNotClosing || res.Culprit != c.culprit || res.Changed {
				t.Fatalf("verdict %q culprit %q (want %q), changed %v: %v", res.Verdict, res.Culprit, c.culprit, res.Changed, err)
			}
		})
	}
}

// A forge that cannot report closing references makes the gate note
// CLOSING-REFS-UNKNOWN on every stage of the train; the train says it once.
func TestTrainNotesClosingRefsUnknownOnce(t *testing.T) {
	w := newWorld(t, "")
	w.setQueued()
	w.forge("body", "Refs #5\n")
	w.forge("issueLabels", "in-progress,"+PartialSliceLabel)
	res, err := w.train(TrainOpts{Targets: []string{"#7"}})
	if err != nil || res.Verdict != GatePass {
		t.Fatalf("%+v %v", res, err)
	}
	if got := strings.Count(strings.Join(res.Notes, "\n"), "CLOSING-REFS-UNKNOWN"); got != 1 {
		t.Errorf("CLOSING-REFS-UNKNOWN notes = %d, want 1: %q", got, res.Notes)
	}
}

// planWorld is trainWorld whose verify fails if it ever runs: a plan runs no tests.
// b1 and b2 both write clash.txt (a real conflict); b3 touches only its own file.
func planWorld(t *testing.T) *world {
	t.Helper()
	w := trainWorld(t, "false", "b1", "b2", "b3")
	for _, b := range []string{"b1", "b2"} {
		gitq(t, w.dir, "checkout", "-q", b)
		trainWrite(t, w, "clash.txt", b)
		gitq(t, w.dir, "checkout", "-q", "main")
	}
	return w
}

func TestTrainPlanNamesLargestConflictFreeSubset(t *testing.T) {
	w := planWorld(t)
	before := gitq(t, w.dir, "rev-parse", "main")
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2", "b3"}, Plan: true})
	if err != nil || res.Plan == nil || res.Changed || len(res.Landed) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	p := res.Plan
	if len(p.Conflicts) != 1 || p.Conflicts[0].A != "b1" || p.Conflicts[0].B != "b2" || strings.Join(p.Conflicts[0].Paths, ",") != "clash.txt" {
		t.Errorf("conflicts: %+v", p.Conflicts)
	}
	if strings.Join(p.Landable, ",") != "b1,b3" {
		t.Errorf("landable: %v", p.Landable)
	}
	if len(p.Blocked) != 1 || p.Blocked[0].Target != "b2" || strings.Join(p.Blocked[0].By, ",") != "b1" {
		t.Errorf("blocked: %+v", p.Blocked)
	}
	if want := "rota worker train --base main --order b1,b3 b1 b3"; p.Command != want {
		t.Errorf("command %q, want %q", p.Command, want)
	}
	if after := gitq(t, w.dir, "rev-parse", "main"); after != before || w.onMain("b3.txt") {
		t.Error("a plan must push and land nothing")
	}
}

func TestTrainPlanTakesNoLandLock(t *testing.T) {
	w := planWorld(t)
	var res TrainResult
	var perr error
	lockErr := w.env(false).WithDefaults().withLandLock(bg, w.dir, "", func(bool) error { // would block a train that tried the lock
		ctx, cancel := context.WithTimeout(bg, 10*time.Second)
		defer cancel()
		res, perr = w.env(false).Train(ctx, w.dir, TrainOpts{Base: "main", Targets: []string{"b1", "b2", "b3"}, Plan: true})
		return nil
	})
	if lockErr != nil || perr != nil || res.Plan == nil {
		t.Fatalf("%+v %v %v", res, perr, lockErr)
	}
}

func TestTrainPlanWithoutConflictsLandsWholeTrain(t *testing.T) {
	w := orderWorld(t) // b1 and b2 share a path but merge cleanly
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2", "b3"}, Plan: true})
	if err != nil || res.Plan == nil {
		t.Fatalf("%+v %v", res, err)
	}
	p := res.Plan
	if len(p.Conflicts) != 0 || len(p.Blocked) != 0 || strings.Join(p.Landable, ",") != "b3,b2,b1" {
		t.Errorf("plan: %+v", p)
	}
}

func TestTrainPlanNoVerifyMirrorsRefusal(t *testing.T) {
	for _, c := range []struct {
		name     string
		config   string
		noVerify bool
		want     bool
	}{
		{"empty local", `{"test":{"full":[],"e2e":[]}}`, false, true},
		{"e2e only", `{"test":{"full":[],"e2e":["false"]}}`, false, false},
		{"full only", `{"test":{"full":["false"],"e2e":[]}}`, false, false},
		{"CI", `{"test":{"fullWhere":"ci","ciChecks":["test"],"full":[],"e2e":[]}}`, false, false},
		{"explicit override", `{"test":{"full":[],"e2e":[]}}`, true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := trainWorld(t, "false", "b1")
			w.setConfig(c.config)
			res, err := w.train(TrainOpts{Targets: []string{"b1"}, Plan: true, NoVerify: c.noVerify})
			if err != nil || res.Verdict != GatePlan || res.Plan == nil {
				t.Fatalf("%+v %v", res, err)
			}
			if res.Plan.NoVerify != c.want {
				t.Errorf("plan.noVerify = %v, want %v", res.Plan.NoVerify, c.want)
			}
			if got := strings.HasSuffix(res.Plan.Command, " --no-verify"); got != c.want {
				t.Errorf("command = %q, want --no-verify suffix: %v", res.Plan.Command, c.want)
			}
		})
	}
}

func TestTrainWithoutPlanStillStopsOnOrderConflict(t *testing.T) {
	w := planWorld(t)
	w.setConfig(`{"test":{"full":["true"]}}`)
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2", "b3"}})
	if err != nil || res.Verdict != GateOrder || res.Plan != nil || res.Changed {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestPairwiseMergeFailureIsCheckBroke(t *testing.T) {
	for _, plan := range []bool{true, false} {
		t.Run(fmt.Sprintf("plan=%v", plan), func(t *testing.T) {
			w := planWorld(t)
			if !plan {
				trainWrite(t, w, "main-only.txt", "main advanced")
			}
			e := w.env(false).WithDefaults()
			realGit := e.Git
			e.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
				if len(args) > 0 && args[0] == "merge-tree" {
					return git.Result{ExitCode: 128, Stdout: "injected failure"}, nil
				}
				return realGit(ctx, dir, args...)
			}
			before := gitq(t, w.dir, "rev-parse", "main")
			var verdict, message string
			var changed bool
			var err error
			wantMessage := "git merge-tree main b1 exited 128: injected failure"
			if plan {
				res, runErr := e.Train(bg, w.dir, TrainOpts{Base: "main", Targets: []string{"b1", "b2"}, Plan: true})
				verdict, message, changed, err = res.Verdict, res.Err, res.Changed, runErr
				wantMessage = "git merge-tree of b1 and b2 exited 128"
			} else {
				res, runErr := e.Gate(bg, w.dir, GateOpts{Base: "main", Slot: "b1", CheckOnly: true})
				verdict, message, changed, err = res.Verdict, res.Err, res.Changed, runErr
			}
			if err != nil || verdict != GateCheckBroke || changed || !strings.Contains(message, wantMessage) {
				t.Fatalf("verdict=%s changed=%v message=%q err=%v", verdict, changed, message, err)
			}
			if after := gitq(t, w.dir, "rev-parse", "main"); after != before {
				t.Errorf("main moved: %s -> %s", before, after)
			}
		})
	}
}
