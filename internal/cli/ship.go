package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/section"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/verdict"
)

// shipCommands is the `rota ship` group (A8, #52).
func shipCommands() *Command {
	return &Command{Name: "ship", Summary: "PR bodies, pull requests, merges and undo", Subs: []*Command{
		{Name: "body", Summary: "build a PR body from a branch's commits", Repo: true, Verb: noFlags(shipBody)},
		{Name: "pr", Summary: "push a branch and open a PR or MR", Repo: true, Verb: shipPR},
		{Name: "merge", Summary: "merge a branch into the base branch with --no-ff", Repo: true, Verb: shipMerge},
		{Name: "pr-merge", Summary: "merge a PR in issue mode", Repo: true, Verb: shipPRMerge},
		{Name: "undo", Summary: "roll back the last cycle merge on the base branch", Repo: true, Verb: shipUndo},
	}}
}

// shipGit runs git in dir; a failure to run it is mapped onto the exit table.
func shipGit(c *Ctx, dir string, args ...string) (git.Result, error) {
	res, err := git.Repo{Dir: dir}.Run(c.Context(), args...)
	return res, gitErr(err)
}

// shipLine is s without its trailing newlines, as bash `$(...)` returns it.
func shipLine(s string) string { return strings.TrimRight(s, "\n") }

// shipFirstLine is the first non-blank line of git's or a CLI's stderr.
func shipFirstLine(s string) string {
	for _, l := range pystr.Splitlines(s) {
		if l = pystr.Strip(l); l != "" {
			return l
		}
	}
	return ""
}

// shipBlocked is a refusal carrying {blockedBy, changed: false}.
func shipBlocked(by, format string, a ...any) (Result, error) {
	return Result{Data: gitObj("blockedBy", by, "changed", false)}, Refused(format, a...)
}

// shipVerdictBlock refuses (exit 4) when a recorded FAIL blocks branch from
// shipping (B3): the data carries the blocking record, so a caller can add to
// it. It returns nil, nil when nothing blocks. dir is the checkout the branch
// lives in and root the project holding the verdict store.
func shipVerdictBlock(c *Ctx, dir, root, branch string) (*jsonx.Object, error) {
	repo := c.Repo
	if repo == "" && repos.Umbrella(root) {
		if r, err := repos.Which(dir); err == nil {
			repo = r.Name
		}
	}
	cfg := config.Load(filepath.Join(root, ".rota", "config.json"))
	s := verdict.Settings{Runner: configString(cfg, "ship.secondOpinionRunner")}
	r, ok := verdict.Blocking(verdict.Load(root).Branches[verdict.BranchKey(repo, branch)], s)
	if !ok {
		return nil, nil
	}
	// A branch whose tip cannot be read has moved on as far as we can tell.
	stale := true
	if out, err := reviewGit(c.Context(), dir, "rev-parse", "--short", branch); err == nil {
		stale = r.Sha != strings.TrimSpace(out)
	}
	d := gitObj("blockedBy", "verdict", "kind", r.Kind, "verdict", r.Verdict, "sha", r.Sha, "stale", stale, "changed", false)
	return d, Refused("%s %s recorded for %s; not shipped", r.Kind, r.Verdict, branch).
		WithHint("see: rota verdict show " + branch)
}

// shipPRVerdict is shipVerdictBlock for the head branch of PR pr, with the
// PR number added to the refusal data.
func shipPRVerdict(c *Ctx, pr int, branch string) (Result, error) {
	dir, err := gitDir(c)
	if err != nil {
		return Result{}, err
	}
	root, err := c.Root()
	if err != nil {
		return Result{}, err
	}
	d, err := shipVerdictBlock(c, dir, root, branch)
	if err != nil {
		d.Set("pr", pr)
		return Result{Data: d}, err
	}
	return Result{}, nil
}

