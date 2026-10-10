package round

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/worker"
)

// closingForge is the fake forge plus PRClose: it records the close and flips
// the PR's state the way the real one does.
type closingForge struct {
	Forge
	remote  *fakeRemote
	closed  []int
	comment string
}

func (c *closingForge) PRClose(_ context.Context, pr int, comment string) error {
	c.closed = append(c.closed, pr)
	c.comment = comment
	c.remote.states[pr] = "closed"
	return nil
}

// pickFx is a best-of:2 issue 12: ben (the moveFx assignment) and dana are its
// two attempts. Neither has a PR until a test gives one.
type pickFx struct {
	*moveFx
	forge  *closingForge
	benBr  string
	danaBr string
	danaWT string
}

func newPickFx(t *testing.T) *pickFx {
	t.Helper()
	f := &pickFx{moveFx: newMoveFx(t)}
	f.forge = &closingForge{Forge: f.moveFx.forge.asForge(), remote: f.moveFx.forge}
	f.moveFx.forge.states = map[int]string{}
	f.env.Forge = f.forge
	f.benBr, f.danaBr = f.slot("ben").Branch(), "dana/12-second-attempt"
	f.danaWT = f.wt("dana")
	sh(t, f.danaWT, "switch", "-q", "-c", f.danaBr)
	f.hold(t, "dana", "busy", "")
	f.be.claims["12"] = "ben@1"
	if err := worker.Update(f.root, func(d *worker.Doc) {
		d.SetBestOf(worker.BestOf{Issue: "12", Round: 1, Attempts: []worker.BestOfAttempt{
			{Slot: "ben", Branch: f.benBr, ClaimID: "ben@1"},
			{Slot: "dana", Branch: f.danaBr, ClaimID: "dana@1"},
		}})
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

// hold makes dana hold issue 12 on her attempt branch in the given state and PR.
func (f *pickFx) hold(t *testing.T, slot, state, pr string) {
	t.Helper()
	if err := rawSlot(f.root, slot, func(s *jsonx.Object) {
		s.Set("task", "12")
		s.Set("state", state)
		s.Set("branch", f.danaBr)
		s.Set("claimId", "dana@1")
		if pr != "" {
			s.Set("pr", pr)
		}
	}); err != nil {
		t.Fatal(err)
	}
}

func (f *pickFx) pick(pr, reason string) (Picked, error) {
	return f.env.Pick(bg, f.root, f.be, PickOpts{ID: "12", PR: pr, Reason: reason, HolderPID: 100})
}

func (f *pickFx) bothHavePRs(t *testing.T) {
	t.Helper()
	f.finish(t, "ben", pr7)
	f.hold(t, "dana", "done", "https://github.com/o/r/pull/8")
	f.moveFx.forge.states[7], f.moveFx.forge.states[8] = "open", "open"
}

func wantBlocked(t *testing.T, err error, by string) {
	t.Helper()
	var blk *BlockedError
	if !errors.As(err, &blk) || blk.By != by {
		t.Fatalf("want blocked by %q, got %v", by, err)
	}
}

func TestPickClosesTheQueuedLoserAndRecordsThePick(t *testing.T) {
	f := newPickFx(t)
	f.finish(t, "ben", pr7)
	// dana's PR sits in the queue: her slot moved on.
	if err := worker.Update(f.root, func(d *worker.Doc) {
		d.QueuePR(worker.QueuedPR{Issue: "12", Branch: f.danaBr, PR: "https://github.com/o/r/pull/8", From: "dana", ClaimID: "dana@1", Round: 1})
		d.Slot("dana").Park(false)
	}); err != nil {
		t.Fatal(err)
	}
	f.moveFx.forge.states[7], f.moveFx.forge.states[8] = "open", "open"
	if _, err := worker.RecordBounce(f.root, "12", "", "abc"); err != nil {
		t.Fatal(err)
	}

	res, err := f.pick("#7", "cleaner diff\nsecond line")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || !res.Closed || res.Winner.Slot != "ben" || res.Winner.PR != "#7" || res.Loser.Slot != "dana" || res.Loser.PR != "#8" || res.Loser.State != AttemptOpenPR {
		t.Fatalf("%+v", res)
	}
	if len(f.forge.closed) != 1 || f.forge.closed[0] != 8 {
		t.Fatalf("closed %v", f.forge.closed)
	}
	for _, want := range []string{"#12 is best-of:2", "picked #7 (slot ben)", "Reason:\ncleaner diff\nsecond line", "Branch `" + f.danaBr + "` stays for salvage until `rota reap`."} {
		if !strings.Contains(f.forge.comment, want) {
			t.Errorf("close comment lacks %q:\n%s", want, f.forge.comment)
		}
	}
	if len(f.queued()) != 0 {
		t.Errorf("the loser's queued record stays: %+v", f.queued())
	}
	if f.be.claims["12"] != "ben@1" {
		t.Errorf("the winner's claim must stay: %v", f.be.claims)
	}
	if b := worker.LoadRegistryTolerant(f.root).BestOf("12"); b == nil || !b.Picked("#7") || b.Picked("#8") {
		t.Errorf("pick not recorded: %+v", b)
	}
	if n := worker.LoadRegistryTolerant(f.root).Bounces("12"); n != 0 {
		t.Errorf("bounces restart at the pick: %d", n)
	}
	if got := f.be.comments["12"]; len(got) == 0 || !strings.Contains(got[len(got)-1], "Best-of pick: #7 (ben) over #8 (dana)") || strings.Contains(got[len(got)-1], "second line") {
		t.Errorf("issue comment: %v", got)
	}
}

func TestPickParksAFinishedLoserSlotAndReleasesItsClaim(t *testing.T) {
	f := newPickFx(t)
	f.bothHavePRs(t)
	f.be.claims["12"] = "dana@1" // the fake keeps one claim per issue: this one is the loser's

	res, err := f.pick("7", "ben's is smaller")
	if err != nil || !res.Closed || res.Loser.Slot != "dana" {
		t.Fatalf("%+v %v", res, err)
	}
	if f.be.claims["12"] != "" {
		t.Errorf("the loser's claim must be released: %v", f.be.claims)
	}
	if s := f.slot("dana"); s.HeldID() != "" || s.State() != "idle" || s.Branch() != "park/dana" {
		t.Errorf("dana should be parked and free: %v", s)
	}
	if !f.remoteHas(t, f.danaBr) {
		t.Error("the loser's branch stays on origin")
	}
	if s := f.slot("ben"); s.HeldID() != "12" || s.PR() != pr7 {
		t.Errorf("the winner's slot is untouched: %v", s)
	}
}

func TestPickLeavesAWorkingLoserSlotAndWarns(t *testing.T) {
	f := newPickFx(t)
	f.finish(t, "ben", pr7)
	f.hold(t, "dana", "dead", "")
	res, err := f.pick("#7", "dana died")
	if err != nil {
		t.Fatal(err)
	}
	if res.Loser.State != AttemptNoPR || res.Closed || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "rota round reclaim dana") {
		t.Fatalf("%+v", res)
	}
	if f.slot("dana").HeldID() != "12" {
		t.Error("the slot is left for reclaim")
	}
}

func TestPickRefusals(t *testing.T) {
	f := newPickFx(t)
	f.bothHavePRs(t)
	snap := func() string { return worker.LoadRegistryTolerant(f.root).BestOf("12").Pick + "|" + f.be.claims["12"] }
	before := snap()

	_, err := f.pick("#9", "why")
	wantBlocked(t, err, BlockNotAttempt)
	if _, err = f.pick("#7", "  \n"); err == nil || errors.As(err, new(*BlockedError)) {
		t.Errorf("an empty reason is a usage error: %v", err)
	}
	_, err = f.env.Pick(bg, f.root, f.be, PickOpts{ID: "13", PR: "#7", Reason: "x", HolderPID: 100})
	wantBlocked(t, err, BlockNotBestOf)
	_, err = f.env.Pick(bg, f.root, f.be, PickOpts{ID: "12", PR: "#7", Reason: "x", HolderPID: 999})
	wantBlocked(t, err, BlockNoRound)
	if len(f.forge.closed) != 0 || snap() != before {
		t.Errorf("a refusal changes nothing: closed %v, %s", f.forge.closed, snap())
	}
}

func TestPickAcceptsTheRemainingPRWhenTheOtherAttemptIsGone(t *testing.T) {
	f := newPickFx(t)
	f.finish(t, "ben", pr7)
	if _, err := f.ret("dana", "gave up", nil); err != nil {
		t.Fatal(err)
	}
	res, err := f.pick("#7", "only one left")
	if err != nil {
		t.Fatal(err)
	}
	if res.Loser.State != AttemptGone || res.Closed || !res.Changed || res.Winner.Slot != "ben" {
		t.Fatalf("%+v", res)
	}
	if b := worker.LoadRegistryTolerant(f.root).BestOf("12"); b == nil || !b.Picked("#7") {
		t.Errorf("pick not recorded: %+v", b)
	}
}

func TestPickRefusesWhileTheOtherAttemptIsStillBuilding(t *testing.T) {
	f := newPickFx(t)
	f.finish(t, "ben", pr7)
	for _, st := range []string{"busy", ""} {
		f.hold(t, "dana", st, "")
		_, err := f.pick("#7", "why")
		wantBlocked(t, err, BlockAttemptRunning)
		if b := worker.LoadRegistryTolerant(f.root).BestOf("12"); b.Pick != "" {
			t.Errorf("state %q: nothing may be recorded", st)
		}
	}
}

func TestPickRepeatIsANoOpAndAnotherPRIsRefused(t *testing.T) {
	f := newPickFx(t)
	f.bothHavePRs(t)
	if _, err := f.pick("#7", "first"); err != nil {
		t.Fatal(err)
	}
	f.forge.closed = nil
	res, err := f.pick("https://github.com/o/r/pull/7", "again")
	if err != nil || res.Changed || res.Closed || len(f.forge.closed) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	_, err = f.pick("#8", "changed my mind")
	wantBlocked(t, err, BlockPicked)
}

// An adopted loser's checkout is not rota's to park: the pick closes its PR,
// warns, and leaves the slot for `rota worker pool reap`.
func TestPickLeavesAnExternalLoserSlotAndWarns(t *testing.T) {
	f := newPickFx(t)
	f.bothHavePRs(t)
	if err := rawSlot(f.root, "dana", func(s *jsonx.Object) { s.Set("kind", "external") }); err != nil {
		t.Fatal(err)
	}
	res, err := f.pick("7", "ben's is smaller")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "adopted external slot") || !strings.Contains(res.Warnings[0], "rota worker pool reap dana") {
		t.Fatalf("want the release warning: %+v", res)
	}
	if s := f.slot("dana"); s.Branch() != f.danaBr || s.HeldID() != "12" {
		t.Errorf("an adopted slot is never parked: %v", s)
	}
	if got := gittest.Run(t, f.danaWT, "symbolic-ref", "--short", "HEAD"); got != f.danaBr {
		t.Errorf("worktree moved to %s", got)
	}
}
