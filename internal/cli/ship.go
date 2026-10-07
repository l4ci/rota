package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/land"
	"github.com/l4ci/rota/internal/proof"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/ship"
	"github.com/l4ci/rota/internal/status"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/verdict"
	"github.com/l4ci/rota/internal/worker"
)

// shipCommands is the `rota ship` group (#52). The flow itself is
// internal/ship; the verbs here parse flags, wire the ports and render.
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

// shipGitRunner adapts git in dir to ship.Git, mapping a failure to run git
// onto the exit table.
type shipGitRunner struct {
	c   *Ctx
	dir string
}

func (r shipGitRunner) Run(args ...string) (git.Result, error) {
	return shipGit(r.c, r.dir, args...)
}

// shipErr maps an internal/ship failure onto the envelope and exit table; an
// error it does not know is already shaped by the port that raised it.
func shipErr(err error) (Result, error) {
	var ref *ship.Refusal
	var nf *ship.NotFoundError
	var ge *ship.GitError
	var pe *ship.PartialError
	var vb *ship.VerdictBlockedError
	var nc *ship.NoCommitsError
	var ui *ship.UnknownItemError
	switch {
	case errors.As(err, new(*land.CleanupError)):
		return Result{}, Unavailable("%s", err.Error())
	case errors.As(err, &ref):
		res, e := shipBlocked(ref.By, "%s", ref.Msg)
		if ref.Hint != "" {
			e.(*Error).WithHint(ref.Hint)
		}
		return res, e
	case errors.As(err, &vb):
		d := gitObj("blockedBy", "verdict", "kind", vb.Record.Kind, "verdict", vb.Record.Verdict, "sha", vb.Record.Sha, "stale", vb.Stale, "changed", false)
		return Result{Data: d}, Refused("%s", vb.Error()).WithHint("see: rota verdict show " + vb.Branch)
	case errors.As(err, &nf), errors.As(err, &ui):
		return Result{}, Resolution("%s", err.Error())
	case errors.As(err, &nc):
		return Result{}, Failed("%s", nc.Error())
	case errors.As(err, &ge), errors.As(err, &pe):
		return Result{}, Unavailable("%s", err.Error())
	}
	return Result{}, err
}

// shipVerdict is the B3 verdict gate for the checkout in dir. root is the
// project holding the verdict store.
func shipVerdict(c *Ctx, dir, root string) ship.VerdictCheck {
	repo := c.Repo
	if repo == "" && repos.Umbrella(root) {
		if r, err := repos.Which(dir); err == nil {
			repo = r.Name
		}
	}
	cfg := config.Load(rotatree.Config(root))
	return ship.VerdictCheck{
		Git:      shipGitRunner{c, dir},
		Store:    verdict.Load(root),
		Repo:     repo,
		Settings: verdict.Settings{Runner: config.String(cfg, "ship.secondOpinionRunner")},
	}
}