// shipBodyArg reads --body-file (- is stdin); a missing, unreadable or blank
// body is exit 2. Trailing newlines go, as `$(cat)` dropped them.
func shipBodyArg(c *Ctx, path, what string) (string, error) {
	if path == "" {
		return "", Usage("--body-file is required")
	}
	var raw []byte
	var err error
	if path == "-" {
		raw, err = io.ReadAll(c.Stdin)
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return "", Usage("cannot read --body-file %s: %v", path, unwrapPathErr(err))
	}
	if pystr.Strip(string(raw)) == "" {
		return "", Usage("%s is empty", what)
	}
	return shipLine(string(raw)), nil
}

// shipRoot is the project whose .rota/ holds the config and backlog of the
// checkout in dir.
func shipRoot(dir string) string {
	if root, err := git.FindRoot(dir, registeredRels); err == nil {
		return root
	}
	return dir
}

// shipExistingBranch resolves the checkout a branch-writing verb runs in and
// checks the branch exists: exit 2 at an umbrella root without --repo, 3 for
// a missing branch.
func shipExistingBranch(c *Ctx, branch string) (string, error) {
	dir, err := gitDir(c)
	if err != nil {
		return "", err
	}
	ok, err := git.Repo{Dir: dir}.Verify(c.Context(), branch+"^{commit}")
	if err != nil {
		return "", gitErr(err)
	}
	if !ok {
		return "", Resolution("branch '%s' not found", branch)
	}
	return dir, nil
}

// shipClearWorktree removes the linked worktree that has branch checked out
// (hv-worktree-clear), so the branch can be pushed or merged and deleted.
func shipClearWorktree(c *Ctx, dir, branch string) error {
	res, err := shipGit(c, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return err
	}
	wt := shipLinkedWorktree(res.Stdout, branch)
	if wt == "" && c.Repo != "" {
		// Layout B: the worktree is on disk under the umbrella.
		p := git.WorktreePath(shipRoot(dir), c.Repo, branch)
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			wt = p
		}
	}
	if wt == "" {
		return nil
	}
	rm, err := shipGit(c, dir, "worktree", "remove", wt)
	if err != nil {
		return err
	}
	if rm.Code != 0 {
		return Unavailable("git worktree remove %s: %s", wt, shipFirstLine(rm.Stderr))
	}
	return nil
}

// shipLinkedWorktree is the path of the linked (not the first) worktree in a
// porcelain listing that has branch checked out, "" if none.
func shipLinkedWorktree(porcelain, branch string) string {
	wt, count := "", 0
	for _, l := range strings.Split(porcelain, "\n") {
		if p, ok := strings.CutPrefix(l, "worktree "); ok {
			wt = p
			count++
		} else if l == "branch refs/heads/"+branch && count > 1 {
			return wt
		}
	}
	return ""
}

// ---- ship body -------------------------------------------------------------

var shipIssueRe = regexp.MustCompile(`(?:GH|GL):[` + pystr.SpaceClass + `]*#(\p{Nd}+)`)

func shipBody(c *Ctx, args []string) (Result, error) {
	t, err := resolveBranch(c, args)
	if err != nil {
		return Result{}, err
	}
	rng := t.Base + ".." + t.Branch
	subj, err := shipGit(c, t.Dir, "log", "--no-merges", "--format=%s", rng)
	if err != nil {
		return Result{}, err
	}
	full, err := shipGit(c, t.Dir, "log", "--no-merges", "--format=%B", rng)
	if err != nil {
		return Result{}, err
	}
	if subj.Code != 0 || full.Code != 0 {
		return Result{}, Unavailable("git log %s: %s", rng, shipFirstLine(subj.Stderr+full.Stderr))
	}
	var subjects []string
	for _, l := range pystr.Splitlines(subj.Stdout) {
		if pystr.Strip(l) != "" {
			subjects = append(subjects, pystr.Strip(l))
		}
	}
	if len(subjects) == 0 {
		return Result{}, Failed("no commits between base and %s", t.Branch)
	}
	var b strings.Builder
	b.WriteString("## Summary\n\n")
	for _, s := range subjects {
		b.WriteString("- " + s + "\n")
	}
	b.WriteString("\n")
	if ids := backlog.FindItemIDs(full.Stdout, ""); len(ids) > 0 {
		corpus := (&backlog.File{Root: t.CorpusRoot}).Corpus()
		origin := map[string]string{}
		b.WriteString("## Items resolved\n\n")
		for _, id := range ids {
			line, title, ok := backlog.FindOrigin(corpus, id)
			if ok {
				origin[id] = line
			}
			if ok && title != "" {
				b.WriteString("- [" + id + "] " + title + "\n")
			} else {
				b.WriteString("- [" + id + "]\n")
			}
		}
		b.WriteString("\n")
		seen := map[string]bool{}
		var closes []string
		for _, id := range ids {
			for _, m := range shipIssueRe.FindAllStringSubmatch(origin[id], -1) {
				if !seen[m[1]] {
					seen[m[1]] = true
					closes = append(closes, m[1])
				}
			}
		}
		if len(closes) > 0 {
			for _, n := range closes {
				b.WriteString("Closes #" + n + "\n")
			}
			b.WriteString("\n")
		}
	}
	return Result{Data: gitObj("branch", t.Branch, "body", b.String()), Text: b.String()}, nil
}

