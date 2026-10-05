package worker

import (
	"github.com/l4ci/rota/internal/exitcode"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/golden"
)

// Fixture goldens: the Go port runs on a freshly built project and must leave
// the .rota/workers.json the retired shell helper wrote, frozen under
// testdata/golden.

func goInit(t *testing.T, dir string, o InitOpts) (InitResult, error) {
	t.Helper()
	return Env{}.PoolInit(bg, dir, o, &Accounts{})
}

func TestPoolInit(t *testing.T) {
	for name, cfg := range map[string]string{"tmux": `{}`, "herdr": `{"work":{"dispatch":"herdr"}}`} {
		t.Run(name, func(t *testing.T) {
			b := newProject(t, cfg)
			got := map[string]string{}
			res, err := goInit(t, b, InitOpts{Slots: 3, Base: "main", Session: "s1"})
			if err != nil {
				t.Fatal(err)
			}
			got["after init"] = registry(t, b)
			if !res.Changed || len(res.Slots) != 3 {
				t.Errorf("result = %+v", res)
			}
			// Idempotent: a second run changes nothing, and grows with a larger count.
			again, err := goInit(t, b, InitOpts{Slots: 3, Base: "main", Session: "s1"})
			if err != nil || again.Changed {
				t.Errorf("re-init changed=%v err=%v", again.Changed, err)
			}
			goInit(t, b, InitOpts{Slots: 4, Base: "main", Session: "s1"})
			got["after grow"] = registry(t, b)
			golden.Check(t, map[string]any{"config": cfg, "steps": []string{"init --slots 3 --base main --session s1", "init --slots 4 --base main --session s1"}}, got)
			if _, err := os.Stat(filepath.Join(b, ".worktrees", "w4", ".git")); err != nil {
				t.Errorf("w4 worktree missing: %v", err)
			}
		})
	}
}

func TestPoolInitDefaultsToCurrentBranchAndHv(t *testing.T) {
	b := newProject(t, `{}`)
	res, err := goInit(t, b, InitOpts{Slots: 1})
	if err != nil || res.Base != "main" || res.Session != "rota" {
		t.Fatalf("%+v %v", res, err)
	}
	golden.Check(t, map[string]any{"config": `{}`, "argv": "init --slots 1"}, map[string]string{"workers.json": registry(t, b)})
}

func TestPoolInitErrors(t *testing.T) {
	dir := newProject(t, `{}`)
	_, err := goInit(t, dir, InitOpts{Slots: 1, Base: "nope"})
	if we, ok := err.(*exitcode.Error); !ok || we.Exit != exitcode.ExitResolution || !strings.Contains(we.Message, "base branch 'nope' does not exist") {
		t.Errorf("missing base: %v", err)
	}
	sh(t, dir, "git", "checkout", "-q", "--detach")
	_, err = goInit(t, dir, InitOpts{Slots: 1})
	if we, ok := err.(*exitcode.Error); !ok || we.Exit != exitcode.ExitResolution || !strings.Contains(we.Message, "cannot resolve base branch") {
		t.Errorf("detached HEAD without --base: %v", err)
	}
}

func TestPoolInitMigratesWindowToHandleAndKeepsLiveTab(t *testing.T) {
	cfg := `{"work":{"dispatch":"herdr"}}`
	b := newProject(t, cfg)
	goInit(t, b, InitOpts{Slots: 2, Base: "main"})
	// slot 1 had a live tab recorded; slot 2 is an unmigrated pre-herdr registry
	raw, _ := os.ReadFile(RegistryPath(b))
	s := strings.Replace(string(raw), `"handle": null,`, `"handle": "w9:t4",`, 1)
	s = strings.Replace(s, `"handle": null,`, `"window": "rota:w2",`, 1)
	os.WriteFile(RegistryPath(b), []byte(s), 0o644)
	goInit(t, b, InitOpts{Slots: 2, Base: "main"})
	golden.Check(t, map[string]any{"config": cfg, "steps": []string{"init --slots 2 --base main", "slot 1 handle w9:t4, slot 2 window rota:w2", "init --slots 2 --base main"}}, map[string]string{"workers.json": registry(t, b)})
	if got := registry(t, b); !strings.Contains(got, `"handle": "w9:t4"`) || !strings.Contains(got, `"handle": "rota:w2"`) || strings.Contains(got, `"window"`) {
		t.Errorf("window not migrated or live tab clobbered:\n%s", got)
	}
}

func TestPoolInitRegistersTheBranchActuallyCheckedOut(t *testing.T) {
	b := newProject(t, `{}`)
	goInit(t, b, InitOpts{Slots: 1, Base: "main"})
	sh(t, filepath.Join(b, ".worktrees", "w1"), "git", "switch", "-q", "-c", "rota-worker/w1-t9")
	goInit(t, b, InitOpts{Slots: 1, Base: "main"})
	golden.Check(t, map[string]any{"config": `{}`, "steps": []string{"init --slots 1 --base main", "git switch -c rota-worker/w1-t9 in w1", "init --slots 1 --base main"}}, map[string]string{"workers.json": registry(t, b)})
	if !strings.Contains(registry(t, b), `"branch": "rota-worker/w1-t9"`) {
		t.Error("init registered the init-time name, not the checked-out branch")
	}
}

