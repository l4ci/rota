package roundwatch

import (
	"context"
	"github.com/l4ci/rota/internal/git"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/rotastatetest"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/worker"
)

func testEnv(now *time.Time, alive map[int]bool) roundlease.Env {
	return roundlease.Env{
		Host:      "h",
		Alive:     func(pid int) bool { return alive[pid] },
		StartTime: func(int) (uint64, bool) { return 0, false },
		Now:       func() time.Time { return *now },
	}
}

func TestDiff(t *testing.T) {
	ben, dana := Key{Kind: KindSlotPR, ID: "ben"}, Key{Kind: KindSlotPR, ID: "dana"}
	got := Diff(
		Snapshot{PRStateKey("ben"): "OPEN", ben: "u1", dana: "u2"},
		Snapshot{PRStateKey("ben"): "merged", ben: "u1", SupportKey("7", SupportComment): "ann#1"},
	)
	want := []Change{
		{Key: dana, From: "u2"},
		{Key: PRStateKey("ben"), From: "OPEN", To: "merged"},
		{Key: SupportKey("7", SupportComment), To: "ann#1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diff = %+v, want %+v", got, want)
	}
	if len(Diff(Snapshot{ben: "u1"}, Snapshot{ben: "u1"})) != 0 {
		t.Fatal("equal snapshots must not differ")
	}
}

// The text form keeps the seen file readable across versions; every kind
// round-trips through it.
func TestKeyTextRoundTrips(t *testing.T) {
	for _, k := range []Key{
		{Kind: KindSlotPR, ID: "ben"}, PRStateKey("ben"), {Kind: KindEscalation, ID: "e1"},
		EscalationStatusKey("e1"), ReviewKey("nia"), GateKey, SupportKey("7", SupportPRHead),
	} {
		b, _ := k.MarshalText()
		var back Key
		if err := back.UnmarshalText(b); err != nil || back != k {
			t.Errorf("%+v -> %q -> %+v (%v)", k, b, back, err)
		}
	}
	var bad Key
	if bad.UnmarshalText([]byte("nonsense/x")) == nil {
		t.Error("an unknown key must not parse")
	}
}

// fakeClock advances only when the watch waits or sleeps.
type fakeClock struct{ now time.Time }

func (f *fakeClock) env(wait func(d time.Duration) (*SlotNews, error), local, forge func() Snapshot) Env {
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
		Forge: func(context.Context) Snapshot { return forge() },
	}
}

func empty() Snapshot { return Snapshot{} }

