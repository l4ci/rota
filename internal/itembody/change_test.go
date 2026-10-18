package itembody

import (
	"reflect"
	"testing"
)

func TestParseChange(t *testing.T) {
	tests := []struct {
		name string
		body string
		want Change
	}{
		{"absent", "## Goal\n\nx\n", Change{}},
		{"first line not a delta", "## Change\n\nTweak rota round assign\nServes: AC-1\n", Change{}},
		{"empty section", "## Change\n\n## Touches\n", Change{}},
		{"added", "## Change\n\nADDED rota round assign\nServes: AC-1\n",
			Change{Kind: "ADDED", Capability: "rota round assign", Serves: []string{"AC-1"}, OK: true}},
		{"modified lowercase", "## Change\n\nmodified Foo\n", Change{Kind: "MODIFIED", Capability: "foo", OK: true}},
		{"removed", "## Change\n\nREMOVED x.y\nReason: unused\nMigration: none needed\n",
			Change{Kind: "REMOVED", Capability: "x.y", Reason: "unused", Migration: "none needed", OK: true}},
		{"renamed", "## Change\n\nRENAMED old to new\n", Change{Kind: "RENAMED", Capability: "old to new", OK: true}},
		{"bullet markers", "## Change\n\n- ADDED a\n- Serves: AC-2\n* Reason: r\n",
			Change{Kind: "ADDED", Capability: "a", Serves: []string{"AC-2"}, Reason: "r", OK: true}},
		{"milestone prefix", "## Change\n\nADDED a\nServes: M08 AC-2\n",
			Change{Kind: "ADDED", Capability: "a", Serves: []string{"AC-2"}, OK: true}},
		{"two ids", "## Change\n\nADDED a\nServes: AC-1, M01 AC-3\n",
			Change{Kind: "ADDED", Capability: "a", Serves: []string{"AC-1", "AC-3"}, OK: true}},
		{"reason without migration", "## Change\n\nREMOVED a\nReason: gone\nMigration:\n",
			Change{Kind: "REMOVED", Capability: "a", Reason: "gone", OK: true}},
		{"capability normalised", "## Change\n\nMODIFIED `Rota Round Assign`\n",
			Change{Kind: "MODIFIED", Capability: "rota round assign", OK: true}},
		{"other lines ignored", "## Change\n\nADDED a\nBefore: x\nAfter: y\nprose\n",
			Change{Kind: "ADDED", Capability: "a", OK: true}},
		{"stops at next heading", "## Change\n\nADDED a\n\n## Touches\n\n- Serves: AC-9\n",
			Change{Kind: "ADDED", Capability: "a", OK: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseChange(tc.body); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}
