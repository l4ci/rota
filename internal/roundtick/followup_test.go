package roundtick

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/round"
)

// fu wires a follow-up step onto the fake: list is what waits, and a start
// marks the receiving slot working on the issue, as the transfer does.
type fu struct {
	list    []FollowUp
	reserve []string
	size    int
	manual  bool
	started []string
	refuse  map[string]error
}

func (x *fu) wire(f *fake, e *Env) {
	e.FollowUp = FollowUpSources{
		List:    func(context.Context) ([]FollowUp, error) { return x.list, nil },
		Auto:    !x.manual,
		Reserve: func() []string { return x.reserve },
		Size:    x.size,
		Start: func(_ context.Context, u FollowUp, slot string, reserve bool) error {
			if err := x.refuse[u.Issue]; err != nil {
				return err
			}
			tag := ""
			if reserve {
				tag = " reserve"
			}
			x.started = append(x.started, u.Issue+"->"+slot+tag)
			for i := range f.slots {
				if f.slots[i].Name == slot {
					f.slots[i].State, f.slots[i].Issue = "working", u.Issue
				}
			}
			return nil
		},
	}
}

func needs(r Result, kind string) []string {
	var out []string
	for _, it := range r.NeedsYou {
		if it.Kind == kind {
			out = append(out, it.Target+": "+it.Why)
		}
	}
	return out
}

// AC-2: a person's follow-up takes the idle active slot before a ready
// candidate does.
func TestFollowUpBeforeBacklog(t *testing.T) {
	f := &fake{
		slots: []Slot{{Name: "ben", State: "idle"}, {Name: "dana", State: "working", Issue: "#3"}},
		cands: []round.Candidate{cand("#5", true)},
	}
	x := &fu{list: []FollowUp{{Issue: "#7", PR: "#70", Who: "alice"}}}
	e := f.env()
	e.Active = func() []string { return []string{"ben", "dana"} }
	x.wire(f, &e)
	r, err := Run(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"#7->ben"}; !reflect.DeepEqual(x.started, want) {
		t.Fatalf("started %v, want %v", x.started, want)
	}
	if len(f.assigned) != 0 {
		t.Fatalf("assigned %v, want none: the follow-up took the only idle slot", f.assigned)
	}
	if !strings.Contains(strings.Join(f.audit, ","), "followup #7") {
		t.Fatalf("audit %v, want the follow-up start", f.audit)
	}
	if got := needs(r, "followup"); len(got) != 0 {
		t.Fatalf("followup items %v, want none", got)
	}
}

