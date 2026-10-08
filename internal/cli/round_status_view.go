package cli

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tui"
	"github.com/l4ci/rota/internal/worker"
)

// The `rota round status --ui` screen: slots, the review queue and the queued
// candidates with why each is blocked, over a detail pane. Read-only: r
// refreshes, a Tick refreshes by itself, q/Esc leaves. Assigning and bouncing
// stay with the orchestrator.

// roundRefresh is how often the screen reloads on its own. A status pass is
// about 16 forge calls (~5 s); it runs inside Exec, so a slow pass never
// overlaps the next one, and roundScreen.Update skips a Tick that lands right
// after a pass finished.
const roundRefresh = 5 * time.Second

// roundSnap is what the screen shows. Slots and Review start from the verb's
// Data; the first refresh adds the detail Data does not carry (PR titles,
// evidence) and the candidates.
type roundSnap struct {
	Slots       []round.Row
	Review      []round.QueuedPR
	Cands       []roundCand
	CandsLoaded bool
	CandsErr    string
	Host        string
	Notes       []string // unavailable sources and status warnings
}

// roundCand is a queued candidate. Why is empty for a ready one.
type roundCand struct {
	ID, Title string
	Ready     bool
	Why       []string
}

// roundLoader reads a fresh snapshot. The real one starts every pass with an
// empty forge read cache, as `round watch` does.
type roundLoader func() (roundSnap, error)

// roundStatusView is round status's ViewFunc.
func roundStatusView(c *Ctx, res Result) (tui.Model, error) {
	d, ok := res.Data.(*jsonx.Object)
	if !ok {
		return nil, &Error{Exit: ExitInternal, Message: "round status data has an unexpected shape"}
	}
	root, err := c.Root()
	if err != nil {
		return nil, err
	}
	load := func() (roundSnap, error) { return loadRoundSnap(c, root) }
	snap := snapFromData(d)
	fillBurn(c, root, snap.Slots)
	return newRoundScreen(snap, load, time.Now, roundRefresh), nil
}

// snapFromData reads the slots, review queue and notes out of round status Data.
func snapFromData(d *jsonx.Object) roundSnap {
	s := roundSnap{Host: jsonx.Str(d, "host")}
	list := func(key string) []*jsonx.Object {
		v, _ := d.Get(key)
		items, _ := v.([]any)
		var out []*jsonx.Object
		for _, it := range items {
			if o, ok := it.(*jsonx.Object); ok {
				out = append(out, o)
			}
		}
		return out
	}
	for _, o := range list("slots") {
		r := round.Row{
			Name: jsonx.Str(o, "name"), Issue: jsonx.Str(o, "issue"), Branch: jsonx.Str(o, "branch"),
			PR: jsonx.Str(o, "pr"), PRState: jsonx.Str(o, "prState"), HostState: jsonx.Str(o, "hostState"),
			State: jsonx.Str(o, "state"),
			Kind:  jsonx.Str(o, "kind"), Tier: jsonx.Str(o, "tier"), Model: jsonx.Str(o, "model"),
			TierReason: jsonx.Str(o, "tierReason"), BestOf: jsonx.Str(o, "bestOf"),
		}
		v, _ := o.Get("bounces")
		r.Bounces, _ = jsonx.Int(v)
		v, _ = o.Get("drift")
		ds, _ := v.([]any)
		for _, x := range ds {
			if str, ok := x.(string); ok {
				r.Drift = append(r.Drift, str)
			}
		}
		s.Slots = append(s.Slots, r)
	}
	for _, o := range list("review") {
		s.Review = append(s.Review, round.QueuedPR{Issue: jsonx.Str(o, "issue"), PR: jsonx.Str(o, "pr"), Branch: jsonx.Str(o, "branch"), From: jsonx.Str(o, "from")})
	}
	v, _ := d.Get("unavailable")
	us, _ := v.([]any)
	for _, x := range us {
		if str, ok := x.(string); ok {
			s.Notes = append(s.Notes, str+" unavailable")
		}
	}
	return s
}

