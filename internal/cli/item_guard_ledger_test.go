package cli

import (
	"context"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/worker"
)

type createOnlyBackend struct{ backlog.Backend }

func (createOnlyBackend) Name() string { return "file" }
func (createOnlyBackend) Create(backlog.CreateInput) (backlog.CreateResult, error) {
	return backlog.CreateResult{ID: "55"}, nil
}

// A create entry goes through the ledger Recorder, so it carries the round of
// the lease like every other entry.
func TestRecordingBackendStampsTheRound(t *testing.T) {
	root, cd := t.TempDir(), t.TempDir()
	d := testDeps()
	lease := roundlease.Env{Host: "h", Alive: func(p int) bool { return p == 100 },
		StartTime: func(p int) (uint64, bool) { return uint64(p) * 10, p == 100 },
		Now:       func() time.Time { return time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC) }}
	if _, _, _, err := lease.Acquire(cd, root, roundlease.Holder{PID: 100}, 7); err != nil {
		t.Fatal(err)
	}
	d.WorkerEnv = func() worker.Env {
		return worker.Env{Lease: func() roundlease.Env { return lease },
			Git: func(context.Context, string, ...string) (git.Result, error) { return git.Result{Stdout: cd}, nil }}
	}
	rb := recordingBackend{Backend: createOnlyBackend{}, c: &Ctx{Deps: d}, root: root}
	if _, err := rb.Create(backlog.CreateInput{Title: "t"}); err != nil {
		t.Fatal(err)
	}
	es, _ := ledger.Load(root)
	if len(es) != 1 || es[0].Kind != ledger.KindCreate || es[0].Round != 7 || es[0].Issue != "55" {
		t.Fatalf("entries %+v, want one create entry in round 7", es)
	}
}
