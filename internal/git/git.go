// Package git is the git layer of the A8 verbs (#52): base-branch
// resolution, clean-tree and feature-branch checks, branch creation and the
// umbrella worktree layout, ported from bin/hv-base-branch,
// hv-guard-clean, hv-guard-feature-branch, hv-multi-branch-create,
// hv-worktree-path and hv-require-git-context.
package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/proc"
	"github.com/l4ci/rota/internal/rotatree"
)

// Timeout bounds one git call.
const Timeout = 2 * time.Minute

// ErrNoGit is returned when the git binary cannot be run.
var ErrNoGit = errors.New("git is not installed")

// IsMergeConflict reports whether the output of a failed `git merge` says the
// merge conflicted, as opposed to failing for another reason (no committer
// identity, a hook, a locked index). It matches git's C-locale words, which
// Run asks for.
func IsMergeConflict(out string) bool {
	return strings.Contains(out, "CONFLICT") || strings.Contains(out, "Automatic merge failed")
}

// Repo runs git in Dir ("" is the process cwd).
type Repo struct{ Dir string }

// Result is one git call: what proc reports for any command.
type Result = proc.Result

// Runner runs git with args in dir. A non-zero exit is a Result, not an error;
// err is ErrNoGit when git is missing, or the context's error. It is the
// injection point for every layer that runs git: tests fake it, production
// passes Exec.
type Runner func(ctx context.Context, dir string, args ...string) (Result, error)

// Exec is the production Runner.
func Exec(ctx context.Context, dir string, args ...string) (Result, error) {
	return Via(proc.Run, ctx, dir, args...)
}

// Via runs git through a process runner with the policy every git call shares:
// the git timeout and the C locale. git translates its messages ("CONFLICT
// (content)" among them) in some locales; callers match on them, so ask for
// the C locale.
func Via(run proc.Runner, ctx context.Context, dir string, args ...string) (Result, error) {
	res, err := run(ctx, proc.Cmd{
		Name: "git", Args: args, Dir: dir, Timeout: Timeout,
		Env: []string{"LC_ALL=C", "LANGUAGE=C"},
	})
	if errors.Is(err, exec.ErrNotFound) {
		return Result{}, ErrNoGit
	}
	return res, err
}

// Run runs git with args. A non-zero exit is a Result, not an error; err is
// ErrNoGit when git is missing, or the context's error.
func (r Repo) Run(ctx context.Context, args ...string) (Result, error) {
	return Exec(ctx, r.Dir, args...)
}

// ok reports whether a call exited 0; a failure to run is an error.
func (r Repo) ok(ctx context.Context, args ...string) (bool, error) {
	res, err := r.Run(ctx, args...)
	if err != nil {
		return false, err
	}
	return res.ExitCode == 0, nil
}

// Verify reports whether ref resolves (`git rev-parse --verify`).
func (r Repo) Verify(ctx context.Context, ref string) (bool, error) {
	return r.ok(ctx, "rev-parse", "--verify", "-q", ref)
}

