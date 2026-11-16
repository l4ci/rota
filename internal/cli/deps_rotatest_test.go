//go:build rotatest

package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/proc"
)

// Under the rotatest tag invocationDeps reads the ROTA_TEST_* overrides once,
// at the edge; defaultDeps (see TestDefaultDepsIgnoresTestOverrides) never does.
func TestInvocationDepsReadsTestOverrides(t *testing.T) {
	t.Setenv("ROTA_TEST_NOW", "2001-02-03T04:05:06Z")
	t.Setenv("ROTA_TEST_TODAY", "2001-02-04")
	t.Setenv("ROTA_TEST_HOLDER_PID", "4242")
	t.Setenv("ROTA_TEST_LATEST_VERSION", "99.0.0")
	d := invocationDeps()
	d.Proc = func(context.Context, proc.Cmd) (proc.Result, error) {
		t.Error("the override must replace the release read")
		return proc.Result{}, nil
	}
	if got := d.Now(); !got.Equal(time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)) {
		t.Errorf("Now = %v", got)
	}
	if got := d.Today(); got.Day() != 4 {
		t.Errorf("Today = %v, want the 4th", got)
	}
	if got := d.HolderPID(); got != 4242 {
		t.Errorf("HolderPID = %d", got)
	}
	if got := d.UpdateEnv().Latest(); got != "99.0.0" {
		t.Errorf("Latest = %q", got)
	}
}

func TestInvocationDepsReportsBadToday(t *testing.T) {
	t.Setenv("ROTA_TEST_TODAY", "tomorrow")
	if err := invocationDeps().ClockErr; err == nil || !strings.Contains(err.Error(), "ROTA_TEST_TODAY") {
		t.Errorf("ClockErr = %v", err)
	}
}
