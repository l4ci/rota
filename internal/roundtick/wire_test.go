package roundtick

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
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
	root    string
	info    tracker.PRInfo
	waiting []ReadyPR
	waits   int
	gated   []string
}

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
	return round.Outcome{}, nil
}
func (w *fakeWorld) Root() string { return w.root }
func (w *fakeWorld) Landing(_ context.Context, req postgate.Request) (postgate.Env, postgate.Request, error) {
	if req.Gate != nil {
		w.gated = append(w.gated, req.Gate.Base+":"+req.Gate.Slot)
	}
	return postgate.Env{Worker: worker.Env{}}, req, nil
}
func (w *fakeWorld) ReviewReadiness(string, string) (string, bool) { return "", false }
func (w *fakeWorld) ReviewLoop(string) ReviewOutcome               { return ReviewOutcome{} }
func (w *fakeWorld) Capped(context.Context) string                 { return "" }
func (w *fakeWorld) OpenItems() int                                { return 0 }
func (w *fakeWorld) Candidates(context.Context) ([]round.Candidate, error) {
	return nil, nil
}
func (w *fakeWorld) Assign(context.Context, string) (round.Assigned, error) {
	return round.Assigned{}, nil
}
func (w *fakeWorld) Review() Reviewer { return nil }
func (w *fakeWorld) Audit(Action)     {}
func (w *fakeWorld) Say(string)       {}
func (w *fakeWorld) Warn(string)      {}
func (w *fakeWorld) Now() time.Time   { return time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC) }

func tickWorld(t *testing.T, w *fakeWorld) (Result, []string) {
	t.Helper()
	w.root = gittest.NewRepo(t, "main")
	path := rotatree.Workers(w.root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	reg := `{"round":1,"slots":[{"name":"ben","branch":"ben/1-x","base":"main","state":"done","pr":"#11"}]}`
	if err := os.WriteFile(path, []byte(reg), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		Settings:      roundcfg.Settings{TrainFill: roundcfg.TrainFill{Max: 4}},
		MergeApproval: gate.MergeNone,
		Base:          "main",
	}
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
