package itembody_test

import (
	"reflect"
	"testing"

	"github.com/l4ci/rota/internal/itembody"
)

func TestCheckTemplate(t *testing.T) {
	for _, tc := range []struct {
		name, kind, tag, body string
		want                  []string
	}{
		{"major", "features", "Major", "", []string{"required:Acceptance", "required:Out of scope", "recommended:Touches"}},
		{"minor", "features", "Minor", "", []string{"required:Acceptance", "required:Out of scope"}},
		{"cosmetic", "features", "Cosmetic", "", []string{"required:Acceptance", "required:Out of scope"}},
		{"untagged feature", "features", "", "", []string{"required:Acceptance", "required:Out of scope"}},
		{"headings only", "features", "Major", "# ACCEPTANCE\n### out OF scope\n###### TOUCHES\n", nil},
		{"criteria grammar", "features", "Minor", "### User acceptance criteria\n## Out of scope\n", nil},
		{"checkbox is no heading", "features", "Minor", "- [ ] works\n## Out of scope\n", []string{"required:Acceptance"}},
		{"bug", "bugs", "P1", "", []string{"recommended:Repro", "recommended:Expected", "recommended:Actual"}},
		{"bug missing expected actual", "bugs", "P0", "## REPRO\n", []string{"recommended:Expected", "recommended:Actual"}},
		{"bug headings", "bugs", "P3", "# Repro\n### EXPECTED\n###### actual\n", nil},
		{"bug description", "bugs", "P2", "Repro: click save. Expected: saved. Actual: crashes.", nil},
		{"slash labels are not files", "bugs", "P2", "Repro: save. Expected/actual: saved/crashes.", nil},
		{"URL is not a file pointer", "tasks", "", "See https://example.com/docs/item.md", nil},
		{"task", "tasks", "", "", nil},
		{"files need pointers", "tasks", "", "Update internal/cli/item.go", []string{"recommended:Pointers"}},
		{"pointers present", "tasks", "", "Update item.go\n### POINTERS\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, missing := range itembody.CheckTemplate(tc.kind, tc.tag, tc.body) {
				got = append(got, missing.Level+":"+missing.Name)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("missing sections = %v, want %v", got, tc.want)
			}
		})
	}
}
