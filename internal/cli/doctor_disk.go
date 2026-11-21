package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"syscall"

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
func doctorLeftovers(ctx context.Context, run git.Runner, root, temp string, tmp *reap.Input, processUses func() ([]reap.ProcUse, error)) []string {
	c := reap.TakeCensus(ctx, reap.CensusInput{Root: root, SystemTemp: temp, Tmp: tmp, ProcessUses: processUses, Git: run})
	var out []string
	if c.LeakedTemp.N > 0 {
		out = append(out, fmt.Sprintf("%d leaked temp dirs under %s (%s) that `rota reap --kind systmp --apply` would remove; preview with `rota reap --kind systmp`", c.LeakedTemp.N, temp, strutil.HumanBytes(uint64(c.LeakedTemp.Size))))
	}
	if c.StaleTmp.N > 0 {
		out = append(out, fmt.Sprintf("%d entries under %s (%s) that `rota reap --kind tmp --apply` would remove; preview with `rota reap --kind tmp`", c.StaleTmp.N, tmp.TempRoot, strutil.HumanBytes(uint64(c.StaleTmp.Size))))
	}
	out = append(out, c.Warnings...)
	if c.Prunable.N > 0 {
		out = append(out, fmt.Sprintf("%d stale scratch worktrees (run: git worktree prune; rota reap lists the rest)", c.Prunable.N))
	}
	return out
}

// doctorDiskInput fills the disk fields of in: the threshold from config
// (doctor.minFreeDiskPercent) and the leftovers scan doctor runs when the
// volume is under it. An unreadable threshold is recorded in in.ConfigErrors
// and the default stands for the disk check.
func (d *Deps) doctorDiskInput(ctx context.Context, in *doctor.Input, cfg any, root string) {
	min, err := config.Int(cfg, "doctor.minFreeDiskPercent", 0, 100)
	if err != nil {
		in.ConfigErrors = append(in.ConfigErrors, err.Error())
		min = config.IntOr(cfg, "doctor.minFreeDiskPercent", 0, 100)
	}
	in.MinFreeDiskPercent = min
	dir := root
	if dir == "" {
		dir = in.Dir
	}
	in.Disk = d.DiskUsage(dir)
	if cache := goCacheDir(d.Getenv, d.UserCacheDir); cache != "" && !sameVolume(cache, dir) {
		in.CacheDisk = d.DiskUsage(cache)
	}
	in.ScanLeftovers = func() []string {
		tmp, warn := d.reapTmpInput(ctx, root)
		return append(doctorLeftovers(ctx, d.Git, root, d.TempDir(), tmp, d.ProcessUses), warn...)
	}
}

// reapTmpInput is the reap input `rota reap --kind tmp` lists with, so doctor
// counts through the same held rules. Nil when there is no project or temp
// root; warn says why the round status could not be read.
func (d *Deps) reapTmpInput(ctx context.Context, root string) (in *reap.Input, warn []string) {
	if root == "" {
		return nil, nil
	}
	tempRoot, _ := rotatree.FindTempRoot(ctx, d.Git, root)
	if tempRoot == "" {
		return nil, nil
	}
	env, ops := d.ReapEnv(ctx, root)
	rep, err := env.Status(ctx, root)
	if err != nil {
		return nil, []string{"temp root not checked: " + err.Error()}
	}
	return &reap.Input{Root: root, Base: env.Base, Git: env.Git, Report: rep, Host: ops, TempRoot: tempRoot, ProcessUses: d.ProcessUses}, nil
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
