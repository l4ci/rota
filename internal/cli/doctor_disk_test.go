package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/doctor"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/proc"
	"github.com/l4ci/rota/internal/reap"
	"github.com/l4ci/rota/internal/round"
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
			now := time.Now()
			for _, entry := range []struct {
				name string
				age  time.Duration
			}{
				{"rota-smoke.old", 3 * time.Hour}, {"rota-gate-logs-old", 3 * time.Hour},
				{"rota-review-1001", 3 * time.Hour}, {"rota-gate-tmp.old", 3 * time.Hour}, {"rota-review-3003", 0},
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
				if err := os.Chtimes(filepath.Join(p, "f"), age, age); err != nil {
					t.Fatal(err)
				}
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
			d.ProcessUses = func() ([]reap.ProcUse, error) { return nil, nil }
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
			want := "3 leaked temp dirs under " + temp + " (6.0 KiB) that `rota reap --kind systmp --apply` would remove; preview with `rota reap --kind systmp`"
			got := in.ScanLeftovers()
			if len(got) != 1 || got[0] != want {
				t.Fatalf("leftovers = %q, want %q", got, want)
			}
			rep := doctor.Run(context.Background(), doctor.Input{
				Disk: in.Disk, CacheDisk: in.CacheDisk, MinFreeDiskPercent: in.MinFreeDiskPercent,
				ScanLeftovers: in.ScanLeftovers,
				Exec: func(context.Context, proc.Cmd) (proc.Result, error) {
					return proc.Result{}, nil
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

func TestDoctorLeftoversListsStaleProjectTempEntries(t *testing.T) {
	now := time.Now()
	tempRoot := t.TempDir()
	for _, e := range []struct {
		name string
		age  time.Duration
	}{{"old-a", 3 * time.Hour}, {"old-b", 5 * time.Hour}, {"fresh", 10 * time.Minute}} {
		p := filepath.Join(tempRoot, e.name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(p, "f")
		if err := os.WriteFile(f, make([]byte, 1024), 0o644); err != nil {
			t.Fatal(err)
		}
		ts := now.Add(-e.age)
		os.Chtimes(f, ts, ts)
		os.Chtimes(p, ts, ts)
	}
	if err := os.WriteFile(filepath.Join(tempRoot, ".gitignore"), []byte("*\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(context.Context, string, ...string) (git.Result, error) { return git.Result{ExitCode: 1}, nil }
	root := t.TempDir()
	in := reapInput(root, tempRoot, "worktree "+root+"\n")
	in.ProcessUses = func() ([]reap.ProcUse, error) { return nil, nil }
	want := "2 entries under " + tempRoot + " (2.0 KiB) that `rota reap --kind tmp --apply` would remove; preview with `rota reap --kind tmp`"
	got := doctorLeftovers(context.Background(), run, "", t.TempDir(), &in, nil)
	if len(got) != 1 || got[0] != want {
		t.Fatalf("leftovers = %q, want [%q]", got, want)
	}
	if got := doctorLeftovers(context.Background(), run, "", t.TempDir(), nil, nil); len(got) != 0 {
		t.Fatalf("no temp root: leftovers = %q", got)
	}
	in.TempRoot = filepath.Join(tempRoot, "absent")
	if got := doctorLeftovers(context.Background(), run, "", t.TempDir(), &in, nil); len(got) != 0 {
		t.Fatalf("missing temp root was created or listed: %q", got)
	}
}

// reapInput is the minimum reap.Input to list worktree and tmp candidates
// from a scripted `git worktree list`.
func reapInput(root, tempRoot, porcelain string) reap.Input {
	run := func(context.Context, string, ...string) (git.Result, error) {
		return git.Result{Stdout: porcelain}, nil
	}
	return reap.Input{Root: root, Base: "main", Git: run, Report: &round.Report{}, TempRoot: tempRoot}
}

func ageTree(t *testing.T, dir string, ts time.Time) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "f"), make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(filepath.Join(dir, "f"), ts, ts)
	os.Chtimes(dir, ts, ts)
}

// Doctor's hint says `rota reap --kind tmp` lists what it counted, so an
// unowned root (no rota .gitignore) must read 0 on both sides.
func TestDoctorAndReapAgreeOnAnUnownedTempRoot(t *testing.T) {
	now := time.Now()
	tempRoot := t.TempDir()
	old := filepath.Join(tempRoot, "old-run")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	ageTree(t, old, now.Add(-3*time.Hour))
	run := func(context.Context, string, ...string) (git.Result, error) { return git.Result{ExitCode: 1}, nil }
	in := reapInput(t.TempDir(), tempRoot, "worktree /nowhere\n")
	got := doctorLeftovers(context.Background(), run, "", t.TempDir(), &in, nil)
	if len(got) != 1 || !strings.Contains(got[0], "not a temp root") || strings.Contains(got[0], "entries under") {
		t.Errorf("doctor counted an unowned root or dropped reap's warning: %q", got)
	}
	res, err := reap.Find(context.Background(), in, []string{reap.KindTmp})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Candidates) != 0 {
		t.Errorf("reap listed %v from an unowned root", res.Candidates)
	}
}

// A prunable checkout outside .worktrees/ is not reap's to list, so doctor
// must not count it either; the one inside is counted and listed by both.
func TestDoctorAndReapAgreeOnPrunableCheckoutsOutsideWorktrees(t *testing.T) {
	root := t.TempDir()
	porcelain := "worktree " + root + "\n\n" +
		"worktree " + filepath.Join(root, ".worktrees", "gone") + "\nbranch refs/heads/kit/1-a\nprunable gitdir file points to non-existent location\n\n" +
		"worktree " + filepath.Join(t.TempDir(), "elsewhere") + "\nbranch refs/heads/kit/2-b\nprunable gitdir file points to non-existent location\n"
	run := func(context.Context, string, ...string) (git.Result, error) {
		return git.Result{Stdout: porcelain}, nil
	}
	counted := 0
	rin := reapInput(root, "", porcelain)
	if got := doctorLeftovers(context.Background(), run, root, t.TempDir(), &rin, nil); len(got) == 1 && strings.HasPrefix(got[0], "1 stale scratch worktrees") {
		counted = 1
	}
	res, err := reap.Find(context.Background(), reapInput(root, "", porcelain), []string{reap.KindWorktree})
	if err != nil {
		t.Fatal(err)
	}
	if counted != 1 || len(res.Candidates) != 1 {
		t.Errorf("doctor counted %d, reap listed %d (%v); want 1 and 1", counted, len(res.Candidates), res.Candidates)
	}
}

// A tmp entry a live process uses is listed by reap but held, so doctor must
// not call it reclaimable.
func TestDoctorLeftoversSkipsHeldTmpEntries(t *testing.T) {
	now := time.Now()
	tempRoot, root := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(tempRoot, ".gitignore"), []byte("*\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"free", "busy"} {
		p := filepath.Join(tempRoot, n)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		ageTree(t, p, now.Add(-3*time.Hour))
	}
	in := reapInput(root, tempRoot, "worktree "+root+"\n")
	in.ProcessUses = func() ([]reap.ProcUse, error) {
		return []reap.ProcUse{{PID: 7, Paths: []string{filepath.Join(tempRoot, "busy")}}}, nil
	}
	run := func(context.Context, string, ...string) (git.Result, error) { return git.Result{ExitCode: 1}, nil }
	got := doctorLeftovers(context.Background(), run, "", t.TempDir(), &in, nil)
	if len(got) != 1 || !strings.HasPrefix(got[0], "1 entries under ") {
		t.Fatalf("leftovers = %q, want one line counting only the unheld entry", got)
	}
}

// Doctor and reap build one reap.Input, so a held-rule field one sets the
// other sets too: a field only reap fills would make doctor count differently.
func TestDoctorReapInputMatchesReapVerbInput(t *testing.T) {
	root, d := reapProject(t, nil, true)
	ctx := context.Background()
	want, _, err := d.reapInput(ctx, root, []string{"kit"})
	if err != nil {
		t.Fatal(err)
	}
	got, warn := d.doctorReapInput(ctx, root)
	if got == nil || len(warn) != 0 {
		t.Fatalf("doctor input = %v, warn %v", got, warn)
	}
	if got.Root != want.Root || got.Base != want.Base || got.TempRoot != want.TempRoot || got.SystemTemp != want.SystemTemp ||
		got.SkillCache != want.SkillCache || got.SkillCacheKeep != want.SkillCacheKeep {
		t.Errorf("value fields differ:\ndoctor %+v\nreap   %+v", got, want)
	}
	has := func(in reap.Input) []bool {
		return []bool{in.Git != nil, in.Report != nil, in.Host != nil, in.Lease != nil, in.Refresh != nil, in.ProcessUses != nil, in.Adopted != nil}
	}
	if !reflect.DeepEqual(has(*got), has(want)) {
		t.Errorf("wired fields differ: doctor %v, reap %v", has(*got), has(want))
	}
}