// loadRoundSnap is one full pass: status, then the candidates. Nothing prints:
// a warning on stderr would tear the screen, so warnings become Notes.
func loadRoundSnap(c *Ctx, root string) (roundSnap, error) {
	ctx := c.Context()
	c.deps().freshReads()
	env := c.deps().RoundEnv(ctx, root)
	rep, err := env.Status(ctx, root)
	if err != nil {
		return roundSnap{}, err
	}
	s := roundSnap{Slots: rep.Rows, Review: rep.Queued, Host: rep.Host, CandsLoaded: true}
	fillBurn(c, root, s.Slots)
	for _, u := range rep.Unavailable {
		s.Notes = append(s.Notes, u+" unavailable")
	}
	s.Notes = append(s.Notes, rep.Warnings...)
	set, err := roundcfg.Load(root)
	if err != nil {
		s.CandsErr = err.Error()
		return s, nil
	}
	be, err := openBacklog(c, root, false, "")
	if err != nil {
		s.CandsErr = err.Error()
		return s, nil
	}
	scope, slate := round.SlateOf(root)
	if scope == "" {
		scope = set.Scope
	}
	cands, err := env.Candidates(ctx, root, be, round.CandidateOpts{Scope: scope, Slate: slate, Shared: set.SharedPaths, ScopeOverlap: set.ScopeOverlap})
	if err != nil {
		s.CandsErr = err.Error()
		return s, nil
	}
	for _, cd := range cands {
		s.Cands = append(s.Cands, roundCand{ID: cd.ID, Title: cd.Title, Ready: cd.Ready(), Why: blockedWhy(cd)})
	}
	return s, nil
}

// fillBurn sets Row.Burn for every slot holding an issue: the quota it has
// consumed, in percentage points of its account's headroom. That is the
// headroom the slot's latest assign entry recorded minus the headroom the
// meter reads now, floored at 0 (a window that reset in between reads as
// nothing consumed). Codex slots, accounts with no reading at either end and
// slots with no assign entry stay nil (n/a). The meters are fetched once, for
// all accounts, however many slots there are; the --ui screen is the only
// caller, so plain and --json status never fetch.
func fillBurn(c *Ctx, root string, rows []round.Row) {
	entries, _ := ledger.Load(root)
	if len(entries) == 0 {
		return
	}
	var meters []worker.Meter
	fetched := false
	for i := range rows {
		r := &rows[i]
		if r.Issue == "" {
			continue
		}
		var last *ledger.Entry
		for j := len(entries) - 1; j >= 0; j-- {
			e := &entries[j]
			if e.Kind == ledger.KindAssign && e.Slot == r.Name && strings.TrimPrefix(e.Issue, "#") == strings.TrimPrefix(r.Issue, "#") {
				last = e
				break
			}
		}
		if last == nil || last.Harness == harness.Codex || last.Account == "" {
			continue
		}
		start, ok := last.DetailFloat("headroom")
		if !ok {
			continue
		}
		if !fetched {
			meters, fetched = c.deps().WorkerAccounts().Meters(c.Context(), root), true
		}
		if now := worker.HeadroomOf(meters, last.Account); now != nil {
			burn := max(start-*now, 0)
			r.Burn = &burn
		}
	}
}

func burnLabel(r round.Row) string {
	switch {
	case r.Burn != nil:
		return fmt.Sprintf("%.0f%%", *r.Burn)
	case r.Issue != "":
		return "n/a"
	}
	return "-"
}

