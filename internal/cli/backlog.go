package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/frontmatter"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/section"
	"github.com/l4ci/rota/internal/stale"
	"github.com/l4ci/rota/internal/status"
)

// The backlog views and maintenance verbs, `rota summary`, `rota status` and
// `rota refactor`. Shapes, flags and exits are the verb contract's
// (docs/design/contract/); the old helpers named on each verb are
// the behaviour to match.

func backlogCommands() []*Command {
	return []*Command{
		{Name: "backlog", Summary: "backlog views and upkeep", Subs: []*Command{
			{Name: "list", Summary: "open items as sorted tables, with clusters", Repo: true, Verb: backlogList},
			{Name: "ids", Summary: "IDs of the open items tagged with a milestone", Repo: true, Verb: backlogIDs},
			{Name: "milestones", Summary: "milestones the given items are tagged with", Repo: true, Verb: backlogMilestones},
			{Name: "drift", Summary: "open items that commits already mention", Repo: true, Verb: backlogDrift},
			{Name: "backfill", Summary: "stamp Since: on open items that lack it", Repo: true, Verb: backlogBackfill},
			{Name: "archive", Summary: "move old completed items to ARCHIVE.md", Repo: true, Verb: backlogArchive},
			{Name: "stale", Summary: "stale map, knowledge or backlog entries", Repo: true, Verb: backlogStale},
		}},
		{Name: "summary", Summary: "compact project state", Repo: true, Verb: summaryVerb},
		{Name: "status", Summary: "active work streams", Subs: []*Command{
			{Name: "add", Summary: "record an active work stream", Repo: true, Verb: statusAdd},
			{Name: "rm", Summary: "end a work stream and drop its handoff note", Repo: true, Verb: statusRm},
			{Name: "show", Summary: "which repo a branch's stream is in", Repo: true, Verb: statusShow},
			{Name: "handoff", Summary: "path of a branch's handoff note", Repo: true, Verb: statusHandoff},
		}},
		{Name: "refactor", Summary: "refactor cycle bookkeeping", Subs: []*Command{
			{Name: "age", Summary: "features and bugs completed since the last refactor", Repo: true, Verb: refactorAge},
			{Name: "reset", Summary: "zero the since-refactor counters", Repo: true, Verb: refactorReset},
			{Name: "targets", Summary: "what a refactor can cover", Verb: refactorTargets},
		}},
	}
}

func anySlice(xs []string) []any {
	out := make([]any, len(xs))
	for i, x := range xs {
		out[i] = x
	}
	return out
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ---- backlog list ----------------------------------------------------------------

var relatedIDRe = regexp.MustCompile(`[A-Z]\p{Nd}+`)

func backlogList(fs *flag.FlagSet) RunFunc {
	grep := fs.String("grep", "", "keep rows whose bullet contains this, case-insensitively")
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 0, 0, "backlog list takes no positional arguments"); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		be, err := openBacklog(c, root, false, "")
		if err != nil {
			return backlogFail(err)
		}
		rows, md, ok, err := backlog.OpenRows(be)
		if err != nil {
			return backlogFail(err)
		}
		data := jsonObj("inProgress", []any{}, "bugs", []any{}, "features", []any{}, "tasks", []any{}, "clusters", []any{})
		if !ok {
			return Result{Data: data, Text: "No .rota/BACKLOG.md yet. Run rota init then /rota-capture."}, nil
		}
		var active []backlog.Active
		for _, e := range status.Entries(root) {
			active = append(active, backlog.Active{Branch: e.Branch, Repo: e.Repo, Items: e.Items, StartedAt: e.StartedAt})
		}
		l := backlog.BuildListing(rows, md, active, *grep)

		typeOfID := map[string]string{}
		for _, r := range rows {
			typeOfID[r.ID] = r.Type
		}
		progress := []any{}
		for _, p := range l.InProgress {
			typ := p.Type
			if typ == "" {
				typ = typeOfID[p.ID]
			}
			o := jsonObj("id", p.ID, "type", typ, "title", p.Title, "branch", p.Branch, "startedAt", p.StartedAt)
			if p.Repo != "" {
				o.Set("repo", p.Repo)
			}
			progress = append(progress, o)
		}
		data.Set("inProgress", progress)
		issues := be.Name() == "issues"
		rowsOf := func(rs []backlog.ListRow, tagName string) []any {
			out := []any{}
			for _, r := range rs {
				o := jsonObj("id", r.ID)
				if tagName != "" {
					o.Set(tagName, r.Tag)
				}
				o.Set("title", r.Title)
				rel := relatedIDRe.FindAllString(r.Related, -1)
				if issues {
					for i := range rel {
						rel[i] = rel[i][1:]
					}
				}
				if rel == nil {
					rel = []string{}
				}
				o.Set("related", rel)
				if r.Milestone != "" {
					o.Set("milestone", r.Milestone)
				}
				out = append(out, o)
			}
			return out
		}
		data.Set("bugs", rowsOf(l.Bugs, "priority"))
		data.Set("features", rowsOf(l.Features, "size"))
		data.Set("tasks", rowsOf(l.Tasks, ""))
		clusters := []any{}
		for _, cl := range l.Clusters {
			clusters = append(clusters, cl)
		}
		data.Set("clusters", clusters)
		return Result{Data: data, Text: l.Text()}, nil
	}
}

