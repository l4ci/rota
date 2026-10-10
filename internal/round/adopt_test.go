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
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

const filesBody = "## Acceptance\n- [ ] ok\n\n## Files\n- internal/cli/round.go\n"

func adoptFixture(t *testing.T) (string, Env, *fakeRemote) {
	t.Helper()
	root := newRepo(t, map[string]string{"codex-a": "codex/12-thing"})
	fb := &fakeRemote{}
	fb.add("12", "Thing", "M01", false, filesBody)
	fb.add("13", "Other", "M01", false, filesBody)
	return root, Env{Git: git.Exec, Base: "main", Worker: worker.Env{Getenv: noEnv}}, fb
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
		s := worker.LoadRegistryTolerant(root).Slot("ext-1")
		if s == nil || !s.IsExternal() || s.Handle() != "" || s.Task() != "12" || s.PR() != "https://x/pull/7" ||
			s.Branch() != "codex/12-thing" || s.Base() != "main" {
			t.Fatalf("%s: slot %v", ref, s)
		}
		if got, _ := filepath.EvalSymlinks(s.Worktree()); got == "" || got != res.Worktree {
			t.Errorf("%s: worktree %q vs %q", ref, got, res.Worktree)
		}
	}
	if adopted, err := worker.AdoptedBranches(root); err != nil || !adopted["codex/12-thing"] {
		t.Error("adopt left no ledger record, so reap cannot tell the branch was adopted")
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
	if worker.LoadRegistryTolerant(root).Slot("ext-1") != nil {
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
	if worker.LoadRegistryTolerant(root).Slot("ext-1") != nil {
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
	e, be := Env{Git: git.Exec, Base: "main", Worker: worker.Env{Getenv: noEnv}}, &fakeRemote{}
	be.add("12", "Thing", "M01", false, filesBody)
	for _, ref := range []string{"main", "park/ben", filepath.Join(root, ".worktrees", "ben")} {
		_, err := e.Adopt(bg, root, be, AdoptOpts{Ref: ref, Issue: "12"})
		if exitOf(err) != exitcode.ExitUsage {
			t.Errorf("%s: want exit 2, got %v", ref, err)
		}
	}
	if len(worker.LoadRegistryTolerant(root).Slots()) != 0 {
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

// --pr is checked against the forge: a PR that does not exist, or one headed by
// another branch, would leave the slot waiting on a PR that is not its own.
func TestAdoptValidatesThePRAgainstTheForge(t *testing.T) {
	root, e, be := adoptFixture(t)
	fr := &fakeRemote{prViews: map[int]tracker.PRInfo{
		7: {Head: "codex/12-thing", State: "OPEN"},
		8: {Head: "someone/else", State: "OPEN"},
	}}
	e.Forge = fr.asForge()
	for _, c := range []struct{ pr, want string }{
		{"not-a-pr", "no PR number"},
		{"https://x/pull/9", "no PR 9"},
		{"https://x/pull/8", "headed by someone/else"},
	} {
		_, err := e.Adopt(bg, root, be, AdoptOpts{Ref: "codex/12-thing", Issue: "12", PR: c.pr})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("--pr %s: want an error naming %q, got %v", c.pr, c.want, err)
		}
		if len(worker.LoadRegistryTolerant(root).Slots()) != 0 {
			t.Fatalf("--pr %s: a refused adoption registered a slot", c.pr)
		}
	}
	if _, err := e.Adopt(bg, root, be, AdoptOpts{Ref: "codex/12-thing", Issue: "12", PR: "https://x/pull/7"}); err != nil {
		t.Fatalf("a PR headed by the branch should adopt: %v", err)
	}
}

// A forge that fails refuses the adoption (guessing "fine" is what the check is
// for); a missing forge CLI or no forge at all skips the read and adopts.
func TestAdoptPRForgeFailurePaths(t *testing.T) {
	down := &tracker.Error{Kind: tracker.KindUnavailable, Message: "gh: connection refused"}
	noCLI := &tracker.Error{Kind: tracker.KindUnavailable, Message: "gh is not installed"}
	for _, c := range []struct {
		name    string
		forge   func(*fakeRemote) Forge
		viewErr error
		wantErr string
	}{
		{"forge down refuses", func(f *fakeRemote) Forge { return f.asForge() }, down, "could not read PR 7 from the forge"},
		{"missing CLI skips", func(f *fakeRemote) Forge { return f.asForge() }, noCLI, ""},
		{"no forge skips", func(*fakeRemote) Forge { return nil }, nil, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			root, e, be := adoptFixture(t)
			fr := &fakeRemote{prViewErr: c.viewErr}
			e.Forge = c.forge(fr)
			_, err := e.Adopt(bg, root, be, AdoptOpts{Ref: "codex/12-thing", Issue: "12", PR: "https://x/pull/7"})
			if c.wantErr == "" {
				if err != nil || len(worker.LoadRegistryTolerant(root).Slots()) != 1 {
					t.Fatalf("want an adoption, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) || len(worker.LoadRegistryTolerant(root).Slots()) != 0 {
				t.Fatalf("want a refusal naming %q, got %v", c.wantErr, err)
			}
		})
	}
}
