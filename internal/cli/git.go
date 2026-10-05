package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/repos"
)

// gitCommands is the `rota git` group (#52).
func gitCommands() *Command {
	return &Command{Name: "git", Summary: "base branch, guards, branches and worktree paths", Subs: []*Command{
		{Name: "base", Summary: "print the resolved base branch", Repo: true, Verb: noFlags(gitBase)},
		{Name: "guard", Summary: "refuse unsafe git states", Subs: []*Command{
			{Name: "clean", Summary: "fail when the working tree is dirty", Repo: true, Verb: gitGuardClean},
			{Name: "feature-branch", Summary: "fail on the base branch or a detached HEAD", Repo: true, Verb: noFlags(gitGuardFeature)},
		}},
		{Name: "branch", Summary: "create one branch in several sub-repos, or in none", Verb: gitBranch},
		{Name: "worktree-path", Summary: "print the umbrella worktree path of a sub-repo branch", Repo: true, Verb: noFlags(gitWorktreePath)},
	}}
}

func gitObj(kv ...any) *jsonx.Object {
	o := jsonx.NewObject()
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

// gitErr maps a failure to run git onto the exit table.
func gitErr(err error) error {
	if errors.Is(err, git.ErrNoGit) {
		return Unavailable("git is not installed")
	}
	return err
}

// gitDir is where a scoped git verb runs: the --repo sub-repo, else the
// working directory, which must not be an umbrella root (exit 2).
func gitDir(c *Ctx) (string, error) {
	dir, err := c.RepoPath()
	if err != nil || dir != "" {
		return dir, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if git.AtUmbrellaRoot(cwd, func() bool { return len(registeredRels(cwd)) > 0 }) {
		return "", Usage("%s from the umbrella root has no git context; pass --repo <name>", c.Path)
	}
	return cwd, nil
}

// registeredRels lists the sub-repo paths as written in root's repos.json.
func registeredRels(root string) []string {
	var out []string
	for _, r := range repos.Load(root) {
		out = append(out, r.Rel)
	}
	return out
}

// configuredBase is git.baseBranch of the project that holds dir, or ""
// when there is none or a stray sub-repo .rota/ masks it (hv-base-branch).
func configuredBase(dir string) string {
	root, err := git.FindRoot(dir, registeredRels)
	if err != nil {
		return ""
	}
	v, _ := config.Lookup(config.Load(filepath.Join(root, ".rota", "config.json")), "git.baseBranch")
	s, _ := v.(string)
	return s
}

func resolveBase(ctx context.Context, dir string) (string, bool, error) {
	base, ok, err := git.Repo{Dir: dir}.Base(ctx, configuredBase(dir))
	return base, ok, gitErr(err)
}

func gitBase(c *Ctx, args []string) (Result, error) {
	if len(args) > 0 {
		return Result{}, Usage("unexpected argument %q", args[0])
	}
	dir, err := gitDir(c)
	if err != nil {
		return Result{}, err
	}
	base, ok, err := resolveBase(c.Context(), dir)
	if err != nil {
		return Result{}, err
	}
	if !ok {
		return Result{}, Resolution("could not determine base branch (tried git.baseBranch, main, master, trunk, origin/HEAD)")
	}
	return Result{Data: gitObj("base", base), Text: base}, nil
}

func gitGuardClean(fs *flag.FlagSet) RunFunc {
	context_ := fs.String("context", "this command", "what the check guards, for the message")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) > 0 {
			return Result{}, Usage("unexpected argument %q; the context goes in --context", args[0])
		}
		ctx := c.Context()
		what := *context_
		dir, err := c.RepoPath()
		if err != nil {
			return Result{}, err
		}
		if dir == "" {
			if dir, err = os.Getwd(); err != nil {
				return Result{}, err
			}
		}
		r := git.Repo{Dir: dir}
		isRepo, err := r.IsRepo(ctx)
		if err != nil {
			return Result{}, gitErr(err)
		}
		if isRepo {
			dirty, _, err := r.Dirty(ctx)
			if err != nil {
				return Result{}, gitErr(err)
			}
			if !dirty {
				return Result{Data: gitObj("clean", true, "greenfield", false, "dirtyRepos", []string{}), Text: "clean"}, nil
			}
			head, err := r.HasHead(ctx)
			if err != nil {
				return Result{}, gitErr(err)
			}
			res := Result{Data: gitObj("clean", false, "greenfield", !head, "dirtyRepos", []string{})}
			if !head {
				return res, Failed("uncommitted changes on a fresh repo (no commits yet); make a baseline commit before running %s", what).
					WithHint(`git add -A && git commit -m "chore: import initial files"`)
			}
			return res, Failed("uncommitted changes; stash (`git stash`) or commit before running %s", what)
		}
		// Not a repo: an umbrella root walks its sub-repos.
		if !git.AtUmbrellaRoot(dir, func() bool { return len(registeredRels(dir)) > 0 }) {
			return Result{}, Resolution("not a git repository; %s requires git", what)
		}
		var dirty []string
		_, list, err := c.RepoList()
		if err != nil {
			return Result{}, err
		}
		for _, repo := range list {
			d, ok, err := git.Repo{Dir: repo.Path}.Dirty(ctx)
			switch {
			case err != nil && errors.Is(err, git.ErrNoGit):
				return Result{}, gitErr(err)
			case err != nil || !ok:
				dirty = append(dirty, repo.Name+" (not a git repo at "+repo.Rel+")")
			case d:
				dirty = append(dirty, repo.Name)
			}
		}
		if len(dirty) == 0 {
			return Result{Data: gitObj("clean", true, "greenfield", false, "dirtyRepos", []string{}), Text: "clean"}, nil
		}
		return Result{Data: gitObj("clean", false, "greenfield", false, "dirtyRepos", dirty)},
			Failed("uncommitted changes in sub-repo(s) %s; stash or commit before running %s", strings.Join(dirty, ", "), what)
	}
}

