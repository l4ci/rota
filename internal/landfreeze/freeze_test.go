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

func TestFreezeRepairBacklogID(t *testing.T) {
	p := filepath.Join(t.TempDir(), "freeze.json")
	r := &Record{Active: true, Base: "main", SHA: strings.Repeat("a", 40), RepairIssue: "B07"}
	if err := ValidateRepair(r.RepairIssue, ""); err != nil {
		t.Fatalf("valid backlog repair rejected: %v", err)
	}
	if err := Save(p, r); err != nil {
		t.Fatal(err)
	}
	stored, err := Read(p)
	if err != nil || !stored.Allows("B07", "") || stored.Allows("B08", "") {
		t.Fatalf("backlog repair must survive a read and exempt only B07: %+v %v", stored, err)
	}
	for _, bad := range []string{"12\nfoo", "B07 ", " B07", "B 07", "B07\u200b"} {
		if err := ValidateRepair(bad, ""); err == nil {
			t.Errorf("invalid issue accepted: %q", bad)
		}
	}
	if err := ValidateRepair("", "B07"); err == nil {
		t.Error("PR repair must remain numeric")
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

func TestRecordBinds(t *testing.T) {
	cases := []struct {
		name string
		r    *Record
		base string
		want bool
	}{
		{"nil record", nil, "main", false},
		{"active same base", &Record{Active: true, Base: "main"}, "main", true},
		{"active other base", &Record{Active: true, Base: "main"}, "release", false},
		{"inactive same base", &Record{Active: false, Base: "main"}, "main", false},
		{"inactive other base", &Record{Active: false, Base: "main"}, "release", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.r.Binds(tc.base); got != tc.want {
				t.Fatalf("Binds(%q) = %v, want %v", tc.base, got, tc.want)
			}
		})
	}
}
