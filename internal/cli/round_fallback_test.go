package cli

import (
	"context"
	"fmt"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/rotatree"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/limits"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/roundtick"
	"github.com/l4ci/rota/internal/worker"
)

// codexFirst is a config whose first harness (codex) has one login and whose
// second (claude) has no pool, so a spent login makes claude the fallback.
const codexFirst = `{"round":{"harnesses":["codex","claude"],"accounts":[{"kind":"codex","name":"c1","dir":"/h1"}]}}`

func spendLogin(t *testing.T, root string, resets time.Time) {
	t.Helper()
	addLimit(t, root, limits.Entry{Session: "ben", Kind: harness.Codex, Account: "c1", Source: limits.SourceText, Window: limits.WindowUnknown,
		DetectedAt: limits.Time(time.Now()), ResetsAt: limits.Time(resets), Action: limits.ActionSleep, Status: limits.StatusWaiting})
}

func TestRoundStatusShowsTheFallback(t *testing.T) {
	root, deps := roundFixture(t, []host.Agent{})
	if err := os.WriteFile(filepath.Join(root, ".rota", "config.json"), []byte(codexFirst), 0o644); err != nil {
		t.Fatal(err)
	}
	_, out, _ := rotaInWith(t, deps, root, "--json", "round", "status")
	if _, ok := data(t, out)["fallback"]; ok {
		t.Fatalf("no fallback while the first harness has headroom: %s", out)
	}
	resets := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	spendLogin(t, root, resets)
	_, out, _ = rotaInWith(t, deps, root, "--json", "round", "status")
	fb, _ := data(t, out)["fallback"].(map[string]any)
	if fb == nil || fb["from"] != harness.Codex || fb["to"] != harness.Claude || fb["resetsAt"] != resets.Format(time.RFC3339) {
		t.Fatalf("fallback = %v", fb)
	}
	_, text, _ := rotaInWith(t, deps, root, "round", "status")
	if !strings.Contains(text, "fallback\tcodex -> claude until "+resets.Format("2006-01-02 15:04 MST")) {
		t.Errorf("text: %q", text)
	}
}

func TestRoundCandidatesShowTheFallback(t *testing.T) {
	root, deps := roundFixture(t, []host.Agent{})
	if err := os.WriteFile(filepath.Join(root, ".rota", "config.json"), []byte(codexFirst), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".rota", "BACKLOG.md"), []byte("# TODO\n\n## Tasks\n\n## Completed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".rota", "counters.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resets := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	spendLogin(t, root, resets)
	code, out, errOut := rotaInWith(t, deps, root, "--json", "round", "candidates")
	if code != 0 {
		t.Fatalf("candidates: %d %s %s", code, out, errOut)
	}
	fb, _ := data(t, out)["fallback"].(map[string]any)
	if fb == nil || fb["from"] != harness.Codex || fb["to"] != harness.Claude || fb["resetsAt"] != resets.Format(time.RFC3339) {
		t.Fatalf("fallback = %v", fb)
	}
	_, text, _ := rotaInWith(t, deps, root, "round", "candidates")
	if !strings.HasPrefix(text, "fallback\tnew work goes to claude; codex accounts reset at "+resets.Format("2006-01-02 15:04 MST")) {
		t.Errorf("text: %q", text)
	}
}

// With every Claude account cooling and a later harness free, the tick does
// not cap: it assigns, and Assign picks the harness.
func TestTickAssignsOnTheFallbackHarness(t *testing.T) {
	root := gitRepo(t)
	write(t, filepath.Join(root, ".rota", "config.json"),
		`{"round":{"dispatch":"herdr","harnesses":["claude","codex"],"accounts":[{"name":"a","configDir":"/nowhere"}],"roster":["ben"]}}`)
	set, err := roundcfg.FromConfig(config.Load(rotatree.Config(root)))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	renv := round.Env{Worker: worker.Env{Now: func() time.Time { return now }, Accounts: &worker.Accounts{Env: worker.Env{Now: func() time.Time { return now }},
		Fetch: func(context.Context, string, string) (*jsonx.Object, string) {
			v, err := jsonx.Decode([]byte(fmt.Sprintf(`{"five_hour":{"utilization":100,"resets_at":"%s"}}`, now.Add(time.Hour).Format(time.RFC3339))))
			if err != nil {
				t.Fatal(err)
			}
			return v.(*jsonx.Object), ""
		}}}}
	assigned := 0
	r, err := roundtick.Run(context.Background(), roundtick.Env{
		Cap:       1,
		Reconcile: func(context.Context) ([]string, []string, error) { return nil, nil, nil },
		Slots:     func() []roundtick.Slot { return []roundtick.Slot{{Name: "ben", State: "idle"}} },
		Candidates: func(context.Context) ([]roundtick.Candidate, error) {
			return []roundtick.Candidate{{ID: "#1", Ready: true}}, nil
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

// The tick says which harness an assign moved to, and why.
func TestTickFallbackLine(t *testing.T) {
	got := fallbackLine("12", &round.Fallback{From: "claude", To: "codex", Why: "claude accounts are cooling down"})
	if want := "fallback\t#12: claude -> codex: claude accounts are cooling down"; got != want {
		t.Errorf("line %q, want %q", got, want)
	}
}
