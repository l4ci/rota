package cli

import (
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/tui"
	"github.com/l4ci/rota/internal/worker"
)

// The issue-focused overview of `rota round status --ui`: one table grouped by
// where an issue stands. It only reads the snapshot; nothing here assigns,
// offers or repairs.

const (
	grpActive = iota
	grpReview
	grpSupport
	grpUpcoming
)

var groupTitles = [...]string{grpActive: "Active", grpReview: "PR review", grpSupport: "Support queue", grpUpcoming: "Upcoming"}

// tone is the colour a status takes. Every status also has a text label, so
// colour is only a second channel (and NO_COLOR loses nothing).
type tone int

const (
	toneNone tone = iota
	toneOK
	toneWarn
	toneBad
)

func (t tone) paint(st tui.Style, s string) string {
	switch t {
	case toneOK:
		return st.Green(s)
	case toneWarn:
		return st.Yellow(s)
	case toneBad:
		return st.Red(s)
	}
	return s
}

// entry is one selectable row. key is the row's stable identity, so a refresh
// keeps the cursor on the same issue when its group changes.
type entry struct {
	key    string
	group  int
	issue  string
	title  string
	worker string
	status string
	tone   tone
	model  string
	pr     string
	label  string // detail pane title
	// issueURL and prURL are the row's browser pages, "" when unknown.
	issueURL, prURL string
	body            string
}

// entries are the rows in table order: Active, PR review, Support queue, Upcoming.
func (s roundSnap) entries() []entry {
	var by [len(groupTitles)][]entry
	add := func(e entry) { by[e.group] = append(by[e.group], e) }
	inReview := map[string]bool{}
	for _, r := range s.Slots {
		if r.Issue == "" {
			continue // parked: counted in the totals, not a row
		}
		e := slotEntry(r, s.IssueURLs)
		if !s.CandsLoaded && r.HostState == "" {
			e.status = "checking"
		}
		if awaitingReview(r) {
			e.group = grpReview
			inReview[r.PR] = true
		}
		add(e)
	}
	for _, q := range s.Review {
		if q.State == "merged" || q.State == "closed" || inReview[q.PR] {
			continue
		}
		add(reviewEntry(q, s.IssueURLs))
	}
	for _, it := range s.Support {
		add(supportEntry(it, s.IssueURLs))
	}
	for _, c := range s.Cands {
		add(candEntry(c, s.IssueURLs))
	}
	var out []entry
	for _, g := range by {
		out = append(out, g...)
	}
	return out
}

// showsGroup is false for the support group while no pass has said whether the
// round has a support machine.
func (s roundSnap) showsGroup(g int) bool {
	return g != grpSupport || s.SupportLoaded || s.SupportErr != ""
}

// awaitingReview is a slot whose worker reported done and whose PR is still
// open: the PR is ready for the maintainer, not just present.
func awaitingReview(r round.Row) bool {
	if r.PR == "" || r.PRState == "merged" || r.PRState == "closed" {
		return false
	}
	return (r.Event != nil && r.Event.Kind == worker.EventDone) || r.State == "done" || r.HostState == "done"
}

// totals are the lines above the table: the known local workers and, when the
// round has a support machine, its queue. Remote workers are never counted.
func (s roundSnap) totals() []string {
	var working, review, parked, unavailable, idle, holding int
	var parkedNames []string
	for _, r := range s.Slots {
		switch {
		case r.Issue == "":
			parked++
			parkedNames = append(parkedNames, r.Name)
		case !s.CandsLoaded:
			holding++ // the host has not been read: no state to count yet
		case r.HostState == "dead" || r.HostState == "unverifiable" || r.NoLogin != "":
			unavailable++
		case awaitingReview(r):
			review++
		case r.HostState == "working":
			working++
		default:
			idle++
		}
	}
	parkedText := fmt.Sprintf("%d parked", parked)
	if parked > 0 {
		parkedText += " (" + strings.Join(parkedNames, ", ") + ")"
	}
	var parts []string
	if !s.CandsLoaded {
		parts = []string{fmt.Sprintf("%d holding issues (state loading)", holding), parkedText}
	} else {
		parts = []string{fmt.Sprintf("%d working", working), fmt.Sprintf("%d in review", review)}
		if idle > 0 {
			parts = append(parts, fmt.Sprintf("%d idle", idle))
		}
		parts = append(parts, parkedText, fmt.Sprintf("%d unavailable", unavailable))
	}
	out := []string{fmt.Sprintf("workers %d known: %s", len(s.Slots), strings.Join(parts, " · "))}
	switch {
	case s.SupportErr != "":
		out = append(out, "support: unavailable")
	case s.SupportLoaded:
		var waiting, active, unknown int
		for _, it := range s.Support {
			switch {
			case it.Unknown:
				unknown++
			case it.Active():
				active++
			default:
				waiting++
			}
		}
		out = append(out, fmt.Sprintf("support: %d waiting · %d active · %d unknown (remote workers not reported)", waiting, active, unknown))
	}
	return out
}

