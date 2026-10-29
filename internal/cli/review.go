package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/verdict"
	"github.com/l4ci/rota/internal/worker"
)

// reviewCommands is the `rota review` group (#52).
func reviewCommands() *Command {
	return &Command{Name: "review", Summary: "scope a review, package its diff, build the second-opinion brief, scan for scaffolding", Subs: []*Command{
		{Name: "scope", Summary: "commits, files, item IDs and origin entries of a branch", Repo: true, Verb: noFlags(reviewScope)},
		{Name: "brief", Summary: "fresh-eyes second-opinion brief for a branch", Repo: true, Verb: noFlags(reviewBrief)},
		{Name: "scaffolding", Summary: "added diff lines that look like leftover task scaffolding", Repo: true, Verb: reviewScaffolding},
		{Name: "prepare", Summary: "prepare a disposable reviewer workspace at a pinned commit", Repo: true, Verb: noFlags(reviewPrepare)},
		{Name: "cleanup", Summary: "remove only a prepared reviewer workspace", Verb: noFlags(reviewCleanup)},
		{Name: "probe", Summary: "run a reviewer probe with disposable HOME and config roots", Verb: noFlags(reviewProbe)},
		{Name: "package", Summary: "write a branch's commits, stat and diff to a file for the reviewer", Repo: true, Verb: reviewPackage},
		{Name: "depth", Summary: "review depth (full, light, none) ship.review picks for a branch, and why", Repo: true, Verb: reviewDepth},
		{Name: "queue", Summary: "open issues waiting for review", Repo: true, Verb: noFlags(reviewQueue)},
	}}
}

type reviewCommit struct{ Hash, Subject string }

// reviewIntent is one resolved item; Type is "" when unknown (issue mode
// with a failed lookup never gets here).
type reviewIntent struct{ ID, Title, Entry, Type string }

// reviewInfo is what review scope reports about a branch.
type reviewInfo struct {
	Branch, Base string
	Commits      []reviewCommit
	Files        []string
	IDs          []string
	Intents      []reviewIntent
}

// reviewGit runs git in dir; a failed call is exit 5.
func reviewGit(ctx context.Context, d *Deps, dir string, args ...string) (string, error) {
	res, err := d.Repo(dir).Run(ctx, args...)
	if err != nil {
		return "", gitErr(err)
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(res.Stderr)
		if msg == "" {
			msg = fmt.Sprintf("exit %d", res.ExitCode)
		}
		return "", Unavailable("git %s failed: %s", args[0], msg)
	}
	return res.Stdout, nil
}

