package pidlive

import (
	"context"
	"os"

	"github.com/l4ci/rota/internal/proc"
)

// ReadPathUses lists the paths used by uid's processes: /proc on Linux,
// lsof on hosts without /proc (macOS). An unreadable table is an error.
func ReadPathUses(ctx context.Context, run proc.Runner, uid int) ([]ProcUse, error) {
	return readPathUses(ctx, "/proc", run, uid)
}

func readPathUses(ctx context.Context, procRoot string, run proc.Runner, uid int) ([]ProcUse, error) {
	if _, err := os.Stat(procRoot); err == nil {
		return ReadProcUses(procRoot, uid)
	}
	return ReadLsofUses(ctx, run, uid)
}
