package verdict

import (
	"slices"
	"testing"

	"github.com/l4ci/rota/internal/config"
)

func TestMissingReviews(t *testing.T) {
	spec, qual := ReviewSpec, ReviewQuality
	for _, c := range []struct {
		name    string
		depth   config.ReviewDepth
		hasSpec bool
		have    []string
		want    []string
	}{
		{"full spec none", config.DepthFull, true, nil, []string{spec, qual}},
		{"full nospec none", config.DepthFull, false, nil, []string{spec, qual}},
		{"full spec only spec", config.DepthFull, true, []string{spec}, []string{qual}},
		{"full only quality", config.DepthFull, false, []string{qual}, []string{spec}},
		{"full both", config.DepthFull, true, []string{spec, qual}, nil},
		{"light spec none", config.DepthLight, true, nil, []string{spec}},
		{"light spec has quality", config.DepthLight, true, []string{qual}, []string{spec}},
		{"light spec has spec", config.DepthLight, true, []string{spec}, nil},
		{"light nospec none", config.DepthLight, false, nil, []string{qual}},
		{"light nospec has spec", config.DepthLight, false, []string{spec}, []string{qual}},
		{"light nospec has quality", config.DepthLight, false, []string{qual}, nil},
		{"none", config.DepthNone, true, nil, nil},
		{"unknown depth", config.ReviewDepth("bogus"), true, nil, nil},
		{"extra kinds ignored", config.DepthLight, true, []string{ReviewCross, spec}, nil},
	} {
		if got := MissingReviews(c.depth, c.hasSpec, c.have); !slices.Equal(got, c.want) {
			t.Errorf("%s: MissingReviews(%s, %v, %v) = %v, want %v", c.name, c.depth, c.hasSpec, c.have, got, c.want)
		}
	}
}

func TestCoveredDepth(t *testing.T) {
	spec, qual := ReviewSpec, ReviewQuality
	for _, c := range []struct {
		name    string
		hasSpec bool
		have    []string
		want    config.ReviewDepth
	}{
		{"both", true, []string{spec, qual}, config.DepthFull},
		{"spec only with spec", true, []string{spec}, config.DepthLight},
		{"quality only with spec", true, []string{qual}, config.DepthNone},
		{"quality only no spec", false, []string{qual}, config.DepthLight},
		{"spec only no spec", false, []string{spec}, config.DepthNone},
		{"nothing", true, nil, config.DepthNone},
	} {
		if got := CoveredDepth(c.have, c.hasSpec); got != c.want {
			t.Errorf("%s: CoveredDepth(%v, %v) = %q, want %q", c.name, c.have, c.hasSpec, got, c.want)
		}
	}
}

func TestHasSpec(t *testing.T) {
	for issue, want := range map[string]bool{"1819": true, "#1819": true, "": false, "park": false} {
		if got := HasSpec(issue); got != want {
			t.Errorf("HasSpec(%q) = %v, want %v", issue, got, want)
		}
	}
}

func TestDepthRose(t *testing.T) {
	none, light, full := config.DepthNone, config.DepthLight, config.DepthFull
	for _, c := range []struct {
		name              string
		covered, required config.ReviewDepth
		want              bool
	}{
		{"none to light", none, light, true},
		{"none to full", none, full, true},
		{"light to full", light, full, true},
		{"same", light, light, false},
		{"full to light", full, light, false},
		{"light to none", light, none, false},
		{"unknown covered", "", full, false},
	} {
		if got := DepthRose(c.covered, c.required); got != c.want {
			t.Errorf("%s: DepthRose(%q, %q) = %v, want %v", c.name, c.covered, c.required, got, c.want)
		}
	}
}
