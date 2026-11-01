package cli

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/doctor"
	"github.com/l4ci/rota/internal/git"
)

// leakAge is how old a temp dir must be before doctor calls it leaked: a run
// that is still going owns a younger one.
const leakAge = time.Hour

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
// It only runs once the disk is low, because sizing the temp dirs walks them.
func doctorLeftovers(ctx context.Context, run git.Runner, root, temp string, now time.Time) []string {
	var out []string
	if n, size := leakedTempDirs(temp, now.Add(-leakAge)); n > 0 {
		out = append(out, fmt.Sprintf("%d leaked temp dirs under %s (%s); safe to delete once no run is active", n, temp, doctor.HumanBytes(uint64(size))))
	}
	if root != "" {
		if n := prunableWorktrees(ctx, run, root); n > 0 {
			out = append(out, fmt.Sprintf("%d stale scratch worktrees (run: git worktree prune; rota reap lists the rest)", n))
		}
	}
	return out
}

// leakedTempDirs counts the smoke run dirs (rota-smoke.*) and gate log dirs
// (rota-gate-logs-*) in dir last modified before cutoff, and their total size.
func leakedTempDirs(dir string, cutoff time.Time) (n int, size int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || !(strings.HasPrefix(name, "rota-smoke.") || strings.HasPrefix(name, "rota-gate-logs-")) {
			continue
		}
		if fi, err := e.Info(); err != nil || !fi.ModTime().Before(cutoff) {
			continue
		}
		n++
		filepath.WalkDir(filepath.Join(dir, name), func(_ string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				if fi, err := d.Info(); err == nil {
					size += fi.Size()
				}
			}
			return nil
		})
	}
	return n, size
}

// prunableWorktrees counts checkouts git marks prunable (directory gone).
func prunableWorktrees(ctx context.Context, run git.Runner, root string) int {
	res, err := run(ctx, root, "worktree", "list", "--porcelain")
	if err != nil || res.ExitCode != 0 {
		return 0
	}
	n := 0
	for _, w := range git.ParseWorktrees(res.Stdout) {
		if w.Prunable {
			n++
		}
	}
	return n
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
	in.ScanLeftovers = func() []string { return doctorLeftovers(ctx, d.Git, root, d.TempDir(), now) }
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
