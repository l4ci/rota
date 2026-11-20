package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"syscall"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/doctor"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/reap"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/strutil"
)

func statDisk(dir string) *doctor.Disk {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return nil
	}
	bs := uint64(st.Bsize)
	return &doctor.Disk{Path: dir, Free: uint64(st.Bavail) * bs, Total: uint64(st.Blocks) * bs}
}

// doctorLeftovers names what rota left behind that would give disk back: temp
// dirs a smoke or gate run leaked, and git worktrees whose directory is gone.
// reap.TakeCensus does the counting, so the numbers match `rota reap`. It only
// runs once the disk is low, because sizing the temp dirs walks them.
func doctorLeftovers(ctx context.Context, run git.Runner, root, temp, tempRoot string, now time.Time) []string {
	c := reap.TakeCensus(ctx, reap.CensusInput{Root: root, SystemTemp: temp, TempRoot: tempRoot, Git: run, Now: now})
	var out []string
	if c.LeakedTemp.N > 0 {
		out = append(out, fmt.Sprintf("%d leaked temp dirs under %s (%s); safe to delete once no run is active", c.LeakedTemp.N, temp, strutil.HumanBytes(uint64(c.LeakedTemp.Size))))
	}
	if c.StaleTmp.N > 0 {
		out = append(out, fmt.Sprintf("%d entries older than 1h under %s (%s); preview with `rota reap --kind tmp`, `--apply` to remove", c.StaleTmp.N, tempRoot, strutil.HumanBytes(uint64(c.StaleTmp.Size))))
	}
	if c.Prunable.N > 0 {
		out = append(out, fmt.Sprintf("%d stale scratch worktrees (run: git worktree prune; rota reap lists the rest)", c.Prunable.N))
	}
	return out
}

// doctorDiskInput fills the disk fields of in: the threshold from config
// (doctor.minFreeDiskPercent) and the leftovers scan doctor runs when the
// volume is under it. A non-numeric threshold falls back to the default.
func (d *Deps) doctorDiskInput(ctx context.Context, in *doctor.Input, cfg any, root string) {
	min := config.IntOr(cfg, "doctor.minFreeDiskPercent", 0, 100)
	in.MinFreeDiskPercent = min
	dir := root
	if dir == "" {
		dir = in.Dir
	}
	in.Disk = d.DiskUsage(dir)
	if cache := goCacheDir(d.Getenv, d.UserCacheDir); cache != "" && !sameVolume(cache, dir) {
		in.CacheDisk = d.DiskUsage(cache)
	}
	now := d.Now()
	var tempRoot string
	if root != "" {
		tempRoot, _ = rotatree.FindTempRoot(ctx, d.Git, root)
	}
	in.ScanLeftovers = func() []string { return doctorLeftovers(ctx, d.Git, root, d.TempDir(), tempRoot, now) }
}

// goCacheDir is where the Go build cache lives: $GOCACHE, else go-build under
// the user cache dir, as the go tool resolves it. "" when neither is known.
func goCacheDir(getenv func(string) string, userCacheDir func() (string, error)) string {
	if c := getenv("GOCACHE"); c != "" && c != "off" {
		return c
	}
	if d, err := userCacheDir(); err == nil {
		return filepath.Join(d, "go-build")
	}
	return ""
}

// sameVolume says whether a and b are on one device, or a does not exist (the
// cache may not have been created yet, and then it has nothing to warn about).
func sameVolume(a, b string) bool {
	var sa, sb syscall.Stat_t
	if syscall.Stat(a, &sa) != nil {
		return true
	}
	return syscall.Stat(b, &sb) == nil && sa.Dev == sb.Dev
}