func issueCell(id string, bundle []string) string {
	s := tracker.RefOf(id)
	if n := len(bundle); n > 0 {
		s += fmt.Sprintf(" +%d", n)
	}
	return s
}

func harnessModel(r round.Row) string {
	if r.Kind != "" && r.Model != "" {
		return r.Kind + "/" + r.Model
	}
	return strutil.FirstNonEmpty(r.Model, r.Kind, strings.TrimSpace(r.Tier), "-")
}

func slotEntry(r round.Row, urls map[string]string) entry {
	e := entry{
		key: "slot " + r.Name, group: grpActive, issue: issueCell(r.Issue, r.Bundle),
		title: strutil.FirstNonEmpty(r.PRTitle, r.Branch), worker: r.Name, status: dash(hostLabel(r)),
		model: harnessModel(r), pr: prLabel(r.PR, r.PRState),
		label: r.Name + " · " + tracker.RefOf(r.Issue), issueURL: issueLink(urls, r.Issue), prURL: prLink(r.PR),
	}
	switch {
	case awaitingReview(r):
		e.status, e.tone = "ready for review", toneOK
	case r.HostState == "working":
		e.tone = toneOK
	case r.HostState == "dead" || r.HostState == "unverifiable" || r.NoLogin != "":
		e.tone = toneBad
	default:
		e.tone = toneWarn
	}
	var b []string
	b = append(b, "PR: "+strutil.FirstNonEmpty(r.PRTitle, "no title known"))
	b = append(b, "branch: "+dash(r.Branch))
	b = append(b, linkLines(e)...)
	if r.PR != "" && e.prURL == "" {
		b = append(b, "PR ref: "+r.PR)
	}
	b = append(b, fmt.Sprintf("bounces: %s · burn: %s", bounceLabel(r.Bounces), burnLabel(r)))
	b = append(b, "evidence: "+strutil.FirstNonEmpty(r.Evidence, "none recorded"))
	if r.TierReason != "" {
		b = append(b, "tier: "+r.TierReason)
	}
	if len(r.Drift) > 0 {
		b = append(b, "drift: "+strings.Join(r.Drift, ", "))
	}
	if r.BestOf != "" {
		b = append(b, "best-of with "+r.BestOf)
	}
	return clean(e, b)
}

func reviewEntry(q round.QueuedPR, urls map[string]string) entry {
	e := entry{
		key: "review " + q.Issue + " " + q.PR, group: grpReview, issue: issueCell(q.Issue, q.Bundle),
		title: strutil.FirstNonEmpty(q.Branch, "-"), worker: "moved on", status: "ready for review", tone: toneOK,
		model: "-", pr: prLabel(q.PR, q.State), label: "review · " + tracker.RefOf(q.Issue),
		issueURL: issueLink(urls, q.Issue), prURL: prLink(q.PR),
	}
	return clean(e, append([]string{"PR ref: " + dash(q.PR), "branch: " + dash(q.Branch), "worker: moved on (was " + dash(q.From) + ")"}, linkLines(e)...))
}

func supportEntry(it round.SupportItem, urls map[string]string) entry {
	e := entry{
		key: "support " + it.ID, group: grpSupport, issue: tracker.RefOf(it.ID), title: it.Title,
		worker: "-", status: "waiting", tone: toneWarn, model: "unknown", pr: "-", label: "support · " + tracker.RefOf(it.ID), issueURL: issueLink(urls, it.ID),
	}
	b := append([]string{it.Title}, linkLines(e)...)
	switch {
	case it.Unknown:
		e.status, e.tone = "unknown", toneBad
		b = append(b, "claim could not be read: not shown as waiting")
	case it.Active():
		e.status, e.tone, e.worker = "active", toneOK, it.Claim
		b = append(b, "claimed by "+it.Claim)
	default:
		b = append(b, "offered to the support machine, no claim yet")
	}
	b = append(b, "remote worker and model: not reported")
	return clean(e, b)
}

