// Package git is the git layer of the A8 verbs (#52): base-branch
// resolution, clean-tree and feature-branch checks, branch creation and the
// umbrella worktree layout, ported from bin/hv-base-branch,
// hv-guard-clean, hv-guard-feature-branch, hv-multi-branch-create,
// hv-worktree-path and hv-require-git-context.
package git

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Timeout bounds one git call.
const Timeout = 2 * time.Minute

// ErrNoGit is returned when the git binary cannot be run.
var ErrNoGit = errors.New("git is not installed")

// Repo runs git in Dir ("" is the process cwd).
type Repo struct{ Dir string }

// Result is one git call.
type Result struct {
	Stdout, Stderr string
	Code           int
}

// Run runs git with args. A non-zero exit is a Result, not an error; err is
// ErrNoGit when git is missing, or the context's error.
func (r Repo) Run(ctx context.Context, args ...string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Dir
	// git translates its messages ("CONFLICT (content)" among them) in some
	// locales; callers match on them, so ask for the C locale.
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANGUAGE=C")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		return Result{out.String(), errb.String(), ee.ExitCode()}, nil
	case errors.Is(err, exec.ErrNotFound):
		return Result{}, ErrNoGit
	case err != nil:
		return Result{}, err
	}
	return Result{out.String(), errb.String(), 0}, nil
}

// ok reports whether a call exited 0; a failure to run is an error.
func (r Repo) ok(ctx context.Context, args ...string) (bool, error) {
	res, err := r.Run(ctx, args...)
	if err != nil {
		return false, err
	}
	return res.Code == 0, nil
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
	if err != nil || res.Code != 0 {
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
	if res.Code != 0 || b == "HEAD" {
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
	if err != nil || res.Code != 0 {
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
	return strings.TrimSpace(res.Stderr), res.Code == 0, nil
}

// out runs git and returns stdout with trailing newlines trimmed. ok is
// false when git exited non-zero; a failure to run is an error.
func (r Repo) out(ctx context.Context, args ...string) (s string, ok bool, err error) {
	res, err := r.Run(ctx, args...)
	if err != nil || res.Code != 0 {
		return "", false, err
	}
	return strings.TrimRight(res.Stdout, "\n"), true, nil
}

// CommonDir is the absolute git common dir of Dir (symlinks unresolved), so a
// linked worktree resolves to its main repository's .git. ok is false when
// Dir is not in a repository.
func (r Repo) CommonDir(ctx context.Context) (dir string, ok bool, err error) {
	p, ok, err := r.out(ctx, "rev-parse", "--git-common-dir")
	if err != nil || !ok {
		return "", false, err
	}
	base := r.Dir
	if base == "" {
		if base, err = os.Getwd(); err != nil {
			return "", false, err
		}
	}
	return AbsCommonDir(base, p), true, nil
}

// AbsCommonDir makes the output of `git rev-parse --git-common-dir` run in
// dir absolute (git prints it relative to dir) and clean. For callers that
// run git through their own seam; symlinks stay unresolved.
func AbsCommonDir(dir, out string) string {
	p := strings.TrimSpace(out)
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	return filepath.Clean(p)
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
	if fi, err := os.Stat(filepath.Join(dir, ".rota")); err != nil || !fi.IsDir() {
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
		if fi, err := os.Stat(filepath.Join(dir, ".rota")); err == nil && fi.IsDir() {
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
