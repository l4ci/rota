package backlog

import (
	"errors"
	ms "github.com/l4ci/rota/internal/milestone"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/section"
	"github.com/l4ci/rota/internal/status"
)

// Row is one open item as the listing views (hv-backlog, hv-todo-by-milestone,
// hv-find-milestone-for-items) read it.
type Row struct {
	ID      string // data spelling: "B07" in file mode, "12" in issue mode, "repo:12" in umbrella issue mode
	Key     string // identity inside Related cells: "B07", and "F12" for issue 12
	Repo    string // umbrella issue mode: the owning sub-repo; "" otherwise
	Number  int    // issue mode: the issue number
	Type    string // "B" | "F" | "T"
	Tag     string
	Title   string
	Section string // "Bugs" | "Features" | "Tasks"
	Raw     string // the bullet line, "- **[B07] ..." (what --grep searches)
	Fields  Fields
}

var sectionOfType = map[string]string{"B": "Bugs", "F": "Features", "T": "Tasks"}

// OpenRows lists the open items of be in section order, with the Markdown the
// bullets came from (what hv-backlog's title lookup searches). ok is false when
// there is no backlog to read: no BACKLOG.md in file mode. The file backend
// reads every open bullet, indented ones too, as the helpers do; the issue
// backend lists its open issues.
func OpenRows(be Backend) (rows []Row, md string, ok bool, err error) {
	md, err = be.Markdown(0)
	if errors.Is(err, ErrNotFound) {
		return nil, "", false, nil
	}
	if err != nil {
		return nil, "", false, err
	}
	if be.Name() == "file" {
		for _, e := range OpenBullets(md) {
			b, _ := ParseOpen(e.Line)
			rows = append(rows, Row{ID: e.ID, Key: e.ID, Type: e.ID[:1], Tag: b.Tag, Title: b.Title,
				Section: e.Section, Raw: e.Line, Fields: e.Fields})
		}
		return rows, md, true, nil
	}
	items, err := be.List(false)
	if err != nil {
		return nil, "", false, err
	}
	_, umbrella := be.(*Umbrella)
	for _, it := range items {
		r := Row{ID: it.ID, Key: it.Key(), Number: it.Number, Type: it.Type, Tag: it.Tag,
			Title: it.Title, Section: sectionOfType[it.Type], Raw: it.Line, Fields: it.Fields}
		if umbrella {
			r.Repo = it.Fields.Get("repos")
		}
		rows = append(rows, r)
	}
	return rows, md, true, nil
}

// activeIn is whether an umbrella issue row is claimed by an active stream. A
// stream names its items in any spelling an issue answers to; a spelling that
// does not name the sub-repo ("F12", "12") counts only when the stream is in
// that sub-repo or in none, because another sub-repo's F12 is another item.
func (r Row) activeIn(active []Active) bool {
	if r.Repo == "" {
		return false
	}
	for _, e := range active {
		for _, id := range e.Items {
			if !r.IssueMatches(map[string]bool{id: true}) {
				continue
			}
			qualified := strings.Contains(id, ":") || strings.Index(id, "#") > 0
			if qualified || e.Repo == "" || e.Repo == r.Repo {
				return true
			}
		}
	}
	return false
}

// IssueMatches is whether r is one of the wanted references, in every
// spelling an issue answers to: "12", "#12", "F12" and, in an umbrella,
// "repo:12", "repo:F12", "repo:#12" and "repo#12".
func (r Row) IssueMatches(wanted map[string]bool) bool {
	n := strconv.Itoa(r.Number)
	if wanted[r.ID] || wanted[r.Key] || wanted["#"+n] {
		return true
	}
	if r.Repo == "" {
		return false
	}
	return wanted[n] || wanted[r.Repo+":"+r.Key] || wanted[r.Repo+":#"+n] || wanted[r.Repo+"#"+n]
}

// ParseMilestones is parse_milestones: every milestone ID (M01, M3) in text,
// in order, duplicates kept.
func ParseMilestones(text string) []string { return ms.IDs(text) }

// IDsByMilestone is hv-todo-by-milestone: the open items tagged with mid, in
// backlog order. A multi-valued "Milestone: M01, M03" matches either.
func IDsByMilestone(rows []Row, mid string) []string {
	ids := []string{}
	for _, r := range rows {
		for _, m := range ParseMilestones(r.Fields.Get("milestone")) {
			if m == mid {
				ids = append(ids, r.ID)
				break
			}
		}
	}
	return ids
}

