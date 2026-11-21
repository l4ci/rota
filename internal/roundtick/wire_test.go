package roundtick

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/postgate"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// fakeWorld is a World over in-memory values. Its registry holds one done slot
// with PR #11; prs answers PRView.
type fakeWorld struct {
	observing bool
	root      string
	info      tracker.PRInfo
	waiting   []ReadyPR
	waits     int
	gated     []string
	// repairs is what a read-only Reconcile reports as eligible.
	repairs []round.Finding
	// changed counts the calls that would change the world.
	assigns, audits, landings int
	cands                     []round.Candidate
	reviewer                  Reviewer
	followUp                  FollowUpSources
	// auditReason is the due test-audit suggestion; auditErr fails its read.
	auditReason []string
	auditErr    error
}

func (w *fakeWorld) Observing() bool { return w.observing }

func (w *fakeWorld) reg() worker.Registry {
	r, err := worker.LoadRegistry(w.root)
	if err != nil {
		panic(err)
	}
	return r
}
func (w *fakeWorld) TolerantRegistry() worker.Registry                   { return w.reg() }
func (w *fakeWorld) Registry() (worker.Registry, error)                  { return w.reg(), nil }
func (w *fakeWorld) Forge(context.Context) (PRViewer, error)             { return w, nil }
func (w *fakeWorld) PRView(context.Context, int) (tracker.PRInfo, error) { return w.info, nil }
func (w *fakeWorld) Waiting() []ReadyPR                                  { w.waits++; return w.waiting }
func (w *fakeWorld) Reconcile(context.Context) (round.Outcome, error) {
	return round.Outcome{Proposed: w.repairs}, nil
}
func (w *fakeWorld) Root() string { return w.root }
func (w *fakeWorld) Landing(_ context.Context, req postgate.Request) (postgate.Env, postgate.Request, error) {
	w.landings++
	if req.Gate != nil {
		w.gated = append(w.gated, req.Gate.Base+":"+req.Gate.Slot)
	}
	return worker.Env{Config: worker.NoConfig}.Landing(nil), req, nil
}
func (w *fakeWorld) ReviewReadiness(string, string) (string, bool) { return "", false }
func (w *fakeWorld) ReviewLoop(string) ReviewOutcome               { return ReviewOutcome{} }
func (w *fakeWorld) Stop() (bool, string)                          { return false, "" }
func (w *fakeWorld) Capped(context.Context) string                 { return "" }
func (w *fakeWorld) FollowUp() FollowUpSources                     { return w.followUp }
func (w *fakeWorld) OpenItems() int                                { return 0 }
func (w *fakeWorld) Candidates(context.Context) ([]round.Candidate, error) {
	return w.cands, nil
}
func (w *fakeWorld) Assign(context.Context, string) (round.Assigned, error) {
	w.assigns++
	return round.Assigned{}, nil
}
func (w *fakeWorld) AssignBundle(context.Context, string, []string) (round.Assigned, error) {
	w.assigns++
	return round.Assigned{}, nil
}
func (w *fakeWorld) TestAudit(context.Context) ([]string, error) { return w.auditReason, w.auditErr }
func (w *fakeWorld) Review() Reviewer                            { return w.reviewer }
func (w *fakeWorld) Audit(Action)                                { w.audits++ }
func (w *fakeWorld) Say(string)                                  {}
func (w *fakeWorld) Warn(string)                                 {}
func (w *fakeWorld) Now() time.Time                              { return time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC) }

func tickWorld(t *testing.T, w *fakeWorld) (Result, []string) {
	return tickWorldWith(t, w, Config{})
}