// blockedWhy says why a candidate cannot be assigned yet: the open PR, the
// file overlap with an in-flight item, each failed check (a dependency names
// what it waits on) and a refused pick. Empty for a ready one.
func blockedWhy(c round.Candidate) []string {
	if c.Ready() {
		return nil
	}
	var why []string
	if c.OpenPR != 0 {
		why = append(why, fmt.Sprintf("waiting on open PR #%d", c.OpenPR))
	}
	for _, o := range c.Overlaps {
		why = append(why, overlapText(o))
	}
	for _, ch := range c.Checks {
		if ch.OK {
			continue
		}
		w := ch.Name
		if len(ch.Detail) > 0 {
			w += ": " + strings.Join(ch.Detail, "; ")
		}
		why = append(why, w)
	}
	if c.PickErr != "" {
		why = append(why, c.PickErr)
	}
	return why
}

// loadBox carries a refresh's result from Exec back to Update, which is pure.
type loadBox struct {
	mu   sync.Mutex
	snap roundSnap
	err  error
}

func (b *loadBox) put(s roundSnap, err error) {
	b.mu.Lock()
	b.snap, b.err = s, err
	b.mu.Unlock()
}

func (b *loadBox) take() (roundSnap, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.snap, b.err
}

type roundScreen struct {
	snap    roundSnap
	sel     string // key of the selected entry, so a refresh keeps the cursor
	load    roundLoader
	box     *loadBox
	now     func() time.Time
	every   time.Duration
	updated time.Time
	err     string
	top     int // detail pane scroll
	refresh int // refreshes finished, for the header
	loading bool
}

func newRoundScreen(s roundSnap, load roundLoader, now func() time.Time, every time.Duration) *roundScreen {
	m := &roundScreen{snap: s, load: load, box: &loadBox{}, now: now, every: every, updated: now()}
	if es := m.entries(); len(es) > 0 {
		m.sel = es[0].key
	}
	return m
}

// TickEvery asks runView for a Tick that often.
func (m *roundScreen) TickEvery() time.Duration { return m.every }

// entry is one selectable row.
type entry struct {
	key     string
	section int // 0 slots, 1 review, 2 candidates
	line    string
	title   string
	body    string
}

const (
	secSlots = iota
	secReview
	secCands
)