// nonBlankLines is the old helpers' `[l for l in text.splitlines() if l.strip()]`
// on a shell-captured value (trailing newlines already gone).
func nonBlankLines(s string) []string {
	var out []string
	for _, l := range pystr.Splitlines(s) {
		if pystr.Strip(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// reviewScan reads the branch's commits, files and origin entries
// (hv-review-scope). The caller has checked that the branch is not the base.
func reviewScan(c *Ctx, t branchTarget) (reviewInfo, error) {
	info := reviewInfo{Branch: t.Branch, Base: t.Base, Commits: []reviewCommit{}, Files: []string{}, IDs: []string{}, Intents: []reviewIntent{}}
	ctx := c.Context()
	span := t.Base + ".." + t.Branch
	logOut, err := reviewGit(ctx, c.deps(), t.Dir, "log", "--no-merges", "--format=%h%x1f%s", span)
	if err != nil {
		return info, err
	}
	for _, l := range nonBlankLines(logOut) {
		if h, s, ok := strings.Cut(l, "\x1f"); ok {
			info.Commits = append(info.Commits, reviewCommit{h, s})
		}
	}
	files, err := c.deps().Repo(t.Dir).ChangedPaths(ctx, t.Base+"..."+t.Branch)
	if err != nil {
		return info, diffErr(err)
	}
	set := map[string]bool{}
	for _, f := range files {
		set[f] = true
	}
	for f := range set {
		info.Files = append(info.Files, f)
	}
	sort.Strings(info.Files)
	bodies, err := reviewGit(ctx, c.deps(), t.Dir, "log", "--no-merges", "--format=%B", span)
	if err != nil {
		return info, err
	}
	issue, err := issueBackend(c.deps(), t.CorpusRoot)
	if err != nil {
		return info, err
	}
	if issue {
		reviewScanIssues(c, t, bodies, &info)
		return info, nil
	}
	if ids := backlog.FindItemIDs(bodies, backlog.ItemLetters); ids != nil {
		info.IDs = ids
	}
	// File mode only (the issue branch returned above): the corpus is BACKLOG.md and ARCHIVE.md.
	corpus := fileBackend(t.CorpusRoot).Corpus()
	for _, id := range info.IDs {
		if line, title, ok := backlog.FindOrigin(corpus, id); ok {
			info.Intents = append(info.Intents, reviewIntent{id, title, line, id[:1]})
		}
	}
	return info, nil
}

// reviewScanIssues fills info.IDs and info.Intents from the "#N" refs of the
// branch name and the commits' closing keywords. A ref the tracker cannot
// resolve (offline, unknown) stays in IDs without an intent.
func reviewScanIssues(c *Ctx, t branchTarget, bodies string, info *reviewInfo) {
	var ids []string
	if r := backlog.BranchIssueRef(t.Branch); r != "" {
		ids = append(ids, r)
	}
	for _, r := range backlog.FindIssueRefs(bodies) {
		if !slices.Contains(ids, r) {
			ids = append(ids, r)
		}
	}
	if ids == nil {
		return
	}
	info.IDs = ids
	be, err := openBacklog(c, t.CorpusRoot, false, "")
	if err != nil {
		return
	}
	for _, id := range ids {
		if it, err := be.Get(id); err == nil && it != nil {
			info.Intents = append(info.Intents, reviewIntent{id, it.Title, it.Line, it.Type})
		}
	}
}

// reviewTarget resolves the branch and refuses the base branch; what is the
// verb's name for the message.
func reviewTarget(c *Ctx, args []string, what string) (branchTarget, error) {
	t, err := resolveBranch(c, args)
	if err != nil {
		return t, err
	}
	if t.Branch == t.Base {
		return t, Failed("cannot %s base branch '%s' against itself", what, t.Base)
	}
	return t, nil
}

func reviewScope(c *Ctx, args []string) (Result, error) {
	t, err := reviewTarget(c, args, "review")
	if err != nil {
		return Result{}, err
	}
	info, err := reviewScan(c, t)
	if err != nil {
		return Result{}, err
	}
	commits, intents := []any{}, []any{}
	for _, cm := range info.Commits {
		commits = append(commits, gitObj("hash", cm.Hash, "subject", cm.Subject))
	}
	for _, in := range info.Intents {
		// The old helper emitted null for a bullet without a "Title." part.
		var title any
		if in.Title != "" {
			title = in.Title
		}
		var typ any
		if in.Type != "" {
			typ = in.Type
		}
		intents = append(intents, gitObj("id", in.ID, "type", typ, "title", title, "entry", in.Entry))
	}
	data := gitObj("branch", info.Branch, "base", info.Base, "commitCount", len(info.Commits), "commits", commits,
		"touchedFiles", info.Files, "referencedIds", info.IDs, "intents", intents)
	text := fmt.Sprintf("%s vs %s: %d commits, %d files, %d items", info.Branch, info.Base, len(info.Commits), len(info.Files), len(info.IDs))
	return Result{Data: data, Text: text}, nil
}

func reviewBrief(c *Ctx, args []string) (Result, error) {
	t, err := reviewTarget(c, args, "second-opinion")
	if err != nil {
		return Result{}, err
	}
	ctx := c.Context()
	info, err := reviewScan(c, t)
	if err != nil {
		return Result{}, err
	}
	if len(info.Commits) == 0 {
		return Result{}, Failed("branch '%s' has no commits beyond '%s'", t.Branch, t.Base)
	}
	var b strings.Builder
	p := func(s string) { b.WriteString(s + "\n") }
	p("You are reviewing a feature branch with NO prior conversation context.")
	p("You receive only the goal and the diff. Your job: find what's wrong.")
	p("")
	p("A staff-engineer reviewer with full project context already evaluated this branch.")
	p("Your value is the blind spot — the issue the contextualized reviewer naturalized")
	p("because it knew \"why.\" Reason from the diff alone, as if you'd never seen this")
	p("project before.")
	p("")
	p(fmt.Sprintf("**Branch:** `%s` (%d commits vs `%s`)", info.Branch, len(info.Commits), info.Base))
	p("")
	p("**Goal (what the diff claims to accomplish):**")
	if len(info.Intents) > 0 {
		for _, in := range info.Intents {
			title := in.Title
			if title == "" {
				title = "None" // the old f-string printed the Python None
			}
			label := "[" + in.ID + "]"
			if strings.HasPrefix(in.ID, "#") {
				label = in.ID
			}
			p(fmt.Sprintf("- %s %s — %s", label, title, in.Entry))
		}
	} else {
		p("- (no linked TODO items — judge against commit subjects only)")
	}
	p("")
	p("**Commits:**")
	for _, cm := range info.Commits {
		p(fmt.Sprintf("- `%s` %s", cm.Hash, cm.Subject))
	}
	p("")
	p("**Diff:**")
	p("")
	for _, path := range info.Files {
		// A failing per-file diff printed as empty, as subprocess.run(check=False) did.
		res, err := c.deps().Repo(t.Dir).Run(ctx, "diff", t.Base+"..."+t.Branch, "--", path)
		if err != nil {
			return Result{}, gitErr(err)
		}
		diff := res.Stdout
		if res.ExitCode != 0 {
			diff = ""
		}
		p(fmt.Sprintf("### `%s`", path))
		p("")
		p("```diff")
		p(strings.TrimRight(pystr.UniversalNewlines(diff), "\n"))
		p("```")
		p("")
	}
	for _, l := range []string{
		"## Evaluate",
		"",
		"Return PASS / CONCERN / FAIL with file:line evidence for each issue.",
		"",
		"1. **Goal match** — does the diff deliver the stated goal? Missing pieces,",
		"   scope creep, off-target work? Compare commit subjects to goal text.",
		"2. **Obvious quality** — dead code, error swallowing, untested new branches,",
		"   security smells, API contract breaks, performance cliffs, leaky abstractions,",
		"   off-by-one errors, missing edge cases visible in the diff.",
		"3. **Fresh-eyes inconsistencies** — what would surprise a reader who has no",
		"   project context? Names that don't match what they do, comments that",
		"   contradict the code, dead-flag-style stubs, suspicious silence on a code",
		"   path the rest of the diff treats as load-bearing.",
		"",
		"Be specific: file:line for every concern. Rank by severity. If unsure, say so",
		"— a fresh reviewer flagging an honest uncertainty is more useful than a",
		"confident hand-wave.",
		"",
		"**Verdict block.** End the report with one fenced `json` block and nothing after it:",
		"",
		"```json",
		`{"verdict": "PASS", "summary": "<one line>", "findings": [{"severity": "blocker|major|minor|info", "title": "<what>", "file": "<path>", "line": 42, "detail": "<evidence>"}]}`,
		"```",
		"",
		"`verdict` is one of:",
		"- PASS — nothing worth surfacing",
		"- CONCERNS — works, but flag before merge",
		"- FAIL — merge would regress behavior, break the goal, or violate sane practice",
	} {
		p(l)
	}
	brief := b.String()
	return Result{Data: gitObj("branch", info.Branch, "base", info.Base, "commitCount", len(info.Commits), "brief", brief), Text: brief}, nil
}

// scaffoldRe is the old helper's pattern with Python's Unicode \b and \s: a
// match must start after, and end before, a non-word rune (Go's \b is ASCII
// only, so a letter like "é" next to "Task 1" would differ).
var scaffoldRe = func() *regexp.Regexp {
	const nw = `[^\p{L}\p{N}_]`
	sp := "[" + pystr.SpaceClass + "]"
	return regexp.MustCompile(`(?i)(?:^|` + nw + `)(?:Task` + sp + `+[0-9]+|in flight|placeholder|added later|not yet wired)(?:$|` + nw + `)`)
}()

var scaffoldHunkRe = regexp.MustCompile(`^@@ -[0-9]+(?:,[0-9]+)? \+([0-9]+)(?:,[0-9]+)? @@`)

type scaffoldFinding struct {
	File string
	Line int
	Text string
}

// scaffoldScan walks a unified diff and returns added lines that match.
func scaffoldScan(diff string) []scaffoldFinding {
	out := []scaffoldFinding{}
	file, newLine := "None", 0 // the old helper printed Python's None before any "+++ b/"
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			file, newLine = line[6:], 0
			continue
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			continue
		}
		if m := scaffoldHunkRe.FindStringSubmatch(line); m != nil {
			newLine, _ = strconv.Atoi(m[1])
			continue
		}
		switch {
		case strings.HasPrefix(line, "+"):
			if text := line[1:]; scaffoldRe.MatchString(text) {
				out = append(out, scaffoldFinding{file, newLine, text})
			}
			newLine++
		case strings.HasPrefix(line, " "):
			newLine++
		}
	}
	return out
}

func reviewScaffolding(fs *flag.FlagSet) RunFunc {
	baseFlag := fs.String("base", "", "base branch to diff against (default: the resolved base)")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) > 1 {
			return Result{}, Usage("unexpected argument %q", args[1])
		}
		dir, err := gitDir(c)
		if err != nil {
			return Result{}, err
		}
		ctx := c.Context()
		r := c.deps().Repo(dir)
		base := *baseFlag
		if base == "" {
			b, ok, err := resolveBase(ctx, c.deps(), dir)
			if err != nil {
				return Result{}, err
			}
			if !ok {
				return Result{}, Resolution(noBaseMsg).WithHint("pass --base <ref>")
			}
			base = b
		}
		var branch string
		if len(args) == 1 && args[0] != "" {
			branch = args[0]
		} else {
			if branch, err = r.CurrentBranch(ctx); err != nil {
				return Result{}, gitErr(err)
			}
			if branch == "" {
				return Result{}, Resolution("no branch given and HEAD is not on a branch")
			}
		}
		for _, ref := range []string{base, branch} {
			found, err := r.Verify(ctx, ref+"^{commit}")
			if err != nil {
				return Result{}, gitErr(err)
			}
			if !found {
				return Result{}, Resolution("%s: no such branch", ref)
			}
		}
		diff, err := reviewGit(ctx, c.deps(), dir, "diff", base+"..."+branch)
		if err != nil {
			return Result{}, err
		}
		findings := []any{}
		var lines []string
		// Data drops the CR of a CRLF line (the shim's line splitting did); text keeps it, as the old helper did.
		for _, f := range scaffoldScan(diff) {
			findings = append(findings, gitObj("file", f.File, "line", f.Line, "text", strings.TrimSuffix(f.Text, "\r")))
			lines = append(lines, fmt.Sprintf("%s:%d:%s", f.File, f.Line, f.Text))
		}
		return Result{Data: gitObj("findings", findings), Text: strings.Join(lines, "\n")}, nil
	}
}

