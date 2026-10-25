package round

import (
	"context"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
	"testing"
	"time"
)

func TestAssignReclaimStale(t *testing.T) {
	f := newAssignFixture(t)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	f.be.claims = map[string]string{"12": "mac-ben@3"}
	f.be.claimTimes = map[string]map[string]time.Time{"12": {"mac-ben@3": now.Add(-5 * time.Hour)}}
	f.be.items["12"].Labels = append(f.be.items["12"].Labels, "claimed:mac-ben@mac")
	f.env.Forge = f.be.asForge()
	f.wire(func(w *worker.Env) { w.Now = func() time.Time { return now } })
	f.env.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		if args[0] == "ls-remote" {
			return git.Result{}, nil
		}
		return git.Exec(ctx, dir, args...)
	}
	_, err := f.assign("12", "dana", func(o *AssignOpts) { o.ReclaimStale = "mac-ben@3" })
	if err != nil {
		t.Fatalf("reclaim stale: %v", err)
	}
	if got := f.be.held("12"); len(got) != 1 || got[0] != "dana@1" {
		t.Fatalf("claims after = %v, want [dana@1]", got)
	}
	if s := worker.LoadRegistryTolerant(f.root).Slot("dana"); s.ClaimID() != "dana@1" {
		t.Fatalf("slot claim %s", s.ClaimID())
	}
}

func TestAssignReclaimStaleRefusals(t *testing.T) {
	for _, name := range []string{"fresh", "wrong holder", "disabled", "no claim", "PR woke up", "plain assign"} {
		t.Run(name, func(t *testing.T) {
			f := newAssignFixture(t)
			now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
			holder := "mac-ben@3"
			at := now.Add(-5 * time.Hour)
			f.be.claims = map[string]string{"12": holder}
			f.be.claimTimes = map[string]map[string]time.Time{"12": {holder: at}}
			f.be.items["12"].Labels = append(f.be.items["12"].Labels, "claimed:mac-ben@mac")
			f.env.Forge = f.be.asForge()
			f.wire(func(w *worker.Env) { w.Now = func() time.Time { return now } })
			f.env.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
				if args[0] == "ls-remote" {
					return git.Result{}, nil
				}
				return git.Exec(ctx, dir, args...)
			}
			switch name {
			case "fresh":
				f.be.claimTimes["12"][holder] = now.Add(-time.Minute)
			case "wrong holder":
				holder = "mac-kit@3"
			case "disabled":
				f.env.Round.ClaimStaleMinutes = 0
			case "no claim":
				delete(f.be.claims, "12")
			case "PR woke up":
				f.be.prs = []tracker.PR{{Branch: "mac-ben/12-work", UpdatedAt: now.Add(-time.Minute)}}
			case "plain assign":
				holder = ""
			}
			_, err := f.assign("12", "dana", func(o *AssignOpts) { o.ReclaimStale = holder })
			want := "not-stale"
			if name == "plain assign" {
				want = "claimed"
			}
			if err == nil || blockedBy(t, err) != want {
				t.Fatalf("err=%v want blockedBy %s", err, want)
			}
			if name != "no claim" && f.be.claims["12"] != "mac-ben@3" {
				t.Fatalf("claim changed: %v", f.be.claims)
			}
			if len(f.be.notes) != 0 || len(f.host.spawned) != 0 {
				t.Fatalf("refused assign mutated state")
			}
		})
	}
}

type wakingClaimForge struct {
	remoteForge
	calls int
	now   time.Time
}

func (f *wakingClaimForge) OpenPRs(context.Context) ([]tracker.PR, error) {
	f.calls++
	if f.calls > 1 {
		return []tracker.PR{{Branch: "mac-ben/12-work", UpdatedAt: f.now}}, nil
	}
	return nil, nil
}
func TestAssignReclaimStaleRechecksAtClaim(t *testing.T) {
	f := newAssignFixture(t)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	f.be.claims = map[string]string{"12": "mac-ben@3"}
	f.be.claimTimes = map[string]map[string]time.Time{"12": {"mac-ben@3": now.Add(-5 * time.Hour)}}
	f.be.items["12"].Labels = append(f.be.items["12"].Labels, "claimed:mac-ben@mac")
	forge := &wakingClaimForge{remoteForge: remoteForge{f.be}, now: now}
	f.env.Forge = forge
	f.wire(func(w *worker.Env) { w.Now = func() time.Time { return now } })
	f.env.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		if args[0] == "ls-remote" {
			return git.Result{}, nil
		}
		return git.Exec(ctx, dir, args...)
	}
	_, err := f.assign("12", "dana", func(o *AssignOpts) { o.ReclaimStale = "mac-ben@3" })
	if err == nil || blockedBy(t, err) != "not-stale" {
		t.Fatalf("err=%v want not-stale after activity", err)
	}
	if f.be.claims["12"] != "mac-ben@3" || len(f.be.notes) != 0 {
		t.Fatalf("fresh holder changed: claims=%v notes=%v", f.be.claims, f.be.notes)
	}
}

func TestAssignReclaimStaleBestOf(t *testing.T) {
	f := bestOfFixture(t)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	f.be.claims = map[string]string{"12": "mac-ben@3"}
	f.be.claimTimes = map[string]map[string]time.Time{"12": {"mac-ben@3": now.Add(-5 * time.Hour)}}
	f.be.items["12"].Labels = append(f.be.items["12"].Labels, "claimed:mac-ben@mac")
	f.env.Forge = f.be.asForge()
	f.wire(func(w *worker.Env) { w.Now = func() time.Time { return now } })
	f.env.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		if args[0] == "ls-remote" {
			return git.Result{}, nil
		}
		return git.Exec(ctx, dir, args...)
	}
	res, err := f.assign("12", "", func(o *AssignOpts) { o.ReclaimStale = "mac-ben@3" })
	if err != nil {
		t.Fatalf("reclaim best-of: %v", err)
	}
	if len(res.BestOf) != 2 {
		t.Fatalf("attempts=%v want two", res.BestOf)
	}
	got := f.be.held("12")
	if len(got) != 2 || got[0] != "ben@1" || got[1] != "dana@1" {
		t.Fatalf("claims=%v want [ben@1 dana@1]", got)
	}
}
