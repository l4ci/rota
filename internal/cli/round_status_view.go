package cli

import (
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/rotastate"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/landfreeze"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/round"
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
	// Support is the support machine's queue; SupportLoaded is true once a pass
	// read it, and SupportErr is why it could not be (never an empty queue).
	Support       []round.SupportItem
	SupportLoaded bool
	SupportErr    string
	// IssueURLs maps an issue id (without "#") to its tracker page, as the
	// last pass read them; empty on the registry-only first frame.
	IssueURLs map[string]string
}

// roundCand is a queued candidate. Why is empty for a ready one.
type roundCand struct {
	ID, Title string
	Ready     bool
	Why       []string
}

// roundLoader reads a fresh snapshot. The real one starts every pass with an
// empty forge read cache, as `round watch` does.
type roundLoader func(ctx context.Context) (roundSnap, error)

// roundStatusView is round status's ViewFunc (UIFirst, so the verb has not
// run). It opens on the registry alone and starts the first full pass in the
// background: host, login, forge and quota reads come after the first frame.
func roundStatusView(c *Ctx, _ Result) (tui.Model, error) {
	root, err := c.Root()
	if err != nil {
		return nil, err
	}
	load := func(ctx context.Context) (roundSnap, error) { return loadRoundSnap(ctx, c, root) }
	rep, err := round.LocalReport(root)
	if err != nil {
		return nil, err
	}
	initial := snapFromReport(rep)
	addFreezeNote(c.Context(), c, root, &initial)
	m := newRoundScreen(initial, load, c.deps().Now, roundRefresh)
	m.open = c.deps().OpenURL
	m.loading = true // Init starts the first pass
	return m, nil
}

// snapFromReport is the snapshot of a status pass, without candidates. Every
// frame starts here, the first included.
func snapFromReport(rep *round.Report) roundSnap {
	s := roundSnap{Slots: rep.Rows, Review: rep.Queued, Host: rep.Host.String()}
	for _, u := range rep.Unavailable {
		s.Notes = append(s.Notes, u+" unavailable")
	}
	s.Notes = append(s.Notes, rep.Warnings...)
	for _, finding := range rep.Findings {
		if finding.Kind == round.StaleClaimKind {
			s.Notes = append(s.Notes, round.StaleClaimKind+": "+finding.Detail)
		}
	}
	return s
}

// loadRoundSnap is one full pass: status, then the candidates. Nothing prints:
// a warning on stderr would tear the screen, so warnings become Notes.
func loadRoundSnap(ctx context.Context, c *Ctx, root string) (roundSnap, error) {
	// Every read below goes through c.Context(). The pass's own ctx outlives
	// the command that built c (a palette entry's run has returned by now) and
	// ends with the screen.
	cc := *c
	cc.ctx = ctx
	c = &cc
	c.deps().freshReads()
	env := withBoard(c, root, c.deps().RoundEnv(ctx, root))
	rep, err := env.Status(ctx, root)
	if err != nil {
		return roundSnap{}, err
	}
	s := snapFromReport(rep)
	addFreezeNote(ctx, c, root, &s)
	s.CandsLoaded = true
	fillBurn(ctx, c, root, s.Slots)
	set, err := c.deps().roundSettings(root, roundConfigRequired)
	if err != nil {
		s.CandsErr = err.Error()
		return s, nil
	}
	be, err := openBacklog(c, root, false, "")
	if err != nil {
		s.CandsErr = err.Error()
		return s, nil
	}
	if items, err := be.List(false); err != nil { // answered from the pass's read cache
		s.Notes = append(s.Notes, "issue links unavailable: "+err.Error())
	} else {
		s.IssueURLs = map[string]string{}
		for _, it := range items {
			if it.URL != "" {
				s.IssueURLs[strings.TrimPrefix(it.ID, "#")] = it.URL
			}
		}
	}
	cands, err := env.RoundCandidates(ctx, root, be, set, "")
	if err != nil {
		s.CandsErr = err.Error()
	}
	for _, cd := range cands {
		s.Cands = append(s.Cands, roundCand{ID: cd.ID, Title: cd.Title, Ready: cd.Ready(), Why: cd.NotReady()})
	}
	// The support queue is read on its own: a failure there says so and
	// never reads as an empty queue.
	if set.Support != "" {
		if items, err := env.SupportQueue(ctx, be, set.Support); err != nil {
			s.SupportErr = err.Error()
		} else {
			s.Support, s.SupportLoaded = items, true
		}
	}
	return s, nil
}

// keepStale carries the last good candidates and support queue over a pass that
// could not read them: old rows beat a queue that looks empty. The error stays
// set, so the screen still says the data is old.
func (s *roundSnap) keepStale(old roundSnap) {
	if s.CandsErr != "" && old.CandsLoaded && old.CandsErr == "" {
		s.Cands = old.Cands
	}
	if s.SupportErr != "" && old.SupportLoaded {
		s.Support, s.SupportLoaded = old.Support, true
	}
}

