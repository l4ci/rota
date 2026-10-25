package round

import (
	"context"
	"errors"
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
	locked   bool
	lockErr  error
	accounts *worker.Accounts
}

func (f *fakeWorker) Now() time.Time             { return f.now }
func (f *fakeWorker) Accounts() *worker.Accounts { return f.accounts }
func (f *fakeWorker) WithAccounts(a *worker.Accounts) Worker {
	c := *f
	c.accounts = a
	return &c
}
func (f *fakeWorker) LandLockHeld(context.Context, string) (bool, error) {
	return f.locked, f.lockErr
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
		{"held", &fakeWorker{locked: true}, worker.GateRunning},
		{"unreadable", &fakeWorker{lockErr: errors.New("boom")}, "boom"},
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
			case tc.wantErr == worker.GateRunning:
				var be *BlockedError
				if !errors.As(err, &be) || be.By != worker.GateRunning {
					t.Fatalf("err = %v, want blockedBy %s", err, worker.GateRunning)
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
	acc := &worker.Accounts{}
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
