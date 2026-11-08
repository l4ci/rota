package cli

import (
	"testing"
	"time"

	"github.com/l4ci/rota/internal/roundlease"
)

func TestIdentifyHolder(t *testing.T) {
	cd := t.TempDir()
	alive := map[int]uint64{10: 100, 20: 200}
	env := roundlease.Env{
		Host:      "h",
		Alive:     func(p int) bool { _, ok := alive[p]; return ok },
		StartTime: func(p int) (uint64, bool) { s, ok := alive[p]; return s, ok },
		Now:       func() time.Time { return time.Unix(1_700_000_000, 0) },
	}
	noenv := func(string) string { return "" }
	if w := hookIdentify(env, 10, noenv, cd); w.Orchestrator || !w.LeaseFree {
		t.Fatalf("no lease: %+v", w)
	}
	env.Acquire(cd, "/r", roundlease.Holder{PID: 10, Start: 100}, 1)
	if w := hookIdentify(env, 10, noenv, cd); !w.Orchestrator || w.LeaseFree {
		t.Fatalf("holder: %+v", w)
	}
	if w := hookIdentify(env, 20, noenv, cd); w.Orchestrator || w.LeaseFree {
		t.Fatalf("worker: %+v", w)
	}
	delete(alive, 10)
	if w := hookIdentify(env, 10, noenv, cd); w.Orchestrator || !w.LeaseFree {
		t.Fatalf("stale: %+v", w)
	}
}
