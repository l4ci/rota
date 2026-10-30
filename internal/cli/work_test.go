package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/status"
)

// workDeps stubs the environment: no ROTA_SLOT unless slot is set.
func workDeps(slot string) *Deps {
	d := testDeps()
	real := d.Getenv
	d.Getenv = func(k string) string {
		if k == "ROTA_SLOT" {
			return slot
		}
		return real(k)
	}
	return d
}

// workRepo is a committed single-repo project on main with the file backend:
// B01 "Quick switch overlay" in the backlog, status.json ignored.
func workRepo(t *testing.T, cfg string) string {
	t.Helper()
	root := gittest.TempDir(t)
	gittest.Init(t, root, "main")
	if cfg == "" {
		cfg = `{"issues": {"source": "file"}}`
	}
	write(t, filepath.Join(root, ".rota", "config.json"), cfg+"\n")
	write(t, filepath.Join(root, ".rota", "BACKLOG.md"), "# TODO\n\n## Bugs\n- **[B01] [P1] Quick switch overlay.** x\n\n## Completed\n")
	write(t, filepath.Join(root, ".rota", "counters.json"), "{}\n")
	write(t, filepath.Join(root, ".gitignore"), ".rota/status.json\n.rota/**/*.lock\n")
	write(t, filepath.Join(root, "Foo.gd"), "extends Node\n")
	gitT(t, root, "add", "-A")
	gitT(t, root, "commit", "-q", "-m", "seed")
	return root
}

func workItems(t *testing.T, d *jsonx.Object) []*jsonx.Object {
	t.Helper()
	var out []*jsonx.Object
	for _, v := range list(d, "items") {
		o, ok := v.(*jsonx.Object)
		if !ok {
			t.Fatalf("items entry %v", v)
		}
		out = append(out, o)
	}
	return out
}

func TestWorkStartCreatesBranchAndRegisters(t *testing.T) {
	root := workRepo(t, "")
	d := runOKWith(t, workDeps(""), root, 0, "work", "start", "B01")
	if get(d, "branch") != "rota/b01-quick-switch-overlay" || get(d, "base") != "main" ||
		get(d, "isolation") != "branch" || get(d, "worktree") != nil || get(d, "changed") != true {
		t.Fatalf("data %v", d)
	}
	if got := gitT(t, root, "rev-parse", "--abbrev-ref", "HEAD"); strings.TrimSpace(got) != "rota/b01-quick-switch-overlay" {
		t.Fatalf("current branch %q", got)
	}
	e, ok := status.Find(root, "rota/b01-quick-switch-overlay", "")
	if !ok || !reflect.DeepEqual(e.Items, []string{"B01"}) {
		t.Fatalf("status entry %v %v", e, ok)
	}
	it := workItems(t, d)
	if len(it) != 1 || get(it[0], "id") != "B01" || get(it[0], "claim") != "none" || get(it[0], "ready") != true {
		t.Fatalf("items %v", it)
	}
}

func TestWorkStartSecondRunChangesNothing(t *testing.T) {
	root := workRepo(t, "")
	deps := workDeps("")
	runOKWith(t, deps, root, 0, "work", "start", "B01")
	write(t, filepath.Join(root, "wip.txt"), "work in progress\n") // a dirty tree is fine on a re-run
	d := runOKWith(t, deps, root, 0, "work", "start", "B01")
	if get(d, "changed") != false || get(d, "branch") != "rota/b01-quick-switch-overlay" {
		t.Fatalf("second run: %v", d)
	}
	if n := len(status.Entries(root)); n != 1 {
		t.Fatalf("%d status entries", n)
	}
	// Off the branch, a re-run checks it out again and says so.
	gitT(t, root, "stash", "-u", "-q")
	gitT(t, root, "checkout", "-q", "main")
	d = runOKWith(t, deps, root, 0, "work", "start", "B01")
	if get(d, "changed") != true {
		t.Fatalf("checkout run: %v", d)
	}
	if got := strings.TrimSpace(gitT(t, root, "rev-parse", "--abbrev-ref", "HEAD")); got != "rota/b01-quick-switch-overlay" {
		t.Fatalf("current branch %q", got)
	}
}

