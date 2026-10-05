package cli

import (
	"context"
	"flag"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/pystr"
)

// reviewCommands is the `rota review` group (#52).
func reviewCommands() *Command {
	return &Command{Name: "review", Summary: "scope a review, build the second-opinion brief, scan for scaffolding", Subs: []*Command{
		{Name: "scope", Summary: "commits, files, item IDs and origin entries of a branch", Repo: true, Verb: noFlags(reviewScope)},
		{Name: "brief", Summary: "fresh-eyes second-opinion brief for a branch", Repo: true, Verb: noFlags(reviewBrief)},
		{Name: "scaffolding", Summary: "added diff lines that look like leftover task scaffolding", Repo: true, Verb: reviewScaffolding},
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
func reviewGit(ctx context.Context, dir string, args ...string) (string, error) {
	res, err := git.Repo{Dir: dir}.Run(ctx, args...)
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
	logOut, err := reviewGit(ctx, t.Dir, "log", "--no-merges", "--format=%h%x1f%s", span)
	if err != nil {
		return info, err
	}
	for _, l := range nonBlankLines(logOut) {
		if h, s, ok := strings.Cut(l, "\x1f"); ok {
			info.Commits = append(info.Commits, reviewCommit{h, s})
		}
	}
	filesOut, err := reviewGit(ctx, t.Dir, "diff", "--name-only", t.Base+"..."+t.Branch)
	if err != nil {
		return info, err
	}
	set := map[string]bool{}
	for _, f := range nonBlankLines(filesOut) {
		set[f] = true
	}
	for f := range set {
		info.Files = append(info.Files, f)
	}
	sort.Strings(info.Files)
	bodies, err := reviewGit(ctx, t.Dir, "log", "--no-merges", "--format=%B", span)
	if err != nil {
		return info, err
	}
	issue, err := issueBackend(t.CorpusRoot)
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

// pyUniversalNewlines is what subprocess.run(text=True) does to captured output.
func pyUniversalNewlines(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
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
		res, err := git.Repo{Dir: t.Dir}.Run(ctx, "diff", t.Base+"..."+t.Branch, "--", path)
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
		p(strings.TrimRight(pyUniversalNewlines(diff), "\n"))
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
	baseFlag := fs.String("base", "", "base branch to diff against (default: the resolved base, else main)")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) > 1 {
			return Result{}, Usage("unexpected argument %q", args[1])
		}
		dir, err := gitDir(c)
		if err != nil {
			return Result{}, err
		}
		ctx := c.Context()
		r := git.Repo{Dir: dir}
		base := *baseFlag
		if base == "" {
			b, ok, err := resolveBase(ctx, dir)
			if err != nil {
				return Result{}, err
			}
			if base = b; !ok {
				base = "main"
			}
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
		diff, err := reviewGit(ctx, dir, "diff", base+"..."+branch)
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
		row := jsonObj("id", r.ID, "type", r.Type, "number", r.Number, "title", r.Title)
		if r.Repo != "" {
			row.Set("repo", r.Repo)
		}
		row.Set("prs", prs)
		items = append(items, row)
		lines = append(lines, r.Type+strconv.Itoa(r.Number)+" "+r.Title)
	}
	return Result{Data: jsonObj("items", items), Text: strings.Join(lines, "\n")}, nil
}