func candEntry(c roundCand, urls map[string]string) entry {
	e := entry{
		key: "cand " + c.ID, group: grpUpcoming, issue: tracker.RefOf(c.ID), title: c.Title,
		worker: "-", status: "ready", tone: toneOK, model: "-", pr: "-", label: "upcoming · " + tracker.RefOf(c.ID), issueURL: issueLink(urls, c.ID),
	}
	b := append([]string{c.Title}, linkLines(e)...)
	if !c.Ready {
		e.status, e.tone = "blocked", toneWarn
		b = append(b, "blocked:", "- "+strings.Join(c.Why, "\n- "))
	}
	return clean(e, b)
}

// issueLink is the tracker page of issue id, "" when unknown or not a plain
// http(s) URL.
func issueLink(urls map[string]string, id string) string {
	u := urls[strings.TrimPrefix(strings.TrimSpace(id), "#")]
	if tui.SafeURL(u) {
		return u
	}
	return ""
}

// prLink is a PR reference as a URL; a bare number or other spelling is "".
func prLink(pr string) string {
	if tui.SafeURL(pr) {
		return pr
	}
	return ""
}

// linkLines are the detail lines that spell the row's URLs out, for a
// terminal that renders no hyperlinks.
func linkLines(e entry) []string {
	var out []string
	if e.issueURL != "" {
		out = append(out, "issue: "+e.issueURL)
	}
	if e.prURL != "" {
		out = append(out, "PR: "+e.prURL)
	}
	return out
}

// clean sanitizes every field a screen draws: they come from the tracker and
// the registry, which may carry control sequences.
func clean(e entry, body []string) entry {
	for _, f := range []*string{&e.issue, &e.title, &e.worker, &e.status, &e.model, &e.pr, &e.label} {
		*f = tui.Sanitize(*f, false)
	}
	e.body = tui.Sanitize(strings.Join(body, "\n"), true)
	return e
}

func prLabel(pr, state string) string {
	if pr == "" {
		return "-"
	}
	s := pr
	if n, ok := worker.PRRefNumber(pr); ok {
		s = fmt.Sprintf("#%d", n)
	}
	if state != "" {
		s += " " + state
	}
	return s
}

// hostLabel is the host column: an adopted slot shows its derived state after
// "external", since no host reports one.
func hostLabel(r round.Row) string {
	if r.State != "" {
		return r.HostState + "/" + r.State
	}
	return r.HostState
}

func bounceLabel(n int) string {
	if n == 0 {
		return "-"
	}
	return fmt.Sprint(n)
}

// cols lays the table out for a width: fixed columns, the title takes the rest.
type cols struct{ issue, worker, status, model, pr, title int }

func overviewCols(w int) cols {
	c := cols{issue: 8, worker: 9, status: 18, model: 16, pr: 12}
	c.title = w - 2 - (c.issue + c.worker + c.status + c.model + c.pr + 5)
	if c.title < 14 { // narrow: the model column goes, the detail pane keeps it
		c.model = 0
		c.title = w - 2 - (c.issue + c.worker + c.status + c.pr + 4)
	}
	c.title = max(c.title, 8)
	return c
}

func (c cols) join(cells [6]string, paint func(i int, s string) string) string {
	widths := [6]int{c.issue, c.title, c.worker, c.status, c.model, c.pr}
	var parts []string
	for i, cell := range cells {
		if widths[i] == 0 {
			continue
		}
		parts = append(parts, tui.Pad(paint(i, tui.Fit(cell, widths[i])), widths[i]))
	}
	return strings.TrimRight(strings.Join(parts, " "), " ")
}

func (c cols) header() string {
	return c.join([6]string{"issue", "title", "worker", "status", "harness/model", "PR"}, func(_ int, s string) string { return s })
}

// row draws one entry; the status cell takes its tone's colour.
func (c cols) row(e entry, st tui.Style) string {
	return c.join([6]string{e.issue, e.title, e.worker, e.status, e.model, e.pr}, func(i int, s string) string {
		switch i {
		case 0:
			return st.Link(e.issueURL, s)
		case 3:
			return e.tone.paint(st, s)
		case 5:
			return st.Link(e.prURL, s)
		}
		return s
	})
}
