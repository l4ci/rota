package roundwatch

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/rotastate"
)

func TestMaintainerSnapshotFreshness(t *testing.T) {
	common := t.TempDir()
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	if line := MaintainerSnapshotLine(common, now, DefaultHeartbeat); line != "" {
		t.Fatalf("missing snapshot: %q", line)
	}
	for _, tc := range []struct {
		name     string
		count    int
		delay    time.Duration
		interval time.Duration
		want     string
	}{
		{"fresh", 3, 4 * time.Minute, DefaultHeartbeat, "3 wait on you, as of 4m ago"},
		{"at-expiry", 1, DefaultHeartbeat, DefaultHeartbeat, "1 waits on you, as of 10m ago"},
		{"expired", 3, DefaultHeartbeat + time.Nanosecond, DefaultHeartbeat, ""},
		{"short-watch", 3, 4 * time.Minute, time.Minute, ""},
		{"future", 3, -time.Second, DefaultHeartbeat, ""},
		{"zero", 0, 0, DefaultHeartbeat, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := SaveMaintainerSnapshot(common, now, tc.count); err != nil {
				t.Fatal(err)
			}
			if got := MaintainerSnapshotLine(common, now.Add(tc.delay), tc.interval); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
	for _, body := range []string{`{`, `{}`, `{"count":3,"recordedAt":"nonsense"}`} {
		if err := os.WriteFile(rotastate.File(common, "maintainer-digest.json"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if line := MaintainerSnapshotLine(common, now, DefaultHeartbeat); strings.TrimSpace(line) != "" {
			t.Errorf("invalid snapshot %s: %q", body, line)
		}
	}
}