// shipPRVerdict is the verdict gate for the head branch of PR pr, with the PR
// number added to the refusal data.
func shipPRVerdict(c *Ctx, pr int, branch string) (Result, error) {
	dir, err := gitDir(c)
	if err != nil {
		return Result{}, err
	}
	root, err := c.Root()
	if err != nil {
		return Result{}, err
	}
	if err := shipVerdict(c, dir, root).Block(branch); err != nil {
		res, e := shipErr(err)
		if d, ok := res.Data.(*jsonx.Object); ok {
			d.Set("pr", pr)
		}
		return res, e
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

// shipWorktreeCheck recognizes /rota-work's status registration. A branch
// match or a familiar directory name alone never authorizes deletion.
func shipWorktreeCheck(c *Ctx, dir, branch string) func(string) error {
	return func(wt string) error {
		listing, err := shipGit(c, dir, "worktree", "list", "--porcelain")
		if err != nil {
			return err
		}
		if listing.ExitCode != 0 {
			return Unavailable("cannot read worktree ownership: %s", shipFirstLine(listing.Stderr))
		}
		primary := strings.TrimPrefix(strings.SplitN(listing.Stdout, "\n", 2)[0], "worktree ")
		roots := []string{shipRoot(primary), primary, shipRoot(dir)}
		resolve := func(root, path string) string {
			if filepath.IsAbs(path) {
				return path
			}
			return filepath.Join(root, path)
		}
		for _, root := range roots {
			for _, slot := range worker.LoadRegistry(root).Slots() {
				if path := slot.Worktree(); path != "" && ship.PathContains(resolve(root, path), wt) {
					return &ship.Refusal{By: "worktree", Msg: "worktree belongs to round slot " + slot.Name() + ": " + wt, Hint: "leave slot cleanup to the round orchestrator"}
				}
			}
		}
		repo := c.Repo
		if repo == "" {
			if r, err := repos.Which(dir); err == nil {
				repo = r.Name
			}
		}
		for _, root := range roots {
			for _, entry := range status.Entries(root) {
				if entry.Branch != branch || entry.Repo != repo || entry.Worktree == "" {
					continue
				}
				path := resolve(root, entry.Worktree)
				if ship.PathContains(path, wt) && ship.PathContains(wt, path) {
					return nil
				}
			}
		}
		return &ship.Refusal{By: "worktree", Msg: "worktree is not registered as cycle work: " + wt, Hint: "preserve this worktree; use the owning workflow to manage it"}
	}
}

// shipBase is the base branch of dir; none resolving is exit 3.
func shipBase(c *Ctx, dir string) (string, error) {
	base, ok, err := resolveBase(c.Context(), dir)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", Resolution("could not determine base branch (tried git.baseBranch, main, master, trunk, origin/HEAD)")
	}
	return base, nil
}

// ---- ship body -------------------------------------------------------------

func shipBody(c *Ctx, args []string) (Result, error) {
	t, err := resolveBranch(c, args)
	if err != nil {
		return Result{}, err
	}
	body, err := ship.Body(shipGitRunner{c, t.Dir}, t.Base, t.Branch, func() (ship.TitleOf, error) {
		f, err := shipTitles(c, t.CorpusRoot)
		return ship.TitleOf(f), err
	}, func(id string) ([]proof.Row, error) {
		_, st, _, err := openProof(c, id)
		if err != nil {
			return nil, err
		}
		rows, _, err := proof.Show(st, id)
		return rows, err
	})
	if err != nil {
		return shipErr(err)
	}
	return Result{Data: gitObj("branch", t.Branch, "body", body), Text: body}, nil
}

// shipTitles resolves an item ID to its origin line and title: through the
// tracker in issue mode (an unresolvable ID has none), else through the file
// corpus.
func shipTitles(c *Ctx, root string) (func(id string) (line, title string, ok bool), error) {
	issue, err := issueBackend(root)
	if err != nil {
		return nil, err
	}
	if !issue {
		// File mode only (issue mode takes the tracker below): the corpus is BACKLOG.md and ARCHIVE.md.
		corpus := fileBackend(root).Corpus()
		return func(id string) (string, string, bool) { return backlog.FindOrigin(corpus, id) }, nil
	}
	be, err := openBacklog(c, root, false, "")
	if err != nil {
		// The body is advisory text: an unreachable tracker lists the IDs bare.
		return func(string) (string, string, bool) { return "", "", false }, nil
	}
	return func(id string) (string, string, bool) {
		it, err := be.Get(id)
		if err != nil || it == nil {
			return "", "", false
		}
		return it.Line, it.Title, true
	}, nil
}

// ---- ship pr ---------------------------------------------------------------

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
		cfg := config.Load(rotatree.Config(root))

		cl, err := c.deps().forgeOrGitHub(ctx, cfg, "", dir)
		if err != nil {
			return Result{}, trackerErr(err)
		}
		given := pystr.SplitCSV(*items)
		pr, err := ship.OpenPR(ctx, ship.PRPorts{
			Git:     shipGitRunner{c, dir},
			Forge:   shipForge{cl},
			Closes:  func(ids []string) (string, error) { return shipClosesLines(c, root, cfg, ids) },
			Verdict: shipVerdict(c, dir, root).Block,
			Base:    func() (string, error) { return shipBase(c, dir) },
		}, ship.PRRequest{Branch: branch, Title: *title, Body: body, Items: given})
		if err != nil {
			return shipErr(err)
		}
		data := gitObj("branch", branch, "url", pr.URL, "provider", pr.Provider)
		if pr.Number != 0 {
			data.Set("number", pr.Number)
		}
		data.Set("items", given)
		data.Set("changed", true)
		return Result{Data: data, Text: pr.URL}, nil
	}
}