func TestWorkStartRefusesUserChange(t *testing.T) {
	root := workRepo(t, "")
	write(t, filepath.Join(root, "notes.txt"), "mine\n")
	write(t, filepath.Join(root, "Foo.gd.uid"), "uid://x\n")
	d := runOKWith(t, workDeps(""), root, ExitFailed, "work", "start", "B01")
	if !reflect.DeepEqual(list(d, "dirty"), []any{"notes.txt"}) {
		t.Fatalf("dirty %v", get(d, "dirty"))
	}
	if gitT(t, root, "branch", "--list", "rota/*") != "" {
		t.Fatal("a branch was created")
	}
	if _, ok := status.Find(root, "rota/b01-quick-switch-overlay", ""); ok {
		t.Fatal("a status entry was written")
	}
	if strings.Count(gitT(t, root, "log", "--oneline"), "\n") > 1 {
		t.Fatal("a sweep commit was made")
	}
}

func TestWorkStartSweepsSiblings(t *testing.T) {
	root := workRepo(t, "")
	write(t, filepath.Join(root, "Foo.gd.uid"), "uid://x\n")
	write(t, filepath.Join(root, ".DS_Store"), "x")
	write(t, filepath.Join(root, ".rota", "designs", "B01.md"), "design\n")
	write(t, filepath.Join(root, ".rota", "plans", "B01.md"), "plan\n")
	d := runOKWith(t, workDeps(""), root, 0, "work", "start", "B01")
	want := []any{".DS_Store", "Foo.gd.uid", ".rota/designs/B01.md", ".rota/plans/B01.md"}
	if !reflect.DeepEqual(list(d, "swept"), want) {
		t.Fatalf("swept %v", get(d, "swept"))
	}
	log := gitT(t, root, "log", "--format=%s")
	if !strings.Contains(log, "chore: sweep tool-generated siblings") || !strings.Contains(log, "chore: capture B01 design and plan") {
		t.Fatalf("log:\n%s", log)
	}
	if out := gitT(t, root, "status", "--porcelain"); out != "" {
		t.Fatalf("tree still dirty: %q", out)
	}
	if got := strings.TrimSpace(gitT(t, root, "rev-parse", "--abbrev-ref", "HEAD")); got != "rota/b01-quick-switch-overlay" {
		t.Fatalf("current branch %q", got)
	}
}

func TestWorkStartNeedsABaseline(t *testing.T) {
	root := gittest.TempDir(t)
	gittest.Init(t, root, "main")
	write(t, filepath.Join(root, ".rota", "config.json"), `{"issues": {"source": "file"}}`+"\n")
	write(t, filepath.Join(root, ".rota", "BACKLOG.md"), "# TODO\n\n## Bugs\n- **[B01] [P1] Quick.** x\n\n## Completed\n")
	runOKWith(t, workDeps(""), root, ExitFailed, "work", "start", "B01")
}

func TestWorkStartWorktreeIsolation(t *testing.T) {
	root := workRepo(t, `{"issues": {"source": "file"}, "work": {"isolation": "worktree"}}`)
	d := runOKWith(t, workDeps(""), root, 0, "work", "start", "B01")
	wt := ".claude/worktrees/rota/b01-quick-switch-overlay"
	if get(d, "isolation") != "worktree" || get(d, "worktree") != wt || get(d, "changed") != true {
		t.Fatalf("data %v", d)
	}
	if _, err := os.Stat(filepath.Join(root, wt, "Foo.gd")); err != nil {
		t.Fatalf("worktree missing: %v", err)
	}
	if got := strings.TrimSpace(gitT(t, root, "rev-parse", "--abbrev-ref", "HEAD")); got != "main" {
		t.Fatalf("main checkout moved to %q", got)
	}
	if e, ok := status.Find(root, "rota/b01-quick-switch-overlay", ""); !ok || e.Worktree != wt {
		t.Fatalf("status entry %v %v", e, ok)
	}
	if d = runOKWith(t, workDeps(""), root, 0, "work", "start", "B01"); get(d, "changed") != false || get(d, "worktree") != wt {
		t.Fatalf("second run %v", d)
	}
}

