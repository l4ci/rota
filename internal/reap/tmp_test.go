package reap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/round"
)

func agentAt(cwd string) host.Agent { return host.Agent{Tab: "t", Name: "x", Cwd: cwd} }

// tmpRoot builds a temp root with a .gitignore and the named entries, each a
// directory holding one 2048-byte file aged by the given duration.
func tmpRoot(t *testing.T, entries map[string]time.Duration) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	os.Chtimes(filepath.Join(dir, ".gitignore"), old, old)
	for name, age := range entries {
		e := filepath.Join(dir, name)
		if err := os.MkdirAll(e, 0o755); err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(e, "data")
		if err := os.WriteFile(f, make([]byte, 2048), 0o644); err != nil {
			t.Fatal(err)
		}
		ts := time.Now().Add(-age)
		os.Chtimes(f, ts, ts)
		os.Chtimes(e, ts, ts)
	}
	return dir
}

func tmpInput(t *testing.T, dir string, cwds []string, cwdErr error) Input {
	g := repo(nil, nil)
	in := input(g, nil, nil, nil)
	in.TempRoot = dir
	in.ProcessUses = func() ([]ProcUse, error) {
		if cwdErr != nil {
			return nil, cwdErr
		}
		return []ProcUse{{PID: 42, Paths: cwds}}, nil
	}
	return in
}

func TestTmpStaleEntryListedWithSizeAgeAndRemoved(t *testing.T) {
	dir := tmpRoot(t, map[string]time.Duration{"run-a": 5 * time.Hour})
	in := tmpInput(t, dir, nil, nil)
	res := find(t, in, KindTmp)
	if got := ids(res.Candidates); len(got) != 1 || got[0] != "tmp:run-a" {
		t.Fatalf("candidates = %v", got)
	}
	c := res.Candidates[0]
	if c.Path != filepath.Join(dir, "run-a") || !strings.Contains(c.Reason, "2.0 KiB") || !strings.Contains(c.Reason, "5h") {
		t.Errorf("candidate = %+v", c)
	}
	applied := Apply(bg, in, res.Candidates)
	if len(applied.Reaped) != 1 || len(applied.Failed) != 0 {
		t.Fatalf("apply = %+v", applied)
	}
	if _, err := os.Lstat(c.Path); !os.IsNotExist(err) {
		t.Errorf("entry still there: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".gitignore")); err != nil {
		t.Errorf(".gitignore gone: %v", err)
	}
}

func TestTmpHeld(t *testing.T) {
	for _, tc := range []struct {
		name string
		age  time.Duration
		cwds func(dir string) []string
		err  error
		want string
	}{
		{name: "young", age: 10 * time.Minute, want: "younger than"},
		{name: "process cwd under it", age: 5 * time.Hour, cwds: func(d string) []string { return []string{filepath.Join(d, "run-a", "sub")} }, want: "process"},
		{name: "process cwd is it", age: 5 * time.Hour, cwds: func(d string) []string { return []string{filepath.Join(d, "run-a")} }, want: "process"},
		{name: "sibling prefix is not under it", age: 5 * time.Hour, cwds: func(d string) []string { return []string{filepath.Join(d, "run-ab")} }, want: ""},
		{name: "process HOME or open file under it", age: 5 * time.Hour, cwds: func(d string) []string { return []string{"/elsewhere", filepath.Join(d, "run-a", "home")} }, want: "live process 42 uses"},
		{name: "process table unreadable", age: 5 * time.Hour, err: errors.New("no /proc"), want: "cannot prove no process uses it"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := tmpRoot(t, map[string]time.Duration{"run-a": tc.age})
			var cwds []string
			if tc.cwds != nil {
				cwds = tc.cwds(dir)
			}
			in := tmpInput(t, dir, cwds, tc.err)
			res := find(t, in, KindTmp)
			if len(res.Candidates) != 1 {
				t.Fatalf("candidates = %v", ids(res.Candidates))
			}
			c := res.Candidates[0]
			if tc.want == "" {
				if c.Held != "" {
					t.Fatalf("held = %q", c.Held)
				}
				return
			}
			if !strings.Contains(c.Held, tc.want) {
				t.Fatalf("held = %q, want %q", c.Held, tc.want)
			}
			applied := Apply(bg, in, res.Candidates)
			if len(applied.Reaped) != 0 {
				t.Errorf("held entry reaped: %+v", applied)
			}
			if _, err := os.Lstat(c.Path); err != nil {
				t.Errorf("held entry removed: %v", err)
			}
		})
	}
}

func TestTmpNilProcessUsesHolds(t *testing.T) {
	dir := tmpRoot(t, map[string]time.Duration{"run-a": 5 * time.Hour})
	in := tmpInput(t, dir, nil, nil)
	in.ProcessUses = nil
	res := find(t, in, KindTmp)
	if len(res.Candidates) != 1 || !strings.Contains(res.Candidates[0].Held, "cannot prove no process uses it") {
		t.Fatalf("candidates = %v", ids(res.Candidates))
	}
}

func TestTmpAgentCwdUnderEntryHolds(t *testing.T) {
	dir := tmpRoot(t, map[string]time.Duration{"run-a": 5 * time.Hour})
	in := tmpInput(t, dir, nil, nil)
	in.Report.Agents = append(in.Report.Agents, agentAt(filepath.Join(dir, "run-a", "x")))
	res := find(t, in, KindTmp)
	if len(res.Candidates) != 1 || !strings.Contains(res.Candidates[0].Held, "agent") {
		t.Fatalf("candidates = %v", ids(res.Candidates))
	}
}

