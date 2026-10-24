//go:build rotatest

package migrate

import (
	"os"
	"strconv"
	"time"
)

// A binary built with -tags rotatest takes the bulk pace from the environment,
// so the smoke suite does not wait a second per tracker write. Release builds
// carry no such override.
func init() {
	if ms, err := strconv.ParseInt(os.Getenv("ROTA_ISSUES_BULK_PACE_MS"), 10, 64); err == nil && ms >= 0 {
		BulkPace = time.Duration(ms) * time.Millisecond
	}
}