// shipForge maps the forge's failures onto the exit table.
type shipForge struct{ ship.Forge }

func (f shipForge) PRCreate(ctx context.Context, s tracker.PRSpec) (string, error) {
	url, err := f.Forge.PRCreate(ctx, s)
	if err != nil {
		return "", trackerErr(err)
	}
	return url, nil
}

// shipClosesLines is one `Closes #<n>` line per item in issue mode, "" in
// file mode, where --items is accepted and ignored.
func shipClosesLines(c *Ctx, root string, cfg any, ids []string) (string, error) {
	if len(ids) == 0 {
		return "", nil
	}
	name, _, err := backendMode(root)
	if err != nil {
		return "", err
	}
	if name == "file" {
		return "", nil
	}
	b, err := openBacklog(c, root, false, "")
	if err != nil {
		return "", shipBackendErr(err)
	}
	// In an umbrella the PR opens in one sub-repo, so its items resolve there:
	// --repo, else the sub-repo the working directory is in.
	sub := ""
	if u, ok := b.(backlog.SubRepoScoped); ok {
		if sub = u.SubRepo(); sub == "" {
			return "", Usage("umbrella issue mode needs --repo <name> with --items")
		}
	}
	lines, err := ship.ClosesLines(b, sub, ids)
	var ui *ship.UnknownItemError
	if err != nil && !errors.As(err, &ui) {
		return "", shipBackendErr(err)
	}
	return lines, err
}

