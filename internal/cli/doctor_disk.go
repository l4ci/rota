package cli

import (
	"context"
	"encoding/json"
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

// doctorDisk reads the free space of the volume holding dir. ROTA_TEST_DOCTOR_DISK
// ("<free>:<total>" bytes) replaces the read (a test hook, like
// ROTA_TEST_DOCTOR_PATH, not part of the CLI).
func doctorDisk(dir string) *doctor.Disk {
	if v := os.Getenv("ROTA_TEST_DOCTOR_DISK"); v != "" {
		var free, total uint64
		if _, err := fmt.Sscanf(v, "%d:%d", &free, &total); err != nil {
			return nil
		}
		return &doctor.Disk{Path: dir, Free: free, Total: total}
	}
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
func doctorLeftovers(ctx context.Context, run git.Runner, root string, now time.Time) []string {
	var out []string
	if n, size := leakedTempDirs(os.TempDir(), now.Add(-leakAge)); n > 0 {
		out = append(out, fmt.Sprintf("%d leaked temp dirs under %s (%s); safe to delete once no run is active", n, os.TempDir(), doctor.HumanBytes(uint64(size))))
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
	for _, l := range strings.Split(res.Stdout, "\n") {
		if strings.HasPrefix(l, "prunable") {
			n++
		}
	}
	return n
}

// doctorDiskInput fills the disk fields of in: the threshold from config
// (doctor.minFreeDiskPercent, default 10) and, when the volume is under it,
// the leftovers worth naming. A non-numeric threshold falls back to the default.
func doctorDiskInput(ctx context.Context, in *doctor.Input, cfg any, root string, run git.Runner, now time.Time) {
	min := 10
	if cfg != nil {
		if v, ok := config.Lookup(cfg, "doctor.minFreeDiskPercent"); ok {
			if num, ok := v.(json.Number); ok {
				if n, err := num.Int64(); err == nil && n >= 0 && n <= 100 {
					min = int(n)
				}
			}
		}
	}
	in.MinFreeDiskPercent = min
	dir := root
	if dir == "" {
		dir = in.Dir
	}
	in.Disk = doctorDisk(dir)
	if in.Disk != nil && in.Disk.Total > 0 && min > 0 &&
		float64(in.Disk.Free)/float64(in.Disk.Total)*100 < float64(min) {
		in.Leftovers = doctorLeftovers(ctx, run, root, now)
	}
}
