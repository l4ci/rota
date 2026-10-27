package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/roundtick"
	"github.com/l4ci/rota/internal/roundwatch"
	"github.com/l4ci/rota/internal/worker"
)

func TestAutopilotHoldingWaitsForHeartbeat(t *testing.T) {
	c, root, tick := fillTickFixture(t, `"light"`, true, true)
	initial := tick()
	cd, err := rotastate.CommonDir(root)
	if err != nil {
		t.Fatal(err)
	}
	// Existing drift is already reported. Reset only fill deduplication so
	// the first watch tick has a new holding notice and no new attention item.
	if err := roundtick.SaveState(cd, 1, roundtick.Result{Reported: initial.Reported}); err != nil {
		t.Fatal(err)
	}
	now := c.deps().Now()
	c.deps().Now = func() time.Time { return now }
	lenv := c.deps().LeaseEnv()
	lenv.Now = func() time.Time { return now }
	waits := 0
	env := roundwatch.Env{
		Now:   func() time.Time { return now },
		Local: func() map[string]string { return map[string]string{} },
		Wait: func(_ context.Context, d time.Duration) (*roundwatch.SlotNews, error) {
			waits++
			now = now.Add(d)
			return nil, nil
		},
		Sleep: func(_ context.Context, d time.Duration) { now = now.Add(d) },
	}
	set, err := c.deps().roundSettings(root, roundConfigRequired)
	if err != nil {
		t.Fatal(err)
	}
	res, err := autopilotWatch(c, context.Background(), env, roundwatch.Opts{Heartbeat: 2 * time.Minute, Poll: time.Minute}, set, root, cd, lenv, "", scopePID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if waits != 2 {
		t.Fatalf("holding woke watch before heartbeat: waits=%d text=%s", waits, res.Text)
	}
	if reason, _ := res.Data.(*jsonx.Object).Get("reason"); reason != roundwatch.ReasonHeartbeat {
		t.Fatalf("watch reason=%v, want heartbeat", reason)
	}
	if !strings.Contains(res.Text, "holding train for main: 1/4 ready, oldest waited 8m of 20m") {
		t.Fatalf("heartbeat lost current holding status: %s", res.Text)
	}
}

func TestRoundStatusShowsCurrentTrainFill(t *testing.T) {
	c, root, tick := fillTickFixture(t, `"light"`, true, true)
	tick()
	pinNow(c.Deps, "2026-10-27T09:01:00Z")
	if err := worker.UpdateEscalations(root, func(list []worker.Escalation) []worker.Escalation {
		return append(list, worker.Escalation{ID: "fill-decision", Kind: "issue", Number: 12, Title: "Keep the current design?", Status: "pending", SentAt: "2026-10-27T09:00:00Z"})
	}); err != nil {
		t.Fatal(err)
	}
	code, out, stderr := rotaInWith(t, c.Deps, root, "round", "status")
	if code != 0 {
		t.Fatalf("status: %d %s %s", code, out, stderr)
	}
	if !strings.Contains(out, "holding train for main: 1/4 ready, oldest waited 7m of 20m") {
		t.Fatalf("status missing current fill wait for %s: %s", root, out)
	}
	cd, err := rotastate.CommonDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if line := roundwatch.MaintainerSnapshotLine(cd, c.Deps.Now(), roundwatch.DefaultHeartbeat); line != "1 waits on you, as of 0s ago" {
		t.Fatalf("fill status did not refresh the maintainer snapshot: %q", line)
	}
	code, out, stderr = rotaInWith(t, c.Deps, root, "--json", "round", "status")
	if code != 0 {
		t.Fatalf("status JSON: %d %s %s", code, out, stderr)
	}
	d := data(t, out)
	if holding, ok := d["holding"].([]any); !ok || len(holding) != 1 {
		t.Fatal("status JSON missing holding progress")
	}
	if waits, ok := d["needsYou"].([]any); !ok || len(waits) != 1 {
		t.Fatalf("fill status lost the maintainer decision: %s", out)
	}
}
