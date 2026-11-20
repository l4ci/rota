package roundhook

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/git"
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

// fakeEnv is a worker.Env whose registry is the given JSON text and whose git
// says it is outside any repo: the seam under test, with nothing on disk.
func fakeEnv(registry string) worker.Env {
	return worker.Env{
		Registry: func(string) worker.Registry { return worker.ParseRegistry(registry) },
		Git:      func(context.Context, string, ...string) (git.Result, error) { return git.Result{}, nil },
	}
}

const noRoot = "/nonexistent/rota-root"

func TestNeedsWatchAndDigest(t *testing.T) {
	for _, tc := range []struct {
		state  string
		active bool
	}{
		{"busy", true},
		{"", true}, // unrecorded is not proof that a worker is idle
		{"idle", false},
		{"IDLE", false},
		{"IdLe", false},
	} {
		t.Run(fmt.Sprintf("state=%q", tc.state), func(t *testing.T) {
			we := fakeEnv(fmt.Sprintf(`{"slots":[{"name":"ben","state":"idle"},{"name":"dana","state":%q,"task":"31"}]}`, tc.state))
			if need := NeedsWatch(we, noRoot); need != tc.active {
				t.Errorf("NeedsWatch = %v, want %v", need, tc.active)
			}
			d := Digest(we, noRoot, 2, false)
			if strings.Contains(d, "dana") != tc.active || strings.Contains(d, "#31") != tc.active || strings.Contains(d, "ben") {
				t.Errorf("digest slot visibility: %q", d)
			}
			if !strings.Contains(d, "round 2") || !strings.HasSuffix(d, "NO WATCH ARMED: run `rota round watch` in the background now.") {
				t.Errorf("digest round/watch status: %q", d)
			}
		})
	}
	we := fakeEnv(`{"slots":[{"name":"ben","state":"done","pr":"https://x/pull/7"}]}`)
	if !NeedsWatch(we, noRoot) {
		t.Fatal("a done slot with an open PR needs a watch")
	}
	if d := Digest(we, noRoot, 0, true); !strings.Contains(d, "needs you") || !strings.HasSuffix(d, "Watch armed.") {
		t.Fatalf("digest %q", d)
	}
}

func TestExitDigest(t *testing.T) {
	if got, want := ExitDigest(fakeEnv(""), noRoot, 0), "rota round: no active slots. Watch exiting; re-arm `rota round watch`."; got != want {
		t.Errorf("empty round exit digest = %q, want %q", got, want)
	}
	we := fakeEnv(`{"slots":[{"name":"ben","state":"idle"},{"name":"dana","state":"done","task":"31","pr":"https://x/pull/7"}],"escalations":[{"id":"E1","status":"pending"}]}`)
	want := "rota round 2: dana done #31 https://x/pull/7 (needs you); 1 escalation(s) pending. Watch exiting; re-arm `rota round watch`."
	if got := ExitDigest(we, noRoot, 2); got != want {
		t.Errorf("active round exit digest = %q, want %q", got, want)
	}
}

func TestStopBlock(t *testing.T) {
	for _, tc := range []struct {
		name, registry      string
		armed, alive, block bool
		attention           string
	}{
		{name: "missing registry"},
		{name: "idle", registry: `{"slots":[{"name":"ben","state":"idle"}]}`},
		{name: "busy unarmed", registry: `{"slots":[{"name":"ben","state":"busy"}]}`, block: true},
		{name: "already waiting", registry: `{"slots":[{"name":"ben","state":"done"}]}`, block: true, attention: "Waiting on you already: ben done."},
		{name: "live watch", registry: `{"slots":[{"name":"ben","state":"busy"}]}`, armed: true, alive: true},
		{name: "dead watch", registry: `{"slots":[{"name":"ben","state":"busy"}]}`, armed: true, block: true},
		{name: "solo", registry: `{"host":"solo","slots":[{"name":"ben","state":"busy"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cd := t.TempDir()
			now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
			alive := map[int]bool{10: true}
			env := testEnv(&now, alive)
			if tc.armed {
				release, err := roundlease.Arm(env, cd, 10, time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
				alive[10] = tc.alive
			}
			reason, block := StopBlock(fakeEnv(tc.registry), env, noRoot, cd)
			if block != tc.block {
				t.Fatalf("block=%v, reason=%q", block, reason)
			}
			if block {
				if !strings.Contains(reason, "Start `rota round watch` as a background command now") || !strings.Contains(reason, tc.attention) {
					t.Fatalf("reason=%q", reason)
				}
			} else if reason != "" {
				t.Fatalf("passing hook returned reason %q", reason)
			}
		})
	}
}

func TestDigestShowsWhyASlotIsUnverifiable(t *testing.T) {
	we := fakeEnv(`{"slots":[{"name":"ben","state":"unverifiable","reason":"host-unreachable","task":"31"}]}`)
	if got := Digest(we, noRoot, 1, true); !strings.Contains(got, "ben unverifiable:host-unreachable #31") {
		t.Errorf("digest = %q", got)
	}
}

// #1843: a done slot whose PR merged is not news for the orchestrator.
func TestMergedDoneSlotNeedsNoWake(t *testing.T) {
	we := fakeEnv(`{"slots":[{"name":"dana","state":"done","pr":"https://x/pull/225","prMerged":true}]}`)
	if d := Digest(we, noRoot, 1, true); strings.Contains(d, "needs you") {
		t.Fatalf("digest %q", d)
	}
	if _, attn := viewOf(we.Registry(noRoot)).needsWatch(); len(attn) != 0 {
		t.Fatalf("attn %v", attn)
	}
}