func gitGuardFeature(c *Ctx, args []string) (Result, error) {
	if len(args) > 1 {
		return Result{}, Usage("unexpected argument %q", args[1])
	}
	dir, err := gitDir(c)
	if err != nil {
		return Result{}, err
	}
	ctx := c.Context()
	branch := ""
	if len(args) == 1 {
		branch = args[0]
	}
	if branch == "" {
		if branch, err = (git.Repo{Dir: dir}).CurrentBranch(ctx); err != nil {
			return Result{}, gitErr(err)
		}
	}
	if branch == "" || branch == "HEAD" {
		return Result{Data: gitObj("feature", false, "reason", "detached")},
			Failed("could not determine the current branch (detached HEAD?)")
	}
	base, ok, err := resolveBase(ctx, dir)
	if err != nil {
		return Result{}, err
	}
	if !ok {
		// No base resolves: the conventional names still count as one.
		switch branch {
		case "main", "master", "trunk":
			return Result{Data: gitObj("feature", false, "branch", branch, "reason", "base")},
				Failed("refusing to operate on '%s' (no base configured but the branch name matches a conventional base); check out a feature branch first", branch)
		}
		return Result{Data: gitObj("feature", true, "branch", branch), Text: branch}, nil
	}
	if branch == base {
		return Result{Data: gitObj("feature", false, "branch", branch, "base", base, "reason", "base")},
			Failed("refusing to operate on '%s' (project base branch); check out a feature branch first", branch)
	}
	return Result{Data: gitObj("feature", true, "branch", branch, "base", base), Text: branch}, nil
}

func gitBranch(fs *flag.FlagSet) RunFunc {
	reposFlag := fs.String("repos", "", "comma-separated sub-repo names (no spaces)")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) != 1 || args[0] == "" {
			return Result{}, Usage("usage: rota git branch <name> --repos <a,b,...>")
		}
		branch := args[0]
		if *reposFlag == "" {
			return Result{}, Usage("--repos is required")
		}
		names := strings.Split(*reposFlag, ",")
		for _, n := range names {
			if n == "" || strings.TrimSpace(n) != n {
				return Result{}, Usage("--repos %q: names are comma-separated with no spaces or empty entries", *reposFlag)
			}
		}
		_, repos, err := c.Repos()
		if err != nil {
			return Result{}, err
		}
		var missing []string
		for _, n := range names {
			if _, ok := repos[n]; !ok {
				missing = append(missing, n)
			}
		}
		if len(missing) > 0 {
			return Result{}, Resolution("unregistered sub-repo(s): %s", strings.Join(missing, ", "))
		}
		ctx := c.Context()
		var taken []string
		for _, n := range names {
			exists, err := git.Repo{Dir: repos[n]}.BranchExists(ctx, branch)
			if err != nil {
				return Result{}, gitErr(err)
			}
			if exists {
				taken = append(taken, n)
			}
		}
		if len(taken) > 0 {
			return Result{Data: gitObj("branch", branch, "repos", names, "changed", false)},
				Refused("branch '%s' already exists in: %s", branch, strings.Join(taken, ", "))
		}
		var created []string
		// partial names the repos that already got the branch: exit 5
		// carries no data, so a part-done run says so in its message.
		partial := func() string {
			if len(created) == 0 {
				return "; created nowhere"
			}
			return "; already created in: " + strings.Join(created, ", ")
		}
		for _, n := range names {
			msg, ok, err := git.Repo{Dir: repos[n]}.CreateBranch(ctx, branch)
			if err != nil {
				if errors.Is(err, git.ErrNoGit) {
					return Result{}, Unavailable("git is not installed%s", partial())
				}
				return Result{}, fmt.Errorf("creating '%s' in %s: %w%s", branch, n, err, partial())
			}
			if !ok {
				return Result{}, Unavailable("failed to create '%s' in %s: %s%s", branch, n, msg, partial())
			}
			created = append(created, n)
		}
		return Result{Data: gitObj("branch", branch, "repos", names, "changed", true),
			Text: "created " + branch + " in " + strings.Join(names, ", ")}, nil
	}
}

func gitWorktreePath(c *Ctx, args []string) (Result, error) {
	if len(args) != 1 || args[0] == "" {
		return Result{}, Usage("usage: rota git worktree-path --repo <name> <branch>")
	}
	if c.Repo == "" {
		return Result{}, Usage("--repo is required")
	}
	if _, err := c.RepoPath(); err != nil {
		return Result{}, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return Result{}, err
	}
	root, err := git.FindRoot(cwd, registeredRels)
	if err != nil {
		return Result{}, Resolution("cannot resolve the umbrella: %v", err)
	}
	p := git.WorktreePath(root, c.Repo, args[0])
	return Result{Data: gitObj("path", p), Text: p}, nil
}
