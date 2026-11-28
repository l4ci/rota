package round

import (
	"context"
	"errors"
	"github.com/l4ci/rota/internal/landing"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/worker"
)

// fakeWorker is a Worker that answers only the methods a test overrides; any
// other call panics on the nil embedded interface, which fails the test that
// reached an operation it did not mean to.
type fakeWorker struct {
	Worker
	now      time.Time
	gate     worker.LandGate
	gateErr  error
	accounts *worker.Accounts
}

func (f *fakeWorker) Now() time.Time             { return f.now }
func (f *fakeWorker) Accounts() *worker.Accounts { return f.accounts }
func (f *fakeWorker) WithAccounts(a *worker.Accounts) Worker {
	c := *f
	c.accounts = a
	return &c
}
func (f *fakeWorker) LandGate(context.Context, string) (worker.LandGate, error) {
	return f.gate, f.gateErr
}

// The refusal names what the gate holds the lock for and says a retry is safe.
func TestRefuseWhileGateRunsNamesTheHolder(t *testing.T) {
	held := worker.LandGate{Held: true, All: true, Holder: "#1900"}
	err := Env{Worker: &fakeWorker{gate: held}}.refuseWhileGateRuns(bg, t.TempDir(), "assign")
	if err == nil {
		t.Fatal("want a gate-running refusal")
	}
	for _, want := range []string{"for #1900 ", "safe to retry"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q lacks %q", err, want)
		}
	}
	held.Holder = ""
	err = Env{Worker: &fakeWorker{gate: held}}.refuseWhileGateRuns(bg, t.TempDir(), "assign")
	if err == nil || !strings.Contains(err.Error(), "safe to retry") || strings.Contains(err.Error(), " for ") {
		t.Errorf("refusal without a holder = %v, want the retry hint and no name", err)
	}
}

// A verb that switches a worktree's branch refuses while a gate holds the land
// lock, and reads that lock through the Worker alone.
func TestRefuseWhileGateRunsReadsTheWorkerSeam(t *testing.T) {
	cases := []struct {
		name    string
		w       *fakeWorker
		wantErr string
	}{
		{"free", &fakeWorker{}, ""},
		{"held", &fakeWorker{gate: worker.LandGate{Held: true, All: true}}, landing.GateRunning},
		{"unreadable", &fakeWorker{gateErr: errors.New("boom")}, "boom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Env{Worker: tc.w}.refuseWhileGateRuns(bg, t.TempDir(), "assign")
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("err = %v, want nil", err)
			case tc.wantErr == "":
			case err == nil:
				t.Fatalf("err = nil, want %q", tc.wantErr)
			case tc.wantErr == landing.GateRunning:
				var be *BlockedError
				if !errors.As(err, &be) || be.By != landing.GateRunning {
					t.Fatalf("err = %v, want blockedBy %s", err, landing.GateRunning)
				}
			case err.Error() != tc.wantErr:
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

// The round's clock is the Worker's, and WithAccounts turns picking on through
// the Worker without touching the original.
func TestEnvClockAndAccountsComeFromTheWorker(t *testing.T) {
	at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	e := Env{Worker: &fakeWorker{now: at}}
	if got := e.now(); !got.Equal(at) {
		t.Errorf("now = %v, want %v", got, at)
	}
	acc := &worker.Accounts{Env: worker.Env{Config: worker.NoConfig}}
	picked := e.WithAccounts(acc)
	if !picked.AccountPicking || picked.Worker.Accounts() != acc {
		t.Errorf("WithAccounts: picking = %v, accounts = %v", picked.AccountPicking, picked.Worker.Accounts())
	}
	if e.AccountPicking || e.Worker.Accounts() != nil {
		t.Error("WithAccounts changed the original env")
	}
	if off := picked.WithAccounts(nil); off.AccountPicking || off.Worker.Accounts() != nil {
		t.Error("WithAccounts(nil) left picking on")
	}
}

// #2639, #4248: a held lock refuses only the slots it gates; an unscoped
// landing and a call naming no slot refuse everything, and unreadableOnly
// refuses the unscoped one alone.
func TestRefuseGateOnlyForGatedSlots(t *testing.T) {
	scoped := worker.LandGate{Held: true, Slots: map[string]bool{"ben": true}}
	unscoped := worker.LandGate{Held: true, All: true}
	cases := []struct {
		name           string
		gate           worker.LandGate
		unreadableOnly bool
		slots          []string
		refuse         bool
	}{
		{"ungated slot", scoped, false, []string{"kit"}, false},
		{"gated slot", scoped, false, []string{"kit", "ben"}, true},
		{"no slot named", scoped, false, nil, true},
		{"unscoped", unscoped, false, []string{"kit"}, true},
		{"unreadableOnly, gated slot", scoped, true, []string{"ben"}, false},
		{"unreadableOnly, unscoped", unscoped, true, []string{"kit"}, true},
	}
	for _, tc := range cases {
		err := Env{Worker: &fakeWorker{gate: tc.gate}}.refuseGate(bg, t.TempDir(), "assign", tc.unreadableOnly, tc.slots...)
		var be *BlockedError
		got := errors.As(err, &be) && be.By == landing.GateRunning
		if got != tc.refuse || (!tc.refuse && err != nil) {
			t.Errorf("%s: err = %v, want refuse=%v", tc.name, err, tc.refuse)
		}
	}
}