// ---- ship pr ---------------------------------------------------------------

var shipNumberRe = regexp.MustCompile(`(\p{Nd}+)/?$`)

func shipPR(fs *flag.FlagSet) RunFunc {
	title := fs.String("title", "", "PR title")
	bodyFile := fs.String("body-file", "", "PR body: a path, or - for stdin")
	items := fs.String("items", "", "item IDs the PR closes, comma separated (issue mode)")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) != 1 || args[0] == "" {
			return Result{}, Usage("usage: rota ship pr <branch> --title <text> --body-file <path|->")
		}
		branch := args[0]
		if *title == "" {
			return Result{}, Usage("--title is required")
		}
		body, err := shipBodyArg(c, *bodyFile, "the PR body")
		if err != nil {
			return Result{}, err
		}
		dir, err := shipExistingBranch(c, branch)
		if err != nil {
			return Result{}, err
		}
		ctx := c.Context()
		root := shipRoot(dir)
		cfg := config.Load(filepath.Join(root, ".rota", "config.json"))

		// The provider and the Closes lines come before the push, so a bad
		// --items ID fails with nothing pushed.
		cl, err := tracker.NewCLI(ctx, tracker.SettingsFromConfig(cfg), "", dir, trackerOptions...)
		if err != nil {
			// An origin that is neither GitHub nor GitLab falls back to github.
			if cl, err = tracker.NewCLI(ctx, tracker.SettingsFromConfig(cfg), "github", dir, trackerOptions...); err != nil {
				return Result{}, trackerErr(err)
			}
		}
		var ids []string
		for _, x := range strings.Split(*items, ",") {
			if x = pystr.Strip(x); x != "" {
				ids = append(ids, x)
			}
		}
		if lines, err := shipClosesLines(c, root, cfg, ids); err != nil {
			return Result{}, err
		} else if lines != "" {
			body += "\n\n" + lines
		}
		if d, err := shipVerdictBlock(c, dir, root, branch); err != nil {
			return Result{Data: d}, err
		}

		var base string
		if cl.Provider == "gitlab" {
			var ok bool
			if base, ok, err = resolveBase(ctx, dir); err != nil {
				return Result{}, err
			} else if !ok {
				return Result{}, Resolution("could not determine base branch (tried git.baseBranch, main, master, trunk, origin/HEAD)")
			}
		}
		if err := shipClearWorktree(c, dir, branch); err != nil {
			return Result{}, err
		}
		push, err := shipGit(c, dir, "push", "-u", "origin", branch)
		if err != nil {
			return Result{}, err
		}
		if push.Code != 0 {
			return Result{}, Unavailable("git push -u origin %s failed: %s", branch, shipFirstLine(push.Stderr))
		}
		var cli string
		var cargs []string
		var stdin io.Reader
		if cl.Provider == "gitlab" {
			cli = "glab"
			cargs = []string{"mr", "create", "--title", *title, "--description", body,
				"--source-branch", branch, "--target-branch", base, "--yes"}
		} else {
			cli = "gh"
			cargs = []string{"pr", "create", "--title", *title, "--body-file", "-"}
			stdin = strings.NewReader(body)
		}
		res, err := cl.Run(ctx, cargs, stdin)
		if err != nil {
			return Result{}, trackerErr(err)
		}
		if res.ExitCode != 0 {
			msg := shipFirstLine(string(res.Stderr))
			return Result{}, Unavailable("%s %s exited %d: %s", cli, cargs[0]+" "+cargs[1], res.ExitCode, msg)
		}
		url := ""
		for _, l := range pystr.Splitlines(string(res.Stdout)) {
			if pystr.Strip(l) != "" {
				url = pystr.Strip(l)
			}
		}
		data := gitObj("branch", branch, "url", url, "provider", cl.Provider)
		if m := shipNumberRe.FindStringSubmatch(url); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				data.Set("number", n)
			}
		}
		given := []string{}
		for _, x := range strings.Split(*items, ",") {
			if x != "" {
				given = append(given, x)
			}
		}
		data.Set("items", given)
		data.Set("changed", true)
		return Result{Data: data, Text: url}, nil
	}
}