// ---- backlog ids / milestones ----------------------------------------------------

func backlogIDs(fs *flag.FlagSet) RunFunc {
	milestone := fs.String("milestone", "", "milestone ID, such as M01")
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 0, 0, "backlog ids takes no positional arguments"); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		if *milestone == "" {
			return Result{}, Usage("--milestone is required")
		}
		be, err := openBacklog(c, root, false, "")
		if err != nil {
			return backlogFail(err)
		}
		rows, _, _, err := backlog.OpenRows(be)
		if err != nil {
			return backlogFail(err)
		}
		ids := backlog.IDsByMilestone(rows, *milestone)
		return Result{Data: jsonObj("milestone", *milestone, "ids", ids), Text: strings.Join(ids, "\n")}, nil
	}
}

func backlogMilestones(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 1, -1, "backlog milestones takes one or more item IDs"); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		be, err := openBacklog(c, root, false, "")
		if err != nil {
			return backlogFail(err)
		}
		rows, _, _, err := backlog.OpenRows(be)
		if err != nil {
			return backlogFail(err)
		}
		wanted := map[string]bool{}
		for _, a := range args {
			wanted[a] = true
		}
		issues := be.Name() == "issues"
		ms := backlog.MilestonesFor(rows, func(r backlog.Row) bool {
			return wanted[r.ID] || (issues && r.IssueMatches(wanted))
		})
		return Result{Data: jsonObj("milestones", ms), Text: strings.Join(ms, "\n")}, nil
	}
}

// ---- backlog drift / backfill / archive ------------------------------------------

func backlogDrift(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 0, 0, "backlog drift takes no arguments"); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		ops, err := openBacklogFile(c, root, `PRs carry "Closes #N", so the tracker closes shipped issues`)
		if err != nil {
			return backlogFailRead(err)
		}
		var targets []backlog.Target
		if registry := repos.Load(root); len(registry) > 0 {
			for _, r := range registry {
				if c.Repo == "" || r.Name == c.Repo {
					targets = append(targets, backlog.Target{Name: r.Name, Dir: r.Path})
				}
			}
		} else {
			targets = []backlog.Target{{Dir: root}}
		}
		drift, syms, err := ops.Drift(targets)
		if err != nil {
			return backlogFail(err)
		}
		dl, sl := []any{}, []any{}
		var lines []string
		for _, d := range drift {
			cs := []any{}
			for _, cm := range d.Commits {
				cs = append(cs, jsonObj("repo", cm.Repo, "hash", cm.Hash, "subject", cm.Subject))
				lines = append(lines, fmt.Sprintf("%s: %s %s", d.ID, cm.Hash, cm.Subject))
			}
			dl = append(dl, jsonObj("id", d.ID, "type", d.Type, "commits", cs))
		}
		for _, s := range syms {
			sl = append(sl, jsonObj("id", s.ID, "type", s.Type, "symbols", s.Symbols, "files", s.Files))
			lines = append(lines, fmt.Sprintf("%s: symbols %s in %s", s.ID, strings.Join(s.Symbols, ", "), strings.Join(s.Files, ", ")))
		}
		return Result{Data: jsonObj("drift", dl, "symbolDrift", sl), Text: strings.Join(lines, "\n")}, nil
	}
}

