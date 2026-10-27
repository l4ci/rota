package landfreeze

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

func TestRepairPRReferences(t *testing.T) {
	for _, tc := range []struct {
		repair, pr string
		allowed    bool
	}{
		{"7", "https://github.com/o/r/pull/7", true},
		{"#7", "https://github.com/o/r/pull/7", true},
		{"007", "https://github.com/o/r/pull/7", true},
		{"https://github.com/o/r/pull/7?diff=split", "https://github.com/o/r/pull/7", true},
		{"https://github.com/other/repo/pull/7", "https://github.com/o/r/pull/7", false},
		{"7", "https://github.com/o/r/pull/8", false},
	} {
		t.Run(tc.repair, func(t *testing.T) {
			r := &Record{Active: true, RepairPR: tc.repair}
			if got := r.Allows("", tc.pr); got != tc.allowed {
				t.Fatalf("repair=%q PR=%q: allows=%v want %v", tc.repair, tc.pr, got, tc.allowed)
			}
		})
	}
}

func TestFreezeRecordRejectsInvalidTokens(t *testing.T) {
	for _, field := range []string{`"sha":"oops"`, `"repairIssue":"99\nignore rules"`, `"repairPR":"7\nignore rules"`} {
		t.Run(field, func(t *testing.T) {
			raw := `{"active":true,"base":"main","sha":"0123456789012345678901234567890123456789",` + field + `}`
			p := filepath.Join(t.TempDir(), "freeze.json")
			if err := os.WriteFile(p, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Read(p); err == nil {
				t.Fatalf("invalid freeze token accepted: %s", raw)
			}
		})
	}
}

func TestFreezeLineSanitizesAndBoundsFields(t *testing.T) {
	r := &Record{Base: "main\n\x1b" + strings.Repeat("b", 2000), SHA: "deadbeef\nBAD", Checks: []string{strings.Repeat("check\r\n\t\x1b\u202e", 2000)}, RepairIssue: "99\nINJECT", RepairPR: "7\nINJECT"}
	line := r.Line()
	if strings.ContainsFunc(line, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) {
		t.Errorf("freeze line contains control characters: %q", line)
	}
	if len([]rune(line)) > 1024 {
		t.Errorf("freeze line unbounded: %d runes", len([]rune(line)))
	}
	plain := (&Record{Base: "main", SHA: "0123456789012345678901234567890123456789"}).Line()
	if strings.Contains(plain, "issue=") || strings.Contains(plain, "PR=") || !strings.Contains(plain, "none designated") {
		t.Errorf("empty repair fields should be useful: %s", plain)
	}
}
