package postgate

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/landing"
)

// fakeBook records the bookkeeping calls, in memory.
type fakeBook struct {
	calls []string
	posts []post
	// failOn names a call that returns an error.
	failOn     string
	targets    map[string]GateTarget
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

func (b *fakeBook) Targets(root string, args []string) (map[string]GateTarget, error) {
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
	for _, v := range []string{landing.GateStale, landing.GateProvenanceFail} {
		b := &fakeBook{}
		res := landing.GateResult{Verdict: v, Slot: "ext-1", SHA: "a1b2c3d", Branch: "mac-ben/7-thing", PR: "https://github.com/o/r/pull/70", Err: v + ": fix it"}
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
	if err := Gate(b, "root", "12", landing.GateResult{Verdict: "other"}); err != nil || len(b.calls) != 0 || len(b.posts) != 0 {
		t.Fatalf("other verdict: err %v calls %v posts %v", err, b.calls, b.posts)
	}
	if err := Gate(b, "root", "12", landing.GateResult{Verdict: landing.GatePass}); err != nil {
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
	if err := Gate(b, "root", "", landing.GateResult{Verdict: landing.GatePass}); err != nil || len(b.calls) != 0 {
		t.Fatalf("err %v calls %v", err, b.calls)
	}
}

func TestLandedStopsAtTheFirstFailure(t *testing.T) {
	b := &fakeBook{failOn: "bestof"}
	if err := Gate(b, "root", "12", landing.GateResult{Verdict: landing.GatePass}); err == nil {
		t.Fatal("want the bookkeeping error")
	}
	if want := []string{"start:12", "bestof:12"}; !reflect.DeepEqual(b.calls, want) {
		t.Errorf("calls %v, want %v", b.calls, want)
	}
}

// A landed integration PR ends every member issue, though the gate's own
// target (the integration PR) has none.
func TestGateIntegrationLandsMembers(t *testing.T) {
	b := &fakeBook{}
	if err := Gate(b, "root", "", landing.GateResult{Verdict: landing.GatePass, Slot: "#42", TargetSlot: "ben", IntegrationMembers: []string{"5", "6"}}); err != nil {
		t.Fatal(err)
	}
	if want := append(landedCalls("5"), landedCalls("6")...); !reflect.DeepEqual(b.calls, want) {
		t.Errorf("calls %v, want %v", b.calls, want)
	}
	if !reflect.DeepEqual(b.releases, []string{"5@ben", "6@ben"}) {
		t.Errorf("integration release = %v", b.releases)
	}
	// A retry after the forge merged it ends them too.
	b = &fakeBook{}
	if err := Gate(b, "root", "", landing.GateResult{Verdict: landing.GateAlreadyMerged, Slot: "#42", TargetSlot: "ben", IntegrationMembers: []string{"5"}}); err != nil || !reflect.DeepEqual(b.calls, landedCalls("5")) {
		t.Fatalf("already-merged retry: %v %v", err, b.calls)
	}
	if !reflect.DeepEqual(b.releases, []string{"5@ben"}) {
		t.Errorf("integration retry release = %v", b.releases)
	}
	// A refused integration landing ends nothing.
	b = &fakeBook{}
	if err := Gate(b, "root", "", landing.GateResult{Verdict: landing.GateStale, Slot: "#42", TargetSlot: "ben", IntegrationMembers: []string{"5"}}); err != nil || len(b.calls) != 0 || len(b.releases) != 0 {
		t.Fatalf("refused landing: %v %v", err, b.calls)
	}
}

// A merged-remotely gate that changed the base landed the PR (#4305).
func TestGateMergedRemotelyEndsTheItem(t *testing.T) {
	b := &fakeBook{}
	if err := Gate(b, "root", "5", landing.GateResult{Verdict: landing.GateMergedRemotely, Changed: true, TargetSlot: "ben"}); err != nil || !reflect.DeepEqual(b.calls, landedCalls("5")) {
		t.Fatalf("%v %v", err, b.calls)
	}
}