func backlogBackfill(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 0, 0, "backlog backfill takes no arguments"); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		ops, err := openBacklogFile(c, root, "Since: anchors exist only in the file backend")
		if err != nil {
			return backlogFail(err)
		}
		head, ok, gerr := git.Repo{Dir: root}.ShortHead(context.Background())
		if gerr != nil || !ok || head == "" {
			return Result{}, Unavailable("not in a git repo with a HEAD commit: cannot backfill Since:")
		}
		n, err := ops.BackfillSince(head)
		if err != nil {
			return backlogFail(err)
		}
		return Result{Data: jsonObj("stamped", n, "changed", n > 0), Text: fmt.Sprintf("stamped %d", n)}, nil
	}
}

func backlogArchive(fs *flag.FlagSet) RunFunc {
	days := fs.Int("days", 5, "move done lines older than this many days")
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 0, 0, "backlog archive takes no positional arguments"); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		if *days < 0 {
			return Result{}, Usage("--days must be a number")
		}
		ops, err := openBacklogFile(c, root, "closed issues are the archive")
		if err != nil {
			return backlogFail(err)
		}
		today, err := ageToday()
		if err != nil {
			return Result{}, err
		}
		moved, err := ops.Archive(*days, today)
		if err != nil {
			return backlogFail(err)
		}
		return Result{Data: jsonObj("days", *days, "moved", moved, "changed", moved > 0), Text: fmt.Sprintf("archived %d", moved)}, nil
	}
}

// ageToday is the day archive and stale measure age against: today, or the
// ROTA_TEST_TODAY override the tests pin it with.
func ageToday() (time.Time, error) {
	v := os.Getenv("ROTA_TEST_TODAY")
	if v == "" {
		return time.Now(), nil
	}
	t, ok := stale.ParseDate(v)
	if !ok {
		return time.Time{}, Usage("ROTA_TEST_TODAY must be YYYY-MM-DD, got %q", v)
	}
	return t, nil
}

// ---- backlog stale ---------------------------------------------------------------

func backlogStale(fs *flag.FlagSet) RunFunc {
	kind := fs.String("kind", "", "map|knowledge|todo")
	days := fs.Int("days", 90, "list entries this many days old or older")
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 0, 0, "backlog stale takes no positional arguments"); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		if !hasString(stale.Kinds, *kind) {
			return Result{}, Usage("--kind must be map|knowledge|todo")
		}
		today, err := ageToday()
		if err != nil {
			return Result{}, err
		}
		today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
		entries, err := stale.Find(root, *kind, *days, today)
		if err != nil {
			return backlogFail(err)
		}
		list := []any{}
		var lines []string
		for _, e := range entries {
			list = append(list, jsonObj("name", e.Name, "date", e.Date))
			lines = append(lines, e.Name+" "+e.Date)
		}
		return Result{Data: jsonObj("kind", *kind, "days", *days, "entries", list), Text: strings.Join(lines, "\n")}, nil
	}
}

// ---- summary ---------------------------------------------------------------------

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// topicsLine is the "N topics (a, b, c, d, ...)" line of hv-summary and the
// topic names it shows, for a file of ## sections; ok is false for a missing
// file or one with no topics.
func topicsLine(path string) (count int, shown []string, ok bool) {
	text, err := fsio.ReadText(path)
	if err != nil {
		return 0, nil, false
	}
	var names []string
	for _, t := range section.Topics(text) {
		names = append(names, t.Name)
	}
	if len(names) == 0 {
		return 0, nil, false
	}
	return len(names), names[:min(4, len(names))], true
}

