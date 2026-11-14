package roundtick

import (
	"context"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/round"
)

func bcand(id, tag string, declared ...string) round.Candidate {
	return round.Candidate{ID: id, Tag: tag, Scopes: declared, Declared: declared}
}

// bundleFake records AssignBundle calls beside the single assigns.
func bundleFake(cands ...round.Candidate) (*fake, *[]string, Env) {
	f := &fake{
		slots: []Slot{{Name: "ben", State: "idle"}, {Name: "dana", State: "idle"}},
		cands: cands,
	}
	var bundles []string
	e := f.env()
	e.AutoBundle = true
	e.BundleMax = 3
	e.AssignBundle = func(_ context.Context, lead string, members []string) ([]string, error) {
		bundles = append(bundles, lead+"+"+strings.Join(members, ","))
		return []string{"ben"}, nil
	}
	return f, &bundles, e
}

func TestTickBundlesStrictlyRelatedItems(t *testing.T) {
	f, bundles, e := bundleFake(bcand("12", "Minor", "a.go"), bcand("10", "Minor", "a.go", "b.go"), bcand("11", "Cosmetic", "b.go"), bcand("40", "Minor", "z.go"))
	r, err := Run(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(*bundles, ";"); got != "10+11,12" {
		t.Fatalf("bundles %q, want lead 10 with members 11,12", got)
	}
	if strings.Join(f.assigned, ",") != "40" {
		t.Errorf("singles %v, want only the unrelated 40 on the second slot", f.assigned)
	}
	var named bool
	for _, a := range r.Did {
		if a.Target == "10" && strings.Contains(a.Detail, "bundle") && strings.Contains(a.Detail, "+11") && strings.Contains(a.Detail, "a.go") && strings.Contains(a.Detail, "b.go") {
			named = true
		}
	}
	if !named {
		t.Errorf("tick output must name the bundle and shared paths: %+v", r.Did)
	}
}

func TestTickAssignsSinglesWhenBundlingIsOffOrUnsafe(t *testing.T) {
	// Off.
	f, bundles, e := bundleFake(bcand("10", "Minor", "a.go"), bcand("11", "Minor", "a.go"))
	e.AutoBundle = false
	if _, err := Run(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if len(*bundles) != 0 || strings.Join(f.assigned, ",") != "10,11" {
		t.Errorf("off: bundles %v singles %v", *bundles, f.assigned)
	}
	// A Major item never joins.
	f, bundles, e = bundleFake(bcand("10", "Major", "a.go"), bcand("11", "Minor", "a.go"))
	if _, err := Run(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if len(*bundles) != 0 || strings.Join(f.assigned, ",") != "10,11" {
		t.Errorf("major: bundles %v singles %v", *bundles, f.assigned)
	}
}

func TestTickFallsBackToSinglesWhenAssignRefusesTheBundle(t *testing.T) {
	f, _, e := bundleFake(bcand("10", "Minor", "a.go"), bcand("11", "Minor", "a.go"))
	e.AssignBundle = func(context.Context, string, []string) ([]string, error) {
		return nil, &round.BlockedError{By: blockedby.Bundle, Msg: "refused"}
	}
	if _, err := Run(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.assigned, ",") != "10,11" {
		t.Errorf("singles %v, want 10,11 after the refusal", f.assigned)
	}
}

func TestTickRefusedBundleKeepsMembersThatPrecededTheLead(t *testing.T) {
	// Backlog order puts 11 before its lead 10.
	f, _, e := bundleFake(bcand("11", "Minor", "a.go"), bcand("10", "Minor", "a.go"))
	e.AssignBundle = func(context.Context, string, []string) ([]string, error) {
		return nil, &round.BlockedError{By: blockedby.Bundle, Msg: "refused"}
	}
	if _, err := Run(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.assigned, ",") != "10,11" {
		t.Errorf("singles %v, want 10,11", f.assigned)
	}
}
