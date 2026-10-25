package doctor

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestDoctorStaleClaims(t *testing.T) {
	in := Input{Look: func(string) (string, bool) { return "", false }}
	if c := statusOf(Run(context.Background(), in), "stale claims"); c.Name != "" {
		t.Fatalf("empty scan adds a row: %+v", c)
	}
	in.StaleClaims = []StaleClaim{{Issue: "7", Holder: "mac-ben@3", Last: time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)}, {Issue: "9", Holder: "dana@4", Last: time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC)}}
	var rows []Check
	for _, c := range Run(context.Background(), in).Checks {
		if c.Name == "stale claims" {
			rows = append(rows, c)
		}
	}
	if len(rows) != 2 {
		t.Fatalf("stale claims rows = %+v, want 2", rows)
	}
	for i, want := range []string{"claim mac-ben@3 on #7", "claim dana@4 on #9"} {
		c := rows[i]
		if c.Status != Warn || !strings.Contains(c.Detail, want) || !strings.Contains(c.Detail, "2026-10-24T0") || !strings.Contains(c.Hint, "maintainer") {
			t.Errorf("row = %+v", c)
		}
	}
}