// Base resolves the base branch: configured when it exists, then main,
// master, trunk, then origin/HEAD. ok is false when none resolves.
func (r Repo) Base(ctx context.Context, configured string) (base string, ok bool, err error) {
	cands := []string{"main", "master", "trunk"}
	if configured != "" {
		cands = append([]string{configured}, cands...)
	}
	for _, b := range cands {
		if found, err := r.Verify(ctx, b); err != nil || found {
			return b, found, err
		}
	}
	res, err := r.Run(ctx, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err != nil || res.ExitCode != 0 {
		return "", false, err
	}
	head := strings.TrimRight(res.Stdout, "\n")
	if head == "" {
		return "", false, nil
	}
	return strings.TrimPrefix(head, "refs/remotes/origin/"), true, nil
}

// CurrentBranch is the checked-out branch, "" when HEAD is detached or
// unreadable.
func (r Repo) CurrentBranch(ctx context.Context) (string, error) {
	res, err := r.Run(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	b := strings.TrimRight(res.Stdout, "\n")
	if res.ExitCode != 0 || b == "HEAD" {
		return "", nil
	}
	return b, nil
}

// IsRepo reports whether Dir is inside a git work tree or git dir.
func (r Repo) IsRepo(ctx context.Context) (bool, error) {
	return r.ok(ctx, "rev-parse", "--git-dir")
}

// Dirty reports uncommitted changes (`git status --porcelain` not empty).
// ok is false when status itself fails (not a repo).
func (r Repo) Dirty(ctx context.Context) (dirty, ok bool, err error) {
	res, err := r.Run(ctx, "status", "--porcelain")
	if err != nil || res.ExitCode != 0 {
		return false, false, err
	}
	return strings.TrimSpace(res.Stdout) != "", true, nil
}

// HasHead reports whether the repo has a first commit.
func (r Repo) HasHead(ctx context.Context) (bool, error) {
	return r.Verify(ctx, "HEAD")
}

// BranchExists reports whether refs/heads/<name> exists.
func (r Repo) BranchExists(ctx context.Context, name string) (bool, error) {
	return r.ok(ctx, "show-ref", "--verify", "--quiet", "refs/heads/"+name)
}

// CreateBranch creates name at HEAD without switching to it. msg is git's
// stderr when it refuses.
func (r Repo) CreateBranch(ctx context.Context, name string) (msg string, ok bool, err error) {
	res, err := r.Run(ctx, "branch", name)
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(res.Stderr), res.ExitCode == 0, nil
}

// out runs git and returns stdout with trailing newlines trimmed. ok is
// false when git exited non-zero; a failure to run is an error.
func (r Repo) out(ctx context.Context, args ...string) (s string, ok bool, err error) {
	res, err := r.Run(ctx, args...)
	if err != nil || res.ExitCode != 0 {
		return "", false, err
	}
	return strings.TrimRight(res.Stdout, "\n"), true, nil
}

// CommonDir is the absolute, symlink-resolved git common dir of Dir, so a
// linked worktree resolves to its main repository's .git and every spelling of
// the path agrees. ok is false when Dir is not in a repository.
func (r Repo) CommonDir(ctx context.Context) (dir string, ok bool, err error) {
	return CommonDirVia(ctx, Exec, r.Dir)
}

// CommonDirVia is Repo.CommonDir through a Runner, for layers that run git
// through their own seam. dir "" is the process cwd.
func CommonDirVia(ctx context.Context, run Runner, dir string) (string, bool, error) {
	res, err := run(ctx, dir, "rev-parse", "--git-common-dir")
	if err != nil || res.ExitCode != 0 {
		return "", false, err
	}
	p := strings.TrimSpace(res.Stdout)
	if !filepath.IsAbs(p) {
		base := dir
		if base == "" {
			if base, err = os.Getwd(); err != nil {
				return "", false, err
			}
		}
		p = filepath.Join(base, p)
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	return filepath.Clean(p), true, nil
}

// Toplevel is the root of Dir's work tree; ok is false outside one.
func (r Repo) Toplevel(ctx context.Context) (string, bool, error) {
	return r.out(ctx, "rev-parse", "--show-toplevel")
}

// ShortHead is HEAD's abbreviated hash; ok is false without a first commit.
func (r Repo) ShortHead(ctx context.Context) (string, bool, error) {
	return r.out(ctx, "rev-parse", "--short", "HEAD")
}

// LastCommitDate is the committer date (YYYY-MM-DD) of the newest commit that
// touched path; ok is false when git fails or the path has no history.
func (r Repo) LastCommitDate(ctx context.Context, path string) (string, bool, error) {
	d, ok, err := r.out(ctx, "log", "-1", "--format=%cs", "--", path)
	return d, ok && d != "", err
}

// ForEachRef lists the short names of the refs under prefix
// ("refs/heads/spike/"); ok is false when git fails.
func (r Repo) ForEachRef(ctx context.Context, prefix string) ([]string, bool, error) {
	o, ok, err := r.out(ctx, "for-each-ref", "--format=%(refname:short)", prefix)
	if err != nil || !ok {
		return nil, false, err
	}
	var refs []string
	for _, l := range strings.Split(o, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			refs = append(refs, l)
		}
	}
	return refs, true, nil
}

// WorktreePath is the umbrella's Layout B worktree for a sub-repo branch.
func WorktreePath(umbrella, repo, branch string) string {
	return umbrella + "/.claude/worktrees/" + repo + "/" + branch
}

// AtUmbrellaRoot reports whether dir is an umbrella root with no git of its
// own: no .git directory, a .rota directory, and at least one registered
// sub-repo (hv-require-git-context). registered reports the last part.
func AtUmbrellaRoot(dir string, registered func() bool) bool {
	if fi, err := os.Stat(filepath.Join(dir, ".git")); err == nil && fi.IsDir() {
		return false
	}
	if !rotatree.Exists(dir) {
		return false
	}
	return registered()
}

// ErrNoRoot and ErrMasked are FindRoot's failures.
var (
	ErrNoRoot = errors.New("no .rota/ directory here or in any parent")
	ErrMasked = errors.New("stray .rota/ inside a registered sub-repo masks the umbrella")
)

// FindRoot walks up from start (physical paths) to the nearest directory
// holding .rota/, as hv-walk-up --detect-masking does: when a higher .rota/
// registers that directory's tree as a sub-repo in its repos.json, the
// nearer .rota/ is a stray one and FindRoot returns ErrMasked. registered
// lists a candidate root's sub-repo paths as written in its repos.json.
func FindRoot(start string, registered func(root string) []string) (string, error) {
	dir, err := filepath.EvalSymlinks(start)
	if err != nil {
		dir = start
	}
	var cands []string
	for {
		if rotatree.Exists(dir) {
			cands = append(cands, dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if len(cands) == 0 {
		return "", ErrNoRoot
	}
	first := cands[0]
	for _, parent := range cands[1:] {
		for _, rel := range registered(parent) {
			sub := filepath.Join(parent, rel)
			if real, err := filepath.EvalSymlinks(sub); err == nil {
				sub = real
			}
			if first == sub || strings.HasPrefix(first, sub+string(filepath.Separator)) {
				return "", ErrMasked
			}
		}
	}
	return first, nil
}
