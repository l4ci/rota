package reap

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// sysTemp builds a system temp dir with the named dirs, each holding one
// 1024-byte file aged by the given duration (dir mtime aged to match).
func sysTemp(t *testing.T, entries map[string]time.Duration) string {
	t.Helper()
	dir := t.TempDir()
	for name, age := range entries {
		e := filepath.Join(dir, name)
		if err := os.MkdirAll(e, 0o755); err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(e, "data")
		if err := os.WriteFile(f, make([]byte, 1024), 0o644); err != nil {
			t.Fatal(err)
		}
		ts := time.Now().Add(-age)
		os.Chtimes(f, ts, ts)
		os.Chtimes(e, ts, ts)
	}
	return dir
}

func sysInput(t *testing.T, dir string, uses []ProcUse) Input {
	in := input(repo(nil, nil), nil, nil, nil)
	in.SystemTemp = dir
	in.ProcessUses = func() ([]ProcUse, error) { return uses, nil }
	return in
}

func TestSysTmpListsOnlyLeakedPrefixesAndHoldsLikeTmp(t *testing.T) {
	dir := sysTemp(t, map[string]time.Duration{
		"rota-smoke.old": 3 * time.Hour, "rota-gate-tmp.old": 3 * time.Hour, ReviewWorkspacePrefix + "1001": 3 * time.Hour,
		"rota-smoke.fresh": 10 * time.Minute, "unrelated": 3 * time.Hour, "rota-gate.lock": 3 * time.Hour,
		// Look-alikes: another tool's prefix match, the project temp root's gate dirs, empty suffixes, a non-numeric review name.
		"rota-gate-foo-other-tool": 3 * time.Hour, "rota-gate-logs-x": 3 * time.Hour, "rota-gate-bin-x": 3 * time.Hour,
		"rota-smoke.": 3 * time.Hour, "rota-gate-tmp.": 3 * time.Hour, ReviewWorkspacePrefix + "old": 3 * time.Hour, ReviewWorkspacePrefix + "12ab": 3 * time.Hour, ReviewWorkspacePrefix: 3 * time.Hour,
	})
	// A long-running run: the dir's own mtime is old, a nested file is new.
	busy := filepath.Join(dir, "rota-gate-tmp.busy")
	os.MkdirAll(filepath.Join(busy, "sub"), 0o755)
	os.WriteFile(filepath.Join(busy, "sub", "log"), []byte("x"), 0o644)
	old := time.Now().Add(-3 * time.Hour)
	os.Chtimes(busy, old, old)
	// A file with a matching prefix is not a dir.
	os.WriteFile(filepath.Join(dir, "rota-smoke.file"), nil, 0o644)

	res := find(t, sysInput(t, dir, []ProcUse{{PID: 7, Paths: []string{filepath.Join(dir, "rota-gate-tmp.old")}}}), KindSysTmp)
	got := map[string]string{}
	for _, c := range res.Candidates {
		got[c.ID] = c.Held
	}
	if len(got) != 5 {
		t.Fatalf("candidates = %v, want the 5 prefixed dirs", ids(res.Candidates))
	}
	if h := got["systmp:rota-smoke.old"]; h != "" {
		t.Errorf("old smoke dir held: %s", h)
	}
	if h := got["systmp:"+ReviewWorkspacePrefix+"1001"]; h != "" {
		t.Errorf("old review dir held: %s", h)
	}
	if !strings.Contains(got["systmp:rota-smoke.fresh"], "younger") {
		t.Errorf("fresh dir not held as young: %q", got["systmp:rota-smoke.fresh"])
	}
	if !strings.Contains(got["systmp:rota-gate-tmp.old"], "live process 7") {
		t.Errorf("dir in use not held: %q", got["systmp:rota-gate-tmp.old"])
	}
	if got["systmp:rota-gate-tmp.busy"] == "" {
		t.Errorf("a nested write under an old dir mtime must hold the dir")
	}
}