// shipClosesLines is one `Closes #<n>` line per item in issue mode, "" in
// file mode, where --items is accepted and ignored. An unknown item is exit 3.
func shipClosesLines(c *Ctx, root string, cfg any, ids []string) (string, error) {
	if len(ids) == 0 {
		return "", nil
	}
	name, err := config.Backend(cfg)
	if err != nil {
		return "", &Error{Exit: ExitInternal, Message: err.Error()}
	}
	if name == "file" {
		return "", nil
	}
	b, err := a4Open(c, root, false, "")
	if err != nil {
		return "", shipBackendErr(err)
	}
	// In an umbrella the PR opens in one sub-repo, so its items resolve there:
	// --repo, else the sub-repo the working directory is in.
	sub := ""
	if u, ok := b.(*backlog.Umbrella); ok {
		if sub = u.Scope; sub == "" {
			sub = u.CwdRepo
		}
		if sub == "" {
			return "", Usage("umbrella issue mode needs --repo <name> with --items")
		}
	}
	var lines []string
	for _, ref := range ids {
		it, err := b.Get(ref)
		if err == nil && sub != "" && !strings.HasPrefix(it.ID, sub+":") {
			err = backlog.ErrNotFound // qualified with another sub-repo
		}
		if errors.Is(err, backlog.ErrNotFound) {
			return "", Resolution("--items %s: no such item in the issue tracker", ref)
		}
		if err != nil {
			return "", shipBackendErr(err)
		}
		lines = append(lines, "Closes #"+strconv.Itoa(it.Number))
	}
	return strings.Join(lines, "\n"), nil
}

// shipBackendErr maps a backlog failure onto the exit table; one the
// mapping does not know is a tracker or storage failure (exit 5).
func shipBackendErr(err error) error {
	_, ferr := a4Fail(err)
	var e *Error
	if errors.As(ferr, &e) {
		return e
	}
	return Unavailable("%v", err)
}

// ---- ship merge ------------------------------------------------------------

