package cli

import (
	"slices"
	"testing"
)

func TestReportKeyItems(t *testing.T) {
	for _, tt := range []struct {
		name, branch, title string
		want                []string
	}{
		{"branch then title", "ben/proj-12-fix", "Resolve ENG-8 and PROJ-12", []string{"PROJ-12", "ENG-8"}},
		{"case and duplicate", "fix-proj-12", "PROJ-12 proj-12 ENG_2-09", []string{"PROJ-12", "ENG_2-09"}},
		{"token boundaries", "ben/2PROJ-12/PROJ-1234", "fix-PROJ-12 and PREFIX_PROJ-4", []string{"PROJ-1234", "PROJ-12", "PREFIX_PROJ-4"}},
		{"numbers and malformed", "ben/2132-feature", "close #2132; A--9; 9PROJ-1", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := reportKeyItems(tt.branch, tt.title); !slices.Equal(got, tt.want) {
				t.Fatalf("keys = %v, want %v", got, tt.want)
			}
		})
	}
}