func TestSysTmpHoldsUnreadableSubtreeAndNeverFollowsSymlinks(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads every subtree")
	}
	dir := sysTemp(t, map[string]time.Duration{"rota-smoke.locked": 3 * time.Hour})
	locked := filepath.Join(dir, "rota-smoke.locked", "closed")
	os.Mkdir(locked, 0o755)
	os.WriteFile(filepath.Join(locked, "f"), nil, 0o644)
	os.Chmod(locked, 0)
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	target := t.TempDir()
	os.WriteFile(filepath.Join(target, "keep"), nil, 0o644)
	if err := os.Symlink(target, filepath.Join(dir, "rota-gate-tmp.link")); err != nil {
		t.Fatal(err)
	}
	res := find(t, sysInput(t, dir, nil), KindSysTmp)
	if len(res.Candidates) != 1 || !strings.Contains(res.Candidates[0].Held, "unreadable") {
		t.Fatalf("candidates = %v, want only the locked dir, held as unreadable", ids(res.Candidates))
	}
}

func TestSysTmpSkipsDirsOfOtherUsers(t *testing.T) {
	dir := sysTemp(t, map[string]time.Duration{"rota-smoke.mine": 3 * time.Hour})
	in := sysInput(t, dir, nil)
	defer func(f func() int) { sysTmpUID = f }(sysTmpUID)
	sysTmpUID = func() int { return os.Getuid() + 1 }
	if res := find(t, in, KindSysTmp); len(res.Candidates) != 0 {
		t.Fatalf("another user's dirs listed: %v", ids(res.Candidates))
	}
}

func TestSysTmpApplyRemovesFreeAndReprovesHeld(t *testing.T) {
	dir := sysTemp(t, map[string]time.Duration{"rota-smoke.old": 3 * time.Hour, "rota-review-2002": 3 * time.Hour})
	in := sysInput(t, dir, nil)
	res := find(t, in, KindSysTmp)
	// A process takes the review dir after Find; Apply re-reads the process table.
	in.ProcessUses = func() ([]ProcUse, error) {
		return []ProcUse{{PID: 9, Paths: []string{filepath.Join(dir, "rota-review-2002")}}}, nil
	}
	applied := Apply(bg, in, res.Candidates)
	if len(applied.Reaped) != 1 || applied.Reaped[0] != "systmp:rota-smoke.old" || len(applied.Failed) != 1 {
		t.Fatalf("reaped %v failed %v", applied.Reaped, applied.Failed)
	}
	if _, err := os.Stat(filepath.Join(dir, "rota-smoke.old")); !os.IsNotExist(err) {
		t.Errorf("free dir still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "rota-review-2002")); err != nil {
		t.Errorf("dir in use was removed: %v", err)
	}
	// A forged candidate naming anything else is refused.
	for _, name := range []string{"unrelated", "../x", "rota-smoke.old/../x"} {
		r := Apply(bg, in, []Candidate{{ID: "systmp:" + name, Kind: KindSysTmp, Name: name}})
		if len(r.Failed) != 1 {
			t.Errorf("%q not refused: %+v", name, r)
		}
	}
}

// The names the shell harness creates come from this package; a drifted
// mktemp template there would leak dirs reap never lists. Pin the system-temp
// mktemp lines themselves, not a comment or the project-temp rota-gate-* dirs.
func TestSysTmpPrefixesMatchTheShellHarness(t *testing.T) {
	for file, re := range map[string]*regexp.Regexp{
		"runner.sh": regexp.MustCompile(`(?m)^d="\$\(mktemp -d -t ` + regexp.QuoteMeta(SmokePrefix) + `X{6}\)"`),
		"gate.sh":   regexp.MustCompile(`(?m)^GATE_SCRATCH="\$\(mktemp -d "\$\{TMPDIR:-/tmp\}/` + regexp.QuoteMeta(GatePrefix) + `X{6}"\)"`),
	} {
		b, err := os.ReadFile(filepath.Join("..", "..", "test", file))
		if err != nil {
			t.Fatal(err)
		}
		if !re.Match(b) {
			t.Errorf("test/%s has no system-temp mktemp line matching %s", file, re)
		}
	}
}