func reviewQueue(c *Ctx, args []string) (Result, error) {
	if len(args) > 0 {
		return Result{}, Usage("usage: rota review queue")
	}
	be, err := openIssueBackend(c, "", false)
	if err != nil {
		return backlogFailRead(err)
	}
	rows, err := be.ReviewQueue()
	if err != nil {
		return backlogFailRead(err)
	}
	items, lines := []any{}, []string{}
	for _, r := range rows {
		prs := []any{}
		for _, p := range r.PRs {
			prs = append(prs, jsonObj("number", p.Number, "title", p.Title, "branch", p.Branch, "url", p.URL, "body", p.Body))
		}
		row := jsonObj("id", r.ID, "type", r.Type)
		if r.Number > 0 {
			row.Set("number", r.Number)
		}
		row.Set("title", r.Title)
		if r.Repo != "" {
			row.Set("repo", r.Repo)
		}
		row.Set("prs", prs)
		items = append(items, row)
		ref := r.ID
		if r.Number > 0 {
			ref = r.Type + strconv.Itoa(r.Number)
		}
		lines = append(lines, ref+" "+r.Title)
	}
	return Result{Data: jsonObj("items", items), Text: strings.Join(lines, "\n")}, nil
}

// reviewDepth is `rota review depth`: the depth ship.review picks for a branch,
// from its diff size against the base and the labels of the branch's issue
// (read from the tracker when the policy has a labels map) plus any --labels. /rota-ship prints it and runs /rota-review accordingly.
func reviewDepth(fs *flag.FlagSet) RunFunc {
	labelsFlag := fs.String("labels", "", "extra labels, added to the branch issue's own")
	return func(c *Ctx, args []string) (Result, error) {
		t, err := reviewTarget(c, args, "review")
		if err != nil {
			return Result{}, err
		}
		var extra []string
		for _, l := range strings.Split(*labelsFlag, ",") {
			if l = strings.TrimSpace(l); l != "" {
				extra = append(extra, l)
			}
		}
		res, why, err := branchReviewDepth(c, t, extra)
		if err != nil {
			return Result{}, err
		}
		depth := res.Depth
		data := gitObj("branch", t.Branch, "base", t.Base, "depth", string(depth), "signal", res.Signal, "why", why, "changedLines", res.Changed, "labels", res.Labels)
		text := fmt.Sprintf("REVIEW-DEPTH %s — %s (%s)", t.Branch, depth, why)
		// Printed only when a label asks about it, so a Minor PR's output is
		// unchanged.
		if res.Cross != "" {
			data.Set("cross", res.Cross)
			data.Set("crossWhy", res.CrossWhy)
			text += fmt.Sprintf("\nREVIEW-CROSS %s — %s (%s)", t.Branch, res.Cross, res.CrossWhy)
		}
		// Always present, so a caller reads "none" as an empty list, not a missing key.
		lenses := make([]any, 0, len(res.Lenses))
		for _, l := range res.Lenses {
			lenses = append(lenses, jsonObj("name", l.Name, "why", l.Why))
			text += fmt.Sprintf("\nREVIEW-LENS %s — %s (%s)", t.Branch, l.Name, l.Why)
		}
		data.Set("lenses", lenses)
		// A bundled branch closes more than its lead: the reviewer checks every
		// bundled issue's acceptance.
		if root, err := backlogScope(c); err == nil {
			if members := branchBundle(c, root, t.Branch); len(members) > 0 {
				refs := make([]string, len(members))
				for i, m := range members {
					refs[i] = m.Ref()
				}
				data.Set("bundle", refs)
				text += " bundle: " + strings.Join(refs, ", ")
			}
		}
		// The depth can rise after the review ran (a bounce fix outside the
		// issue's files); the newest review set on record is the old depth.
		if root, err := backlogScope(c); err == nil {
			bare, _, _ := splitRemote(c, t.Dir, t.Branch)
			if last := verdict.LastReviewSet(root, bare); len(last) > 0 {
				if old := verdict.CoveredDepth(last, worker.BranchHasSpec(t.Branch)); verdict.DepthRose(old, depth) {
					note := fmt.Sprintf("review depth %s -> %s (%s): record the missing review verdict before gating", old, depth, why)
					data.Set("previousDepth", string(old))
					data.Set("depthNote", note)
					text += "\n" + note
				}
			}
		}
		return Result{Data: data, Text: text}, nil
	}
}