// fillBurn sets Row.Burn for every slot holding an issue: the quota it has
// consumed, in percentage points of its account's headroom. That is the
// headroom the slot's latest assign entry recorded minus the headroom the
// meter reads now. A quota window reset (headroom rose), codex slots, accounts
// with no reading at either end and slots with no assign entry stay nil (n/a).
// The meters are fetched once for all accounts, however many slots there are;
// the --ui screen is the only
// caller, so plain and --json status never fetch.
func fillBurn(ctx context.Context, c *Ctx, root string, rows []round.Row) {
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
		last := ledger.LatestAssign(entries, ledger.AssignQuery{Issue: r.Issue, Slot: r.Name})
		if last == nil || last.Account == "" {
			continue
		}
		if !harness.Resolve(last.Harness).UsageMeter() {
			continue
		}
		start, ok := last.DetailFloat("headroom")
		if !ok {
			continue
		}
		if !fetched {
			meters, fetched = c.deps().WorkerAccounts().Meters(ctx, root), true
		}
		r.Burn = ledger.QuotaBurn(&start, worker.HeadroomOf(meters, last.Account))
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

// loadBox carries a refresh's result from Exec back to Update, which is pure.
type loadBox struct {
	mu     sync.Mutex
	snap   roundSnap
	err    error
	cancel context.CancelFunc // of the pass in flight
	// stopped is set by stop and never cleared: a pass whose goroutine has not
	// registered its cancel yet sees it there and does not start.
	stopped bool
}

func (b *loadBox) cancelSet() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cancel != nil
}

// stop cancels the pass in flight, when there is one: the screen is closing.
// It also bars passes that have not registered yet.
func (b *loadBox) stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopped = true
	if b.cancel != nil {
		b.cancel()
	}
}

// register records the cancel of a pass about to run, and is false when the
// screen already closed: the pass must not start.
func (b *loadBox) register(cancel context.CancelFunc) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopped {
		return false
	}
	b.cancel = cancel
	return true
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

// screens numbers the screens opened in this process, so a Done can be told
// from a previous screen's.
var screens atomic.Uint64

type roundScreen struct {
	tag     string // marks this screen's passes; a Done with another tag is not ours
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
	open    func(url string) error // opens a link in the browser; nil without one
	note    string                 // outcome of the last open-link key
}

func newRoundScreen(s roundSnap, load roundLoader, now func() time.Time, every time.Duration) *roundScreen {
	m := &roundScreen{tag: fmt.Sprintf("load-%d", screens.Add(1)), snap: s, load: load, box: &loadBox{}, now: now, every: every, updated: now()}
	if es := m.entries(); len(es) > 0 {
		m.sel = es[0].key
	}
	return m
}

// TickEvery asks runView for a Tick that often.
func (m *roundScreen) TickEvery() time.Duration { return m.every }

// Init starts the first full pass when the screen opened on registry data alone.
func (m *roundScreen) Init() tui.Cmd {
	if !m.loading {
		return tui.Cmd{}
	}
	return m.pass()
}

func (m *roundScreen) entries() []entry { return m.snap.entries() }

