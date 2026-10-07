package hvmigrate

import (
	"strings"
	"testing"
)

func TestUnifiedDiffMergesCloseHunks(t *testing.T) {
	var a, b []string
	for i := 1; i <= 30; i++ {
		a = append(a, "line\n")
		b = append(b, "line\n")
	}
	b[2], b[8] = "X\n", "Y\n" // 5 unchanged lines between: one hunk
	b[25] = "Z\n"             // far away: a second hunk
	got := UnifiedDiff(strings.Join(a, ""), strings.Join(b, ""), "f")
	if strings.Count(got, "@@ -") != 2 || !strings.Contains(got, "@@ -1,12 +1,12 @@\n") || !strings.Contains(got, "@@ -23,7 +23,7 @@\n") {
		t.Errorf("hunks:\n%s", got)
	}
	if UnifiedDiff("same\n", "same\n", "f") != "" {
		t.Error("identical texts produced a diff")
	}
}