// MilestonesFor is hv-find-milestone-for-items: the milestones the wanted
// items are tagged with, unique and numerically sorted. match says whether a
// row is one of the wanted items. Unknown or untagged items add nothing.
func MilestonesFor(rows []Row, match func(Row) bool) []string {
	set := map[string]bool{}
	for _, r := range rows {
		if !match(r) {
			continue
		}
		for _, m := range ParseMilestones(r.Fields.Get("milestone")) {
			set[m] = true
		}
	}
	out := make([]string, 0, len(set))
	for m := range set {
		out = append(out, m)
	}
	num := func(m string) int { n, _ := Atoi(m[1:]); return n }
	sort.Slice(out, func(i, j int) bool {
		if a, b := num(out[i]), num(out[j]); a != b {
			return a < b
		}
		return out[i] < out[j]
	})
	return out
}

// CountOpen counts the bullets of each open section the way hv-summary does:
// a line that starts "- **[" after stripping, whether or not it parses as an
// open bullet. Missing sections count 0.
func CountOpen(md string) map[string]int {
	out := map[string]int{}
	for _, name := range OpenSections {
		s, e, ok := section.Find(md, name)
		if !ok {
			continue
		}
		for _, line := range pystr.Splitlines(md[s:e]) {
			if strings.HasPrefix(pystr.Strip(line), "- **[") {
				out[name]++
			}
		}
	}
	return out
}

// ---- rota backlog list -----------------------------------------------------------

// Active is one active stream, as hv-backlog reads status.json.
type Active struct {
	Branch    string
	Repo      string
	Items     []string
	StartedAt string
}

// InProgress is one row of the In Progress table.
type InProgress struct {
	ID, Type, Title, Branch, StartedAt, Repo string
}

// ListRow is one row of the Bugs, Features or Tasks table.
type ListRow struct {
	Row
	Related   string // the Related field without its trailing dots
	Milestone string // "M01, M03", "" when untagged
}

// Listing is what rota backlog list shows.
type Listing struct {
	InProgress []InProgress
	Bugs       []ListRow
	Features   []ListRow
	Tasks      []ListRow
	Clusters   [][]string // item IDs, data spelling

	text        string
	clusterKeys [][]string
	titles      map[string]string
	outIDs      map[string]string
}

var (
	progressTitleRe = regexp.MustCompile(`\]` + sp + `+([^.]+?)\.`)
	idInRelatedRe   = BracketedIDRe
)

// titleOf is hv-backlog's title_of: the text between the ID and the first "."
// of the item's bullet, which keeps the [tag] of a tagged item; "?" when the
// ID has no bullet or no ".".
func titleOf(md, id string) string {
	m := regexp.MustCompile(`\*\*\[` + regexp.QuoteMeta(id) + `\][^*]*\*\*`).FindString(md)
	if m == "" {
		return "?"
	}
	t := progressTitleRe.FindStringSubmatch(m)
	if t == nil {
		return "?"
	}
	return pystr.Strip(t[1])
}

// BuildListing assembles rota backlog list. Items that are active appear only in
// In Progress. grep, when non-empty, keeps the Bugs, Features and Tasks rows
// whose bullet contains it, case-insensitively, and the clusters with a member
// that matched; In Progress is never filtered.
func BuildListing(rows []Row, md string, active []Active, grep string) *Listing {
	l := &Listing{Clusters: [][]string{}}
	activeIDs := map[string]bool{}
	for _, e := range active {
		for _, id := range e.Items {
			activeIDs[id] = true
			l.InProgress = append(l.InProgress, InProgress{ID: id, Type: ItemType(id), Title: titleOf(md, id),
				Branch: e.Branch, StartedAt: e.StartedAt, Repo: e.Repo})
		}
	}
	pat := strings.ToLower(grep)
	match := func(r Row) bool { return pat == "" || strings.Contains(strings.ToLower(r.Raw), pat) }

	prio := map[string]int{"P0": 0, "P1": 1, "P2": 2}
	size := map[string]int{"Cosmetic": 0, "Minor": 1, "Major": 2}
	rank := func(m map[string]int, tag string) int {
		if n, ok := m[tag]; ok {
			return n
		}
		return 3
	}
	for _, r := range rows {
		if activeIDs[r.ID] || r.activeIn(active) || !match(r) {
			continue
		}
		lr := ListRow{Row: r, Related: strings.TrimRight(r.Fields.Get("related"), "."),
			Milestone: strings.Join(ParseMilestones(r.Fields.Get("milestone")), ", ")}
		switch r.Section {
		case "Bugs":
			l.Bugs = append(l.Bugs, lr)
		case "Features":
			l.Features = append(l.Features, lr)
		default:
			l.Tasks = append(l.Tasks, lr)
		}
	}
	sort.SliceStable(l.Bugs, func(i, j int) bool { return rank(prio, l.Bugs[i].Tag) < rank(prio, l.Bugs[j].Tag) })
	sort.SliceStable(l.Features, func(i, j int) bool { return rank(size, l.Features[i].Tag) < rank(size, l.Features[j].Tag) })

	l.cluster(rows, match)
	l.render(md, active, grep)
	return l
}

