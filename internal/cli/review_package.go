package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/worker"
)

// reviewPackageDir is where review packages land, relative to the project root (gitignored).
const reviewPackageDir = rotatree.RelPrefix + rotatree.ReviewDir

// reviewPackageContext is the unified-diff context a package carries, so the
// reviewer sees each change in its surroundings.
const reviewPackageContext = "-U10"

// reviewPackage writes a branch's commits, --stat and full diff to one file
// under .rota/review/ and reports its path, so the reviewer reads the file
// instead of the orchestrator pasting per-file diffs into its context.
func reviewPackage(fs *flag.FlagSet) RunFunc {
	baseFlag := fs.String("base", "", "base ref to diff against (default: the resolved base)")
	sinceFlag := fs.String("since", "", "package only the commits after this sha (re-review after fixes)")
	return func(c *Ctx, args []string) (Result, error) {
		t, err := reviewTarget(c, args, "package")
		if err != nil {
			return Result{}, err
		}
		if *baseFlag != "" {
			t.Base = *baseFlag
		}
		ctx := c.Context()
		r := c.deps().Repo(t.Dir)
		for _, ref := range []string{t.Base, *sinceFlag} {
			if ref == "" {
				continue
			}
			found, err := r.Verify(ctx, ref+"^{commit}")
			if err != nil {
				return Result{}, gitErr(err)
			}
			if !found {
				return Result{}, Resolution("%s: no such commit or branch", ref)
			}
		}
		// head is the sha every read below uses and the one reported: the sha a
		// verdict on this review binds to (verdict add --sha), even if the branch
		// moves while the package is written.
		head, err := reviewGit(ctx, c.deps(), t.Dir, "rev-parse", "--verify", t.Branch+"^{commit}")
		if err != nil {
			return Result{}, err
		}
		head = strings.TrimSpace(head)
		if err := reviewRequirePushedHead(c, t.Dir, t.Branch, head); err != nil {
			return Result{}, err
		}
		// An integration branch is reviewed on its fix commits only: --since
		// defaults to just before the first of them (#3096).
		if *sinceFlag == "" {
			if since, ok, err := integrationSince(c, t.Dir, t.Branch, head); err != nil {
				return Result{}, err
			} else if ok {
				*sinceFlag = since
			}
		}
		// base...branch is the merge-base diff, so a base that has moved on since
		// the branch forked is fine; only unrelated histories have no merge-base.
		// --since is a point on the branch, so it must be an ancestor of its tip.
		var from, logSpan, diffSpan string
		ownOnly := false
		if *sinceFlag != "" {
			if ok, err := reviewIsAncestor(c, t.Dir, *sinceFlag, head); err != nil {
				return Result{}, err
			} else if !ok {
				return Result{}, Resolution("--since %s is not an ancestor of '%s'", *sinceFlag, t.Branch)
			}
			from = *sinceFlag
			logSpan = from + ".." + head
			diffSpan = logSpan
			// A base merge inside since..head carries the base's changes: diff
			// only the branch's own non-merge commits then.
			merges, err := reviewGit(ctx, c.deps(), t.Dir, "rev-list", "--count", "--merges", logSpan)
			if err != nil {
				return Result{}, err
			}
			ownOnly = strings.TrimSpace(merges) != "0"
		} else {
			if _, err := reviewGit(ctx, c.deps(), t.Dir, "merge-base", t.Base, head); err != nil {
				return Result{}, Resolution("'%s' and base '%s' share no history", t.Branch, t.Base)
			}
			from = t.Base
			logSpan = from + ".." + head
			diffSpan = t.Base + "..." + head
		}
		count, err := reviewGit(ctx, c.deps(), t.Dir, "rev-list", "--count", logSpan)
		if err != nil {
			return Result{}, err
		}
		if strings.TrimSpace(count) == "0" {
			return Result{}, Resolution("no commits in %s", logSpan)
		}
		commits, err := reviewGit(ctx, c.deps(), t.Dir, "log", "--no-merges", "--format=%h %s", logSpan)
		if err != nil {
			return Result{}, err
		}
		var stat, diff string
		var files int
		if ownOnly {
			if stat, diff, files, err = reviewOwnCommitsDiff(c, t.Dir, logSpan); err != nil {
				return Result{}, err
			}
		} else {
			if stat, err = reviewGit(ctx, c.deps(), t.Dir, "diff", "--stat", diffSpan); err != nil {
				return Result{}, err
			}
			if diff, err = reviewGit(ctx, c.deps(), t.Dir, "diff", reviewPackageContext, diffSpan); err != nil {
				return Result{}, err
			}
			paths, err := git.ChangedPaths(ctx, c.deps().Git, t.Dir, diffSpan)
			if err != nil {
				return Result{}, diffErr(err)
			}
			files = len(paths)
		}

		var b strings.Builder
		fmt.Fprintf(&b, "# Review package: %s vs %s\n\n", t.Branch, from)
		fmt.Fprintf(&b, "## Commits\n\n%s\n## Stat\n\n```\n%s```\n\n", commits, stat)
		fmt.Fprintf(&b, "## Diff (%s)\n\n```diff\n%s```\n", reviewPackageContext, diff)

		path := filepath.Join(t.CorpusRoot, filepath.FromSlash(reviewPackageDir), reviewPackageName(t.Branch)+".md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return Result{}, err
		}
		if err := fsio.WriteFileAtomic(path, []byte(b.String())); err != nil {
			return Result{}, err
		}
		data := jsonx.Obj("path", path, "files", files, "bytes", b.Len(), "head", head)
		return Result{Data: data, Text: path}, nil
	}
}