func shipMerge(fs *flag.FlagSet) RunFunc {
	bodyFile := fs.String("body-file", "", "merge message: a path, or - for stdin")
	confirm := confirmFlags(fs)
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) != 1 || args[0] == "" {
			return Result{}, Usage("usage: rota ship merge <branch> --body-file <path|->")
		}
		branch := args[0]
		conf, err := confirm()
		if err != nil {
			return Result{}, err
		}
		msg, err := shipBodyArg(c, *bodyFile, "the merge message")
		if err != nil {
			return Result{}, err
		}
		policy, err := mergePolicy(c)
		if err != nil {
			return Result{}, err
		}
		dir, err := shipExistingBranch(c, branch)
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
		if base == branch {
			return shipBlocked("base branch", "'%s' is the base branch", branch)
		}
		if d, err := shipVerdictBlock(c, dir, shipRoot(dir), branch); err != nil {
			return Result{Data: d}, err
		}
		files := func() ([]string, error) { return shipChangedFiles(c, dir, base+"..."+branch) }
		if res, err := clearMerge(c, policy, branch, conf, approvalReq{}, files, nil); err != nil {
			return res, err
		}
		if err := shipClearWorktree(c, dir, branch); err != nil {
			return Result{}, err
		}
		co, err := shipGit(c, dir, "checkout", "-q", base)
		if err != nil {
			return Result{}, err
		}
		if co.Code != 0 {
			return Result{}, Unavailable("git checkout %s: %s", base, shipFirstLine(co.Stderr))
		}
		mg, err := shipGit(c, dir, "merge", "--no-ff", branch, "-m", msg)
		if err != nil {
			return Result{}, err
		}
		if mg.Code != 0 {
			if all := mg.Stdout + mg.Stderr; strings.Contains(all, "CONFLICT") || strings.Contains(all, "Automatic merge failed") {
				// The tree is left as it was.
				shipGit(c, dir, "merge", "--abort")
				return shipBlocked("conflict", "merge conflict; merge aborted")
			}
			return Result{}, Unavailable("git merge %s: %s", branch, shipFirstLine(mg.Stderr+mg.Stdout))
		}
		del, err := shipGit(c, dir, "branch", "-d", branch)
		if err != nil {
			return Result{}, err
		}
		if del.Code != 0 {
			return Result{}, Unavailable("git branch -d %s: %s", branch, shipFirstLine(del.Stderr))
		}
		sha, err := shipGit(c, dir, "log", "-1", "--format=%h")
		if err != nil {
			return Result{}, err
		}
		short := shipLine(sha.Stdout)
		return Result{Data: gitObj("branch", branch, "base", base, "sha", short, "changed", true), Text: short}, nil
	}
}

// shipChangedFiles lists the files a merge range changes, for the
// merge-approval gate.
func shipChangedFiles(c *Ctx, dir, rng string) ([]string, error) {
	res, err := shipGit(c, dir, "diff", "--name-only", rng)
	if err != nil {
		return nil, err
	}
	if res.Code != 0 {
		return nil, Unavailable("git diff %s: %s", rng, shipFirstLine(res.Stderr))
	}
	return pystr.Splitlines(strings.TrimSpace(res.Stdout)), nil
}

// ---- ship pr-merge ---------------------------------------------------------

// shipPRMerge merges a PR in issue mode (hv-pr-merge).
func shipPRMerge(fs *flag.FlagSet) RunFunc {
	itemsFlag := fs.String("items", "", "item IDs the PR closes, comma separated")
	confirm := approvalFlags(fs)
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) != 1 {
			return Result{}, Usage("usage: rota ship pr-merge <pr> [--items <ID>[,<ID>...]]")
		}
		conf, req, err := confirm()
		if err != nil {
			return Result{}, err
		}
		pr, err := strconv.Atoi(args[0])
		if err != nil || strings.Trim(args[0], "0123456789") != "" {
			return Result{}, Usage("<pr> must be all digits")
		}
		if err := releaseNotAtUmbrella(c); err != nil {
			return Result{}, err
		}
		var items []string
		for _, s := range strings.Split(*itemsFlag, ",") {
			if s = pystr.Strip(s); s != "" {
				items = append(items, s)
			}
		}
		be, err := a8Issues(c, "use: rota ship merge", true)
		if err != nil {
			return a4Fail(err)
		}
		policy, err := mergePolicy(c)
		if err != nil {
			return Result{}, err
		}
		// The verdict check (B3) runs before the merge-approval gate (B1), so
		// a branch that would be refused never writes an audit line.
		var gateRes Result
		var gateErr error
		approve := func(branch string, files func() ([]string, error)) error {
			if branch == "" {
				c.Warn("could not resolve the head branch of PR %d; verdicts not checked", pr)
			} else if gateRes, gateErr = shipPRVerdict(c, pr, branch); gateErr != nil {
				return gateErr
			}
			req.Thread = func() (approvalThread, error) {
				return approvalThread{Kind: "pr", Number: pr, Title: fmt.Sprintf("Merge approval: PR #%d", pr)}, nil
			}
			gateRes, gateErr = clearMerge(c, policy, "PR "+args[0], conf, req, files, a4Obj("pr", pr))
			return gateErr
		}
		res, err := be.MergePRGated(pr, items, approve)
		var mf *backlog.MergeFailedError
		switch {
		case gateErr != nil:
			var e *Error
			if errors.As(gateErr, &e) {
				return gateRes, gateErr
			}
			return a4Fail(gateErr) // listing the PR's files failed at the tracker
		case errors.As(err, &mf):
			return Result{Data: a4Obj("pr", pr, "merged", false, "unproven", []string{}, "changesRequested", []string{}, "changed", false)},
				Refused("%s", err.Error())
		case err != nil:
			return a4Fail(err)
		case len(res.Unproven) > 0:
			ids := []string{}
			for _, u := range res.Unproven {
				ids = append(ids, u.ID)
			}
			return Result{Data: a4Obj("pr", pr, "merged", false, "unproven", ids, "changesRequested", ids, "changed", true)},
				Refused("an item has no proof; not merged")
		}
		closed, lines := []string{}, []string{"merged " + args[0] + " as " + res.SHA[:min(7, len(res.SHA))]}
		for _, r := range res.Closed {
			closed = append(closed, r.ID)
			lines = append(lines, "closed "+r.Ref())
		}
		return Result{Data: a4Obj("pr", pr, "sha", res.SHA[:min(7, len(res.SHA))], "closed", closed, "changed", true),
			Text: strings.Join(lines, "\n")}, nil
	}
}