func (m *roundScreen) entries() []entry {
	var out []entry
	for _, r := range m.snap.Slots {
		out = append(out, slotEntry(r))
	}
	for _, q := range m.snap.Review {
		out = append(out, reviewEntry(q))
	}
	for _, c := range m.snap.Cands {
		out = append(out, candEntry(c))
	}
	return out
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

func tierModel(r round.Row) string {
	s := strings.Trim(strings.Join([]string{r.Tier, r.Model}, " "), " ")
	if s == "" {
		s = strings.TrimSuffix(r.Kind+"/"+r.Model, "/")
	}
	return dash(s)
}

func slotEntry(r round.Row) entry {
	issue := dash(r.Issue)
	if r.Issue != "" {
		issue = "#" + strings.TrimPrefix(r.Issue, "#")
	}
	line := fmt.Sprintf("%-8s %-7s %-10s %-14s %-18s %-7s %s", r.Name, issue, dash(hostLabel(r)), prLabel(r.PR, r.PRState), tierModel(r), bounceLabel(r.Bounces), burnLabel(r))
	var b []string
	b = append(b, "PR: "+firstOf(r.PRTitle, "no title known"))
	b = append(b, "branch: "+dash(r.Branch))
	if r.PR != "" {
		b = append(b, "url: "+r.PR)
	}
	b = append(b, "evidence: "+firstOf(r.Evidence, "none recorded"))
	if r.TierReason != "" {
		b = append(b, "tier: "+r.TierReason)
	}
	if len(r.Drift) > 0 {
		b = append(b, "drift: "+strings.Join(r.Drift, ", "))
	}
	if r.BestOf != "" {
		b = append(b, "best-of with "+r.BestOf)
	}
	return entry{key: "slot " + r.Name, section: secSlots, line: tui.Sanitize(line, false), title: tui.Sanitize(r.Name+" · "+issue, false), body: tui.Sanitize(strings.Join(b, "\n"), true)}
}

func bounceLabel(n int) string {
	if n == 0 {
		return "-"
	}
	return fmt.Sprint(n)
}

func reviewEntry(q round.QueuedPR) entry {
	line := fmt.Sprintf("#%-7s %-14s %-30s %s", q.Issue, prLabel(q.PR, ""), dash(q.Branch), dash(q.From))
	body := "PR: " + dash(q.PR) + "\nbranch: " + dash(q.Branch) + "\nfrom: " + dash(q.From)
	return entry{key: "review " + q.Issue + " " + q.PR, section: secReview, line: tui.Sanitize(line, false), title: tui.Sanitize("review · #"+q.Issue, false), body: tui.Sanitize(body, true)}
}

func candEntry(c roundCand) entry {
	state := "ready"
	if !c.Ready {
		state = "blocked: " + strings.Join(c.Why, "; ")
	}
	line := fmt.Sprintf("#%-7s %s — %s", strings.TrimPrefix(c.ID, "#"), c.Title, state)
	body := c.Title + "\n" + state
	if !c.Ready {
		body = c.Title + "\nblocked:\n- " + strings.Join(c.Why, "\n- ")
	}
	return entry{key: "cand " + c.ID, section: secCands, line: tui.Sanitize(line, false), title: tui.Sanitize("candidate · #"+strings.TrimPrefix(c.ID, "#"), false), body: tui.Sanitize(body, true)}
}

func (m *roundScreen) Update(msg tui.Msg) (tui.Model, tui.Cmd) {
	n := *m
	switch t := msg.(type) {
	case tui.Tick:
		if n.loading || n.now().Sub(n.updated) < n.every/2 {
			return &n, tui.Cmd{} // a pass just finished: do not chase it
		}
		return n.startRefresh()
	case tui.Done:
		n.loading = false
		snap, err := n.box.take()
		n.updated = n.now()
		if err != nil {
			n.err = err.Error()
			return &n, tui.Cmd{}
		}
		n.err = ""
		n.snap = snap
		n.refresh++
		n.keepSelection()
		return &n, tui.Cmd{}
	case tui.Key:
		switch {
		case t.Kind == tui.KeyEsc || t.Is('q') || t.Kind == tui.KeyCtrlC:
			return &n, tui.Cmd{Quit: true}
		case t.Is('r'):
			return n.startRefresh()
		case t.Kind == tui.KeyUp || t.Is('k'):
			n.move(-1)
		case t.Kind == tui.KeyDown || t.Is('j'):
			n.move(1)
		case t.Kind == tui.KeyPgUp:
			n.top = max(n.top-5, 0)
		case t.Kind == tui.KeyPgDn:
			n.top += 5
		}
	}
	return &n, tui.Cmd{}
}

func (m *roundScreen) startRefresh() (tui.Model, tui.Cmd) {
	n := *m
	n.loading = true
	box, load := n.box, n.load
	return &n, tui.Cmd{Exec: func() error {
		s, err := load()
		box.put(s, err)
		return nil
	}}
}

func (m *roundScreen) index(es []entry) int {
	for i, e := range es {
		if e.key == m.sel {
			return i
		}
	}
	return 0
}

func (m *roundScreen) move(d int) {
	es := m.entries()
	if len(es) == 0 {
		return
	}
	i := min(max(m.index(es)+d, 0), len(es)-1)
	if es[i].key != m.sel {
		m.top = 0
	}
	m.sel = es[i].key
}

// keepSelection holds the cursor on the same entry across a refresh; when it
// is gone the cursor stays at the same position.
func (m *roundScreen) keepSelection() {
	es := m.entries()
	if len(es) == 0 {
		m.sel = ""
		return
	}
	for _, e := range es {
		if e.key == m.sel {
			return
		}
	}
	m.sel = es[0].key
	m.top = 0
}

var roundSections = [...]struct{ title, cols string }{
	secSlots:  {"SLOTS", fmt.Sprintf("%-8s %-7s %-10s %-14s %-18s %-7s %s", "name", "issue", "state", "PR", "tier model", "bounces", "burn")},
	secReview: {"REVIEW QUEUE", fmt.Sprintf("%-8s %-14s %-30s %s", "issue", "PR", "branch", "from")},
	secCands:  {"CANDIDATES", ""},
}

const detailRows = 8

func (m *roundScreen) Render(w, h int, st tui.Style) string {
	if w <= 0 {
		w = 100
	}
	es := m.entries()
	sel := m.index(es)
	status := "updated " + m.updated.Format("15:04:05")
	if m.loading {
		status = "refreshing…"
	}
	head := st.Bold("Round status")
	if m.snap.Host != "" {
		head += st.Dim(" · " + tui.Sanitize(m.snap.Host, false))
	}
	head += st.Dim(" · " + status)

	// Body lines, each remembering the entry it shows (-1 for a heading).
	type bl struct {
		text string
		ent  int
	}
	var lines []bl
	add := func(t string, e int) { lines = append(lines, bl{t, e}) }
	next := 0
	for sec := range roundSections {
		s := roundSections[sec]
		var inSec []entry
		for _, e := range es {
			if e.section == sec {
				inSec = append(inSec, e)
			}
		}
		add(st.Bold(s.title)+st.Dim(fmt.Sprintf(" (%d)", len(inSec))), -1)
		if s.cols != "" && len(inSec) > 0 {
			add(st.Dim("  "+tui.Fit(s.cols, w-2)), -1)
		}
		for _, e := range inSec {
			mark, text := "  ", tui.Fit(e.line, w-2)
			if next == sel {
				mark, text = "▸ ", st.Bold(text)
			}
			add(mark+text, next)
			next++
		}
		switch {
		case len(inSec) == 0 && sec == secCands && !m.snap.CandsLoaded:
			add(st.Dim("  loading…"), -1)
		case len(inSec) == 0 && sec == secCands && m.snap.CandsErr != "":
			add(st.Yellow("  "+tui.Fit(tui.Sanitize("candidates: "+m.snap.CandsErr, false), w-2)), -1)
		case len(inSec) == 0:
			add(st.Dim("  none"), -1)
		}
	}
	for _, n := range m.snap.Notes {
		add(st.Yellow(tui.Fit("! "+tui.Sanitize(n, false), w)), -1)
	}
	if m.err != "" {
		add(st.Red(tui.Fit("! refresh failed: "+tui.Sanitize(m.err, false), w)), -1)
	}

	room := len(lines)
	if h > 0 {
		room = max(h-detailRows-3, 3) // header, rule, bar
	}
	start := 0
	if len(lines) > room {
		at := 0
		for i, l := range lines {
			if l.ent == sel && len(es) > 0 {
				at = i
			}
		}
		start = min(max(at-room/2, 0), len(lines)-room)
	}
	var body []string
	body = append(body, head)
	for i := start; i < len(lines) && i < start+room; i++ {
		body = append(body, lines[i].text)
	}
	for h > 0 && len(body) < room+1 {
		body = append(body, "")
	}
	body = append(body, st.Dim(strings.Repeat("─", w)))
	var dt tui.Detail
	if len(es) > 0 {
		e := es[sel]
		dt = tui.Detail{Title: e.title, Body: e.body, Top: m.top}
	} else {
		dt = tui.Detail{Body: "nothing in the round yet"}
	}
	body = append(body, dt.Render(w, detailRows, st))
	bar := tui.Hints([]tui.Hint{{Key: "↑/↓", Desc: "select"}, {Key: "PgUp/PgDn", Desc: "scroll"}, {Key: "r", Desc: "refresh"}, {Key: "q", Desc: "back"}}, w, st)
	return tui.Frame(strings.Join(body, "\n"), bar, h)
}
