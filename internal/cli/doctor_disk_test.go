package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/doctor"
	"github.com/l4ci/rota/internal/git"
)

func TestLeakedTempDirs(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-3 * time.Hour)
	mk := func(name string, age time.Time) {
		p := filepath.Join(dir, name)
		os.MkdirAll(p, 0o755)
		os.WriteFile(filepath.Join(p, "f"), make([]byte, 2048), 0o644)
		os.Chtimes(p, age, age)
	}
	mk("rota-smoke.old", old)
	mk("tmp.other", old) // another program's mktemp dir is not ours
	mk("rota-gate-logs-old", old)
	mk("rota-smoke.fresh", time.Now()) // a live run's dir is not leaked
	mk("other", old)                   // not ours
	n, size := leakedTempDirs(dir, time.Now().Add(-leakAge))
	if n != 2 || size != 4096 {
		t.Errorf("leakedTempDirs = %d, %d; want 2, 4096", n, size)
	}
}

func TestDoctorDiskInput(t *testing.T) {
	cfg := config.Load(filepath.Join(t.TempDir(), "absent.json"))
	t.Setenv("ROTA_TEST_DOCTOR_DISK", "1000:100000") // 1% free
	t.Setenv("TMPDIR", t.TempDir())
	in := doctor.Input{Dir: t.TempDir()}
	doctorDiskInput(context.Background(), &in, cfg, "", git.Exec)
	if in.MinFreeDiskPercent != 10 || in.Disk == nil || in.Disk.Free != 1000 {
		t.Fatalf("default threshold / disk not read: %+v", in)
	}
	rep := doctor.Run(context.Background(), doctor.Input{Disk: in.Disk, MinFreeDiskPercent: in.MinFreeDiskPercent,
		Exec: func(context.Context, string, []string, []string, string) (doctor.Result, error) {
			return doctor.Result{}, nil
		},
		Look: func(string) (string, bool) { return "", false }})
	found := false
	for _, c := range rep.Checks {
		if c.Name == "disk" && c.Status == doctor.Warn && strings.Contains(c.Detail, "1%") {
			found = true
		}
	}
	if !found {
		t.Errorf("no disk warning in %+v", rep.Checks)
	}
	t.Setenv("ROTA_TEST_DOCTOR_DISK", "50000:100000")
	in = doctor.Input{Dir: t.TempDir()}
	doctorDiskInput(context.Background(), &in, cfg, "", git.Exec)
	if len(in.Leftovers) != 0 {
		t.Errorf("leftovers are only scanned when the disk is low: %v", in.Leftovers)
	}
}
