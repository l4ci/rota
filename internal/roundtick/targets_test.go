package roundtick

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/l4ci/rota/internal/postgate"
	"github.com/l4ci/rota/internal/worker"
)

func TestTickUsesTargetFactsAndRawGateRun(t *testing.T) {
	f := &fake{slots: []Slot{{Name: "ben", State: "done", PR: "#7"}}}
	e := f.env()
	e.Base, e.BaseOverride = "main", "cycle"
	e.Target = func(string) (worker.GateTarget, error) {
		t.Fatal("an explicit base needs no registry lookup")
		return worker.GateTarget{}, nil
	}
	e.RunGate = func(_ context.Context, target, base string) GateRun {
		if target != "ben" || base != "cycle" {
			t.Errorf("gate %q into %q, want ben into cycle", target, base)
		}
		return GateRun{Result: worker.GateResult{Verdict: worker.GateBestOfUnpicked, Err: "pick an attempt\nmore"}, Refused: true}
	}
	r, err := Run(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	want := []Item{{Kind: "gate-failed", Target: "ben", Why: "best-of-unpicked: pick an attempt"}}
	if len(r.Did) != 0 || len(r.Held) != 0 || !reflect.DeepEqual(r.NeedsYou, want) {
		t.Fatalf("did=%+v held=%v needsYou=%+v, want no merge or hold and %+v", r.Did, r.Held, r.NeedsYou, want)
	}
}

func TestTickChoosesTargetBases(t *testing.T) {
	for _, tc := range []struct {
		name, target, override, recorded, want string
		err                                    error
	}{
		{name: "slot recorded base", target: "ben", recorded: "cycle", want: "cycle"},
		{name: "queued PR recorded base", target: "#8", recorded: "release", want: "release"},
		{name: "override", target: "ben", override: "explicit", recorded: "cycle", want: "explicit"},
		{name: "empty recorded base", target: "ben", want: "main"},
		{name: "registry unreadable", target: "ben", err: errors.New("corrupt registry"), want: "main"},
		{name: "target missing", target: "#8", err: errors.New("unknown target"), want: "main"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{}
			if tc.target == "ben" {
				f.slots = []Slot{{Name: "ben", State: "done", PR: "#7"}}
			} else {
				f.queued = []string{tc.target}
			}
			e := f.env()
			e.BaseOverride = tc.override
			e.Target = func(target string) (worker.GateTarget, error) {
				if target != tc.target || tc.override != "" {
					t.Fatalf("unexpected target lookup %q", target)
				}
				return worker.GateTarget{Base: tc.recorded}, tc.err
			}
			r, err := Run(context.Background(), e)
			want := []Action{{Action: "merge", Target: tc.target, Detail: "into " + tc.want}}
			if err != nil || !reflect.DeepEqual(r.Did, want) || len(r.NeedsYou) != 0 {
				t.Fatalf("err=%v result=%+v, want did=%+v", err, r, want)
			}
		})
	}
}

func TestTickInterpretsGateRuns(t *testing.T) {
	bookErr := errors.New("tracker down\nretry later")
	for _, tc := range []struct {
		name   string
		run    GateRun
		landed bool
		why    string
		say    []string
	}{
		{name: "pass with bookkeeping failure", run: GateRun{Result: worker.GateResult{Verdict: worker.GatePass}, BookErr: bookErr}, landed: true, say: []string{"BOUNCE-COUNT ben — tracker down\nretry later"}},
		{name: "fresh", run: GateRun{Result: worker.GateResult{Verdict: worker.GateFresh}}, landed: true},
		{name: "failed check", run: GateRun{Result: worker.GateResult{Verdict: worker.GateVerifyFailed, Err: "tests red\nmore"}, BookErr: bookErr}, why: "verify-failed: tests red", say: []string{"BOUNCE-COUNT ben — tracker down\nretry later"}},
		{name: "refusal", run: GateRun{Result: worker.GateResult{Verdict: worker.GateBestOfUnpicked, Err: "result detail"}, Err: errors.New("choose one\nmore"), Refused: true, BookErr: bookErr}, why: "best-of-unpicked: choose one"},
		{name: "unexpected error discards pass", run: GateRun{Result: worker.GateResult{Verdict: worker.GatePass, Err: "result detail"}, Err: errors.New("git failed\nmore"), BookErr: bookErr}, why: "gate-error: git failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{slots: []Slot{{Name: "ben", State: "done", PR: "#7"}}}
			f.gateOut = func(string) GateRun { return tc.run }
			e := f.env()
			var said []string
			e.Say = func(line string) { said = append(said, line) }
			r, err := Run(context.Background(), e)
			if err != nil {
				t.Fatal(err)
			}
			var wantDid []Action
			var wantNeeds []Item
			if tc.landed {
				wantDid = []Action{{Action: "merge", Target: "ben", Detail: "into main"}}
			} else {
				wantNeeds = []Item{{Kind: "gate-failed", Target: "ben", Why: tc.why}}
			}
			if !slices.Equal(r.Did, wantDid) || !slices.Equal(r.NeedsYou, wantNeeds) || !slices.Equal(said, tc.say) {
				t.Fatalf("did=%+v needsYou=%+v say=%q, want %+v %+v %q", r.Did, r.NeedsYou, said, wantDid, wantNeeds, tc.say)
			}
		})
	}
}