// #79: a slot registered at a legacy path stays there; a foreign repo is refused.
func TestPoolInitLegacySlotStays(t *testing.T) {
	b := newProject(t, `{}`)
	legacy := filepath.Join(b, ".claude", "worktrees", "rota-worker", "w1")
	sh(t, b, "git", "worktree", "add", "-q", "-b", "rota-worker/w1", legacy, "main")
	reg := `{"session":"rota","slots":[{"name":"w1","branch":"rota-worker/w1","worktree":"` + legacy + `","base":"main","handle":"rota:w1","state":"idle","task":null,"pr":null,"relays":[],"configDir":null}]}`
	os.WriteFile(RegistryPath(b), []byte(reg), 0o644)
	res, err := goInit(t, b, InitOpts{Slots: 1, Base: "main"})
	if err != nil {
		t.Fatal(err)
	}
	golden.Check(t, map[string]any{"config": `{}`, "legacy slot": "w1 at <root>/.claude/worktrees/rota-worker/w1 on rota-worker/w1, handle rota:w1", "argv": "init --slots 1 --base main"}, map[string]string{"workers.json": registry(t, b)})
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "stays at") {
		t.Errorf("warnings = %v", res.Warnings)
	}
	if _, err := os.Stat(filepath.Join(b, ".worktrees", "w1")); err == nil {
		t.Error("a second worktree was created at the new root")
	}
}

func TestPoolInitRefusesForeignRepoWorktree(t *testing.T) {
	b, foreignB := newProject(t, `{}`), newProject(t, `{}`)
	reg := `{"session":"rota","slots":[{"name":"w1","branch":"main","worktree":"` + foreignB + `","base":"main","handle":null,"state":"idle","task":null,"pr":null,"relays":[],"configDir":null}]}`
	os.WriteFile(RegistryPath(b), []byte(reg), 0o644)
	before, _ := os.ReadFile(RegistryPath(b))
	_, err := goInit(t, b, InitOpts{Slots: 1, Base: "main"})
	we, ok := err.(*exitcode.Error)
	if !ok || we.Exit != exitcode.ExitResolution || !strings.Contains(we.Message, "slot w1 is registered at "+foreignB+", a worktree of another repository") {
		t.Fatalf("go: %v", err)
	}
	after, _ := os.ReadFile(RegistryPath(b))
	if string(before) != string(after) {
		t.Error("registry changed on a refusal")
	}
}

func TestPoolInitWarnsWhenWorktreesNotIgnored(t *testing.T) {
	dir := newProject(t, `{}`)
	os.WriteFile(filepath.Join(dir, ".gitignore"), nil, 0o644)
	res, err := goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	if err != nil || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], ".worktrees/ is not gitignored") {
		t.Errorf("%+v %v", res.Warnings, err)
	}
}

// Known quirk, ported as is: a plain directory inside the project passes the
// "healthy slot" test (`git -C <dir> rev-parse --git-dir` finds the project's
// own .git), so debris from an interrupted reap is adopted, not cleared. The
// golden pins what the retired helper did; changing it is a behaviour change.
func TestPoolInitTreatsAPlainDirInsideTheProjectAsHealthy(t *testing.T) {
	b := newProject(t, `{}`)
	os.MkdirAll(filepath.Join(b, ".worktrees", "w1"), 0o755)
	os.WriteFile(filepath.Join(b, ".worktrees", "w1", "junk"), []byte("x"), 0o644)
	if _, err := goInit(t, b, InitOpts{Slots: 1, Base: "main"}); err != nil {
		t.Fatal(err)
	}
	golden.Check(t, map[string]any{"config": `{}`, "plain dir": ".worktrees/w1 holding junk", "argv": "init --slots 1 --base main"}, map[string]string{"workers.json": registry(t, b)})
}

func TestPoolReap(t *testing.T) {
	b := newProject(t, `{}`)
	got := map[string]string{}
	goInit(t, b, InitOpts{Slots: 3, Base: "main"})

	reaped, err := Env{}.Reap(b, []string{"w2", "nope"}, false)
	if err != nil || len(reaped) != 1 || reaped[0] != "w2" {
		t.Fatalf("reaped = %v, %v", reaped, err)
	}
	got["after reap w2"] = registry(t, b)
	if _, err := os.Stat(filepath.Join(b, ".worktrees", "w2")); err == nil {
		t.Error("worktree survived the reap")
	}
	if out := sh(t, b, "git", "branch", "--list", "rota-worker/w2"); out != "" {
		t.Errorf("branch survived: %s", out)
	}

	reaped, _ = Env{}.Reap(b, nil, true)
	if len(reaped) != 2 {
		t.Errorf("reaped = %v", reaped)
	}
	got["after reap --all"] = registry(t, b)
	golden.Check(t, map[string]any{"config": `{}`, "steps": []string{"init --slots 3 --base main", "reap --slot w2", "reap --all"}}, got)
}

func TestPoolReapWithoutRegistryIsANoop(t *testing.T) {
	dir := newProject(t, `{}`)
	reaped, err := Env{}.Reap(dir, []string{"w1"}, false)
	if err != nil || len(reaped) != 0 {
		t.Errorf("%v %v", reaped, err)
	}
	if _, err := os.Stat(RegistryPath(dir)); err == nil {
		t.Error("a registry was created")
	}
}

func TestPoolListDropsNullFields(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	session, round, slots := PoolList(dir)
	if session != "rota" || round != nil || len(slots) != 1 {
		t.Fatalf("%v %v %v", session, round, slots)
	}
	for _, k := range []string{"handle"} {
		if _, ok := slots[0].Get(k); !ok {
			t.Errorf("%s missing", k)
		}
	}
	for _, k := range []string{"task", "pr", "configDir"} {
		if _, ok := slots[0].Get(k); ok {
			t.Errorf("null field %s must be absent in data", k)
		}
	}
	if _, ok := slots[0].Get("relays"); !ok {
		t.Error("relays must stay (an empty array)")
	}
	dir2 := newProject(t, `{}`)
	if s, _, sl := PoolList(dir2); s != nil || len(sl) != 0 {
		t.Errorf("no registry: %v %v", s, sl)
	}
}