func topicsText(count int, shown []string) string {
	suffix := ""
	if count > len(shown) {
		suffix = ", ..."
	}
	return fmt.Sprintf("%d topics (%s%s)", count, strings.Join(shown, ", "), suffix)
}

type milestone struct{ id, title string }

// activeMilestones is the active milestones of .rota/milestones/*.md, read the
// way hv-vision-list reads them: frontmatter id (else the file name), title
// and status (else "planned"), files without frontmatter skipped. This is the
// minimal read rota summary needs; the milestone verbs own the rest.
func activeMilestones(root string) []milestone {
	dir := root + "/.rota/milestones"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") && !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var out []milestone
	for _, n := range names {
		text, err := fsio.ReadText(dir + "/" + n)
		if err != nil {
			continue
		}
		fm, _, _ := frontmatter.Parse(text)
		if len(fm) == 0 {
			continue
		}
		id := frontmatter.Str(fm, "id")
		if id == "" {
			id = strings.TrimSuffix(n, ".md")
		}
		st := frontmatter.Str(fm, "status")
		if st == "" {
			st = "planned"
		}
		if st == "active" {
			out = append(out, milestone{id, frontmatter.Str(fm, "title")})
		}
	}
	return out
}

func summaryVerb(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 0, 0, "summary takes no arguments"); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		be, err := openBacklog(c, root, false, "")
		if err != nil {
			return backlogFail(err)
		}
		items, err := be.List(true)
		if errors.Is(err, backlog.ErrNotFound) {
			return Result{}, Resolution("no .rota/BACKLOG.md found").WithHint("run: rota init")
		}
		if err != nil {
			return backlogFail(err)
		}
		var lines []string
		open := map[string]int{}
		var closed []backlog.Item
		for _, it := range items {
			if it.Closed {
				closed = append(closed, it)
			} else {
				open[it.Type]++
			}
		}
		bugs, feats, tasks := open["B"], open["F"], open["T"]
		lines = append(lines, fmt.Sprintf("Backlog: %s, %s, %s", plural(bugs, "bug"), plural(feats, "feature"), plural(tasks, "task")))
		data := jsonObj("backlog", jsonObj("bugs", bugs, "features", feats, "tasks", tasks))

		active := []any{}
		for _, e := range status.Entries(root) {
			ids := strings.Join(e.Items, ", ")
			if ids == "" {
				ids = "?"
			}
			started := first10(e.StartedAt)
			loc, repoStr := "", ""
			if e.Worktree != "" {
				loc = " in " + e.Worktree
			}
			if e.Repo != "" {
				repoStr = " (repo: " + e.Repo + ")"
			}
			lines = append(lines, fmt.Sprintf("Active: %s on %s%s%s (since %s)", ids, e.Branch, loc, repoStr, started))
			o := jsonObj("items", e.Items, "branch", e.Branch)
			if e.Worktree != "" {
				o.Set("worktree", e.Worktree)
			}
			if e.Repo != "" {
				o.Set("repo", e.Repo)
			}
			o.Set("since", started)
			active = append(active, o)
		}
		data.Set("active", active)

		recent := []any{}
		var done []string
		for _, it := range closed[:min(3, len(closed))] {
			s := "[" + it.Key() + "] on " + it.ClosedAt
			if it.Reason != "done" {
				s += " (" + it.Reason + ")"
			}
			done = append(done, s)
			o := jsonObj("id", it.ID, "type", it.Type, "date", it.ClosedAt)
			if it.Reason != "done" {
				o.Set("reason", it.Reason)
			}
			recent = append(recent, o)
		}
		if len(done) > 0 {
			lines = append(lines, "Recent: "+strings.Join(done, ", "))
		}
		data.Set("recent", recent)

		ms := []any{}
		var msText []string
		if be.Name() == "file" { // issue mode keeps milestones in the tracker
			for _, m := range activeMilestones(root) {
				ms = append(ms, jsonObj("id", m.id, "title", m.title))
				msText = append(msText, pystr.Strip(m.id+" "+m.title))
			}
		}
		if len(msText) > 0 {
			lines = append(lines, "Active milestones: "+strings.Join(msText, ", "))
		}
		data.Set("milestones", ms)

		for _, k := range []struct{ label, file, key string }{{"Knowledge", "KNOWLEDGE.md", "knowledge"}, {"Decisions", "DECISIONS.md", "decisions"}} {
			if n, shown, ok := topicsLine(root + "/.rota/" + k.file); ok {
				lines = append(lines, k.label+": "+topicsText(n, shown))
				data.Set(k.key, jsonObj("count", n, "topics", shown))
			}
		}
		if text, err := fsio.ReadText(root + "/.rota/ARCHIVE.md"); err == nil {
			n := 0
			for _, l := range pystr.Splitlines(text) {
				if strings.HasPrefix(l, "- ~~") {
					n++
				}
			}
			if n > 0 {
				s := "s"
				if n == 1 {
					s = ""
				}
				lines = append(lines, fmt.Sprintf("Archive: %d older item%s", n, s))
				data.Set("archive", n)
			}
		}
		return Result{Data: data, Text: strings.Join(lines, "\n")}, nil
	}
}

