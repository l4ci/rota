package plan

import "testing"

func TestNextUnit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		existing []string
		want     string
	}{
		{"empty", nil, "S01"},
		{"contiguous", []string{"S01", "S02"}, "S03"},
		{"gap takes the highest", []string{"S01", "S04"}, "S05"},
		{"unordered", []string{"S09", "S02"}, "S10"},
		{"suffix after the number", []string{"S03-extra"}, "S04"},
		{"non-slice names ignored", []string{"M01", "foo", "S"}, "S01"},
		{"past two digits", []string{"S99"}, "S100"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := nextUnit(tc.existing); got != tc.want {
				t.Fatalf("nextUnit(%v) = %q, want %q", tc.existing, got, tc.want)
			}
		})
	}
}
