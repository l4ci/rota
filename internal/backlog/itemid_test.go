package backlog

import (
	"regexp"
	"testing"
)

func TestValidID(t *testing.T) {
	cases := []struct {
		id     string
		min    int
		wantOK bool
	}{
		{"B07", FileIDDigits, true},
		{"F123", FileIDDigits, true},
		{"T11", FileIDDigits, true},
		{"B7", FileIDDigits, false}, // file IDs are minted two digits wide
		{"B7", 1, true},             // issue-mode numbers may be any width
		{"B", 1, false},
		{"", 1, false},
		{"S01", 1, false}, // slices are plan-key units, not items
		{"M01", 1, false},
		{"b07", 1, false},
		{"B07x", 1, false},
		{" B07", 1, false},
		{"B07\n", 1, false},
		{"B0-7", 1, false},
		{"07", 1, false},
	}
	for _, c := range cases {
		if got := ValidID(c.id, c.min); got != c.wantOK {
			t.Errorf("ValidID(%q, %d) = %v, want %v", c.id, c.min, got, c.wantOK)
		}
	}
}

func TestIDPatternAgreesWithValidID(t *testing.T) {
	for _, min := range []int{1, 2} {
		re := regexp.MustCompile(`\A` + IDPattern(min) + `\z`)
		for _, id := range []string{"B7", "B07", "F1", "T100", "S01", "B", "X12", "B07a"} {
			if re.MatchString(id) != ValidID(id, min) {
				t.Errorf("min %d: IDPattern and ValidID disagree on %q", min, id)
			}
		}
	}
}

func TestItemType(t *testing.T) {
	for id, want := range map[string]string{"B07": "B", "F1": "F", "T9": "T", "S01": "", "M01": "", "": "", "12": ""} {
		if got := ItemType(id); got != want {
			t.Errorf("ItemType(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestDetailPath(t *testing.T) {
	if got := DetailPath("/r", "F03"); got != "/r/.rota/features/F03.md" {
		t.Errorf("DetailPath(F03) = %q", got)
	}
	if got := DetailPath("/r", "S01"); got != "" {
		t.Errorf("DetailPath(S01) = %q, want empty", got)
	}
}
