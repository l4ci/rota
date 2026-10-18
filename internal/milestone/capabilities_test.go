package milestone

import (
	"reflect"
	"testing"
)

func TestCapabilities(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []Capability
	}{
		{"no section", "## Goal\nx\n", nil},
		{"empty section", "## Capabilities\n\n## Goal\n- a\n", nil},
		{"backticked new", "## Capabilities\n\n- `Rota Round Assign` (New)\n", []Capability{{"rota round assign", "new"}}},
		{"no tag is modified", "## Capabilities\n- Foo Bar\n", []Capability{{"foo bar", "modified"}}},
		{"upper-case removed", "## Capabilities\n* `Gone` (REMOVED)\n", []Capability{{"gone", "removed"}}},
		{"spaces in tag", "## Capabilities\n- thing  ( modified )\n", []Capability{{"thing", "modified"}}},
		{"duplicate keeps first", "## Capabilities\n- a (new)\n- A (removed)\n", []Capability{{"a", "new"}}},
		{"placeholder skipped", "## Capabilities\n\n- _(one contract per bullet)_\n", nil},
		{"sorted", "## Capabilities\n- b\n- a (new)\n- c (removed)\n", []Capability{{"a", "new"}, {"b", "modified"}, {"c", "removed"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Capabilities(tc.body)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Capabilities = %v want %v", got, tc.want)
			}
		})
	}
}

func TestCapabilityNames(t *testing.T) {
	got := CapabilityNames([]Capability{{"a", "new"}, {"b", "removed"}})
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("CapabilityNames = %v", got)
	}
}
