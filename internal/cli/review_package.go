package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/git"
)

// reviewPackageDir is where review packages land, relative to the project root (gitignored).
const reviewPackageDir = ".rota/review"

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
		r := git.Repo{Dir: t.Dir}
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
		// base...branch is the merge-base diff, so a base that has moved on since
		// the branch forked is fine; only unrelated histories have no merge-base.
		// --since is a point on the branch, so it must be an ancestor of its tip.
		var from, logSpan, diffSpan string
		if *sinceFlag != "" {
			if ok, err := reviewIsAncestor(c, t.Dir, *sinceFlag, t.Branch); err != nil {
				return Result{}, err
			} else if !ok {
				return Result{}, Resolution("--since %s is not an ancestor of '%s'", *sinceFlag, t.Branch)
			}
			from = *sinceFlag
			logSpan = from + ".." + t.Branch
			diffSpan = logSpan
		} else {
			if _, err := reviewGit(ctx, t.Dir, "merge-base", t.Base, t.Branch); err != nil {
				return Result{}, Resolution("'%s' and base '%s' share no history", t.Branch, t.Base)
			}
			from = t.Base
			logSpan = from + ".." + t.Branch
			diffSpan = t.Base + "..." + t.Branch
		}
		count, err := reviewGit(ctx, t.Dir, "rev-list", "--count", logSpan)
		if err != nil {
			return Result{}, err
		}
		if strings.TrimSpace(count) == "0" {
			return Result{}, Resolution("no commits in %s", logSpan)
		}
		commits, err := reviewGit(ctx, t.Dir, "log", "--no-merges", "--format=%h %s", logSpan)
		if err != nil {
			return Result{}, err
		}
		stat, err := reviewGit(ctx, t.Dir, "diff", "--stat", diffSpan)
		if err != nil {
			return Result{}, err
		}
		diff, err := reviewGit(ctx, t.Dir, "diff", reviewPackageContext, diffSpan)
		if err != nil {
			return Result{}, err
		}
		names, err := reviewGit(ctx, t.Dir, "diff", "--name-only", diffSpan)
		if err != nil {
			return Result{}, err
		}
		files := len(nonBlankLines(names))

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
		data := gitObj("path", path, "files", files, "bytes", b.Len())
		return Result{Data: data, Text: path}, nil
	}
}

// reviewIsAncestor reports whether ancestor is reachable from ref.
func reviewIsAncestor(c *Ctx, dir, ancestor, ref string) (bool, error) {
	res, err := git.Repo{Dir: dir}.Run(c.Context(), "merge-base", "--is-ancestor", ancestor, ref)
	if err != nil {
		return false, gitErr(err)
	}
	switch res.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	}
	return false, Unavailable("git merge-base failed: exit %d", res.ExitCode)
}

// reviewPackageName turns a branch into a file stem: "nia/240-x" -> "nia-240-x".
func reviewPackageName(branch string) string {
	return strings.NewReplacer("/", "-", "\\", "-", " ", "-").Replace(branch)
}
