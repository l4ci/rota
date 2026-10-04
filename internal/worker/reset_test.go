package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/golden"
)

// slotProject builds a project with one initialised slot.
func slotProject(t *testing.T) string {
	t.Helper()
	b := newProject(t, `{}`)
	if _, err := goInit(t, b, InitOpts{Slots: 1, Base: "main"}); err != nil {
		t.Fatal(err)
	}
	return b
}

func wt(d string) string { return filepath.Join(d, ".worktrees", "w1") }

func commitIn(t *testing.T, dir, file string) {
	t.Helper()
	os.WriteFile(filepath.Join(dir, file), []byte(file+"\n"), 0o644)
	sh(t, dir, "git", "add", file)
	sh(t, dir, "git", "commit", "-q", "-m", "add "+file)
}

func TestResetCleanSlotCutsTaskBranch(t *testing.T) {
	b := slotProject(t)
	var want map[string]string
	golden.Golden(t, map[string]any{"config": `{}`, "pool": "init --slots 1 --base main", "argv": "reset --slot w1 --task 'T-7/Fix Me'"}, &want)
	res, err := Env{}.Reset(b, "w1", "T-7/Fix Me", false)
	if err != nil {
		t.Fatal(err)
	}
	mustEqual(t, "workers.json", want["workers.json"], registry(t, b))
	if res.Branch != "rota-worker/w1-t-7-fix-me" || !res.Changed || !res.Clean || res.Retained {
		t.Errorf("%+v", res)
	}
	if want := sh(t, b, "git", "rev-parse", "--short", "main"); res.SHA != want {
		t.Errorf("sha = %s, want %s", res.SHA, want)
	}
	if got := sh(t, wt(b), "git", "symbolic-ref", "--short", "HEAD"); got != "rota-worker/w1-t-7-fix-me" {
		t.Errorf("worktree on %s", got)
	}
	if out := sh(t, b, "git", "branch", "--list", "rota-worker/w1"); out != "" {
		t.Errorf("old per-task branch not dropped: %s", out)
	}
}

func TestResetCheckOnlyChangesNothing(t *testing.T) {
	b := slotProject(t)
	before := registry(t, b)
	res, err := Env{}.Reset(b, "w1", "T1", true)
	if err != nil || !res.Clean || res.Changed || res.Branch != "" || res.SHA != "" {
		t.Fatalf("%+v %v", res, err)
	}
	mustEqual(t, "registry", before, registry(t, b))
	if got := sh(t, wt(b), "git", "symbolic-ref", "--short", "HEAD"); got != "rota-worker/w1" {
		t.Errorf("--check-only moved the worktree to %s", got)
	}
}

func TestResetRefusesDirtyWorktree(t *testing.T) {
	b := slotProject(t)
	os.WriteFile(filepath.Join(wt(b), "untracked.txt"), []byte("x"), 0o644)
	before := registry(t, b)
	res, err := Env{}.Reset(b, "w1", "T2", false)
	we, ok := err.(*Error)
	if !ok || we.Exit != ExitRefused || !strings.Contains(we.Message, "REFUSED w1 — uncommitted changes") {
		t.Fatalf("err = %v", err)
	}
	if res.Clean || res.Changed || len(res.Dirty) != 1 || res.Dirty[0] != "?? untracked.txt" || len(res.Unmerged) != 0 {
		t.Errorf("%+v", res)
	}
	if rd, _ := we.Data.(ResetResult); rd.Dirty == nil {
		t.Errorf("failure data missing: %#v", we.Data)
	}
	mustEqual(t, "registry", before, registry(t, b))
	// the same refusal is exit 1 under --check-only
	_, err = Env{}.Reset(b, "w1", "T2", true)
	if we, ok := err.(*Error); !ok || we.Exit != ExitFailed {
		t.Errorf("--check-only err = %v", err)
	}
}