func TestTickInterpretsTrainRuns(t *testing.T) {
	book := []postgate.Failure{{Target: "ben", Err: errors.New("book one\nmore")}, {Target: "dana", Err: errors.New("book two")}}
	for _, tc := range []struct {
		name string
		run  TrainRun
		did  []Action
		need []Item
		say  []string
	}{
		{
			name: "whole train pass",
			run:  TrainRun{Result: worker.TrainResult{Verdict: worker.GatePass}, Book: book},
			did:  []Action{{Action: "merge", Target: "ben", Detail: "train into main"}, {Action: "merge", Target: "dana", Detail: "train into main"}},
			say:  []string{"BOUNCE-COUNT ben — book one\nmore", "BOUNCE-COUNT dana — book two"},
		},
		{
			name: "partial landing before refusal",
			run:  TrainRun{Result: worker.TrainResult{Verdict: worker.GateBestOfUnpicked, Culprit: "dana", Err: "result detail", Members: []worker.TrainMember{{Target: "ben", Landed: true}}}, Err: errors.New("choose one\nmore"), Refused: true, Book: book},
			did:  []Action{{Action: "merge", Target: "ben", Detail: "train into main"}},
			need: []Item{{Kind: "gate-failed", Target: "dana", Why: "best-of-unpicked: choose one"}},
		},
		{
			name: "failed check with partial landing",
			run:  TrainRun{Result: worker.TrainResult{Verdict: worker.GateVerifyFailed, Culprit: "dana", Err: "tests red\nmore", Members: []worker.TrainMember{{Target: "ben", Landed: true}}}, Book: book},
			did:  []Action{{Action: "merge", Target: "ben", Detail: "train into main"}},
			need: []Item{{Kind: "gate-failed", Target: "dana", Why: "verify-failed: tests red"}},
			say:  []string{"BOUNCE-COUNT ben — book one\nmore", "BOUNCE-COUNT dana — book two"},
		},
		{
			name: "unexpected error discards members and culprit",
			run:  TrainRun{Result: worker.TrainResult{Verdict: worker.GatePass, Culprit: "dana", Members: []worker.TrainMember{{Target: "ben", Landed: true}}}, Err: errors.New("git failed\nmore"), Book: book},
			need: []Item{{Kind: "gate-failed", Target: "ben", Why: "train-error: git failed"}, {Kind: "gate-failed", Target: "dana", Why: "train-error: git failed"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{slots: []Slot{{Name: "ben", State: "done", PR: "#7"}, {Name: "dana", State: "done", PR: "#8"}}}
			f.trainOut = func([]string) TrainRun { return tc.run }
			e := f.env()
			var said []string
			e.Say = func(line string) { said = append(said, line) }
			r, err := Run(context.Background(), e)
			if err != nil || !slices.Equal(r.Did, tc.did) || !slices.Equal(r.NeedsYou, tc.need) || !slices.Equal(said, tc.say) {
				t.Fatalf("err=%v did=%+v needsYou=%+v say=%q, want %+v %+v %q", err, r.Did, r.NeedsYou, said, tc.did, tc.need, tc.say)
			}
		})
	}
}