func first10(s string) string {
	r := []rune(s)
	if len(r) > 10 {
		r = r[:10]
	}
	return string(r)
}

// ---- status ----------------------------------------------------------------------

func splitItems(csv string) []string {
	items := []string{}
	for _, p := range strings.Split(csv, ",") {
		if p = pystr.Strip(p); p != "" {
			items = append(items, p)
		}
	}
	return items
}

func statusAdd(fs *flag.FlagSet) RunFunc {
	items := fs.String("items", "", "item IDs, comma-separated")
	worktree := fs.String("worktree", "", "worktree path (one repo)")
	reposCSV := fs.String("repos", "", "sub-repo names, comma-separated")
	worktrees := fs.String("worktrees", "", "worktree paths, comma-separated, one per --repos name")
	ifAbsent := fs.Bool("if-absent", false, "leave an existing entry alone")
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 1, 1, "status add takes one branch"); err != nil {
			return Result{}, err
		}
		branch := args[0]
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		given := givenFlags(fs)
		if *items == "" {
			return Result{}, Usage("--items is required")
		}
		multi := given["repos"]
		if multi && c.Repo != "" {
			return Result{}, Usage("--repo and --repos are mutually exclusive")
		}
		if given["worktree"] == multi && (given["worktree"] || given["worktrees"]) {
			return Result{}, Usage("use --worktree with one repo, --worktrees with --repos")
		}
		if branch == "" {
			return Result{}, Usage("status add needs a branch")
		}
		list := splitItems(*items)
		scope := []string{c.Repo}
		changed := false
		if multi {
			names := status.ParseReposCSV(*reposCSV)
			if len(names) == 0 {
				return Result{}, Usage("--repos needs at least one name")
			}
			var wt []string
			if given["worktrees"] {
				wt = strings.Split(*worktrees, ",")
				if len(wt) != len(names) {
					return Result{}, Usage("--worktrees must list one path per --repos name")
				}
			}
			if missing := status.Missing(repos.Load(root), names); len(missing) > 0 {
				return Result{}, Resolution("unregistered sub-repo(s): %s", strings.Join(missing, ", "))
			}
			scope = names
			for i, name := range names {
				w := ""
				if wt != nil {
					w = pystr.Strip(wt[i])
				}
				ch, err := status.Add(root, branch, name, list, w, *ifAbsent)
				if err != nil {
					return backlogFail(err)
				}
				changed = changed || ch
			}
		} else {
			ch, err := status.Add(root, branch, c.Repo, list, *worktree, *ifAbsent)
			if err != nil {
				return backlogFail(err)
			}
			changed = ch
		}
		entries := []any{}
		for _, e := range status.Entries(root) {
			if e.Branch != branch || !hasString(scope, e.Repo) {
				continue
			}
			entries = append(entries, jsonObj("repo", nullStr(e.Repo), "items", e.Items, "worktree", nullStr(e.Worktree), "startedAt", e.StartedAt))
		}
		return Result{Data: jsonObj("branch", branch, "entries", entries, "changed", changed), Text: "active: " + branch}, nil
	}
}