func TestWorkStartBranchWithoutEntry(t *testing.T) {
	root := workRepo(t, "")
	gitT(t, root, "branch", "rota/b01-quick-switch-overlay")
	d := runOKWith(t, workDeps(""), root, ExitRefused, "work", "start", "B01")
	if get(d, "blockedBy") != "exists" || get(d, "changed") != false {
		t.Fatalf("data %v", d)
	}
	d = runOKWith(t, workDeps(""), root, 0, "work", "start", "B01", "--branch", "rota/other")
	if get(d, "branch") != "rota/other" {
		t.Fatalf("data %v", d)
	}
}

func TestWorkStartRefusals(t *testing.T) {
	root := workRepo(t, "")
	d := runOKWith(t, workDeps("dana"), root, ExitRefused, "work", "start", "B01")
	if get(d, "blockedBy") != "round worker" || get(d, "changed") != false {
		t.Fatalf("slot: %v", d)
	}
	if gitT(t, root, "branch", "--list", "rota/*") != "" {
		t.Fatal("a branch was created")
	}
	runOKWith(t, workDeps(""), root, ExitUsage, "work", "start")
	runOKWith(t, workDeps(""), root, ExitResolution, "work", "start", "B99")
	write(t, filepath.Join(root, ".rota", "repos.json"), `{"repos":[{"name":"svc","path":"svc"}]}`)
	d = runOKWith(t, workDeps(""), root, ExitRefused, "work", "start", "B01")
	if get(d, "blockedBy") != "umbrella" {
		t.Fatalf("umbrella: %v", d)
	}
}

func TestWorkStartTrackerClaims(t *testing.T) {
	root := workRepo(t, issuesConfig)
	deps := withTracker(t, flowFixture())
	real := deps.Getenv
	deps.Getenv = func(k string) string {
		if k == "ROTA_SLOT" {
			return ""
		}
		return real(k)
	}
	d := runOKWith(t, deps, root, 0, "work", "start", "7")
	branch := "rota/7-add-export"
	if get(d, "branch") != branch || get(d, "changed") != true {
		t.Fatalf("data %v", d)
	}
	it := workItems(t, d)
	if len(it) != 1 || get(it[0], "id") != "7" || get(it[0], "claim") != "claimed" {
		t.Fatalf("items %v", it)
	}
	if get(it[0], "ready") == nil || list(it[0], "missing") == nil {
		t.Fatalf("ready/missing absent: %v", it[0])
	}
	if s := runOKWith(t, deps, root, 0, "item", "show", "7"); get(s, "claimedBy") != branch {
		t.Fatalf("claim not recorded: %v", s)
	}
	d = runOKWith(t, deps, root, 0, "work", "start", "7")
	it = workItems(t, d)
	if get(d, "changed") != false || get(it[0], "claim") != "held" {
		t.Fatalf("second run: %v", d)
	}
}

func TestWorkStartTrackerClaimedByOther(t *testing.T) {
	root := workRepo(t, issuesConfig)
	deps := withTracker(t, flowFixture())
	real := deps.Getenv
	deps.Getenv = func(k string) string {
		if k == "ROTA_SLOT" {
			return ""
		}
		return real(k)
	}
	runOKWith(t, deps, root, 0, "item", "claim", "7", "--as", "bob")
	d := runOKWith(t, deps, root, ExitRefused, "work", "start", "7")
	it := workItems(t, d)
	if get(d, "blockedBy") != "claimed" || len(it) != 1 || get(it[0], "claim") != "dropped" {
		t.Fatalf("data %v", d)
	}
	if gitT(t, root, "branch", "--list", "rota/*") != "" {
		t.Fatal("a branch was created")
	}
}