// cluster finds the connected components of the Related graph, over every
// open item, active ones included.
func (l *Listing) cluster(rows []Row, match func(Row) bool) {
	// An umbrella's sub-repos number their items independently and Related
	// names items of the same sub-repo, so identity is the key plus the repo.
	// It sorts by key first, as the old helper's bare IDs did.
	ident := func(key, repo string) string { return key + "\x00" + repo }
	title := map[string]string{}
	outID := map[string]string{}
	for _, r := range rows {
		id := ident(r.Key, r.Repo)
		title[id], outID[id] = r.Title, r.ID
	}
	adj := map[string]map[string]bool{}
	link := func(a, b string) {
		if adj[a] == nil {
			adj[a] = map[string]bool{}
		}
		adj[a][b] = true
	}
	for _, r := range rows {
		self := ident(r.Key, r.Repo)
		for _, m := range idInRelatedRe.FindAllStringSubmatch(strings.TrimRight(r.Fields.Get("related"), "."), -1) {
			other := ident(m[1], r.Repo)
			if _, ok := title[other]; ok && other != self {
				link(self, other)
				link(other, self)
			}
		}
	}
	keys := make([]string, 0, len(title))
	for k := range title {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	seen := map[string]bool{}
	var comps [][]string
	for _, k := range keys {
		if seen[k] {
			continue
		}
		var comp []string
		queue := []string{k}
		for len(queue) > 0 {
			n := queue[len(queue)-1]
			queue = queue[:len(queue)-1]
			if seen[n] {
				continue
			}
			seen[n] = true
			comp = append(comp, n)
			for nb := range adj[n] {
				queue = append(queue, nb)
			}
		}
		if len(comp) >= 2 {
			sort.Strings(comp)
			comps = append(comps, comp)
		}
	}
	sort.SliceStable(comps, func(i, j int) bool { return comps[i][0] < comps[j][0] })
	matched := map[string]bool{}
	for _, r := range rows {
		if match(r) {
			matched[ident(r.Key, r.Repo)] = true
		}
	}
	var kept [][]string
	for _, c := range comps {
		for _, k := range c {
			if matched[k] {
				kept = append(kept, c)
				break
			}
		}
	}
	comps = kept
	l.clusterKeys = comps
	l.titles, l.outIDs = title, outID
	for _, c := range comps {
		ids := make([]string, len(c))
		for i, k := range c {
			ids[i] = outID[k]
		}
		l.Clusters = append(l.Clusters, ids)
	}
}

// Text is hv-backlog's Markdown, byte for byte.
func (l *Listing) Text() string { return l.text }

// Summary is the data behind `rota summary`: counts, active work, recent
// closures, milestones, topic indexes and the archive size. cli renders it.
type Summary struct {
	Bugs, Features, Tasks int
	Active                []SummaryActive
	Recent                []Item // the last closed items, at most 3
	Milestones            []SummaryMilestone
	Topics                []SummaryTopics // Knowledge, then Decisions; absent when the file has no topics
	Archive               int             // older items in ARCHIVE.md; 0 when none
}

// SummaryActive is one in-flight entry of status.json.
type SummaryActive struct {
	Items            []string
	Branch, Worktree string
	Repo             string
	Since            string // the start date, YYYY-MM-DD
}

// SummaryMilestone is an active milestone.
type SummaryMilestone struct{ ID, Title string }

// SummaryTopics is the topic index of a KNOWLEDGE.md or DECISIONS.md.
type SummaryTopics struct {
	Key, Label string
	Count      int
	Shown      []string // the first few topic names
}

// BuildSummary assembles the summary of the project at root from its items
// (open and closed). Milestones are read only in file mode; issue mode keeps
// them in the tracker.
func BuildSummary(root string, items []Item, fileMode bool) Summary {
	var sm Summary
	var closed []Item
	for _, it := range items {
		if it.Closed {
			closed = append(closed, it)
			continue
		}
		switch it.Type {
		case "B":
			sm.Bugs++
		case "F":
			sm.Features++
		case "T":
			sm.Tasks++
		}
	}
	for _, e := range status.Entries(root) {
		sm.Active = append(sm.Active, SummaryActive{Items: e.Items, Branch: e.Branch, Worktree: e.Worktree, Repo: e.Repo, Since: first10(e.StartedAt)})
	}
	sm.Recent = closed[:min(3, len(closed))]
	if fileMode {
		list, _ := ms.List(root)
		for _, m := range list {
			if m.Status == "active" {
				sm.Milestones = append(sm.Milestones, SummaryMilestone{m.ID, m.Title})
			}
		}
	}
	for _, k := range []struct{ label, file, key string }{{"Knowledge", "KNOWLEDGE.md", "knowledge"}, {"Decisions", "DECISIONS.md", "decisions"}} {
		if t, ok := summaryTopics(root + "/.rota/" + k.file); ok {
			t.Key, t.Label = k.key, k.label
			sm.Topics = append(sm.Topics, t)
		}
	}
	if text, err := fsio.ReadText(root + "/.rota/ARCHIVE.md"); err == nil {
		for _, l := range pystr.Splitlines(text) {
			if strings.HasPrefix(l, "- ~~") {
				sm.Archive++
			}
		}
	}
	return sm
}

// summaryTopics reads the ## sections of path; ok is false for a missing file
// or one with no topics.
func summaryTopics(path string) (SummaryTopics, bool) {
	text, err := fsio.ReadText(path)
	if err != nil {
		return SummaryTopics{}, false
	}
	var names []string
	for _, t := range section.Topics(text) {
		names = append(names, t.Name)
	}
	if len(names) == 0 {
		return SummaryTopics{}, false
	}
	return SummaryTopics{Count: len(names), Shown: names[:min(4, len(names))]}, true
}

func first10(s string) string {
	r := []rune(s)
	if len(r) > 10 {
		r = r[:10]
	}
	return string(r)
}

// render builds the Markdown hv-backlog prints, line by line as the helper
// does: each out entry is one joined segment, and a heading carries its own
// trailing newline.
func (l *Listing) render(md string, active []Active, grep string) {
	var out []string
	if len(active) > 0 {
		showRepo := false
		for _, e := range active {
			showRepo = showRepo || e.Repo != ""
		}
		out = append(out, "### In Progress\n")
		if showRepo {
			out = append(out, "| ID | Title | Branch | Repo | Started |", "|----|-------|--------|------|---------|")
		} else {
			out = append(out, "| ID | Title | Branch | Started |", "|----|-------|--------|---------|")
		}
		for _, p := range l.InProgress {
			if showRepo {
				out = append(out, "| "+p.ID+" | "+p.Title+" | "+p.Branch+" | "+p.Repo+" | "+first10(p.StartedAt)+" |")
			} else {
				out = append(out, "| "+p.ID+" | "+p.Title+" | "+p.Branch+" | "+first10(p.StartedAt)+" |")
			}
		}
		out = append(out, "")
	}
	table := func(label string, rows []ListRow, headers []string, cells func(ListRow) []string) {
		if len(rows) == 0 {
			return
		}
		showMS := false
		for _, r := range rows {
			showMS = showMS || r.Milestone != ""
		}
		if showMS {
			headers = append(append([]string{}, headers...), "Milestone")
		}
		out = append(out, "### "+label+"\n", "| "+strings.Join(headers, " | ")+" |",
			"|"+strings.TrimSuffix(strings.Repeat("----|", len(headers)), "|")+"|")
		for _, r := range rows {
			c := cells(r)
			if showMS {
				c = append(c, r.Milestone)
			}
			out = append(out, "| "+strings.Join(c, " | ")+" |")
		}
		out = append(out, "")
	}
	table("Bugs", l.Bugs, []string{"ID", "Prio", "Title", "Related"},
		func(r ListRow) []string { return []string{r.ID, r.Tag, r.Title, r.Related} })
	table("Features", l.Features, []string{"ID", "Size", "Title", "Related"},
		func(r ListRow) []string { return []string{r.ID, r.Tag, r.Title, r.Related} })
	table("Tasks", l.Tasks, []string{"ID", "Title", "Related"},
		func(r ListRow) []string { return []string{r.ID, r.Title, r.Related} })
	if len(l.clusterKeys) > 0 {
		out = append(out, "### Clusters\n")
		for _, c := range l.clusterKeys {
			sep := ", "
			if len(c) == 2 {
				sep = " \u2194 "
			}
			parts := make([]string, len(c))
			for i, k := range c {
				key, _, _ := strings.Cut(k, "\x00")
				parts[i] = "[" + key + "] " + l.titles[k]
			}
			out = append(out, "- "+strings.Join(parts, sep))
		}
		out = append(out, "")
	}
	if len(active) == 0 && len(l.Bugs) == 0 && len(l.Features) == 0 && len(l.Tasks) == 0 {
		if grep != "" {
			out = append(out, "No matches for pattern '"+grep+"'.")
		} else {
			out = append(out, "Backlog empty. Run /rota-capture to add items.")
		}
	}
	l.text = strings.Join(out, "\n") + "\n"
}