// AC-2: the follow-up and the backlog share the per-tick cap; backlog gets
// what the follow-ups leave.
func TestFollowUpSharesTheTickCap(t *testing.T) {
	f := &fake{
		slots: []Slot{{Name: "ben", State: "idle"}, {Name: "dana", State: "idle"}, {Name: "nia", State: "idle"}},
		cands: []round.Candidate{cand("#5", true), cand("#6", true)},
	}
	x := &fu{list: []FollowUp{{Issue: "#7", PR: "#70"}}}
	e := f.env()
	e.Cap = 2
	x.wire(f, &e)
	if _, err := Run(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if want := []string{"#7->ben"}; !reflect.DeepEqual(x.started, want) {
		t.Fatalf("started %v, want %v", x.started, want)
	}
	if want := []string{"#5"}; !reflect.DeepEqual(f.assigned, want) {
		t.Fatalf("assigned %v, want %v: the cap of 2 leaves one assign", f.assigned, want)
	}
}

// AC-3: with every active slot busy, an opted-in reserve slot takes the
// follow-up; backlog never goes there.
func TestFollowUpReserveWhenActiveFull(t *testing.T) {
	f := &fake{
		slots: []Slot{{Name: "ben", State: "working", Issue: "#3"}, {Name: "kit", State: "idle"}},
		cands: []round.Candidate{cand("#5", true)},
	}
	x := &fu{list: []FollowUp{{Issue: "#7", PR: "#70"}}, reserve: []string{"kit"}, size: 1}
	e := f.env()
	e.Active = func() []string { return []string{"ben"} }
	x.wire(f, &e)
	r, err := Run(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"#7->kit reserve"}; !reflect.DeepEqual(x.started, want) {
		t.Fatalf("started %v, want %v", x.started, want)
	}
	if len(f.assigned) != 0 {
		t.Fatalf("assigned %v: backlog must never take a reserve slot", f.assigned)
	}
	if got := needs(r, "followup"); len(got) != 0 {
		t.Fatalf("followup items %v, want none", got)
	}
	// A reserve slot with nothing waiting stays empty however much backlog is ready.
	f = &fake{
		slots: []Slot{{Name: "ben", State: "working", Issue: "#3"}, {Name: "kit", State: "idle"}},
		cands: []round.Candidate{cand("#5", true)},
	}
	x = &fu{reserve: []string{"kit"}, size: 1}
	e = f.env()
	e.Active = func() []string { return []string{"ben"} }
	x.wire(f, &e)
	if _, err := Run(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if len(f.assigned) != 0 || len(x.started) != 0 {
		t.Fatalf("assigned %v started %v, want nothing", f.assigned, x.started)
	}
}

// AC-3, AC-6: two follow-ups at once and one reserve slot: exactly one starts,
// the other stays pending and says the reserve is busy.
func TestFollowUpReserveIsBounded(t *testing.T) {
	f := &fake{slots: []Slot{{Name: "ben", State: "working", Issue: "#3"}, {Name: "kit", State: "idle"}, {Name: "nia", State: "idle"}}}
	x := &fu{list: []FollowUp{{Issue: "#7", PR: "#70"}, {Issue: "#8", PR: "#80"}}, reserve: []string{"kit"}, size: 1}
	e := f.env()
	e.Active = func() []string { return []string{"ben"} }
	x.wire(f, &e)
	r, err := Run(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"#7->kit reserve"}; !reflect.DeepEqual(x.started, want) {
		t.Fatalf("started %v, want %v (nia is outside the round and the reserve)", x.started, want)
	}
	got := needs(r, "followup")
	if len(got) != 1 || !strings.HasPrefix(got[0], "#8: ") || !strings.Contains(got[0], "reserve") {
		t.Fatalf("followup items %v, want #8 pending on the busy reserve", got)
	}
}

// AC-5: a reserve slot whose follow-up is done (its PR waits) takes the next
// one; the transfer queues the finished PR.
func TestFollowUpReuseDoneReserveSlot(t *testing.T) {
	f := &fake{slots: []Slot{{Name: "ben", State: "working", Issue: "#3"}, {Name: "kit", State: "done", Issue: "#7", PR: "#71"}}}
	x := &fu{list: []FollowUp{{Issue: "#8", PR: "#80"}}, reserve: []string{"kit"}, size: 1}
	e := f.env()
	e.Active = func() []string { return []string{"ben"} }
	e.MergeApproval = "" // no landing in this tick: the slot stays done
	x.wire(f, &e)
	if _, err := Run(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if want := []string{"#8->kit reserve"}; !reflect.DeepEqual(x.started, want) {
		t.Fatalf("started %v, want %v", x.started, want)
	}
}

// AC-4: a follow-up that cannot start is reported with the limiting resource.
func TestFollowUpPendingReasons(t *testing.T) {
	busy := []Slot{{Name: "ben", State: "working", Issue: "#3"}}
	cases := []struct {
		name  string
		slots []Slot
		x     fu
		capd  string
		want  string
	}{
		{"reserve off", busy, fu{}, "", "round.followupReserve is 0"},
		{"roster short", busy, fu{size: 2}, "", "roster"},
		{"manual loop", []Slot{{Name: "ben", State: "idle"}}, fu{manual: true}, "", "round.reviewLoop is manual"},
		{"accounts cooling", []Slot{{Name: "ben", State: "idle"}}, fu{}, "every account is cooling down", "every account is cooling down"},
		{"refused", []Slot{{Name: "ben", State: "idle"}}, fu{refuse: map[string]error{"#7": &round.BlockedError{By: round.BlockOverlap, Msg: "the receiver guard said no"}}}, "", "the receiver guard said no"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fake{slots: append([]Slot(nil), c.slots...)}
			x := c.x
			x.list = []FollowUp{{Issue: "#7", PR: "#70"}}
			e := f.env()
			e.Active = func() []string { return []string{"ben"} }
			if c.capd != "" {
				e.Capped = func(context.Context) string { return c.capd }
			}
			x.wire(f, &e)
			r, err := Run(context.Background(), e)
			if err != nil {
				t.Fatal(err)
			}
			if len(x.started) != 0 {
				t.Fatalf("started %v, want none", x.started)
			}
			got := needs(r, "followup")
			if len(got) != 1 || !strings.Contains(got[0], c.want) {
				t.Fatalf("followup items %v, want one naming %q", got, c.want)
			}
		})
	}
}

// AC-6: a support machine starts no follow-up: the queued PRs are main's.
func TestFollowUpSupportStartsNothing(t *testing.T) {
	f := &fake{slots: []Slot{{Name: "ben", State: "idle"}}}
	x := &fu{list: []FollowUp{{Issue: "#7", PR: "#70"}}}
	e := f.env()
	e.Role = "support"
	x.wire(f, &e)
	if _, err := Run(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if len(x.started) != 0 {
		t.Fatalf("started %v on a support machine", x.started)
	}
}

// #2579: observation proposes the follow-up start without calling Start, and
// backlog does not propose the slot it would take.
func TestFollowUpObserveProposesOnly(t *testing.T) {
	f := &fake{
		slots: []Slot{{Name: "ben", State: "idle"}, {Name: "dana", State: "working", Issue: "#3"}},
		cands: []round.Candidate{cand("#5", true)},
	}
	x := &fu{list: []FollowUp{{Issue: "#7", PR: "#70", Who: "alice"}}}
	e := f.env()
	e.Active = func() []string { return []string{"ben", "dana"} }
	e.Observe = true
	x.wire(f, &e)
	r, err := Run(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if len(x.started) != 0 || len(f.assigned) != 0 {
		t.Fatalf("observation started %v assigned %v", x.started, f.assigned)
	}
	var got []string
	for _, p := range r.Proposed {
		got = append(got, p.Action.Action+" "+p.Target)
	}
	if strings.Join(got, ",") != "followup #7" {
		t.Fatalf("proposed %v, want only the follow-up on the one idle slot", got)
	}
}