// reviewOwnCommitsDiff is the stat, the diff and the changed-file count of the
// branch's own commits in span: the first-parent, non-merge ones, so what a
// merge of the base brought in is left out. Each commit's patch follows its
// "commit <sha> <subject>" line.
func reviewOwnCommitsDiff(c *Ctx, dir, span string) (stat, diff string, files int, err error) {
	ctx := c.Context()
	own := []string{"log", "--first-parent", "--no-merges", "--reverse", "--format=commit %h %s", span}
	if stat, err = reviewGit(ctx, c.deps(), dir, append(own, "--stat")...); err != nil {
		return "", "", 0, err
	}
	if diff, err = reviewGit(ctx, c.deps(), dir, append(own, reviewPackageContext, "-p")...); err != nil {
		return "", "", 0, err
	}
	names, err := reviewGit(ctx, c.deps(), dir, "log", "--first-parent", "--no-merges", "--format=", "--name-only", span)
	if err != nil {
		return "", "", 0, err
	}
	seen := map[string]bool{}
	for _, n := range strings.Split(names, "\n") {
		if n = strings.TrimSpace(n); n != "" {
			seen[n] = true
		}
	}
	return stat, diff, len(seen), nil
}

// reviewRequirePushedHead refuses a branch whose local head differs from
// origin/<branch>: a reviewer briefed on it reviews commits the PR does not
// carry, and verdict add refuses that sha only after the review ran. A branch
// with no origin counterpart passes.
func reviewRequirePushedHead(c *Ctx, dir, branch, head string) error {
	ctx := c.Context()
	ok, err := c.deps().Repo(dir).Verify(ctx, "refs/remotes/origin/"+branch)
	if err != nil {
		return gitErr(err)
	}
	if !ok {
		return nil
	}
	out, err := reviewGit(ctx, c.deps(), dir, "rev-parse", "--verify", "refs/remotes/origin/"+branch+"^{commit}")
	if err != nil {
		return err
	}
	tip := strings.TrimSpace(out)
	if tip == head {
		return nil
	}
	return Resolution("local %s (%s) differs from origin/%s (%s): push the branch, or fetch if origin is ahead, so the review covers the PR head", branch, reviewShort(head), branch, reviewShort(tip))
}

func reviewShort(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// reviewIsAncestor reports whether ancestor is reachable from ref.
func reviewIsAncestor(c *Ctx, dir, ancestor, ref string) (bool, error) {
	ok, err := c.deps().Repo(dir).IsAncestor(c.Context(), ancestor, ref)
	var ae *git.AncestryError
	if errors.As(err, &ae) {
		return false, Unavailable("git merge-base failed: exit %d", ae.ExitCode)
	}
	if err != nil {
		return false, gitErr(err)
	}
	return ok, nil
}

// reviewPackageName turns a branch into a file stem: "nia/240-x" -> "nia-240-x".
func reviewPackageName(branch string) string {
	return strings.NewReplacer("/", "-", "\\", "-", " ", "-").Replace(branch)
}

// integrationSince is the --since default of an open integration branch, ok
// false for any other branch.
func integrationSince(c *Ctx, dir, branch, head string) (string, bool, error) {
	root, err := c.Root()
	if err != nil {
		return "", false, nil
	}
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return "", false, nil
	}
	rec, ok := reg.IntegrationByBranch(branch)
	if !ok || rec.Landed {
		return "", false, nil
	}
	since, err := worker.Env{}.WithDefaults().ReviewSince(dir, rec, head)
	if err != nil {
		return "", false, Resolution("%v", err)
	}
	return since, since != "", nil
}
