package postgate

import (
	"errors"
	"reflect"
	"testing"

	"github.com/l4ci/rota/internal/worker"
)

// fakeBook records the bookkeeping calls, in memory.
type fakeBook struct {
	calls []string
	posts []post
	// failOn names a call that returns an error.
	failOn string
}

type post struct{ branch, pr, head, why string }

func (b *fakeBook) rec(name, issue string) error {
	b.calls = append(b.calls, name+":"+issue)
	if b.failOn == name {
		return errors.New(name + " failed")
	}
	return nil
}

func (b *fakeBook) ClearItemStart(_, issue string) error { return b.rec("start", issue) }
func (b *fakeBook) ClearBestOf(_, issue string) error    { return b.rec("bestof", issue) }
func (b *fakeBook) ClearBounces(_, issue string) error   { return b.rec("bounces", issue) }

func (b *fakeBook) PostReviewBounce(_, branch, pr, head, why string) error {
	b.posts = append(b.posts, post{branch, pr, head, why})
	return nil
}

func landedCalls(issue string) []string {
	return []string{"start:" + issue, "bestof:" + issue, "bounces:" + issue}
}

// A stale merge and a provenance failure are repaired by the worker: they clear
// nothing and park nothing (#2072). They only tell a support worker on its PR.
func TestStaleAndProvenanceFailClearNothingAndPostOnce(t *testing.T) {
	for _, v := range []string{worker.GateStale, worker.GateProvenanceFail} {
		b := &fakeBook{}
		res := worker.GateResult{Verdict: v, Slot: "ext-1", SHA: "a1b2c3d", Branch: "mac-ben/7-thing", PR: "https://github.com/o/r/pull/70", Err: v + ": fix it"}
		if err := Gate(b, "root", "7", res); err != nil {
			t.Fatalf("%s: %v", v, err)
		}
		if len(b.calls) != 0 {
			t.Errorf("%s cleared %v", v, b.calls)
		}
		want := []post{{"mac-ben/7-thing", "https://github.com/o/r/pull/70", "a1b2c3d", v + ": fix it"}}
		if !reflect.DeepEqual(b.posts, want) {
			t.Errorf("%s posts %+v, want %+v", v, b.posts, want)
		}
	}
}

func TestGatePassEndsTheItemClockBestOfAndBounces(t *testing.T) {
	b := &fakeBook{}
	// A verdict nobody bounces or lands on keeps the item running.
	if err := Gate(b, "root", "12", worker.GateResult{Verdict: "other"}); err != nil || len(b.calls) != 0 || len(b.posts) != 0 {
		t.Fatalf("other verdict: err %v calls %v posts %v", err, b.calls, b.posts)
	}
	if err := Gate(b, "root", "12", worker.GateResult{Verdict: worker.GatePass}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(b.calls, landedCalls("12")) {
		t.Errorf("calls %v, want %v", b.calls, landedCalls("12"))
	}
	if len(b.posts) != 0 {
		t.Errorf("a pass posted %+v", b.posts)
	}
}

func TestGateWithoutIssueDoesNothing(t *testing.T) {
	b := &fakeBook{}
	if err := Gate(b, "root", "", worker.GateResult{Verdict: worker.GatePass}); err != nil || len(b.calls) != 0 {
		t.Fatalf("err %v calls %v", err, b.calls)
	}
}

func TestLandedStopsAtTheFirstFailure(t *testing.T) {
	b := &fakeBook{failOn: "bestof"}
	if err := Gate(b, "root", "12", worker.GateResult{Verdict: worker.GatePass}); err == nil {
		t.Fatal("want the bookkeeping error")
	}
	if want := []string{"start:12", "bestof:12"}; !reflect.DeepEqual(b.calls, want) {
		t.Errorf("calls %v, want %v", b.calls, want)
	}
}

func TestTrainLandsMembersAndNotifiesTheCulprit(t *testing.T) {
	b := &fakeBook{}
	r := worker.TrainResult{
		Verdict: worker.GateStale,
		Culprit: "kit",
		Members: []worker.TrainMember{
			{Target: "good", Landed: true},
			{Target: "kit", Branch: "mac-kit/2-x", PR: "https://github.com/o/r/pull/9"},
			{Target: "noissue", Landed: true},
		},
	}
	fails := Train(b, &worker.RoundMemo{}, "root", map[string]string{"good": "1", "kit": "2"}, nil, r)
	if len(fails) != 0 {
		t.Fatalf("failures %+v", fails)
	}
	if !reflect.DeepEqual(b.calls, landedCalls("1")) {
		t.Errorf("calls %v, want only the landed member with an item", b.calls)
	}
	if len(b.posts) != 1 || b.posts[0].branch != "mac-kit/2-x" {
		t.Errorf("posts %+v, want one for the culprit's branch", b.posts)
	}
}

func TestTrainReportsFailuresPerTarget(t *testing.T) {
	b := &fakeBook{failOn: "start"}
	r := worker.TrainResult{Members: []worker.TrainMember{{Target: "a", Landed: true}, {Target: "b", Landed: true}}}
	fails := Train(b, &worker.RoundMemo{}, "root", map[string]string{"a": "1", "b": "2"}, nil, r)
	if len(fails) != 2 || fails[0].Target != "a" || fails[1].Target != "b" {
		t.Errorf("failures %+v", fails)
	}
}

func TestTrainNotifiesACulpritKnownOnlyFromTheRegistry(t *testing.T) {
	b := &fakeBook{}
	r := worker.TrainResult{Verdict: worker.GateStale, Culprit: "kit"}
	targets := map[string]worker.GateTarget{"kit": {Name: "kit", Branch: "mac-kit/2-x", PR: "https://github.com/o/r/pull/9"}}
	fails := Train(b, &worker.RoundMemo{}, "root", map[string]string{"kit": "2"}, targets, r)
	if len(fails) != 0 {
		t.Fatalf("failures %+v", fails)
	}
	if len(b.posts) != 1 || b.posts[0].branch != "mac-kit/2-x" || b.posts[0].pr != "https://github.com/o/r/pull/9" {
		t.Errorf("posts %+v, want one for the registry's branch and PR", b.posts)
	}
}
