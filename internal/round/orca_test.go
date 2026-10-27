package round

import (
	"context"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"testing"
)

func TestOrcaMissingSnapshotHandleCannotBeReclaimed(t *testing.T) {
	f := newMoveFx(t)
	f.env.HostName = host.Kind("orca")
	f.env.Snapshot = func(context.Context) ([]host.Agent, error) { return []host.Agent{}, nil }
	rawSlot(f.root, "ben", func(s *jsonx.Object) { s.Set("handle", "orca:old-runtime"); s.Set("state", "busy") })
	h := f.env.Health(bg, f.root, f.slot("ben"), f.now)
	if h.Health != HealthUnverifiable || h.Known || h.Reason != "stale-handle" {
		t.Fatalf("missing handle health = %+v", h)
	}
	if _, err := f.reclaim("ben", nil); blockedBy(t, err) != BlockUnverifiable {
		t.Fatalf("reclaim did not refuse missing handle: %v", err)
	}
	if len(f.killed) != 0 {
		t.Fatalf("reclaim killed %v", f.killed)
	}
	rep, err := f.env.Status(bg, f.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range rep.Findings {
		if finding.Kind == DeadTab && finding.Slot == "ben" {
			t.Fatal("missing handle produced destructive dead-tab repair")
		}
	}
	found := false
	for _, row := range rep.Rows {
		if row.Name == "ben" {
			found = true
			if row.HostState != "unverifiable" || row.Evidence != "stale-handle" {
				t.Fatalf("row = %+v", row)
			}
		}
	}
	if !found {
		t.Fatal("slot missing from status")
	}
	res, err := f.reclaim("ben", func(o *ReclaimOpts) { o.Force = true })
	if err != nil || !res.Changed || len(f.killed) != 1 {
		t.Fatalf("forced reclaim must still kill/prove session: %+v, %v, killed=%v", res, err, f.killed)
	}
}