// branchBundle is the member issues of the bundle behind branch: the bundle of
// the slot holding the branch, else of the review record queued for it. The
// registry lives in the main checkout, so a worktree falls back to that one.
// Nil when the branch is no bundle's or the registry is unreadable.
func branchBundle(c *Ctx, root, branch string) []tracker.IssueKey {
	_, ok := worker.BranchIssue(branch)
	if !ok {
		return nil
	}
	reg := branchRegistry(c, root)
	var ids []string
	sl, q := reg.BranchHolder(branch)
	if sl != nil {
		ids = sl.Bundle()
	}
	if len(ids) == 0 && q != nil {
		ids = q.Bundle
	}
	var keys []tracker.IssueKey
	for _, id := range ids {
		if k, ok := tracker.ParseKey(id); ok {
			keys = append(keys, k)
		}
	}
	return keys
}

// branchRegistry is the worker registry of root, else of the main checkout
// when root is a worktree without one.
func branchRegistry(c *Ctx, root string) worker.Registry {
	reg := worker.LoadRegistryTolerant(root)
	if !reg.Exists {
		common, err := rotastate.CommonDirVia(c.Context(), c.deps().Git, root)
		if err != nil {
			return reg
		}
		if main, ok := rotastate.MainCheckout(common); ok && main != root {
			reg = worker.LoadRegistryTolerant(main)
		}
	}
	return reg
}