func TestResetRefusesUnmergedCommits(t *testing.T) {
	b := slotProject(t)
	commitIn(t, wt(b), "work.txt")
	res, err := Env{}.Reset(b, "w1", "T3", false)
	we, ok := err.(*Error)
	if !ok || we.Exit != ExitRefused || !strings.Contains(we.Message, "REFUSED w1 — 1 commit(s) not on main") {
		t.Fatalf("err = %v", err)
	}
	if len(res.Unmerged) != 1 || !strings.HasSuffix(res.Unmerged[0], " add work.txt") || len(res.Dirty) != 0 {
		t.Errorf("%+v", res)
	}
}

// git cherry compares by patch: a cherry-picked merge counts as merged.
func TestResetTreatsACherryPickedCommitAsMerged(t *testing.T) {
	b := slotProject(t)
	commitIn(t, wt(b), "work.txt")
	sha := sh(t, wt(b), "git", "rev-parse", "HEAD")
	sh(t, b, "git", "cherry-pick", sha)
	res, err := Env{}.Reset(b, "w1", "", true)
	if err != nil || !res.Clean {
		t.Errorf("%+v %v", res, err)
	}
}

func TestResetRetryKeepsTheTasksOwnWork(t *testing.T) {
	b := slotProject(t)
	var want map[string]string
	golden.Golden(t, map[string]any{"config": `{}`, "pool": "init --slots 1 --base main",
		"steps": []string{"reset --slot w1 --task T4", "write wip.txt in w1, record task T4 on the slot", "reset --slot w1 --task T4"}}, &want)
	if _, err := (Env{}).Reset(b, "w1", "T4", false); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(wt(b), "wip.txt"), []byte("wip"), 0o644)
	// dispatch records the task on the slot; the guard compares against it
	raw, _ := os.ReadFile(RegistryPath(b))
	os.WriteFile(RegistryPath(b), []byte(strings.Replace(string(raw), `"task": null`, `"task": "T4"`, 1)), 0o644)
	before := registry(t, b)
	res, err := Env{}.Reset(b, "w1", "T4", false)
	if err != nil || !res.Retained || res.Changed || res.Clean || res.Branch != "rota-worker/w1-t4" {
		t.Fatalf("%+v %v", res, err)
	}
	mustEqual(t, "registry", before, registry(t, b))
	mustEqual(t, "registry vs golden", want["workers.json"], registry(t, b))
	if _, err := os.Stat(filepath.Join(wt(b), "wip.txt")); err != nil {
		t.Error("the WIP was dropped")
	}
	// a DIFFERENT task on the same dirty slot is still refused
	if _, err := (Env{}).Reset(b, "w1", "T5", false); err == nil {
		t.Error("a new task must not inherit the last task's WIP")
	}
}

func TestResetResolutionFailures(t *testing.T) {
	dir := newProject(t, `{}`)
	exit := func(err error) int {
		if we, ok := err.(*Error); ok {
			return we.Exit
		}
		return -1
	}
	_, err := Env{}.Reset(dir, "w1", "", false)
	if exit(err) != ExitResolution || !strings.Contains(err.Error(), "no worker pool") {
		t.Errorf("no registry: %v", err)
	}
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	if _, err = (Env{}).Reset(dir, "w9", "", false); exit(err) != ExitResolution || !strings.Contains(err.Error(), "slot 'w9' is not in the pool") {
		t.Errorf("unknown slot: %v", err)
	}
	sh(t, dir, "git", "branch", "-m", "main", "trunk")
	if _, err = (Env{}).Reset(dir, "w1", "", false); exit(err) != ExitResolution || !strings.Contains(err.Error(), "base 'main' does not exist") {
		t.Errorf("missing base: %v", err)
	}
	os.RemoveAll(wt(dir))
	if _, err = (Env{}).Reset(dir, "w1", "", false); exit(err) != ExitResolution || !strings.Contains(err.Error(), "worktree missing") {
		t.Errorf("missing worktree: %v", err)
	}
}