// ---- ship undo -------------------------------------------------------------

var shipSections = map[byte]string{'B': "Bugs", 'F': "Features", 'T': "Tasks"}

// shipCycleIDs lists the item IDs whose done line in BACKLOG.md or ARCHIVE.md
// carries one of the cycle's short hashes, first seen first (resolve_cycle_ids).
func shipCycleIDs(root string, hashes map[string]bool) []string {
	var ids []string
	seen := map[string]bool{}
	for _, name := range []string{"BACKLOG.md", "ARCHIVE.md"} {
		text, err := fsio.ReadText(filepath.Join(root, ".rota", name))
		if err != nil {
			continue
		}
		for _, l := range pystr.Splitlines(text) {
			if d, ok := backlog.ParseDone(l); ok && d.ID != "" && hashes[d.Hash] && !seen[d.ID] {
				seen[d.ID] = true
				ids = append(ids, d.ID)
			}
		}
	}
	return ids
}

func shipUndo(fs *flag.FlagSet) RunFunc {
	cycle := fs.String("cycle", "", "undo this merge commit instead of the last cycle")
	allowPost := fs.Bool("allow-post-merge", false, "discard commits made after the cycle merge")
	apply := fs.Bool("apply", false, "reset the base branch and restore the items")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) > 0 {
			return Result{}, Usage("unexpected argument %q", args[0])
		}
		dir, err := gitDir(c)
		if err != nil {
			return Result{}, err
		}
		g := func(a ...string) (string, error) {
			res, err := shipGit(c, dir, a...)
			if err == nil && res.Code != 0 {
				err = Unavailable("git %s: %s", strings.Join(a, " "), shipFirstLine(res.Stderr))
			}
			return shipLine(res.Stdout), err
		}
		ctx := c.Context()
		base, ok, err := resolveBase(ctx, dir)
		if err != nil {
			return Result{}, err
		}
		if !ok {
			return shipBlocked("refused", "could not determine base branch (tried git.baseBranch, main, master, trunk, origin/HEAD)")
		}
		cur := ""
		if res, err := shipGit(c, dir, "symbolic-ref", "--short", "HEAD"); err != nil {
			return Result{}, err
		} else if res.Code == 0 {
			cur = shipLine(res.Stdout)
		}
		if cur != base {
			if cur == "" {
				cur = "(detached HEAD)"
			}
			return shipBlocked("not on base branch", "must run on the base branch (%s), currently on %s", base, cur)
		}
		dirty, _, err := git.Repo{Dir: dir}.Dirty(ctx)
		if err != nil {
			return Result{}, gitErr(err)
		}
		if dirty {
			return shipBlocked("dirty tree", "uncommitted changes: stash (`git stash`) or commit before running ship undo")
		}

		// With no --cycle, HEAD itself must be the merge.
		if *cycle == "" {
			parents, err := g("rev-list", "--parents", "-1", "HEAD")
			if err != nil {
				return Result{}, err
			}
			switch n := len(strings.Fields(parents)); n {
			case 3:
			case 2:
				res, err := shipBlocked("not a merge", "HEAD is not a merge commit; undo is not supported for this shape")
				return res, err.(*Error).WithHint("revert it instead: git revert HEAD (squash- and rebase-merges leave one parent)")
			default:
				return shipBlocked("not a merge", "unexpected HEAD shape (%d parents); manual investigation required", n-1)
			}
		}

		var merge string
		if *cycle != "" {
			res, err := shipGit(c, dir, "rev-parse", "--verify", *cycle+"^{commit}")
			if err != nil {
				return Result{}, err
			}
			if res.Code != 0 {
				return Result{}, Resolution("--cycle hash '%s' is not a valid commit", *cycle)
			}
			merge = shipLine(res.Stdout)
			parents, err := g("rev-list", "--parents", "-n", "1", merge)
			if err != nil {
				return Result{}, err
			}
			if len(strings.Fields(parents)) < 3 {
				return shipBlocked("not a merge", "--cycle commit %s is not a merge commit", *cycle)
			}
		} else {
			if merge, err = g("log", "--first-parent", "--merges", "-1", "--pretty=%H", base); err != nil {
				merge = ""
			}
			if merge == "" {
				return Result{}, Resolution("no merge commit found on %s", base)
			}
		}
		subject, err := g("log", "-1", "--pretty=%s", merge)
		if err != nil {
			return Result{}, err
		}
		if !strings.HasPrefix(subject, "merge: ") {
			if *cycle != "" {
				return shipBlocked("merge subject", "--cycle commit %s has subject not matching '^merge: ' (subject: %s)", *cycle, subject)
			}
			return shipBlocked("merge subject", "most recent merge on %s is not a rota cycle merge (subject: %s)", base, subject)
		}

		short, err := g("rev-parse", "--short", merge)
		if err != nil {
			return Result{}, err
		}
		preMerge, err := g("rev-parse", merge+"^1")
		if err != nil {
			return Result{}, err
		}
		preShort, err := g("rev-parse", "--short", preMerge)
		if err != nil {
			return Result{}, err
		}
		tip, err := g("rev-parse", merge+"^2")
		if err != nil {
			return Result{}, err
		}

		post, _ := g("log", "--first-parent", "--oneline", merge+".."+base)
		postCount := 0
		if post != "" {
			postCount = len(strings.Split(post, "\n"))
		}
		if postCount > 0 && !*allowPost {
			res, err := shipBlocked("post-merge commits", "%d commit(s) on %s after the cycle merge", postCount, base)
			return res, err.(*Error).WithHint("pass --allow-post-merge to discard them, or reset manually first")
		}

		// A remote ref at the cycle tip means the cycle went through a PR.
		if refs, _ := g("for-each-ref", "--format=%(refname:short)", "--points-at", tip, "refs/remotes/"); refs != "" {
			res, err := shipBlocked("pr mode", "PR-mode cycle: rolling back upstream PRs is manual (gh pr close / git revert); remote ref(s) at the cycle tip: %s",
				strings.Join(strings.Split(refs, "\n"), ", "))
			return res, err
		}

		hashes := map[string]bool{}
		hs, _ := g("log", "--pretty=%h", merge+"^1.."+merge+"^2")
		for _, h := range strings.Split(hs, "\n") {
			if h = pystr.Strip(h); h != "" {
				hashes[h] = true
			}
		}
		root := shipRoot(dir)
		ids := shipCycleIDs(root, hashes)

		plan := shipPlan(short, subject, base, postCount, ids, *apply)
		data := gitObj("applied", *apply, "cycle", short, "subject", pystr.Strip(subject), "base", base,
			"items", append([]string{}, ids...))
		if !*apply {
			c.Warn("preview only; pass --apply")
			data.Set("changed", false)
			return Result{Data: data, Text: plan}, nil
		}

		if _, err := g("reset", "--hard", merge+"^1"); err != nil {
			return Result{}, err
		}
		if len(ids) > 0 {
			if err := shipRestore(c, root, ids); err != nil {
				head, _ := g("rev-parse", "--short", "HEAD")
				return Result{}, Unavailable("the reset already happened (HEAD is now %s), but restoring an item failed: %v", head, err)
			}
		}
		restored := "none"
		if len(ids) > 0 {
			restored = strings.Join(ids, ",")
		}
		text := fmt.Sprintf("Undone cycle %s. Reset %s to %s. Restored: %s.", short, base, preShort, restored)
		data.Set("restoredTo", preShort)
		data.Set("restored", append([]string{}, ids...))
		data.Set("changed", true)
		return Result{Data: data, Text: text}, nil
	}
}

