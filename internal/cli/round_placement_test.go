package cli

import (
	"context"
	"fmt"
	"github.com/l4ci/rota/internal/config"
	"path/filepath"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/cfgdir"
	"github.com/l4ci/rota/internal/jsonx"
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
		Fetch: func(context.Context, string, string) (*jsonx.Object, string) {
			v, err := jsonx.Decode([]byte(fmt.Sprintf(`{"five_hour":{"utilization":100,"resets_at":"%s"}}`, now.Add(time.Hour).Format(time.RFC3339))))
			if err != nil {
				t.Fatal(err)
			}
			return v.(*jsonx.Object), ""
		}}})}
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

// The tick says where an assign moved a slot, and how far ahead the account was.
func TestTickPlacementLine(t *testing.T) {
	gap, head := 60.0, 80.0
	got := placementLine("12", &round.Placement{Kind: "codex", Account: "c1", Headroom: &head, Gap: &gap, Margin: 20})
	if want := "moved\t#12: c1 (codex, headroom 80%) moved, best is 60 points ahead (margin 20)"; got != want {
		t.Errorf("line %q, want %q", got, want)
	}
}