func statusRm(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 1, 1, "status rm takes one branch"); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		branch := args[0]
		if branch == "" {
			return Result{}, Usage("status rm needs a branch")
		}
		if _, err := status.HandoffPath(branch, c.Repo); err != nil {
			return Result{}, Usage("%s", err.Error())
		}
		removed, err := status.Remove(root, branch, c.Repo)
		if err != nil {
			return backlogFail(err)
		}
		swept, err := status.RemoveHandoff(root, branch, c.Repo)
		if err != nil {
			return backlogFail(err)
		}
		return Result{Data: jsonObj("branch", branch, "removed", removed, "handoffRemoved", swept, "changed", removed > 0 || swept),
			Text: "removed " + branch}, nil
	}
}

func statusShow(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 1, 1, "status show takes one branch"); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		branch := args[0]
		e, ok := status.Find(root, branch, c.Repo)
		if !ok {
			return Result{Data: jsonObj("branch", branch, "active", false, "repo", nil, "items", []string{}, "worktree", nil)}, nil
		}
		return Result{Data: jsonObj("branch", branch, "active", true, "repo", nullStr(e.Repo), "items", e.Items,
			"worktree", nullStr(e.Worktree), "startedAt", e.StartedAt), Text: e.Repo}, nil
	}
}

func statusHandoff(fs *flag.FlagSet) RunFunc {
	canonical := fs.Bool("canonical", false, "return the write path without probing")
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 1, 1, "status handoff takes one branch"); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		branch := args[0]
		if branch == "" {
			return Result{}, Usage("status handoff needs a branch")
		}
		p, exists, err := status.Handoff(root, branch, c.Repo, *canonical)
		if err != nil {
			return Result{}, Usage("%s", err.Error())
		}
		return Result{Data: jsonObj("branch", branch, "path", nullStr(p), "exists", exists), Text: p}, nil
	}
}

// ---- refactor --------------------------------------------------------------------

func refactorAge(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 0, 0, "refactor age takes no arguments"); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		feats, bugs, err := (&backlog.File{Root: root}).RefactorAge()
		if err != nil {
			return backlogFail(err)
		}
		return Result{Data: jsonObj("features", feats, "bugs", bugs),
			Text: fmt.Sprintf("%s features, %s bugs since the last refactor", numText(feats), numText(bugs))}, nil
	}
}

func numText(v any) string {
	b, _ := jsonx.MarshalCompact(v)
	return string(b)
}

func refactorReset(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 0, 0, "refactor reset takes no arguments"); err != nil {
			return Result{}, err
		}
		root, err := backlogScope(c)
		if err != nil {
			return Result{}, err
		}
		changed, err := (&backlog.File{Root: root}).RefactorReset()
		if err != nil {
			return backlogFail(err)
		}
		return Result{Data: jsonObj("changed", changed), Text: "refactor counters reset"}, nil
	}
}

func refactorTargets(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 0, 0, "refactor targets takes no arguments"); err != nil {
			return Result{}, err
		}
		cwd, err := os.Getwd()
		if err != nil {
			return Result{}, err
		}
		registry := repos.Load(cwd)
		if len(registry) == 0 {
			return Result{Data: jsonObj("umbrella", nil, "subRepos", []any{}), Text: "single repo"}, nil
		}
		sort.Slice(registry, func(i, j int) bool { return registry[i].Name < registry[j].Name })
		subs := []any{}
		var lines []string
		for _, r := range registry {
			subs = append(subs, jsonObj("name", r.Name, "path", r.Path))
			lines = append(lines, r.Name+" "+r.Path)
		}
		return Result{Data: jsonObj("umbrella", jsonObj("hasCode", status.HasCode(cwd, registry)), "subRepos", subs),
			Text: strings.Join(lines, "\n")}, nil
	}
}
