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
	doctorDiskInput(context.Background(), &in, cfg, "", git.Exec, time.Now())
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
	doctorDiskInput(context.Background(), &in, cfg, "", git.Exec, time.Now())
	if len(in.Leftovers) != 0 {
		t.Errorf("leftovers are only scanned when the disk is low: %v", in.Leftovers)
	}
}

func TestDoctorSlotBlocksUsesTheStoredWidth(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".rota"), 0o755)
	os.WriteFile(filepath.Join(root, ".rota", "config.json"), []byte(`{"work":{"portBlock":100}}`), 0o644)
	os.WriteFile(filepath.Join(root, ".rota", "workers.json"), []byte(`{"slots":[
{"name":"w1","worktree":"/p/w1","handle":"h1","portBase":20000,"portBlock":10},
{"name":"w2","worktree":"/p/w2","handle":"h2"}]}`), 0o644)
	got := doctorSlotBlocks(root)
	if len(got) != 1 || got[0].Name != "w1" || got[0].Base != 20000 || got[0].Size != 10 {
		t.Errorf("doctorSlotBlocks = %+v, want w1 at 20000 with the stored width 10, not the config's 100", got)
	}
}
