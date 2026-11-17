package postgate

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/worker"
)

// fakeBook records the bookkeeping calls, in memory.
type fakeBook struct {
	calls []string
	posts []post
	// failOn names a call that returns an error.
	failOn     string
	targets    map[string]worker.GateTarget
	targetsErr error
	origin     bool
	releases   []string
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

func (b *fakeBook) Targets(root string, args []string) (map[string]worker.GateTarget, error) {
	b.calls = append(b.calls, "targets:"+strings.Join(args, ","))
	return b.targets, b.targetsErr
}

func (b *fakeBook) ReleaseSlice(_, issue, slot string) error {
	b.releases = append(b.releases, issue+"@"+slot)
	return nil
}

func (b *fakeBook) HasOrigin(context.Context, string) bool { return b.origin }

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

// A landed integration PR ends every member issue, though the gate's own
// target (the integration PR) has none.
func TestGateIntegrationLandsMembers(t *testing.T) {
	b := &fakeBook{}
	if err := Gate(b, "root", "", worker.GateResult{Verdict: worker.GatePass, IntegrationMembers: []string{"5", "6"}}); err != nil {
		t.Fatal(err)
	}
	if want := append(landedCalls("5"), landedCalls("6")...); !reflect.DeepEqual(b.calls, want) {
		t.Errorf("calls %v, want %v", b.calls, want)
	}
	// A retry after the forge merged it ends them too.
	b = &fakeBook{}
	if err := Gate(b, "root", "", worker.GateResult{Verdict: worker.GateAlreadyMerged, IntegrationMembers: []string{"5"}}); err != nil || !reflect.DeepEqual(b.calls, landedCalls("5")) {
		t.Fatalf("already-merged retry: %v %v", err, b.calls)
	}
	// A refused integration landing ends nothing.
	b = &fakeBook{}
	if err := Gate(b, "root", "", worker.GateResult{Verdict: worker.GateStale, IntegrationMembers: []string{"5"}}); err != nil || len(b.calls) != 0 {
		t.Fatalf("refused landing: %v %v", err, b.calls)
	}
}

// A landed PR gives its slot's claim up through the book (#3629): a gate by
// the result's slot, a train member by its target's slot, and a failed or
// bounced gate releases nothing.
func TestLandedPRReleasesTheSlotsSliceClaim(t *testing.T) {
	b := &fakeBook{}
	if err := Gate(b, "root", "7", worker.GateResult{Verdict: worker.GatePass, Slot: "ben"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(b.releases, []string{"7@ben"}) {
		t.Errorf("gate release = %v", b.releases)
	}

	b = &fakeBook{}
	r := worker.TrainResult{Members: []worker.TrainMember{{Target: "t1", Landed: true}, {Target: "t2"}}}
	fails := Train(b, &worker.RoundMemo{}, "root", map[string]string{"t1": "8", "t2": "9"},
		map[string]worker.GateTarget{"t1": {Name: "dana"}, "t2": {Name: "kit"}}, r)
	if len(fails) != 0 || !reflect.DeepEqual(b.releases, []string{"8@dana"}) {
		t.Errorf("train release = %v, fails %v", b.releases, fails)
	}

	b = &fakeBook{}
	_ = Gate(b, "root", "7", worker.GateResult{Verdict: worker.GateStale, Slot: "ben"})
	if len(b.releases) != 0 {
		t.Errorf("a stale gate released %v", b.releases)
	}
}
