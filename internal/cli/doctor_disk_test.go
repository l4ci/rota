package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/doctor"
	"github.com/l4ci/rota/internal/git"
)

func fixedDisk(free, total uint64) func(string) *doctor.Disk {
	return func(dir string) *doctor.Disk {
		return &doctor.Disk{Path: dir, Free: free, Total: total}
	}
}

func TestDoctorDiskUsesInjectedDirectories(t *testing.T) {
	for _, tc := range []struct {
		name, gocache string
		cacheError    bool
	}{
		{name: "user-cache"},
		{name: "explicit-cache", gocache: "explicit"},
		{name: "off-falls-back", gocache: "off"},
		{name: "unknown-cache", cacheError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			temp, cache := t.TempDir(), t.TempDir()
			now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
			for _, entry := range []struct {
				name string
				age  time.Duration
			}{
				{"rota-smoke.old", 3 * time.Hour}, {"rota-gate-logs-old", 3 * time.Hour},
				{"rota-smoke.fresh", 0}, {"tmp.other", 3 * time.Hour}, {"other", 3 * time.Hour},
			} {
				p := filepath.Join(temp, entry.name)
				if err := os.Mkdir(p, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(p, "f"), make([]byte, 2048), 0o644); err != nil {
					t.Fatal(err)
				}
				age := now.Add(-entry.age)
				if err := os.Chtimes(p, age, age); err != nil {
					t.Fatal(err)
				}
			}
			cachePath := filepath.Join(cache, "go-build")
			if tc.gocache == "explicit" {
				cachePath = filepath.Join(cache, "explicit")
			}
			if err := os.Mkdir(cachePath, 0o755); err != nil {
				t.Fatal(err)
			}
			d := testDeps()
			// A nonexistent working directory has no device, so the existing volume
			// comparison treats the fake cache as a separate volume.
			cwd, home := filepath.Join(t.TempDir(), "absent"), t.TempDir()
			d.Getwd = func() (string, error) { return cwd, nil }
			d.UserHomeDir = func() (string, error) { return home, nil }
			d.LookPath = func(string) (string, error) { return "", errors.New("no tools") }
			d.Getenv = func(k string) string {
				if k == "GOCACHE" {
					if tc.gocache == "explicit" {
						return cachePath
					}
					return tc.gocache
				}
				return ""
			}
			d.TempDir = func() string { return temp }
			d.UserCacheDir = func() (string, error) {
				if tc.cacheError {
					return "", errors.New("no user cache")
				}
				return cache, nil
			}
			d.Now = func() time.Time { return now }
			d.DiskUsage = fixedDisk(1000, 100000)
			d.Git = func(context.Context, string, ...string) (git.Result, error) { return git.Result{ExitCode: 1}, nil }
			in := doctorInput(context.Background(), d)
			if in.MinFreeDiskPercent != 10 || in.Disk == nil || in.Disk.Free != 1000 {
				t.Fatalf("default threshold / disk not read: %+v", in)
			}
			if tc.cacheError {
				if in.CacheDisk != nil {
					t.Fatalf("unknown cache checked: %+v", in.CacheDisk)
				}
			} else if in.CacheDisk == nil || in.CacheDisk.Path != cachePath {
				t.Fatalf("cache disk = %+v, want %s", in.CacheDisk, cachePath)
			}
			want := "2 leaked temp dirs under " + temp + " (4.0 KiB); safe to delete once no run is active"
			got := in.ScanLeftovers()
			if len(got) != 1 || got[0] != want {
				t.Fatalf("leftovers = %q, want %q", got, want)
			}
			rep := doctor.Run(context.Background(), doctor.Input{
				Disk: in.Disk, CacheDisk: in.CacheDisk, MinFreeDiskPercent: in.MinFreeDiskPercent,
				ScanLeftovers: in.ScanLeftovers,
				Exec: func(context.Context, string, []string, []string, string) (doctor.Result, error) {
					return doctor.Result{}, nil
				},
				Look: func(string) (string, bool) { return "", false },
			})
			diskWarn, cacheWarn := false, false
			for _, c := range rep.Checks {
				if c.Name == "disk" && c.Status == doctor.Warn && strings.Contains(c.Detail, "1%") && strings.Contains(c.Hint, temp) {
					diskWarn = true
				}
				if c.Name == "go-cache-disk" && c.Status == doctor.Warn && strings.Contains(c.Hint, "go clean -cache") {
					cacheWarn = true
				}
			}
			if !diskWarn || cacheWarn == tc.cacheError {
				t.Fatalf("disk/cache warnings missing: %+v", rep.Checks)
			}
		})
	}
}

func TestDoctorSlotBlocksUsesTheStoredWidth(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".rota"), 0o755)
	os.WriteFile(filepath.Join(root, ".rota", "config.json"), []byte(`{"round":{"portBlock":100}}`), 0o644)
	os.WriteFile(filepath.Join(root, ".rota", "workers.json"), []byte(`{"slots":[
{"name":"w1","worktree":"/p/w1","handle":"h1","portBase":20000,"portBlock":10},
{"name":"w2","worktree":"/p/w2","handle":"h2"}]}`), 0o644)
	got := doctorSlotBlocks(root)
	if len(got) != 1 || got[0].Name != "w1" || got[0].Base != 20000 || got[0].Size != 10 {
		t.Errorf("doctorSlotBlocks = %+v, want w1 at 20000 with the stored width 10, not the config's 100", got)
	}
}

func TestDoctorDiskSkipsSharedCacheVolume(t *testing.T) {
	cache := t.TempDir()
	in := doctor.Input{Dir: t.TempDir()}
	// Same device as Dir: the repo-volume check already covers it.
	d := testDeps()
	temp := t.TempDir()
	d.TempDir = func() string { return temp }
	d.UserCacheDir = func() (string, error) { return cache, nil }
	d.Getenv = func(k string) string {
		if k == "GOCACHE" {
			return cache
		}
		return ""
	}
	d.DiskUsage = fixedDisk(1000, 100000)
	d.doctorDiskInput(context.Background(), &in, nil, "")
	if in.CacheDisk != nil {
		t.Errorf("cache on the repo volume duplicated the disk check: %+v", in.CacheDisk)
	}
}
