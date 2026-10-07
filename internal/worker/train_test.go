package worker

import (
	"context"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/git"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
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
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2"}})
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
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2"}, Approve: func(f func() ([]string, error)) error {
		files, _ = f()
		return want
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

func TestTrainApprovalRefusalCarriesData(t *testing.T) {
	w := trainWorld(t, "true", "b1", "b2")
	want := &exitcode.Error{Exit: exitcode.ExitRefused, Message: "gate", Data: BlockData{BlockedBy: "manual gate"}}
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2"}, Approve: func(func() ([]string, error)) error { return want }})
	if res.Verdict != GateApprovalRequired {
		t.Fatalf("verdict %q", res.Verdict)
	}
	if bd, ok := exitcode.DataOf[BlockData](err); !ok || bd.BlockedBy != "manual gate" {
		t.Errorf("refusal data = %+v, %v (err %v)", bd, ok, err)
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
