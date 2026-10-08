package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/roundtick"
	"github.com/l4ci/rota/internal/worker"
)

// TestTickWithEveryAccountCoolingFillsNothingAndSaysWhy runs the real tick
// loop against the real quota cap (a cooling work.accounts pool read through its
// meters) and checks what `rota round tick` prints.
func TestTickWithEveryAccountCoolingFillsNothingAndSaysWhy(t *testing.T) {
	root := gitRepo(t)
	write(t, filepath.Join(root, ".rota", "config.json"),
		`{"work":{"dispatch":"herdr","accounts":[{"name":"a","configDir":"/nowhere"}]},"round":{"roster":["ben"]}}`)
	set, err := roundcfg.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	renv := round.Env{Now: func() time.Time { return now }, Accounts: &worker.Accounts{Now: func() time.Time { return now },
		Fetch: func(context.Context, string, string) (*jsonx.Object, string) {
			v, err := jsonx.Decode([]byte(fmt.Sprintf(`{"five_hour":{"utilization":100,"resets_at":"%s"}}`, now.Add(time.Hour).Format(time.RFC3339))))
			if err != nil {
				t.Fatal(err)
			}
			return v.(*jsonx.Object), ""
		}}}

	assigned := 0
	e := roundtick.Env{
		Cap:       1,
		Reconcile: func(context.Context) ([]string, []string, error) { return nil, nil, nil },
		Slots:     func() []roundtick.Slot { return []roundtick.Slot{{Name: "ben", State: "idle"}} },
		Candidates: func(context.Context) ([]roundtick.Candidate, error) {
			return []roundtick.Candidate{{ID: "#1", Ready: true}}, nil
		},
		Assign: func(context.Context, string) ([]string, error) { assigned++; return []string{"ben"}, nil },
		Capped: quotaCapped(renv, root, set),
	}
	r, err := roundtick.Run(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if assigned != 0 {
		t.Fatalf("a capped tick assigned %d", assigned)
	}
	if got := tickLines(r); !strings.HasPrefix(got, "capped\t") || !strings.Contains(got, "work.accounts") || !strings.Contains(got, "13:00") {
		t.Errorf("tick text %q", got)
	}
	if got, _ := tickData(r).Get("capped"); got == nil || got == "" {
		t.Errorf("tick data has no capped reason: %v", got)
	}
	if got := tickLines(roundtick.Result{}); got != "nothing to do" {
		t.Errorf("uncapped text %q", got)
	}
}
