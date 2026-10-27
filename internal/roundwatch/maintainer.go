package roundwatch

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/rotastate"
)

// DefaultHeartbeat is the watch's normal wake interval and the digest's
// freshness limit when there is no live watch marker.
const DefaultHeartbeat = 10 * time.Minute

type maintainerSnapshot struct {
	RecordedAt time.Time `json:"recordedAt"`
	Count      int       `json:"count"`
}

// SaveMaintainerSnapshot is called only after a complete status/forge pass.
// The prompt never refreshes it, so prompting performs no remote I/O.
func SaveMaintainerSnapshot(commonDir string, now time.Time, count int) error {
	if err := os.MkdirAll(rotastate.Dir(commonDir), 0755); err != nil {
		return err
	}
	b, err := json.Marshal(maintainerSnapshot{RecordedAt: now.UTC(), Count: count})
	if err != nil {
		return err
	}
	return fsio.WriteFileAtomic(rotastate.File(commonDir, "maintainer-digest.json"), append(b, '\n'))
}

// MaintainerSnapshotLine omits anything it cannot establish as a fresh count.
// A snapshot in the future is invalid rather than clamped to look current.
func MaintainerSnapshotLine(commonDir string, now time.Time, interval time.Duration) string {
	b, err := os.ReadFile(rotastate.File(commonDir, "maintainer-digest.json"))
	if err != nil {
		return ""
	}
	var snapshot maintainerSnapshot
	if json.Unmarshal(b, &snapshot) != nil || snapshot.Count <= 0 || snapshot.RecordedAt.IsZero() {
		return ""
	}
	age := now.Sub(snapshot.RecordedAt)
	if interval <= 0 || age < 0 || age > interval {
		return ""
	}
	verb := "wait"
	if snapshot.Count == 1 {
		verb = "waits"
	}
	return fmt.Sprintf("%d %s on you, as of %s ago", snapshot.Count, verb, CompactAge(age))
}

// CompactAge formats wait and snapshot ages using one whole s/m/h/d unit.
func CompactAge(age time.Duration) string {
	for _, unit := range []struct {
		duration time.Duration
		suffix   string
	}{{24 * time.Hour, "d"}, {time.Hour, "h"}, {time.Minute, "m"}} {
		if age >= unit.duration {
			return fmt.Sprintf("%d%s", age/unit.duration, unit.suffix)
		}
	}
	return fmt.Sprintf("%ds", max(int64(age/time.Second), 0))
}
