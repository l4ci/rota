package round

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/worker"
)

const filesBody = "## Acceptance\n- [ ] ok\n\n## Files\n- internal/cli/round.go\n"

func adoptFixture(t *testing.T) (string, Env, *fakeRemote) {
	t.Helper()
	root := newRepo(t, map[string]string{"codex-a": "codex/12-thing"})
	fb := &fakeRemote{}
	fb.add("12", "Thing", "M01", false, filesBody)
	fb.add("13", "Other", "M01", false, filesBody)
	return root, Env{Git: git.Exec, Base: "main", Getenv: noEnv}, fb
}

func TestAdoptRegistersExternalSlot(t *testing.T) {
	root, e, be := adoptFixture(t)
	wt := filepath.Join(root, ".worktrees", "codex-a")
	for _, ref := range []string{"codex/12-thing", wt} {
		os.Remove(worker.RegistryPath(root))
		res, err := e.Adopt(bg, root, be, AdoptOpts{Ref: ref, Issue: "#12", PR: "https://x/pull/7"})
		if err != nil {
			t.Fatalf("%s: %v", ref, err)
		}
		if res.Slot != "ext-1" || res.Branch != "codex/12-thing" || res.Issue != "12" {
			t.Fatalf("%s: %+v", ref, res)
		}
		s := worker.LoadRegistry(root).Slot("ext-1")
		if s == nil || !s.IsExternal() || s.Handle() != "" || s.Task() != "12" || s.PR() != "https://x/pull/7" ||
			s.Branch() != "codex/12-thing" || s.Base() != "main" {
			t.Fatalf("%s: slot %v", ref, s)
		}
		if got, _ := filepath.EvalSymlinks(s.Worktree()); got == "" || got != res.Worktree {
			t.Errorf("%s: worktree %q vs %q", ref, got, res.Worktree)
		}
	}
	// A second adoption takes the next free name.
	sh(t, root, "branch", "other-work")
	if res, err := e.Adopt(bg, root, be, AdoptOpts{Ref: "other-work", Issue: "13", AcceptOverlap: true}); err != nil || res.Slot != "ext-2" || res.Worktree != "" {
		t.Errorf("second: %v %+v", err, res)
	}
}

func TestAdoptRefusesOverlap(t *testing.T) {
	root, e, be := adoptFixture(t)
	writeRegistry(t, root, slot(root, "ben", "ben/13-other", func(o *jsonx.Object) { o.Set("task", "13") }))
	_, err := e.Adopt(bg, root, be, AdoptOpts{Ref: "codex/12-thing", Issue: "12"})
	if by := blockedBy(t, err); by != BlockOverlap {
		t.Fatalf("want overlap: %v", err)
	}
	if worker.LoadRegistry(root).Slot("ext-1") != nil {
		t.Fatal("a refused adopt registered a slot")
	}
	res, err := e.Adopt(bg, root, be, AdoptOpts{Ref: "codex/12-thing", Issue: "12", AcceptOverlap: true})
	if err != nil || len(res.Overlaps) != 1 || res.Overlaps[0].Slot != "ben" {
		t.Fatalf("accepted overlap: %v %+v", err, res)
	}
}

func TestAdoptRejectsForeignPath(t *testing.T) {
	root, e, be := adoptFixture(t)
	for _, ref := range []string{t.TempDir(), root, "no-such-branch"} {
		_, err := e.Adopt(bg, root, be, AdoptOpts{Ref: ref, Issue: "12"})
		var xe *exitcode.Error
		if !errors.As(err, &xe) || xe.Exit != exitcode.ExitUsage {
			t.Errorf("%s: want exit 2, got %v", ref, err)
		}
	}
	if worker.LoadRegistry(root).Slot("ext-1") != nil {
		t.Fatal("a rejected adopt registered a slot")
	}
}

func TestAdoptRefusesHeldIssue(t *testing.T) {
	root, e, be := adoptFixture(t)
	writeRegistry(t, root, slot(root, "ben", "ben/12-thing", func(o *jsonx.Object) { o.Set("task", "12") }))
	if _, err := e.Adopt(bg, root, be, AdoptOpts{Ref: "codex/12-thing", Issue: "12"}); blockedBy(t, err) != BlockHeld {
		t.Fatalf("want held: %v", err)
	}
}

func TestAdoptRefusesRegisteredBranch(t *testing.T) {
	root, e, be := adoptFixture(t)
	if _, err := e.Adopt(bg, root, be, AdoptOpts{Ref: "codex/12-thing", Issue: "12"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Adopt(bg, root, be, AdoptOpts{Ref: "codex/12-thing", Issue: "13"}); blockedBy(t, err) != BlockRegistered {
		t.Fatalf("want registered: %v", err)
	}
}

func TestAdoptRefusesTheBaseAndParkBranches(t *testing.T) {
	root := newRepo(t, map[string]string{"ben": "park/ben", "codex-a": "codex/12-thing"})
	e, be := Env{Git: git.Exec, Base: "main", Getenv: noEnv}, &fakeRemote{}
	be.add("12", "Thing", "M01", false, filesBody)
	for _, ref := range []string{"main", "park/ben", filepath.Join(root, ".worktrees", "ben")} {
		_, err := e.Adopt(bg, root, be, AdoptOpts{Ref: ref, Issue: "12"})
		if exitOf(err) != exitcode.ExitUsage {
			t.Errorf("%s: want exit 2, got %v", ref, err)
		}
	}
	if len(worker.LoadRegistry(root).Slots()) != 0 {
		t.Error("a refused adoption registered a slot")
	}
}

func TestAdoptResolvesALocalBranchBeforeACwdRelativeDir(t *testing.T) {
	root, e, be := adoptFixture(t)
	cwd := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
	// A directory named like the branch must not turn it into a path.
	if err := os.MkdirAll(filepath.Join("codex", "12-thing"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := e.Adopt(bg, root, be, AdoptOpts{Ref: "codex/12-thing", Issue: "12"})
	if err != nil || res.Branch != "codex/12-thing" {
		t.Fatalf("branch lost to the directory: %v %+v", err, res)
	}
	// A directory that is no branch is read as a path, and is not a worktree.
	if err := os.MkdirAll("plain-dir", 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Adopt(bg, root, be, AdoptOpts{Ref: "plain-dir", Issue: "13", AcceptOverlap: true}); err == nil || exitOf(err) != exitcode.ExitUsage ||
		!strings.Contains(err.Error(), "not a worktree") {
		t.Errorf("plain dir: %v", err)
	}
}