// branchAuthor is the harness of the slot holding branch, the PR's author for
// the cross-harness review; "" when no slot holds it.
func branchAuthor(c *Ctx, root, branch string) string {
	if sl, _ := branchRegistry(c, root).BranchHolder(branch); sl != nil {
		return sl.HarnessKind()
	}
	return ""
}

// branchReviewDepth resolves the depth ship.review picks for t, from its diff
// size against the base, the labels of the branch's issue (read from the
// tracker when the policy has a labels map) plus extra, and the Door line of
// its open PR. why carries the Door note.
func branchReviewDepth(c *Ctx, t branchTarget, extra []string) (worker.ReviewDepth, string, error) {
	root, err := backlogScope(c)
	if err != nil {
		return worker.ReviewDepth{}, "", err
	}
	cfg := c.deps().Config(root)
	policy, err := config.ReviewPolicyOf(cfg)
	if err != nil {
		return worker.ReviewDepth{}, "", Failed("%v; run rota config check", err)
	}
	cross, err := roundcfg.ParseCrossReview(cfg)
	if err != nil {
		return worker.ReviewDepth{}, "", Failed("%v; run rota config check", err)
	}
	lenses, err := config.LensesOf(cfg)
	if err != nil {
		return worker.ReviewDepth{}, "", Failed("%v; run rota config check", err)
	}
	src := worker.ReviewSources{Policy: policy, Git: c.deps().Git, Dir: t.Dir, Span: t.Base + "..." + t.Branch, Labels: extra,
		Cross: cross, Author: branchAuthor(c, root, t.Branch), Lenses: lenses}
	// The issue behind a `<agent>/<issue>-<slug>` branch is read, as the worker
	// gate does, when the policy can use it (a label to match or a file set to
	// drift from) or a cross-harness review may be required.
	if n, ok := worker.BranchIssue(t.Branch); ok && (policy.NeedsIssue() || cross.Active() || lenses.UsesLabels()) {
		it, err := c.deps().issuesOrNone(c.Context(), cfg, root)
		if err != nil {
			return worker.ReviewDepth{}, "", trackerErr(err)
		}
		if _, none := it.(tracker.NoIssues); !none { // the file source has no issues to read
			src.Issue, src.Issues = n, it
			src.Members = branchBundle(c, root, t.Branch)
		}
	}
	// The branch's open PR carries the Door line. Like the gate, an unreadable
	// forge fails the verb: skipping would print a depth that ignores the line.
	// A local forge or a missing forge CLI has no PR to read.
	prFound, noForge := false, false
	if policy.MayBeBelowFull() {
		f, err := c.deps().forge(c.Context(), cfg, "", root)
		switch {
		case err == nil:
			src.PRBody = func() (string, error) {
				prs, err := f.OpenPRs(c.Context())
				if err != nil {
					return "", err
				}
				for _, pr := range prs {
					if pr.Branch == t.Branch {
						prFound = true
						return pr.Body, nil
					}
				}
				return "", nil
			}
		case errors.Is(err, tracker.ErrLocalForge) || tracker.IsCLIMissing(err):
			noForge = true
		default:
			return worker.ReviewDepth{}, "", Failed("could not reach the forge to read the PR body for its Door line to pick the review depth: %v", err)
		}
	}
	res, err := worker.ResolveReviewDepth(c.Context(), src)
	if err != nil {
		var de *worker.ReviewDepthError
		if errors.As(err, &de) && de.Stage == "diff" {
			return worker.ReviewDepth{}, "", diffErr(de.Err)
		}
		return worker.ReviewDepth{}, "", Failed("%v", err)
	}
	return res, res.Why + worker.DoorNote(policy, res, prFound, noForge), nil
}
