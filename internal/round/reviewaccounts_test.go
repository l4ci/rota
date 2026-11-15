package round

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/harness"
)

// A codex login kept for review (#3315): round.accounts holds only claude.
const reviewOnlyCfg = `{"review":{"accounts":[{"kind":"codex","name":"r1","dir":"/hr1"}]},"round":{"dispatch":"herdr","harnesses":["claude","codex"],"accounts":[{"name":"a","configDir":"/nowhere"}],"tiers":{"codex":{"light":"c-l","standard":"c-s","heavy":"c-h"}}}}`

// AC-2: an account listed only in review.accounts never gets a worker issue,
// however much headroom it has.
func TestPlaceNeverUsesAReviewOnlyAccount(t *testing.T) {
	f := newAssignFixture(t)
	f.pool(t, reviewOnlyCfg, map[string]float64{"a": 95, "r1": 0})
	pf, err := f.place(t, nil)
	wantPlacement(t, pf, err, harness.Claude, "a", true)
}

// AC-2: a harness: label naming a harness whose only logins are review
// accounts refuses, and the hint names round.accounts.
func TestPlaceRefusesAHarnessLabelWithNoWorkerAccount(t *testing.T) {
	f := newAssignFixture(t)
	f.pool(t, reviewOnlyCfg, map[string]float64{"a": 5, "r1": 0})
	f.labelled("12", "harness:codex")
	_, err := f.place(t, nil)
	if by := blockedBy(t, err); by != blockedby.HarnessLabel || !strings.Contains(err.Error(), "round.accounts") {
		t.Fatalf("err %v, want a harness label refusal naming round.accounts", err)
	}
	// --kind is the same pin.
	_, err = f.place(t, func(in *preflightIn) { in.FlagKind = harness.Codex })
	if by := blockedBy(t, err); by != blockedby.HarnessLabel {
		t.Fatalf("--kind err %v", err)
	}
}

// A harness with a round.accounts entry still takes workers beside a review
// login of the same kind.
func TestPlaceKeepsAWorkerAccountBesideAReviewAccount(t *testing.T) {
	f := newAssignFixture(t)
	f.pool(t, strings.Replace(poolCfg, `"round":`, `"review":{"accounts":[{"kind":"codex","name":"r1","dir":"/hr1"}]},"round":`, 1), map[string]float64{"a": 90, "c1": 20, "c2": 60, "r1": 0})
	pf, err := f.place(t, nil)
	wantPlacement(t, pf, err, harness.Codex, "c1", false)
}

// The metered reviewer pool carries review.accounts and round.accounts as two
// lists, with the harness's meters and no review-only kind in the fallback.
func TestCrossPoolsReadsBothListsWithMeters(t *testing.T) {
	f := newAssignFixture(t)
	f.pool(t, reviewOnlyCfg, map[string]float64{"a": 40, "r1": 70})
	got, err := f.env.CrossPools(bg, f.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Review) != 1 || got.Review[0].Name != "r1" || got.Review[0].Kind != harness.Codex || got.Review[0].Meter.Headroom == nil || *got.Review[0].Meter.Headroom != 30 {
		t.Fatalf("review pool %+v, want r1 with headroom 30", got.Review)
	}
	if got.Review[0].Meter.ConfigDir != "/hr1" {
		t.Errorf("review dir %q, want /hr1", got.Review[0].Meter.ConfigDir)
	}
	if len(got.Main) != 1 || got.Main[0].Name != "a" || got.Main[0].Meter.Headroom == nil || *got.Main[0].Meter.Headroom != 60 {
		t.Fatalf("main pool %+v, want only claude a with headroom 60 (codex is review-only)", got.Main)
	}
}

// AC-2 (#3339): when every harness in round.harnesses has only review logins,
// placement refuses with the round.accounts hint instead of falling back to the
// default harness.
func TestPlaceRefusesWhenEveryHarnessIsReviewOnly(t *testing.T) {
	f := newAssignFixture(t)
	cfg := strings.Replace(reviewOnlyCfg, `"harnesses":["claude","codex"]`, `"harnesses":["codex"]`, 1)
	f.pool(t, cfg, map[string]float64{"a": 95, "r1": 0})
	pf, err := f.place(t, nil)
	if err == nil {
		t.Fatalf("placed on %s/%+v, want a refusal", pf.Kind, pf.Placement)
	}
	if by := blockedBy(t, err); by != blockedby.Quota || !strings.Contains(err.Error(), "round.accounts") {
		t.Fatalf("err %v (blockedBy %q), want a refusal naming round.accounts", err, by)
	}
}