func (m *roundScreen) Update(msg tui.Msg) (tui.Model, tui.Cmd) {
	n := *m
	switch t := msg.(type) {
	case tui.Tick:
		if n.loading || n.now().Sub(n.updated) < n.every/2 {
			return &n, tui.Cmd{} // a pass is running or just finished: do not chase it
		}
		return n.startRefresh()
	case tui.Done:
		if t.Tag != n.tag && t.Tag != tagOpen {
			return &n, tui.Cmd{} // a pass of a screen that was closed
		}
		if t.Tag == tagOpen {
			if t.Err != nil {
				n.note = tui.Sanitize(t.Err.Error(), false)
			}
			return &n, tui.Cmd{}
		}
		n.loading = false
		snap, err := n.box.take()
		if err != nil {
			// The last good snapshot and its time stay: only a pass that
			// worked makes the data fresh.
			n.err = err.Error()
			return &n, tui.Cmd{}
		}
		n.updated = n.now()
		n.err = ""
		snap.keepStale(n.snap)
		n.snap = snap
		n.refresh++
		n.keepSelection()
		return &n, tui.Cmd{}
	case tui.Key:
		n.note = ""
		switch {
		case t.Is('i'):
			return n.openLink("issue")
		case t.Is('p'):
			return n.openLink("PR")
		case t.Kind == tui.KeyEsc || t.Is('q') || t.Kind == tui.KeyCtrlC:
			n.box.stop()
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

// tagOpen marks the Done of an open-link Cmd, so it is not taken for a pass.
const tagOpen = "open"

// openLink opens the selected row's issue or PR page. The URL is taken from the
// row at the key press, so a refresh that reorders rows cannot redirect it.
func (m *roundScreen) openLink(kind string) (tui.Model, tui.Cmd) {
	n := *m
	es := n.entries()
	if len(es) == 0 {
		return &n, tui.Cmd{}
	}
	e := es[n.index(es)]
	url := e.issueURL
	if kind == "PR" {
		url = e.prURL
	}
	switch {
	case url == "":
		n.note = "no " + kind + " link for this row"
	case n.open == nil:
		n.note = "no browser available: " + url
	default:
		open := n.open
		n.note = "opening " + url
		return &n, tui.Cmd{Tag: tagOpen, Exec: func() error {
			if err := open(url); err != nil {
				return fmt.Errorf("could not open %s: %w", url, err)
			}
			return nil
		}}
	}
	return &n, tui.Cmd{}
}

// startRefresh begins a pass in the background unless one is running: a second
// request while loading is dropped, so passes never overlap.
func (m *roundScreen) startRefresh() (tui.Model, tui.Cmd) {
	n := *m
	if n.loading {
		return &n, tui.Cmd{}
	}
	n.loading = true
	return &n, n.pass()
}

// pass is the background read; keys keep working while it runs, and leaving the
// screen cancels its context.
func (m *roundScreen) pass() tui.Cmd {
	box, load := m.box, m.load
	return tui.Cmd{Tag: m.tag, Async: func(ctx context.Context) error {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		if !box.register(cancel) {
			return nil // the screen closed before this pass began
		}
		s, err := load(ctx)
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

const detailRows = 8

func (m *roundScreen) Render(w, h int, st tui.Style) string {
	if w <= 0 {
		w = 100
	}
	es := m.entries()
	sel := m.index(es)
	status := "updated " + m.updated.Format("15:04:05")
	switch {
	case m.loading && !m.snap.CandsLoaded:
		status = "loading…"
	case m.loading:
		status += " · refreshing…"
	case m.err != "" && !m.snap.CandsLoaded:
		status = "not loaded"
	}
	head := st.Bold("Round status")
	if m.snap.Host != "" {
		head += st.Dim(" · " + tui.Sanitize(m.snap.Host, false))
	}
	head += st.Dim(" · " + status)
	if m.note != "" {
		head += " · " + st.Yellow(tui.Fit(tui.Sanitize(m.note, false), max(w-len(status)-30, 20)))
	}

	// Body lines, each remembering the entry it shows (-1 for a heading).
	type bl struct {
		text string
		ent  int
	}
	var lines []bl
	add := func(t string, e int) { lines = append(lines, bl{t, e}) }
	for _, t := range m.snap.totals() {
		add(st.Dim(tui.Fit(t, w)), -1)
	}
	cols := overviewCols(w)
	next := 0
	for g := range groupTitles {
		if !m.snap.showsGroup(g) {
			continue
		}
		var inGroup []entry
		for _, e := range es {
			if e.group == g {
				inGroup = append(inGroup, e)
			}
		}
		count := fmt.Sprint(len(inGroup))
		if g == grpSupport && m.snap.SupportErr != "" && !m.snap.SupportLoaded {
			count = "?"
		}
		add(st.Bold(groupTitles[g])+st.Dim(" ("+count+")"), -1)
		if len(inGroup) > 0 {
			add(st.Dim("  "+cols.header()), -1)
		}
		for _, e := range inGroup {
			mark, text := "  ", cols.row(e, st)
			if next == sel {
				mark, text = st.Cyan("› "), st.Bold(cols.row(e, tui.Style{Links: st.Links}))
			}
			add(mark+text, next)
			next++
		}
		switch {
		case g == grpSupport && m.snap.SupportErr != "":
			why := "support queue unavailable: " + m.snap.SupportErr
			if m.snap.SupportLoaded {
				why += " (rows are from the last read)"
			}
			add(st.Yellow("  "+tui.Fit(tui.Sanitize(why, false), w-2)), -1)
		case g == grpUpcoming && len(inGroup) == 0 && !m.snap.CandsLoaded:
			add(st.Dim("  loading…"), -1)
		case g == grpUpcoming && m.snap.CandsErr != "":
			add(st.Yellow("  "+tui.Fit(tui.Sanitize("candidates unavailable: "+m.snap.CandsErr, false), w-2)), -1)
		case len(inGroup) == 0:
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
		dt = tui.Detail{Title: e.label, Body: e.body, Top: m.top}
	} else {
		dt = tui.Detail{Body: "nothing in the round yet"}
	}
	body = append(body, dt.Render(w, detailRows, st))
	bar := tui.Hints([]tui.Hint{{Key: "↑/↓", Desc: "select"}, {Key: "PgUp/PgDn", Desc: "scroll"}, {Key: "i", Desc: "issue link"}, {Key: "p", Desc: "PR link"}, {Key: "r", Desc: "refresh"}, {Key: "q", Desc: "back"}}, w, st)
	return tui.Frame(strings.Join(body, "\n"), bar, h)
}

func addFreezeNote(ctx context.Context, c *Ctx, root string, s *roundSnap) {
	frozen, err := landfreeze.Load(ctx, c.deps().Git, root)
	if errors.Is(err, rotastate.ErrNotRepo) {
		return
	}
	if err != nil {
		s.Notes = append(s.Notes, "landing freeze unreadable: "+err.Error())
		return
	}
	if frozen != nil && frozen.Active {
		s.Notes = append(s.Notes, frozen.Line())
	}
}
