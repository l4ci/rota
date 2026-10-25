package round

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/worker"
)

func TestStatusReportsStaleClaim(t *testing.T) {
	for _, tc := range []struct {
		name  string
		local bool
		age   time.Duration
	}{
		{"remote", false, 26 * time.Hour}, {"local", true, 26 * time.Hour}, {"fresh", false, time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newRepo(t, nil)
			now := time.Date(2026, 10, 25, 11, 0, 0, 0, time.UTC)
			f := &fakeRemote{claims: map[string]string{"7": "mac-ben@3"}, claimTimes: map[string]map[string]time.Time{"7": {"mac-ben@3": now.Add(-tc.age)}}}
			f.add("7", "old claim", "", false, "")
			e := env(nil, f.asForge())
			e.Board = f
			originalGit := e.Git
			e.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
				if len(args) > 0 && args[0] == "ls-remote" {
					return git.Result{}, nil
				}
				return originalGit(ctx, dir, args...)
			}
			e.Worker = wk(worker.Env{Now: func() time.Time { return now }})
			e.Round.ClaimStaleMinutes = 240
			if tc.local {
				writeRegistry(t, root, slot(root, "mac-ben", "mac-ben/7-old", func(o *jsonx.Object) { o.Set("task", "7"); o.Set("claimId", "mac-ben@3") }))
			}
			rep, err := e.Status(bg, root)
			if err != nil {
				t.Fatal(err)
			}
			var stale []Finding
			for _, finding := range rep.Findings {
				if finding.Kind == "stale-claim" {
					stale = append(stale, finding)
				}
			}
			if tc.local || tc.age < 4*time.Hour {
				if len(stale) != 0 {
					t.Fatalf("nonstale findings = %+v, want none", stale)
				}
				return
			}
			if len(stale) != 1 {
				t.Fatalf("findings = %+v, want one stale-claim", rep.Findings)
			}
			if stale[0].Issue != "7" || stale[0].Repair != "" || !strings.Contains(stale[0].Detail, "mac-ben@3") || !strings.Contains(stale[0].Detail, "2026-10-24T09:00:00Z") {
				t.Fatalf("finding = %+v", stale[0])
			}
			if _, err := e.Reconcile(bg, root, true); err != nil {
				t.Fatal(err)
			}
			st, err := f.Status("7")
			if err != nil || st.Claim != "mac-ben@3" {
				t.Fatalf("claim after reconcile = %+v, err %v", st, err)
			}
		})
	}
}
