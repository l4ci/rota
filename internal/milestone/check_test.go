package milestone

import (
	"reflect"
	"testing"
)

const checkBody = "# M01\n\n## Capabilities\n\n- `rota round assign` (modified)\n- round.maxBounces (new)\n\n## Acceptance\n\n- [ ] AC-1: one\n- [ ] AC-2: two\n"

func feat(id, change string) Child { return Child{ID: id, Type: "F", Body: change} }

// three returns three clean features serving AC-1 and AC-2, then overrides.
func three(over ...Child) []Child {
	cs := []Child{
		feat("F1", "## Change\n\nADDED round.maxBounces\nServes: AC-1\n"),
		feat("F2", "## Change\n\nMODIFIED rota round assign\nServes: AC-2\n"),
		feat("F3", "## Change\n\nMODIFIED rota round assign\nServes: AC-2\n"),
	}
	for _, o := range over {
		for i := range cs {
			if cs[i].ID == o.ID {
				cs[i] = o
			}
		}
	}
	return cs
}

func TestCheck(t *testing.T) {
	tests := []struct {
		name     string
		children []Child
		strict   bool
		check    func(t *testing.T, r Report)
	}{
		{"clean", three(), true, func(t *testing.T, r Report) {
			if !r.Ok() || r.BelowThreshold || r.Features != 3 || len(r.Uncovered) != 0 {
				t.Errorf("%+v", r)
			}
		}},
		{"no change section", three(feat("F3", "plain body")), false, func(t *testing.T, r Report) {
			if r.Ok() || !reflect.DeepEqual(r.NoChange, []string{"F3"}) {
				t.Errorf("%+v", r)
			}
		}},
		{"unknown id", three(feat("F3", "## Change\n\nADDED round.maxBounces\nServes: AC-2, AC-7\n")), false, func(t *testing.T, r Report) {
			if r.Ok() || !reflect.DeepEqual(r.Unknown, []Unknown{{ID: "F3", IDs: []string{"AC-7"}}}) {
				t.Errorf("%+v", r)
			}
		}},
		{"removed without reason and migration", three(feat("F3", "## Change\n\nREMOVED round.maxBounces\nServes: AC-2\n")), false, func(t *testing.T, r Report) {
			if r.Ok() || !reflect.DeepEqual(r.NoMigration, []Missing{{ID: "F3", Fields: []string{"Reason", "Migration"}}}) {
				t.Errorf("%+v", r)
			}
		}},
		{"removed without migration", three(feat("F3", "## Change\n\nREMOVED round.maxBounces\nServes: AC-2\nReason: x\n")), false, func(t *testing.T, r Report) {
			if r.Ok() || !reflect.DeepEqual(r.NoMigration, []Missing{{ID: "F3", Fields: []string{"Migration"}}}) {
				t.Errorf("%+v", r)
			}
		}},
		{"change capability not listed", three(feat("F3", "## Change\n\nMODIFIED POST /items\nServes: AC-2\n")), false, func(t *testing.T, r Report) {
			if r.Ok() || !reflect.DeepEqual(r.BadCapability, []BadCapability{{ID: "F3", Capability: "post /items"}}) {
				t.Errorf("%+v", r)
			}
		}},
		{"removed with empty migration line", three(feat("F3", "## Change\n\nREMOVED round.maxBounces\nServes: AC-2\nReason: dropped\nMigration:\n")), false, func(t *testing.T, r Report) {
			if r.Ok() || !reflect.DeepEqual(r.NoMigration, []Missing{{ID: "F3", Fields: []string{"Migration"}}}) {
				t.Errorf("%+v", r)
			}
		}},
		{"renamed names the old capability", three(feat("F3", "## Change\n\nRENAMED rota round assign to rota round pick\nServes: AC-2\n")), false, func(t *testing.T, r Report) {
			if !r.Ok() || len(r.BadCapability) != 0 {
				t.Errorf("%+v", r)
			}
		}},
		{"renamed with unlisted old capability", three(feat("F3", "## Change\n\nRENAMED rota round gone to rota round pick\nServes: AC-2\n")), false, func(t *testing.T, r Report) {
			if r.Ok() || !reflect.DeepEqual(r.BadCapability, []BadCapability{{ID: "F3", Capability: "rota round gone"}}) {
				t.Errorf("%+v", r)
			}
		}},
		{"bad touches", three(feat("F3", "## Change\n\nADDED round.maxBounces\nServes: AC-2\n\n## Touches\n\n- POST /items\n")), false, func(t *testing.T, r Report) {
			if r.Ok() || !reflect.DeepEqual(r.BadTouches, []BadTouches{{ID: "F3", Entries: []string{"post /items"}}}) {
				t.Errorf("%+v", r)
			}
		}},
		{"touches copy of capability is fine", three(feat("F3", "## Change\n\nADDED round.maxBounces\nServes: AC-2\n\n## Touches\n\n- `Rota Round Assign`\n")), true, func(t *testing.T, r Report) {
			if !r.Ok() || len(r.BadTouches) != 0 {
				t.Errorf("%+v", r)
			}
		}},
		{"uncovered is a warning", three(feat("F2", "## Change\n\nADDED round.maxBounces\nServes: AC-1\n"), feat("F3", "## Change\n\nADDED round.maxBounces\nServes: AC-1\n")), false, func(t *testing.T, r Report) {
			if !r.Ok() || !reflect.DeepEqual(r.Uncovered, []string{"AC-2"}) {
				t.Errorf("%+v", r)
			}
		}},
		{"uncovered fails strict", three(feat("F2", "## Change\n\nADDED round.maxBounces\nServes: AC-1\n"), feat("F3", "## Change\n\nADDED round.maxBounces\nServes: AC-1\n")), true, func(t *testing.T, r Report) {
			if r.Ok() || !r.Strict || !reflect.DeepEqual(r.Uncovered, []string{"AC-2"}) {
				t.Errorf("%+v", r)
			}
		}},
		{"bug child ignored", append(three(), Child{ID: "B1", Type: "B", Body: "no change"}), false, func(t *testing.T, r Report) {
			if !r.Ok() || r.Features != 3 || len(r.NoChange) != 0 {
				t.Errorf("%+v", r)
			}
		}},
		{"two features below threshold", three()[:2], true, func(t *testing.T, r Report) {
			if !r.BelowThreshold || r.Features != 2 || !r.Ok() ||
				!reflect.DeepEqual(r.Criteria, []string{"AC-1", "AC-2"}) {
				t.Errorf("%+v", r)
			}
		}},
		{"two features with faults still below", []Child{feat("F1", "x"), feat("F2", "y")}, true, func(t *testing.T, r Report) {
			if !r.BelowThreshold || len(r.NoChange) != 0 || len(r.Uncovered) != 0 {
				t.Errorf("%+v", r)
			}
		}},
		{"slices non-nil", three()[:1], false, func(t *testing.T, r Report) {
			if r.Criteria == nil || r.Uncovered == nil || r.NoChange == nil || r.Unknown == nil ||
				r.NoMigration == nil || r.BadTouches == nil || r.BadCapability == nil {
				t.Errorf("nil slice in %+v", r)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.check(t, Check(checkBody, tc.children, tc.strict))
		})
	}
}