// shipBackendErr maps a backlog failure onto the exit table; one the
// mapping does not know is a tracker or storage failure (exit 5).
func shipBackendErr(err error) error {
	_, ferr := backlogFail(err)
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
		base, err := shipBase(c, dir)
		if err != nil {
			return Result{}, err
		}
		g := shipGitRunner{c, dir}
		sha, err := ship.MergeBranch(ship.MergePorts{
			Git:     g,
			Recover: land.RecoveryGit(c.Context(), git.Exec, dir),
			Verdict: shipVerdict(c, dir, shipRoot(dir)).Block,
			Approve: func() error {
				files := func() ([]string, error) {
					fl, err := ship.ChangedFiles(g, base+"..."+branch)
					if err != nil {
						_, err = shipErr(err)
					}
					return fl, err
				}
				return clearMerge(c, policy, branch, conf, approvalReq{}, files, nil)
			},
			WorktreeCheck: shipWorktreeCheck(c, dir, branch),
		}, branch, base, msg)
		if err != nil {
			if d := refusalData(err); d != nil { // clearMerge's refusal carries its own envelope
				return Result{Data: d}, err
			}
			return shipErr(err)
		}
		return Result{Data: gitObj("branch", branch, "base", base, "sha", sha, "changed", true), Text: sha}, nil
	}
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
		// No --items means the PR body's links, which the backend reads from nil.
		var items []string
		if given := pystr.SplitCSV(*itemsFlag); len(given) > 0 {
			items = given
		}
		be, err := openIssueBackend(c, "use: rota ship merge", true)
		if err != nil {
			return backlogFail(err)
		}
		policy, err := mergePolicy(c)
		if err != nil {
			return Result{}, err
		}
		// The verdict check (B3) runs before the merge-approval gate (B1), so
		// a branch that would be refused never writes an audit line.
		approve := func(branch string, files func() ([]string, error)) error {
			if branch == "" {
				c.Warn("could not resolve the head branch of PR %d; verdicts not checked", pr)
			} else if res, err := shipPRVerdict(c, pr, branch); err != nil {
				return carryData(res, err)
			}
			req.Thread = func() (approvalThread, error) {
				return approvalThread{Kind: "pr", Number: pr, Title: fmt.Sprintf("Merge approval: PR #%d", pr)}, nil
			}
			return clearMerge(c, policy, "PR "+args[0], conf, req, files, jsonObj("pr", pr))
		}
		merged, err := ship.MergePR(be, pr, items, approve)
		var refused *ship.PRMergeRefused
		var unproven *ship.UnprovenError
		switch {
		case refusalData(err) != nil:
			return Result{Data: refusalData(err)}, err
		case errors.As(err, &refused):
			return Result{Data: jsonObj("pr", pr, "merged", false, "unproven", []string{}, "changesRequested", []string{}, "changed", false)},
				Refused("%s", err.Error())
		case errors.As(err, &unproven):
			return Result{Data: jsonObj("pr", pr, "merged", false, "unproven", unproven.IDs, "changesRequested", unproven.IDs, "changed", true)},
				Refused("%s", err.Error())
		case err != nil:
			return backlogFail(err)
		}
		closed, lines := []string{}, []string{"merged " + args[0] + " as " + merged.SHA}
		for _, r := range merged.Closed {
			closed = append(closed, r.ID)
			lines = append(lines, "closed "+r.Ref())
		}
		return Result{Data: jsonObj("pr", pr, "sha", merged.SHA, "closed", closed, "changed", true),
			Text: strings.Join(lines, "\n")}, nil
	}
}

// ---- ship undo -------------------------------------------------------------

var shipSections = map[byte]string{'B': "Bugs", 'F': "Features", 'T': "Tasks"}

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
		} else if res.ExitCode == 0 {
			cur = shipLine(res.Stdout)
		}
		dirty, _, err := git.Repo{Dir: dir}.Dirty(ctx)
		if err != nil {
			return Result{}, gitErr(err)
		}

		root := shipRoot(dir)
		g := shipGitRunner{c, dir}
		plan, err := ship.PlanUndo(g, base, cur, dirty, ship.UndoOpts{
			Cycle: *cycle, AllowPost: *allowPost,
			IDs: func(hashes map[string]bool) []string { return ship.CycleIDs(root, hashes) },
		})
		if err != nil {
			return shipErr(err)
		}
		short, ids := plan.Short, plan.IDs

		text := shipPlan(short, plan.Subject, base, plan.PostCount, ids, *apply)
		data := gitObj("applied", *apply, "cycle", short, "subject", pystr.Strip(plan.Subject), "base", base,
			"items", append([]string{}, ids...))
		if !*apply {
			c.Warn("preview only; pass --apply")
			data.Set("changed", false)
			return Result{Data: data, Text: text}, nil
		}

		if err := ship.ApplyUndo(g, plan, func(ids []string) error {
			// The backend is opened after the reset, so it reads the restored config.
			b, err := openBacklog(c, root, false, "")
			if err != nil {
				return err
			}
			return ship.Restore(b, root, ids, c.Stderr)
		}); err != nil {
			return shipErr(err)
		}
		restored := "none"
		if len(ids) > 0 {
			restored = strings.Join(ids, ",")
		}
		text = fmt.Sprintf("Undone cycle %s. Reset %s to %s. Restored: %s.", short, base, plan.PreShort, restored)
		data.Set("restoredTo", plan.PreShort)
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
	b.WriteString("Plans:    removed on ship for milestone items — not restored\n")
	if !apply {
		b.WriteString("\nRe-run with --apply to apply.\n")
	}
	return b.String()
}
