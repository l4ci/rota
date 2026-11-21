package verdict

import (
	"slices"
	"testing"

	"github.com/l4ci/rota/internal/config"
)

func TestCoverageAt(t *testing.T) {
	const head, old = "bbbbbbb1234", "aaaaaaa1234"
	rec := func(kind, sha, harness string) Record {
		return Record{Kind: kind, Verdict: Pass, Sha: sha, Harness: harness, Model: "m"}
	}
	onlyA := func(h string) bool { return h == "hx-a" }
	lens := []LensNeed{{Kind: LensKind("security"), Why: "touches auth"}}
	spec, qual := ReviewSpec, ReviewQuality
	for _, c := range []struct {
		name   string
		need   Need
		list   []Record
		core   []string
		cross  bool
		lenses []string
		kinds  []string
	}{
		{"full spec none", Need{Depth: config.DepthFull, HasSpec: true}, nil, []string{spec, qual}, false, nil, []string{spec, qual}},
		{"full only quality", Need{Depth: config.DepthFull}, []Record{rec(qual, head, "")}, []string{spec}, false, nil, []string{spec, qual}},
		{"full both", Need{Depth: config.DepthFull, HasSpec: true}, []Record{rec(spec, head, ""), rec(qual, head, "")}, nil, false, nil, []string{spec, qual}},
		{"light spec", Need{Depth: config.DepthLight, HasSpec: true}, nil, []string{spec}, false, nil, []string{spec}},
		{"light nospec has spec", Need{Depth: config.DepthLight}, []Record{rec(spec, head, "")}, []string{qual}, false, nil, []string{qual}},
		{"stale core record", Need{Depth: config.DepthLight, HasSpec: true}, []Record{rec(spec, old, "")}, []string{spec}, false, nil, []string{spec}},
		{"none asks nothing", Need{Depth: config.DepthNone, Cross: true, CrossAccept: onlyA, Lenses: lens}, nil, nil, false, nil, nil},
		{"unknown depth", Need{Depth: config.ReviewDepth("bogus"), HasSpec: true}, nil, nil, false, nil, nil},
		{"cross missing", Need{Depth: config.DepthLight, HasSpec: true, Cross: true, CrossAccept: onlyA}, []Record{rec(spec, head, "")}, nil, true, nil, []string{spec, ReviewCross}},
		{"cross from other harness", Need{Depth: config.DepthLight, HasSpec: true, Cross: true, CrossAccept: onlyA}, []Record{rec(spec, head, ""), rec(ReviewCross, head, "hx-b")}, nil, true, nil, []string{spec, ReviewCross}},
		{"cross satisfied", Need{Depth: config.DepthLight, HasSpec: true, Cross: true, CrossAccept: onlyA}, []Record{rec(spec, head, ""), rec(ReviewCross, head, "hx-a")}, nil, false, nil, []string{spec, ReviewCross}},
		{"cross FAIL covers nothing", Need{Depth: config.DepthLight, HasSpec: true, Cross: true, CrossAccept: onlyA}, []Record{rec(spec, head, ""), {Kind: ReviewCross, Verdict: Fail, Sha: head, Harness: "hx-a", Model: "m"}}, nil, true, nil, []string{spec, ReviewCross}},
		{"cross nil accept takes any", Need{Depth: config.DepthLight, HasSpec: true, Cross: true}, []Record{rec(spec, head, ""), rec(ReviewCross, head, "hx-z")}, nil, false, nil, []string{spec, ReviewCross}},
		{"lens missing", Need{Depth: config.DepthLight, HasSpec: true, Lenses: lens}, []Record{rec(spec, head, "")}, nil, false, []string{LensKind("security")}, []string{spec, LensKind("security")}},
		{"lens stale", Need{Depth: config.DepthLight, HasSpec: true, Lenses: lens}, []Record{rec(spec, head, ""), rec(LensKind("security"), old, "")}, nil, false, []string{LensKind("security")}, []string{spec, LensKind("security")}},
		{"lens recorded", Need{Depth: config.DepthLight, HasSpec: true, Lenses: lens}, []Record{rec(spec, head, ""), rec(LensKind("security"), head, "")}, nil, false, nil, []string{spec, LensKind("security")}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := CoverageAt(c.list, head, c.need)
			if !slices.Equal(got.Core, c.core) || got.Cross != c.cross || !slices.Equal(got.Lenses, c.lenses) {
				t.Errorf("CoverageAt = %+v, want core %v cross %v lenses %v", got, c.core, c.cross, c.lenses)
			}
			if want := len(c.core) == 0 && !c.cross && len(c.lenses) == 0; got.Covered() != want {
				t.Errorf("Covered() = %v, want %v", got.Covered(), want)
			}
			if k := c.need.Kinds(); !slices.Equal(k, c.kinds) {
				t.Errorf("Kinds() = %v, want %v", k, c.kinds)
			}
		})
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