func TestRunHeartbeat(t *testing.T) {
	fc := &fakeClock{now: time.Unix(0, 0)}
	env := fc.env(func(time.Duration) (*SlotNews, error) { return nil, nil }, empty, empty)
	res, err := Run(context.Background(), env, Opts{Heartbeat: 100 * time.Second, Poll: 30 * time.Second, ForgeEvery: 60 * time.Second})
	if err != nil || res.Reason != ReasonHeartbeat || res.Waited != 100*time.Second {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestRunHeartbeatCallsOnHeartbeat(t *testing.T) {
	fc := &fakeClock{now: time.Unix(0, 0)}
	env := fc.env(func(time.Duration) (*SlotNews, error) { return nil, nil }, empty, empty)
	n := 0
	env.OnHeartbeat = func() { n++ }
	if res, err := Run(context.Background(), env, Opts{Heartbeat: 100 * time.Second, Poll: 30 * time.Second}); err != nil || res.Reason != ReasonHeartbeat || n != 1 {
		t.Fatalf("%+v %v calls=%d", res, err, n)
	}
	n = 0
	env = fc.env(func(time.Duration) (*SlotNews, error) { return &SlotNews{Slot: "ben", State: "done"}, nil }, empty, empty)
	env.OnHeartbeat = func() { n++ }
	if _, err := Run(context.Background(), env, Opts{Heartbeat: time.Hour, Poll: 30 * time.Second}); err != nil || n != 0 {
		t.Fatalf("slot news must not clear prompts: %v calls=%d", err, n)
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
	forge := func() Snapshot {
		if fc.now.Sub(time.Unix(0, 0)) >= 60*time.Second {
			return Snapshot{EscalationStatusKey("E1"): "answered"}
		}
		return Snapshot{EscalationStatusKey("E1"): "pending"}
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
	forge := func() Snapshot { return Snapshot{PRStateKey("ben"): "OPEN"} }
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

// fakeEnv is a worker.Env whose registry is the given JSON text and whose git
// says it is outside any repo: the seam under test, with nothing on disk.
func fakeEnv(registry string) worker.Env {
	return worker.Env{Config: worker.NoConfig,
		Registry: func(string) worker.Registry { return worker.ParseRegistry(registry) },
		Git:      func(context.Context, string, ...string) (git.Result, error) { return git.Result{}, nil },
	}
}

const noRoot = "/nonexistent/rota-root"

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

func TestCarryKeepsReviewEntries(t *testing.T) {
	base := Snapshot{ReviewKey("nia"): "1 from rev", PRStateKey("nia"): "open", Key{Kind: KindSlotPR, ID: "nia"}: "u"}
	got := carry(base)
	if got[ReviewKey("nia")] != "1 from rev" || got[PRStateKey("nia")] != "open" || len(got) != 2 {
		t.Fatalf("a tick that skips the forge must not read review input as gone: %v", got)
	}
}

// A gate or train holding the land lock is part of the snapshot, so its release
// reads as a change and wakes a watch whose assign was refused.
func TestLocalSnapshotReportsTheLandLock(t *testing.T) {
	repo := gittest.NewRepo(t, "main")
	cd, err := rotastatetest.CommonDir(repo)
	if err != nil {
		t.Fatal(err)
	}
	path := rotastate.File(cd, "land")
	var during, after Snapshot
	err = fsio.Locked(path, 0, func() error {
		if err := os.WriteFile(path+worker.LandHolderSuffix, worker.EncodeLandHolder(repo, "#1900"), 0o644); err != nil {
			return err
		}
		defer os.Remove(path + worker.LandHolderSuffix)
		during = LocalSnapshot(worker.Env{Config: worker.NoConfig}, repo)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	after = LocalSnapshot(worker.Env{Config: worker.NoConfig}, repo)
	if during[GateKey] != "#1900" {
		t.Errorf("snapshot under the lock = %v, want %s=#1900", during, GateKey)
	}
	if _, ok := after[GateKey]; ok {
		t.Errorf("snapshot after release = %v, want no %s", after, GateKey)
	}
	ch := Diff(during, after)
	if len(ch) != 1 || ch[0].Key != GateKey || ch[0].From != "#1900" || ch[0].To != "" {
		t.Errorf("release change = %+v, want %s #1900 -> gone", ch, GateKey)
	}
}

// A lock taken while the watch runs is not news; its release is.
func TestRunWakesOnGateReleaseOnly(t *testing.T) {
	snaps := []Snapshot{
		{},
		{GateKey: "#1900"},
		{GateKey: "ben"},
		{},
	}
	i := 0
	env := Env{
		Local: func() Snapshot { s := snaps[min(i, len(snaps)-1)]; return s },
		Forge: func(context.Context) Snapshot { return nil },
		Wait: func(context.Context, time.Duration) (*SlotNews, error) {
			i++
			return nil, nil
		},
		Now:   func() time.Time { return time.Unix(0, 0) },
		Sleep: func(context.Context, time.Duration) {},
	}
	res, err := Run(context.Background(), env, Opts{Heartbeat: time.Hour, Poll: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reason != ReasonChange || i != 3 {
		t.Fatalf("woke %q after %d waits, want a change at the release (3)", res.Reason, i)
	}
	if len(res.Changes) != 1 || res.Changes[0].Key != GateKey || res.Changes[0].From != "ben" || res.Changes[0].To != "" {
		t.Errorf("changes = %+v, want %s ben -> gone", res.Changes, GateKey)
	}
}

// The snapshot reads slot PRs and escalation statuses through the caller's Env,
// not the registry file under root.
func TestLocalSnapshotReadsThroughEnv(t *testing.T) {
	we := fakeEnv(`{"slots":[{"name":"ben","state":"busy","pr":"https://x/pull/7"},{"name":"dana","state":"idle"}],"escalations":[{"id":"e1","status":"pending"}]}`)
	got := LocalSnapshot(we, noRoot)
	want := Snapshot{{Kind: KindSlotPR, ID: "ben"}: "https://x/pull/7", {Kind: KindEscalation, ID: "e1"}: "pending"}
	if len(got) != len(want) {
		t.Fatalf("snapshot = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("snapshot[%s] = %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
}