// shipPlan is the block ship undo prints before it changes anything.
func shipPlan(short, subject, base string, postCount int, ids []string, apply bool) string {
	var b strings.Builder
	note := ""
	if postCount > 0 {
		note = fmt.Sprintf(", %d commit(s) past the merge will be discarded", postCount)
	}
	fmt.Fprintf(&b, "Undo plan for last cycle: %s\n\n", short)
	fmt.Fprintf(&b, "Subject:  %s\n", subject)
	fmt.Fprintf(&b, "Base:     %s will reset --hard %s^1 (currently %s%s)\n", base, short, short, note)
	if len(ids) == 0 {
		b.WriteString("Items:    none (cycle resolved no tracked items)\n")
	}
	for i, id := range ids {
		lead := "          "
		if i == 0 {
			lead = "Items:    "
		}
		sec := shipSections[id[0]]
		if sec == "" {
			sec = "Unknown"
		}
		fmt.Fprintf(&b, "%s%s will be restored to BACKLOG.md (%s)\n", lead, id, sec)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "Branch:   deleted by rota ship merge; rerun `git branch <name> %s^2` to keep the work\n", short)
	b.WriteString("Status:   no active entry to clear (cycle already removed it)\n")
	b.WriteString("Handoff:  gitignored — not restorable\n")
	b.WriteString("Plans:    gitignored — not restorable\n")
	if !apply {
		b.WriteString("\nRe-run with --apply to apply.\n")
	}
	return b.String()
}