func TestBranchFor(t *testing.T) {
	for _, c := range []struct{ task, want string }{
		{"", "rota-worker/w1"},
		{"T-7", "rota-worker/w1-t-7"},
		{"F42 Add/Thing", "rota-worker/w1-f42-add-thing"},
		{"a.b_c-d", "rota-worker/w1-a.b_c-d"},
		{"ä", "rota-worker/w1---"}, // tr works bytewise: two bytes, two dashes
	} {
		if got := BranchFor("w1", c.task); got != c.want {
			t.Errorf("BranchFor(%q) = %q, want %q", c.task, got, c.want)
		}
	}
}

func TestBranchForMatchesTr(t *testing.T) {
	for _, task := range []string{"B07", "Fix: login (v2)", "x\ty", "ÄÖ-1"} {
		old := sh(t, t.TempDir(), "bash", "-c", `printf '%s' "$1" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9._\n-' '-'`, "_", task)
		if got := BranchFor("w1", task); got != "rota-worker/w1-"+old {
			t.Errorf("BranchFor(%q) = %q, tr gives %q", task, got, old)
		}
	}
}

// A git call that fails must not read as "clean": the reset would then switch
// -C over unpushed commits. The retired shell helper aborted here under set -e.
func TestResetTreatsAFailingGitAsUnavailableNotClean(t *testing.T) {
	for _, failing := range []string{"status", "cherry"} {
		b := slotProject(t)
		commitIn(t, wt(b), "unpushed.txt")
		before := registry(t, b)
		e := Env{Git: func(ctx context.Context, dir string, args ...string) (string, string, int, error) {
			if len(args) > 0 && args[0] == failing {
				return "", "fatal: boom", 128, nil
			}
			return ExecGit(ctx, dir, args...)
		}}
		for _, checkOnly := range []bool{true, false} {
			res, err := e.Reset(b, "w1", "T9", checkOnly)
			we, ok := err.(*Error)
			if !ok || we.Exit != ExitUnavailable || !strings.Contains(we.Message, "git "+failing) || res.Clean || res.Changed {
				t.Errorf("%s checkOnly=%v: %+v %v", failing, checkOnly, res, err)
			}
		}
		if got := sh(t, wt(b), "git", "symbolic-ref", "--short", "HEAD"); got != "rota-worker/w1" {
			t.Errorf("%s: the worktree was switched to %s", failing, got)
		}
		mustEqual(t, "registry", before, registry(t, b))
		if _, err := os.Stat(filepath.Join(wt(b), "unpushed.txt")); err != nil {
			t.Errorf("%s: unpushed work lost", failing)
		}
	}
	// a git that cannot run at all (code 127) is the same
	e := Env{Git: func(context.Context, string, ...string) (string, string, int, error) {
		return "", "", 0, os.ErrNotExist
	}}
	b := slotProject(t)
	if _, err := e.Reset(b, "w1", "", false); err == nil {
		t.Error("an unrunnable git must fail the reset")
	}
}

func TestExecGitHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(bg)
	cancel()
	_, _, code, err := ExecGit(ctx, t.TempDir(), "status")
	if err == nil && code == 0 {
		t.Error("a cancelled context must stop git")
	}
}

// The gate matches git's English messages (CONFLICT), so ExecGit pins the
// locale whatever the caller has.
func TestExecGitRunsInTheCLocale(t *testing.T) {
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	t.Setenv("LANGUAGE", "de")
	out, _, code, err := ExecGit(bg, t.TempDir(), "-c", "alias.loc=!echo $LC_ALL/$LANGUAGE", "loc")
	if err != nil || code != 0 || strings.TrimSpace(out) != "C/C" {
		t.Errorf("%q %d %v", out, code, err)
	}
}

// Contract: each unmerged entry is `<sha7> <subject>`.
func TestResetUnmergedEntriesAreSevenCharSHAAndSubject(t *testing.T) {
	b := slotProject(t)
	sh(t, b, "git", "config", "core.abbrev", "12")
	commitIn(t, wt(b), "work.txt")
	res, err := Env{}.Reset(b, "w1", "T1", true)
	if err == nil || len(res.Unmerged) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	sha, subject, _ := strings.Cut(res.Unmerged[0], " ")
	if len(sha) != 7 || subject != "add work.txt" {
		t.Errorf("entry = %q", res.Unmerged[0])
	}
}
