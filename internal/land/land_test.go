package land

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/tracker"
)

// repo is a real git repo on main with one commit.
type repo struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T) *repo {
	r := &repo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	r.git("config", "user.email", "t@example.com")
	r.git("config", "user.name", "t")
	r.commit("work.txt", "base\n")
	return r
}

func (r *repo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *repo) commit(file, body string) {
	if err := os.WriteFile(filepath.Join(r.dir, file), []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
	r.git("add", file)
	r.git("commit", "-qm", "edit "+file)
}

func (r *repo) run(args ...string) (git.Result, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var so, se strings.Builder
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code, err = ee.ExitCode(), nil
	}
	return git.Result{Stdout: so.String(), Stderr: se.String(), ExitCode: code}, err
}

// forge refuses a merge pinned to a commit that is no longer the PR head, as
// both forges do, and records what it was asked.
type forge struct {
	head string
	got  []tracker.MergeOpts
}

func (f *forge) PRRequestMerge(_ context.Context, _ int, o tracker.MergeOpts) error {
	f.got = append(f.got, o)
	if o.HeadSHA != f.head {
		return &tracker.Error{Kind: tracker.KindFailed, Code: 1, Message: "head branch was modified"}
	}
	return nil
}

func (f *forge) PRMerge(ctx context.Context, pr int, o tracker.MergeOpts) (string, error) {
	return "merged", f.PRRequestMerge(ctx, pr, o)
}

// TestLocalAndForgeAgree pins the behaviour both adapters share: an unpinned
// merge is refused, and a push after the verified commit never lands.
func TestLocalAndForgeAgree(t *testing.T) {
	t.Run("unpinned is refused", func(t *testing.T) {
		r := newRepo(t)
		if err := MergeLocal(r.run, "", "m"); !errors.Is(err, ErrUnpinned) {
			t.Errorf("local: %v", err)
		}
		f := &forge{head: "abc"}
		if err := RequestForge(context.Background(), f, 1, "", false); !errors.Is(err, ErrUnpinned) {
			t.Errorf("forge request: %v", err)
		}
		if _, err := MergeForge(context.Background(), f, 1, "", true); !errors.Is(err, ErrUnpinned) {
			t.Errorf("forge merge: %v", err)
		}
		if len(f.got) != 0 {
			t.Errorf("forge was called: %v", f.got)
		}
	})

	t.Run("a push after the pin does not land", func(t *testing.T) {
		r := newRepo(t)
		r.git("checkout", "-q", "-b", "feat")
		r.commit("feat.txt", "verified\n")
		pin := r.git("rev-parse", "HEAD")
		r.commit("late.txt", "pushed after the check\n") // the branch moves on
		r.git("checkout", "-q", "main")
		if err := MergeLocal(r.run, pin, "merge feat"); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(r.dir, "late.txt")); err == nil {
			t.Error("local landed a commit past the pin")
		}
		if _, err := os.Stat(filepath.Join(r.dir, "feat.txt")); err != nil {
			t.Error("local did not land the pinned commit")
		}

		f := &forge{head: "newhead"} // the PR head moved past the pinned sha
		if err := RequestForge(context.Background(), f, 1, "pinned", false); err == nil {
			t.Error("forge landed a stale pin")
		}
		if got := f.got[0]; got.HeadSHA != "pinned" {
			t.Errorf("forge was not pinned: %+v", got)
		}
	})

	t.Run("a conflict aborts and leaves the tree as it was", func(t *testing.T) {
		r := newRepo(t)
		r.git("checkout", "-q", "-b", "feat")
		r.commit("work.txt", "feat side\n")
		pin := r.git("rev-parse", "HEAD")
		r.git("checkout", "-q", "main")
		r.commit("work.txt", "main side\n")
		before := r.git("rev-parse", "HEAD")

		err := MergeLocal(r.run, pin, "merge feat")
		var ce *ConflictError
		if !errors.As(err, &ce) {
			t.Fatalf("err = %v, want *ConflictError", err)
		}
		if after := r.git("rev-parse", "HEAD"); after != before {
			t.Errorf("HEAD moved: %s -> %s", before, after)
		}
		if st := r.git("status", "--porcelain"); st != "" {
			t.Errorf("tree dirty after abort: %q", st)
		}
	})
}
