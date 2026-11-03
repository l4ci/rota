package round

import (
	"context"
	"testing"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
)

func TestCoreMissingSnapshotHandleCannotBeReclaimed(t *testing.T) {
	f := newMoveFx(t)
	f.env.HostName = host.HerdrCore
	f.env.Snapshot = func(context.Context) ([]host.Agent, error) { return []host.Agent{}, nil }
	rawSlot(f.root, "ben", func(s *jsonx.Object) { s.Set("handle", "core:old-runtime"); s.Set("state", "busy") })
	h := f.env.Health(bg, f.root, f.slot("ben"), f.now)
	if h.Health != HealthUnverifiable || h.Known {
		t.Fatalf("missing Core identity health=%+v", h)
	}
	if _, err := f.reclaim("ben", nil); blockedBy(t, err) != BlockUnverifiable {
		t.Fatalf("reclaim must refuse uncertain absence: %v", err)
	}
	if len(f.killed) != 0 {
		t.Fatalf("reclaim killed uncertain Core worker: %v", f.killed)
	}
}