// shipRestore reopens each item through the backlog, as hv-uncomplete did.
// The backend is opened after the reset, so it reads the restored config.
// shipActive reports whether BACKLOG.md under root holds id as an active
// bullet outside ## Completed, the no-op case of hv-uncomplete. Checking it
// unlocked keeps the no-op from leaving a .lock sidecar, which the old
// helper never created and which dirties a tree that does not ignore it.
func shipActive(root, id string) bool {
	content, err := fsio.ReadText(filepath.Join(root, ".rota", "BACKLOG.md"))
	if err != nil {
		return false
	}
	cs, ce, hasC := section.Find(content, "Completed")
	for _, m := range regexp.MustCompile(`(?m)^- \*\*\[`+regexp.QuoteMeta(id)+`\]`).FindAllStringIndex(content, -1) {
		if !hasC || m[0] < cs || m[0] >= ce {
			return true
		}
	}
	return false
}

func shipRestore(c *Ctx, root string, ids []string) error {
	b, err := a4Open(c, root, false, "")
	if err != nil {
		return err
	}
	for _, id := range ids {
		changed := false
		if b.Name() != "file" || !shipActive(root, id) {
			if changed, err = b.Reopen(id); err != nil {
				return err
			}
		}
		if !changed {
			fmt.Fprintf(c.Stderr, "noop: [%s] already active in BACKLOG.md\n", id)
		}
	}
	return nil
}
