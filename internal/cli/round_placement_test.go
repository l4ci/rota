package cli

import (
	"context"
	"github.com/l4ci/rota/internal/config"
	"path/filepath"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/cfgdir"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/roundtick"
	"github.com/l4ci/rota/internal/worker"
)

// With every Claude account cooling and a later harness free, the tick does
// not cap: it assigns, and Assign picks the harness.
func TestTickAssignsOnTheFallbackHarness(t *testing.T) {
	root := gitRepo(t)
	write(t, filepath.Join(root, ".rota", "config.json"),
		`{"round":{"dispatch":"herdr","harnesses":["claude","codex"],"accounts":[{"name":"a","configDir":"/nowhere"}],"roster":["ben"]}}`)
	set, err := roundcfg.FromConfig(config.Load(cfgdir.OSEnv(), root))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	renv := round.Env{Worker: round.FromWorkerEnv(worker.Env{Now: func() time.Time { return now }, Accounts: &worker.Accounts{Env: worker.Env{Now: func() time.Time { return now }},
		Read: claudeReading(100, func() *time.Time { r := now.Add(time.Hour); return &r }(), 0)}})}
	assigned := 0
	r, err := roundtick.Run(context.Background(), roundtick.Env{
		Cap:       1,
		Reconcile: func(context.Context) (roundtick.Inspection, error) { return roundtick.Inspection{}, nil },
		Slots:     func() []roundtick.Slot { return []roundtick.Slot{{Name: "ben", State: "idle"}} },
		Candidates: func(context.Context) ([]round.Candidate, error) {
			return []round.Candidate{{ID: "#1"}}, nil
		},
		Assign: func(context.Context, string) ([]string, error) { assigned++; return []string{"ben"}, nil },
		Capped: quotaCapped(renv, root, set),
	})
	if err != nil {
		t.Fatal(err)
	}
	if assigned != 1 || r.Capped != "" {
		t.Fatalf("assigned %d, capped %q", assigned, r.Capped)
	}
}

// One quota reader serves every env of the process, so the Codex readings it
// caches outlive a single autopilot tick.
func TestRoundEnvsShareTheQuotaCache(t *testing.T) {
	_, deps := roundFixture(t, []host.Agent{})
	a := newRoundEnv(deps, deps.workerEnv(), &worker.Accounts{})
	b := newRoundEnv(deps, deps.workerEnv(), &worker.Accounts{})
	if a.Quotas == nil || a.Quotas != b.Quotas || a.Quotas != deps.meterQuotas() {
		t.Fatalf("quota readers differ: %p %p", a.Quotas, b.Quotas)
	}
}
