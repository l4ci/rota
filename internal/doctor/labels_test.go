package doctor

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/labels"
)

func TestLabelRegistryNotice(t *testing.T) {
	want := labels.Label{Name: "required", Description: "A required label.", Color: "123456"}
	for _, tc := range []struct {
		name           string
		have           []labels.Label
		metadata       bool
		problem        string
		status, detail string
	}{
		{"missing", nil, true, "", Warn, "missing: required"},
		{"undescribed", []labels.Label{{Name: "required", Color: want.Color}}, true, "", Warn, "undescribed: required"},
		{"current", []labels.Label{want}, true, "", Pass, "present"},
		{"nonempty metadata", []labels.Label{{Name: "required", Description: "custom", Color: "ffffff"}}, true, "", Pass, "present"},
		{"name only", []labels.Label{{Name: "required"}}, false, "", Pass, "present"},
		{"unreachable", nil, true, "offline", Skip, "offline"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check := labelCheck(LabelState{Entries: []labels.Entry{{Label: want}}, Existing: tc.have, Metadata: tc.metadata, Problem: tc.problem})
			if check.Name != "labels" || check.Status != tc.status || !strings.Contains(check.Detail, tc.detail) {
				t.Fatalf("check = %+v, want %s containing %q", check, tc.status, tc.detail)
			}
			if check.Status == Warn && check.Hint != "run: rota labels sync" {
				t.Fatalf("hint = %q", check.Hint)
			}
		})
	}
}