func TestTmpCheckoutUnderEntryHolds(t *testing.T) {
	dir := tmpRoot(t, map[string]time.Duration{"run-a": 5 * time.Hour})
	in := tmpInput(t, dir, nil, nil)
	in.Git = func(ctx context.Context, d string, args ...string) (git.Result, error) {
		if strings.Join(args, " ") == "worktree list --porcelain" {
			return git.Result{Stdout: "worktree /r\nbranch refs/heads/main\n\nworktree " + filepath.Join(dir, "run-a", "wt") + "\nHEAD abc\n\n"}, nil
		}
		return git.Result{ExitCode: 1}, nil
	}
	res := find(t, in, KindTmp)
	if len(res.Candidates) != 1 || !strings.Contains(res.Candidates[0].Held, "worktree") {
		t.Fatalf("candidates = %v", ids(res.Candidates))
	}
}

func TestTmpGitignoreAndEmptyRootNeverListed(t *testing.T) {
	dir := tmpRoot(t, nil)
	if got := find(t, tmpInput(t, dir, nil, nil), KindTmp).Candidates; len(got) != 0 {
		t.Errorf("candidates = %v", ids(got))
	}
	in := tmpInput(t, "", nil, nil)
	if got := find(t, in, KindTmp).Candidates; len(got) != 0 {
		t.Errorf("empty TempRoot listed %v", ids(got))
	}
}

func TestTmpRemoverRefusesNonChildren(t *testing.T) {
	dir := tmpRoot(t, map[string]time.Duration{"run-a": 5 * time.Hour})
	outside := t.TempDir()
	in := tmpInput(t, dir, nil, nil)
	for _, name := range []string{".gitignore", "..", ".", "", "run-a/sub", "../" + filepath.Base(outside), outside} {
		applied := Apply(bg, in, []Candidate{{ID: "tmp:" + name, Kind: KindTmp, Name: name, Path: filepath.Join(dir, name)}})
		if len(applied.Failed) != 1 || len(applied.Reaped) != 0 {
			t.Errorf("name %q: apply = %+v", name, applied)
		}
	}
	for _, p := range []string{dir, outside, filepath.Join(dir, ".gitignore"), filepath.Join(dir, "run-a")} {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("%s gone: %v", p, err)
		}
	}
}

func TestTmpSymlinkEntryNotFollowed(t *testing.T) {
	dir := tmpRoot(t, nil)
	target := t.TempDir()
	os.WriteFile(filepath.Join(target, "keep"), []byte("x"), 0o644)
	if err := os.Symlink(target, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	// A just-made link is young; drop the age hold so removal really runs.
	old := tmpMinAge
	tmpMinAge = -time.Hour
	t.Cleanup(func() { tmpMinAge = old })
	in := tmpInput(t, dir, nil, nil)
	res := find(t, in, KindTmp)
	if got := ids(res.Candidates); len(got) != 1 || got[0] != "tmp:link" || res.Candidates[0].Held != "" {
		t.Fatalf("candidates = %+v, want the link free to remove", res.Candidates)
	}
	if applied := Apply(bg, in, res.Candidates); len(applied.Reaped) != 1 {
		t.Fatalf("link not reaped: %+v", applied)
	}
	if _, err := os.Lstat(filepath.Join(dir, "link")); !os.IsNotExist(err) {
		t.Errorf("link survived removal: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(target, "keep")); err != nil {
		t.Errorf("symlink target contents removed: %v", err)
	}
}

// Reap deletes only under a root rota made: without the root's own .gitignore
// (or with another one) the directory is not the project temp root.
func TestTmpRootWithoutMarkerListsNothing(t *testing.T) {
	dir := tmpRoot(t, map[string]time.Duration{"run-a": 3 * time.Hour})
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("other\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	in := tmpInput(t, dir, nil, nil)
	got, err := Find(context.Background(), in, []string{KindTmp})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Candidates) != 0 {
		t.Fatalf("an unowned root listed %v", ids(got.Candidates))
	}
	res := Apply(context.Background(), in, []Candidate{{ID: "tmp:run-a", Kind: KindTmp, Name: "run-a"}})
	if len(res.Reaped) != 0 {
		t.Fatalf("an unowned root reaped %v", res.Reaped)
	}
	if _, err := os.Stat(filepath.Join(dir, "run-a")); err != nil {
		t.Fatalf("entry removed: %v", err)
	}
}

// A live slot's TMPDIR lives under <root>/slots/<slot> (#3164): reap holds the
// slots entry while any slot is registered, and offers it once none is.
func TestTmpSlotsDirHeldWhileASlotIsRegistered(t *testing.T) {
	dir := tmpRoot(t, map[string]time.Duration{"slots": 5 * time.Hour})
	in := tmpInput(t, dir, nil, nil)
	in.Report.Rows = []round.Row{{Name: "kit", Registered: true}}
	res := find(t, in, KindTmp)
	if len(res.Candidates) != 1 || !strings.Contains(res.Candidates[0].Held, "slot kit") {
		t.Fatalf("candidates = %v", ids(res.Candidates))
	}
	applied := Apply(bg, in, res.Candidates)
	if len(applied.Reaped) != 0 {
		t.Errorf("held slots dir reaped: %+v", applied)
	}
	in.Report.Rows = nil
	res = find(t, in, KindTmp)
	if len(res.Candidates) != 1 || res.Candidates[0].Held != "" {
		t.Fatalf("with no slot registered: %v", ids(res.Candidates))
	}
}
