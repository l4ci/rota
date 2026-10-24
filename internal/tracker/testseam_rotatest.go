//go:build rotatest

package tracker

import (
	"os"
	"strconv"
	"time"
)

// A binary built with -tags rotatest takes the retry pause from the
// environment, so the smoke suite does not wait a minute per rate-limit case.
// Release builds carry no such override.
func init() {
	if f, err := strconv.ParseFloat(os.Getenv("ROTA_ISSUES_RETRY_WAIT_SECONDS"), 64); err == nil && f >= 0 {
		RetryPause = time.Duration(f * float64(time.Second))
	}
}
