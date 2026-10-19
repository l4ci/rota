package milestone

import (
	"reflect"
	"testing"
)

func TestAudit(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		open       []string
		wantOpen   []string
		wantUntick []string
		clean      bool
	}{
		{"all ticked, none open", "## Acceptance\n\n- [x] AC-1: one\n- [X] AC-2: two\n", nil, []string{}, []string{}, true},
		{"unticked criteria listed", "## Acceptance\n\n- [x] AC-1: one\n- [ ] AC-2: two\n- [ ] AC-3: three\n", nil, []string{}, []string{"AC-2", "AC-3"}, false},
		{"open items listed", "## Acceptance\n\n- [x] AC-1: one\n", []string{"#4", "#9"}, []string{"#4", "#9"}, []string{}, false},
		{"unnumbered unticked gets its label", "## Acceptance\n\n- [x] AC-1: one\n- [ ] two\n", nil, []string{}, []string{"AC-2"}, false},
		{"no acceptance section", "# M01\n\nbody\n", nil, []string{}, []string{}, true},
		{"unticked outside the section is ignored", "## Goal\n\n- [ ] AC-1: aside\n\n## Acceptance\n\n- [x] AC-2: ok\n", nil, []string{}, []string{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := Audit(tt.body, tt.open)
			if !reflect.DeepEqual(a.Open, tt.wantOpen) || !reflect.DeepEqual(a.Unticked, tt.wantUntick) || a.Clean() != tt.clean {
				t.Errorf("%+v", a)
			}
		})
	}
}
