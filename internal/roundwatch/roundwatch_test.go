package roundwatch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/roundlease"
)

func testEnv(now *time.Time, alive map[int]bool) roundlease.Env {
	return roundlease.Env{
		Host:      "h",
		Alive:     func(pid int) bool { return alive[pid] },
		StartTime: func(int) (uint64, bool) { return 0, false },
		Now:       func() time.Time { return *now },
	}
}

func TestArmOneAtATime(t *testing.T) {
	cd := t.TempDir()
	now := time.Now()
	env := testEnv(&now, map[int]bool{10: true, 11: true})
	rel, err := Arm(env, cd, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := Armed(env, cd); !ok || m.PID != 10 || m.Heartbeat != 60 {
		t.Fatalf("armed %+v %v", m, ok)
	}
	var ae *ArmedError
	if _, err := Arm(env, cd, 11, time.Minute); !errors.As(err, &ae) {
		t.Fatalf("a second live watch must be refused: %v", err)
	}
	rel()
	if _, ok := Armed(env, cd); ok {
		t.Fatal("release must remove the marker")
	}
	// A marker whose process died is not armed and is replaced.
	if _, err := Arm(env, cd, 10, time.Minute); err != nil {
		t.Fatal(err)
	}
	env.Alive = func(int) bool { return false }
	if _, ok := Armed(env, cd); ok {
		t.Fatal("a dead watch is not armed")
	}
	env.Alive = func(pid int) bool { return pid == 11 }
	rel2, err := Arm(env, cd, 11, time.Minute)
	if err != nil {
		t.Fatalf("a dead watch must be replaceable: %v", err)
	}
	rel() // the old watch's release must not remove its successor's marker
	if _, ok := Armed(env, cd); !ok {
		t.Fatal("a stale release removed the successor's marker")
	}
	rel2()
}

func TestDiff(t *testing.T) {
	got := Diff(map[string]string{"a": "1", "b": "2", "c": "3"}, map[string]string{"a": "1", "b": "9", "d": "4"})
	var keys []string
	for _, c := range got {
		keys = append(keys, c.Key+":"+c.From+">"+c.To)
	}
	if strings.Join(keys, ",") != "b:2>9,c:3>,d:>4" {
		t.Fatalf("%v", keys)
	}
	if len(Diff(map[string]string{"a": "1"}, map[string]string{"a": "1"})) != 0 {
		t.Fatal("equal snapshots must not differ")
	}
}

// fakeClock advances only when the watch waits or sleeps.
type fakeClock struct{ now time.Time }

func (f *fakeClock) env(wait func(d time.Duration) (*SlotNews, error), local, forge func() map[string]string) Env {
	return Env{
		Now:   func() time.Time { return f.now },
		Sleep: func(_ context.Context, d time.Duration) { f.now = f.now.Add(d) },
		Wait: func(_ context.Context, d time.Duration) (*SlotNews, error) {
			n, err := wait(d)
			if n == nil && err == nil {
				f.now = f.now.Add(d)
			}
			return n, err
		},
		Local: local,
		Forge: func(context.Context) map[string]string { return forge() },
	}
}

func empty() map[string]string { return map[string]string{} }

func TestRunHeartbeat(t *testing.T) {
	fc := &fakeClock{now: time.Unix(0, 0)}
	env := fc.env(func(time.Duration) (*SlotNews, error) { return nil, nil }, empty, empty)
	res, err := Run(context.Background(), env, Opts{Heartbeat: 100 * time.Second, Poll: 30 * time.Second, ForgeEvery: 60 * time.Second})
	if err != nil || res.Reason != ReasonHeartbeat || res.Waited != 100*time.Second {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestRunSlotNews(t *testing.T) {
	fc := &fakeClock{now: time.Unix(0, 0)}
	calls := 0
	env := fc.env(func(time.Duration) (*SlotNews, error) {
		calls++
		if calls == 2 {
			return &SlotNews{Slot: "ben", State: "done"}, nil
		}
		return nil, nil
	}, empty, empty)
	res, err := Run(context.Background(), env, Opts{Heartbeat: time.Hour, Poll: 30 * time.Second})
	if err != nil || res.Reason != ReasonSlot || res.Slot.Slot != "ben" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestRunNothingToWatchStillSeesForge(t *testing.T) {
	fc := &fakeClock{now: time.Unix(0, 0)}
	forge := func() map[string]string {
		if fc.now.Sub(time.Unix(0, 0)) >= 60*time.Second {
			return map[string]string{EscalationStatusKey("E1"): "answered"}
		}
		return map[string]string{EscalationStatusKey("E1"): "pending"}
	}
	env := fc.env(func(time.Duration) (*SlotNews, error) { return nil, ErrNothingToWatch }, empty, forge)
	res, err := Run(context.Background(), env, Opts{Heartbeat: time.Hour, Poll: 30 * time.Second, ForgeEvery: 60 * time.Second})
	if err != nil || res.Reason != ReasonChange || len(res.Changes) != 1 || res.Changes[0].To != "answered" {
		t.Fatalf("%+v %v", res, err)
	}
}

// Between forge ticks the forge-owned entries of the baseline are carried, so
// their absence from a local-only tick is not a change.
func TestRunForgeEntriesNotChangedBetweenTicks(t *testing.T) {
	fc := &fakeClock{now: time.Unix(0, 0)}
	forge := func() map[string]string { return map[string]string{PRStateKey("ben"): "OPEN"} }
	env := fc.env(func(time.Duration) (*SlotNews, error) { return nil, nil }, empty, forge)
	res, err := Run(context.Background(), env, Opts{Heartbeat: 90 * time.Second, Poll: 30 * time.Second, ForgeEvery: time.Hour})
	if err != nil || res.Reason != ReasonHeartbeat {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestRunInterrupt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fc := &fakeClock{now: time.Unix(0, 0)}
	env := fc.env(func(time.Duration) (*SlotNews, error) { cancel(); return nil, nil }, empty, empty)
	res, err := Run(ctx, env, Opts{Heartbeat: time.Hour, Poll: 30 * time.Second})
	if err != nil || res.Reason != ReasonInterrupt {
		t.Fatalf("%+v %v", res, err)
	}
}

func writeRegistry(t *testing.T, root, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".rota", "workers.json"), []byte(body), 0o666); err != nil {
		t.Fatal(err)
	}
}

func TestNeedsWatchAndDigest(t *testing.T) {
	root := t.TempDir()
	writeRegistry(t, root, `{"slots":[{"name":"ben","state":"idle"},{"name":"dana","state":"busy","task":"31"}]}`)
	need, attn := NeedsWatch(root)
	if !need || len(attn) != 0 {
		t.Fatalf("busy slot needs a watch, nothing waits on the orchestrator: %v %v", need, attn)
	}
	d := Digest(root, 2, false)
	if !strings.Contains(d, "dana busy #31") || !strings.Contains(d, "round 2") || !strings.Contains(d, "NO WATCH ARMED") || strings.Contains(d, "ben") {
		t.Fatalf("digest %q", d)
	}
	writeRegistry(t, root, `{"slots":[{"name":"ben","state":"done","pr":"https://x/pull/7"}]}`)
	need, attn = NeedsWatch(root)
	if !need || len(attn) != 1 || attn[0] != "ben done" {
		t.Fatalf("%v %v", need, attn)
	}
	if d := Digest(root, 0, true); !strings.Contains(d, "needs you") || !strings.HasSuffix(d, "Watch armed.") {
		t.Fatalf("digest %q", d)
	}
	writeRegistry(t, root, `{"slots":[{"name":"ben","state":"idle"}]}`)
	if need, _ := NeedsWatch(root); need {
		t.Fatal("an all-idle round has nothing to watch")
	}
}

// With every slot idle there is nothing to wait on, and the watch holds until
// the heartbeat instead of returning at once (#327).
func TestRunNothingToWatchHoldsUntilHeartbeat(t *testing.T) {
	fc := &fakeClock{now: time.Unix(0, 0)}
	env := fc.env(func(time.Duration) (*SlotNews, error) { return nil, ErrNothingToWatch }, empty, empty)
	res, err := Run(context.Background(), env, Opts{Heartbeat: 100 * time.Second, Poll: 30 * time.Second, ForgeEvery: 60 * time.Second})
	if err != nil || res.Reason != ReasonHeartbeat || res.Waited != 100*time.Second {
		t.Fatalf("%+v %v", res, err)
	}
}