func tickWorldWith(t *testing.T, w *fakeWorld, cfg Config) (Result, []string) {
	t.Helper()
	w.root = gittest.NewRepo(t, "main")
	path := rotatree.Workers(w.root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	reg := `{"round":1,"slots":[{"name":"ben","branch":"ben/1-x","base":"main","state":"done","pr":"#11"},{"name":"dana","state":"idle"}]}`
	if err := os.WriteFile(path, []byte(reg), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.Settings = roundcfg.Settings{Roster: []string{"ben", "dana"}, TrainFill: roundcfg.TrainFill{Max: 4}}
	cfg.MergeApproval = gate.MergeNone
	cfg.Base = "main"
	r, err := Run(context.Background(), NewEnv(w, cfg))
	if err != nil {
		t.Fatal(err)
	}
	return r, w.gated
}

// A closed PR and a PR whose base moved are both refusals only the gate can
// word: they skip the train-fill wait and reach it, with the same outcome.
func TestHeadBypassClosedAndBaseMismatchAgree(t *testing.T) {
	closed := &fakeWorld{info: tracker.PRInfo{State: "CLOSED", Base: "main"}}
	moved := &fakeWorld{info: tracker.PRInfo{State: "OPEN", Base: "release"}}
	rc, gc := tickWorld(t, closed)
	rm, gm := tickWorld(t, moved)
	if len(gc) != 1 || gc[0] != "main:ben" {
		t.Fatalf("closed PR did not reach the gate: %v", gc)
	}
	if !reflect.DeepEqual(rc, rm) || !reflect.DeepEqual(gc, gm) {
		t.Fatalf("closed %+v %v != base mismatch %+v %v", rc, gc, rm, gm)
	}
}

// An open PR on the right base with no head SHA is unreadable, not bypassed.
func TestHeadWithoutSHAIsNotBypassed(t *testing.T) {
	w := &fakeWorld{info: tracker.PRInfo{State: "OPEN", Base: "main"}}
	if _, gated := tickWorld(t, w); len(gated) != 0 {
		t.Fatalf("unreadable head reached the gate: %v", gated)
	}
}

// The waiting list is read once per tick, however many targets ask.
func TestWaitingReadOncePerTick(t *testing.T) {
	w := &fakeWorld{
		info:    tracker.PRInfo{State: "OPEN", Base: "main", HeadSHA: "abc"},
		waiting: []ReadyPR{{PR: "#11", Reviewed: true}},
	}
	tickWorld(t, w)
	if w.waits != 1 {
		t.Fatalf("waiting read %d times, want 1", w.waits)
	}
}

// The same pass serves tick and observe: the observing world's reads
// feed proposals through the same Env fields, and nothing that changes the
// world (landing, assigning, auditing) is called.
func TestObserveRunsThePassWithoutMutating(t *testing.T) {
	mints := 0
	w := &fakeWorld{
		observing: true,
		info:      tracker.PRInfo{State: "CLOSED", Base: "main"}, // goes straight to the gate
		repairs:   []round.Finding{{Kind: "slot-stuck", Slot: "ben"}},
		cands:     []round.Candidate{cand("#2", true), cand("#3", false)},
		reviewer:  fakeReview{due: true, mint: func() ([]string, error) { mints++; return nil, nil }},
	}
	r, gated := tickWorldWith(t, w, Config{})
	if !r.Observed {
		t.Fatalf("not an observation: %+v", r)
	}
	var got []string
	for _, p := range r.Proposed {
		got = append(got, p.Action.Action)
	}
	if !reflect.DeepEqual(got, []string{"reconcile", "gate", "mint", "assign"}) {
		t.Fatalf("proposed %v, want the world's repair then the landing", got)
	}
	if len(gated) != 0 || w.assigns != 0 || w.audits != 0 || w.landings != 0 || mints != 0 || len(r.Did) != 0 {
		t.Logf("landing writes %d, mint writes %d", w.landings, mints)
		t.Fatalf("observation changed the world: gated %v assigns %d audits %d did %+v", gated, w.assigns, w.audits, r.Did)
	}
	if !hasItem(r.NeedsYou, "candidate-hold", "#3") {
		t.Errorf("unready candidate hold missing: %+v", r.NeedsYou)
	}
	t.Logf("write calls: assign=%d landing=%d mint=%d audit=%d", w.assigns, w.landings, mints, w.audits)
}

// A due test-audit suggestion is a needs-you item, once per round: the second
// tick (carrying the first's Reported memory) lists it but does not report it
// new. The tick takes no action on it.
func TestTestAuditSuggestionIsNeedsYouOnce(t *testing.T) {
	w := &fakeWorld{
		info:        tracker.PRInfo{State: "OPEN", Base: "main", HeadSHA: "abc"},
		auditReason: []string{"latest full gate 12.000s exceeds 10s"},
	}
	r, gated := tickWorld(t, w)
	var item *Item
	for i := range r.NeedsYou {
		if r.NeedsYou[i].Kind == "test-audit" {
			item = &r.NeedsYou[i]
		}
	}
	if item == nil || !strings.Contains(item.Why, "exceeds 10s") {
		t.Fatalf("no test-audit needs-you item: %+v", r.NeedsYou)
	}
	if len(r.New) == 0 || len(gated) != 0 || len(r.Did) != 0 || w.assigns != 0 {
		t.Fatalf("the suggestion must be news and trigger no action: new %+v gated %v did %+v", r.New, gated, r.Did)
	}
	cfg := Config{Settings: roundcfg.Settings{TrainFill: roundcfg.TrainFill{Max: 4}}, MergeApproval: gate.MergeNone, Base: "main"}
	e := NewEnv(w, cfg)
	e.Reported = r.Reported
	r2, err := Run(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	news := 0
	for _, it := range r2.New {
		if it.Kind == "test-audit" {
			news++
		}
	}
	if news != 0 {
		t.Fatalf("the suggestion was reported again: %+v", r2.New)
	}
}

// No due suggestion, or a failed read, raises nothing.
func TestTestAuditQuietWhenNotDueOrUnreadable(t *testing.T) {
	for _, w := range []*fakeWorld{{}, {auditErr: errors.New("ledger damaged")}} {
		w.info = tracker.PRInfo{State: "OPEN", Base: "main", HeadSHA: "abc"}
		r, _ := tickWorld(t, w)
		for _, it := range r.NeedsYou {
			if it.Kind == "test-audit" {
				t.Fatalf("unexpected item %+v", it)
			}
		}
	}
}

// Observation comes from the world supplying read-only reads, rather than a
// second configuration switch that can disagree with it.
func TestObserveDerivedFromWorld(t *testing.T) {
	w := &fakeWorld{observing: true}
	e := NewEnv(w, Config{})
	if !e.Observe {
		t.Fatal("observing World produced a writable Env")
	}
}

// Even a caller that reaches an execution adapter directly cannot bypass the
// observation seam. Every nested write port is covered as well as World writes.
func TestObserveWorldRefusesExecutionAdapters(t *testing.T) {
	ctx := context.Background()
	writes := 0
	w := &fakeWorld{observing: true,
		reviewer: fakeReview{facts: &round.Architecture{}, mint: func() ([]string, error) { writes++; return nil, nil }},
		followUp: FollowUpSources{Start: func(context.Context, FollowUp, string, bool) error { writes++; return nil }},
	}
	e := NewEnv(w, Config{})
	checks := map[string]func() error{
		"gate":      func() error { return e.RunGate(ctx, "ben", "main").Err },
		"train":     func() error { return e.RunTrain(ctx, []string{"ben"}, "main").Err },
		"assign":    func() error { _, err := e.Assign(ctx, "#1"); return err },
		"bundle":    func() error { _, err := e.AssignBundle(ctx, "#1", []string{"#2"}); return err },
		"mint":      func() error { _, err := e.Review.Mint(ctx, round.Architecture{}); return err },
		"follow-up": func() error { return e.FollowUp.Start(ctx, FollowUp{}, "ben", false) },
	}
	for name, call := range checks {
		if err := call(); !errors.Is(err, errReadOnlyWorld) {
			t.Errorf("%s: got %v, want read-only refusal", name, err)
		}
	}
	e.Audit(Action{})
	if got := writes + w.assigns + w.audits + w.landings; got != 0 {
		t.Fatalf("observation leaked %d writes", got)
	}
}
