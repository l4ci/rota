package limits

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/worker"
)

const (
	// reopenWindow is how long after a resume a new limit message on the same
	// session counts as the same limit still going (the next cycle), not a
	// new one.
	reopenWindow = 10 * time.Minute
	// failedQuiet is how long a session with a failed entry is left alone, so
	// a pane that stays limited does not raise a new entry every pass.
	failedQuiet = 6 * time.Hour
	// tailLines is how much of a pane's end counts: a limit message that has
	// scrolled far up is history, not the pane's state.
	tailLines = 25
)

// Target is one session the watcher can act on: the orchestrator's pane or a
// slot's.
type Target struct {
	// Session is Orchestrator or the slot name.
	Session      string
	Pane         string
	Orchestrator bool
	// Account is the slot's account, "" when none is configured. For a Codex
	// slot it is the work.codexAccounts entry.
	Account string
	// Kind is the slot's harness kind, "" for Claude.
	Kind string
	// Issue is the issue the slot holds, "" for an idle slot (nothing to
	// lose, so its limit is not handled).
	Issue string
}

// Reading is what the account meter says about one account.
type Reading struct {
	// Known: the meter had data. Without it nothing is claimed.
	Known bool
	// Cooling: a spent window with a reset ahead; ResetsAt is the later one.
	Cooling  bool
	ResetsAt time.Time
	Window   string
}

// Match is pane text that matched a limit phrase. Text is the pane text the
// host read, Line the matched line.
type Match struct {
	Session, Line, Text string
}

// Deps is everything the loop touches outside its own memory.
type Deps struct {
	Root     string
	Settings Settings
	Now      func() time.Time
	// Loc is the zone a bare reset time in limit text is read in.
	Loc *time.Location
	// Targets lists the sessions now: the orchestrator's pane and every
	// registered slot that has a session.
	Targets func(ctx context.Context) []Target
	// Capture returns the target's recent pane text.
	Capture func(ctx context.Context, t Target) string
	// OrchestratorData reads the orchestrator's session file.
	OrchestratorData func(now time.Time) Data
	// Meter reads one account's meter (A7). Called only after a text match.
	Meter func(ctx context.Context, account string) Reading
	// PickAccount is `worker account pick --exclude <account>`: the account
	// with the most headroom that is not cooling.
	// For a Codex slot (kind "codex") it picks among work.codexAccounts.
	PickAccount func(ctx context.Context, kind, exclude string) (string, bool)
	// IdleSlot finds an idle slot of the harness kind on the account.
	IdleSlot func(ctx context.Context, kind, account string) (string, bool)
	// Transfer is `rota round transfer <issue> --to <slot>`.
	Transfer func(ctx context.Context, issue, to string) error
	// Send types the prompt into the target's pane and submits it.
	Send func(ctx context.Context, t Target, prompt string) error
	// Escalate posts on the issue thread; returns the escalation id.
	Escalate func(issue int, title, body string) (id string, warnings []string, err error)
	// Notify raises the host notification alone.
	Notify func(title, body string)
	// Poll: capture every target on every pass. Without it text arrives as
	// Matches (herdr), and the first pass alone sweeps the panes.
	Poll bool
	// Tick is the longest wait between passes.
	Tick time.Duration
	// After returns a channel that fires after d; time.After by default.
	After func(d time.Duration) <-chan time.Time
}

// Watcher is the loop. Its methods run on one goroutine.
type Watcher struct {
	Deps
	poll      atomic.Bool
	warnings  []string
	failed    int
	dismissed map[string]string // session -> the message the meter or data said was no limit
}

// New builds a watcher.
func New(d Deps) *Watcher {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Loc == nil {
		d.Loc = time.Local
	}
	if d.After == nil {
		d.After = time.After
	}
	if d.Tick <= 0 {
		d.Tick = 5 * time.Second
	}
	w := &Watcher{Deps: d, dismissed: map[string]string{}}
	w.poll.Store(d.Poll)
	return w
}

// SetPoll switches the capture of every pane on every pass on or off, for a
// feed that died.
func (w *Watcher) SetPoll(on bool) { w.poll.Store(on) }

// Warnings are what went wrong without stopping the loop.
func (w *Watcher) Warnings() []string { return w.warnings }

// Failed is how many entries this watcher marked failed.
func (w *Watcher) Failed() int { return w.failed }

func (w *Watcher) warn(format string, a ...any) {
	w.warnings = append(w.warnings, fmt.Sprintf(format, a...))
}

// Run steps until ctx ends. Text matches arrive on feed (nil for none). A
// signal ends it without touching any entry: a waiting entry stays waiting
// for the next watcher.
func (w *Watcher) Run(ctx context.Context, feed <-chan Match) {
	sweep := true
	var matches []Match
	for {
		w.Step(ctx, matches, sweep)
		matches, sweep = nil, w.poll.Load()
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case m := <-feed:
			matches = append(matches, m)
			for more := true; more; {
				select {
				case m := <-feed:
					matches = append(matches, m)
				default:
					more = false
				}
			}
		case <-w.After(w.wait()):
		}
	}
}

// wait is the time to the next pass: a tick, or sooner when an entry is due.
func (w *Watcher) wait() time.Duration {
	d := w.Tick
	now := w.Now()
	for _, e := range Waiting(Load(w.Root)) {
		if at, ok := e.Resets(); ok {
			if until := at.Add(w.Settings.Margin).Sub(now); until < d {
				d = until
			}
		}
	}
	if d < 10*time.Millisecond {
		d = 10 * time.Millisecond
	}
	return d
}

// Step is one pass: resume what is due, read the orchestrator's data, and
// handle limit text from matches and, with sweep, from capturing every pane.
func (w *Watcher) Step(ctx context.Context, matches []Match, sweep bool) {
	now := w.Now()
	targets := w.Targets(ctx)
	w.resumeDue(ctx, now, targets)
	if ctx.Err() != nil {
		return
	}
	if w.OrchestratorData != nil {
		if d := w.OrchestratorData(now); d.Limited && d.ResetsAt.After(now) {
			w.detect(ctx, now, orchestratorTarget(targets), obs{window: d.Window, source: SourceData, resetsAt: d.ResetsAt, hasReset: true})
		}
	}
	for _, m := range matches {
		w.text(ctx, now, targets, m)
	}
	if sweep && w.Capture != nil {
		for _, t := range targets {
			if ctx.Err() != nil {
				return
			}
			if txt := w.Capture(ctx, t); txt != "" {
				w.text(ctx, now, targets, Match{Session: t.Session, Text: txt})
			}
		}
	}
}

func orchestratorTarget(targets []Target) Target {
	for _, t := range targets {
		if t.Orchestrator {
			return t
		}
	}
	return Target{Session: Orchestrator, Orchestrator: true}
}

func findTarget(targets []Target, session string) (Target, bool) {
	for _, t := range targets {
		if t.Session == session {
			return t, true
		}
	}
	return Target{}, false
}

// obs is what was observed about a limit.
type obs struct {
	window   string
	source   string
	resetsAt time.Time
	hasReset bool
	// cooling: the slot's meter says its account is spent.
	cooling bool
	// text is the message, for the note.
	text string
}

// ---- limit text -------------------------------------------------------------

// findLimit looks for a limit message at the end of the pane text. Only what
// follows the last occurrence of the resume prompt counts, so the message a
// resume already answered cannot start another cycle. "Approaching" warnings
// match the shared phrases but are not limits. It returns the matched line
// and the text from it on (for the reset time).
func findLimit(text, prompt string) (line, rest string, ok bool) {
	if prompt != "" {
		if re := promptRe(prompt); re != nil {
			if locs := re.FindAllStringIndex(text, -1); len(locs) > 0 {
				text = text[locs[len(locs)-1][1]:]
			}
		}
	}
	lines := strings.Split(strings.TrimRight(text, "\n \t"), "\n")
	if len(lines) > tailLines {
		lines = lines[len(lines)-tailLines:]
	}
	tail := strings.Join(lines, "\n")
	for _, re := range worker.LimitPatterns() {
		for _, loc := range re.FindAllStringIndex(tail, -1) {
			if strings.Contains(strings.ToLower(tail[loc[0]:loc[1]]), "approaching") {
				continue
			}
			start := strings.LastIndex(tail[:loc[0]], "\n") + 1
			end := strings.Index(tail[loc[0]:], "\n")
			if end < 0 {
				end = len(tail)
			} else {
				end += loc[0]
			}
			rest = tail[start:]
			if len(rest) > 400 {
				rest = rest[:400]
			}
			return strings.TrimSpace(tail[start:end]), rest, true
		}
	}
	return "", "", false
}

// promptRe matches the prompt as a terminal may have wrapped it.
func promptRe(prompt string) *regexp.Regexp {
	words := strings.Fields(prompt)
	if len(words) == 0 {
		return nil
	}
	for i, w := range words {
		words[i] = regexp.QuoteMeta(w)
	}
	re, err := regexp.Compile(strings.Join(words, `\s+`))
	if err != nil {
		return nil
	}
	return re
}

// text handles one pane text.
func (w *Watcher) text(ctx context.Context, now time.Time, targets []Target, m Match) {
	t, ok := findTarget(targets, m.Session)
	if !ok {
		return
	}
	if !t.Orchestrator && t.Issue == "" {
		return // an idle slot loses nothing
	}
	body := m.Text
	if body == "" {
		body = m.Line
	}
	line, rest, ok := findLimit(body, w.Settings.Prompt)
	if !ok || w.dismissed[t.Session] == line {
		return
	}
	if latest, has := latestFor(Load(w.Root), t.Session); has && latest.Status == StatusWaiting {
		return
	}
	o := obs{source: SourceText, window: WindowUnknown, text: line}
	if t.Orchestrator {
		if w.OrchestratorData != nil {
			if d := w.OrchestratorData(now); d.Known && !(d.Limited && d.ResetsAt.After(now)) {
				w.dismissed[t.Session] = line // the data says no window is spent
				return
			}
		}
	} else if w.Meter != nil && t.Account != "" && t.Kind == "" {
		// The meter reads Claude accounts only: a Codex login has no usage
		// numbers, so its limit message alone is the evidence.
		r := w.Meter(ctx, t.Account)
		switch {
		case r.Known && !r.Cooling:
			w.dismissed[t.Session] = line // the meter says the account has headroom
			return
		case r.Known && r.Cooling:
			o.cooling, o.source, o.resetsAt, o.hasReset = true, SourceData, r.ResetsAt, !r.ResetsAt.IsZero()
			if r.Window != "" {
				o.window = r.Window
			}
		}
	}
	if !o.hasReset {
		if at, ok := ParseReset(line, now, w.Loc); ok {
			o.resetsAt, o.hasReset = at, true
		} else if at, ok := ParseReset(rest, now, w.Loc); ok {
			o.resetsAt, o.hasReset = at, true
		}
	}
	w.detect(ctx, now, t, o)
}

// latestFor is the newest entry of a session.
func latestFor(list []Entry, session string) (Entry, bool) {
	for i := len(list) - 1; i >= 0; i-- {
		if list[i].Session == session {
			return list[i], true
		}
	}
	return Entry{}, false
}

// ---- detect and decide ------------------------------------------------------

// detect records a limit seen on a session, or advances the one it continues.
func (w *Watcher) detect(ctx context.Context, now time.Time, t Target, o obs) {
	latest, has := latestFor(Load(w.Root), t.Session)
	if has && latest.Status == StatusWaiting {
		return
	}
	if has && latest.Status == StatusFailed {
		if at, err := time.Parse(time.RFC3339, latest.ResolvedAt); err == nil && now.Sub(at) < failedQuiet {
			return
		}
	}
	if !o.hasReset {
		o.resetsAt = now.Add(w.Settings.Fallback)
		o.source = SourceText
	}
	if o.window == "" {
		o.window = WindowUnknown
	}

	e := Entry{Session: t.Session, Window: o.window, Source: o.source, DetectedAt: Time(now),
		ResetsAt: Time(o.resetsAt), Account: t.Account, Kind: t.Kind, Status: StatusWaiting}
	if !o.hasReset {
		e.Note = fmt.Sprintf("no reset time found; sleeping the fallback %s", w.Settings.Fallback)
	}
	reopened := false
	if has && latest.Status == StatusResumed {
		if at, err := time.Parse(time.RFC3339, latest.ResolvedAt); err == nil && now.Sub(at) <= reopenWindow {
			reopened = true
			if latest.Cycles >= w.Settings.MaxResumes {
				w.fail(&latest, now, fmt.Sprintf("still limited after %d resume(s)", latest.Cycles))
				return
			}
			e.ID, e.Cycles = latest.ID, latest.Cycles
			e.Note = strings.TrimSpace(fmt.Sprintf("limited again after resume %d. %s", latest.Cycles, e.Note))
		}
	}

	slot, acct, attempt := w.decide(ctx, t, &e)
	// The entry is recorded before the move: a crash during the transfer
	// leaves a waiting entry that names where the issue was going.
	if err := w.store(&e, reopened); err != nil {
		w.warn("limits not recorded: %v", err)
		return
	}
	worker.LedgerNote(w.Root, ledger.Entry{Kind: ledger.KindLimited, Issue: t.Issue, Slot: t.Session, Account: t.Account,
		Detail: ledger.Detail("resetsAt", e.ResetsAt, "window", e.Window)})
	if !attempt {
		return
	}
	if err := w.Transfer(ctx, t.Issue, slot); err != nil {
		e.Action, e.To = ActionSleep, ""
		e.Note = strings.TrimSpace(e.Note + fmt.Sprintf(" transfer of %s to %s failed: %v; sleeping instead.", t.Issue, slot, err))
	} else {
		e.Status, e.ResolvedAt = StatusSwitched, Time(now)
		e.Note = strings.TrimSpace(e.Note + fmt.Sprintf(" moved %s to %s on account %s.", t.Issue, slot, acct))
	}
	if err := Save(w.Root, e); err != nil {
		w.warn("limits not recorded: %v", err)
	}
}

// store appends a new entry, giving it its id, or rewrites a continued one.
func (w *Watcher) store(e *Entry, existing bool) error {
	if existing {
		return Save(w.Root, *e)
	}
	saved, err := Append(w.Root, *e)
	if err == nil {
		*e = saved
	}
	return err
}

// decide chooses sleep or switch. For a switch it fills e.Action and e.To and
// returns the slot and account to move to; every other outcome is a sleep with
// the reason in the note.
func (w *Watcher) decide(ctx context.Context, t Target, e *Entry) (slot, acct string, attempt bool) {
	e.Action = ActionSleep
	sleep := func(why string) (string, string, bool) {
		e.Note = strings.TrimSpace(e.Note + " " + why)
		return "", "", false
	}
	switch {
	case t.Orchestrator:
		return sleep("The orchestrator never switches; it sleeps until the reset.")
	case w.Settings.Mode != ModeSwitch:
		return sleep("limits.mode is sleep.")
	case t.Issue == "":
		return sleep("The slot holds no issue.")
	case w.PickAccount == nil || w.IdleSlot == nil || w.Transfer == nil:
		return sleep("Switching is not available here.")
	}
	acct, ok := w.PickAccount(ctx, t.Kind, t.Account)
	if !ok {
		return sleep("No usable account to switch to.")
	}
	slot, ok = w.IdleSlot(ctx, t.Kind, acct)
	if !ok {
		return sleep(fmt.Sprintf("No idle slot on account %s.", acct))
	}
	e.Action, e.To = ActionSwitch, slot
	return slot, acct, true
}

// ---- resume -----------------------------------------------------------------

// resumeDue types the resume prompt into every waiting entry whose reset and
// margin have passed. Entries from an earlier watcher are covered too, which is
// the start-up pass.
func (w *Watcher) resumeDue(ctx context.Context, now time.Time, targets []Target) {
	for _, e := range Waiting(Load(w.Root)) {
		at, ok := e.Resets()
		if !ok || now.Before(at.Add(w.Settings.Margin)) {
			continue
		}
		t, found := findTarget(targets, e.Session)
		if e.Session == Orchestrator && !found {
			t, found = orchestratorTarget(targets), false
			found = t.Pane != ""
		}
		if !found {
			w.fail(&e, now, "the session's pane is gone, so the resume prompt could not be typed")
			continue
		}
		if w.Send == nil {
			w.warn("no way to type the resume prompt into %s", e.Session)
			continue
		}
		if err := w.Send(ctx, t, w.Settings.Prompt); err != nil {
			w.fail(&e, now, fmt.Sprintf("the resume prompt was not typed: %v", err))
			continue
		}
		e.Cycles++
		e.Status, e.ResolvedAt = StatusResumed, Time(now)
		if err := Save(w.Root, e); err != nil {
			w.warn("limits not recorded: %v", err)
		}
	}
}

// fail marks the entry failed and raises the escalation, or the host
// notification when there is no thread.
func (w *Watcher) fail(e *Entry, now time.Time, why string) {
	w.failed++
	e.Status, e.ResolvedAt = StatusFailed, Time(now)
	if e.Note != "" {
		e.Note += " "
	}
	e.Note += why + "."
	title := fmt.Sprintf("Usage limit not resolved: %s", e.Session)
	body := fmt.Sprintf("The usage-limit watcher gave up on %s (%s): %s.\n\n- Window: %s\n- Detected: %s\n- Reset: %s\n- Resume prompts typed: %d\n- Log: %s (limits %s)\n\n"+
		"Look at the pane, then restart the session or run rota limit watch again.\n",
		e.Session, e.ID, why, e.Window, e.DetectedAt, firstNonEmpty(e.ResetsAt, "unknown"), e.Cycles, worker.RegistryPath(w.Root), e.ID)
	switch {
	case w.Settings.EscalateIssue == 0:
		if w.Notify != nil {
			w.Notify(title, fmt.Sprintf("%s: %s", e.Session, why))
		}
		w.warn("no escalation comment: orchestrator.escalateIssue is unset, so only a host notification was raised")
	case w.Escalate == nil:
		w.warn("no escalation comment: no escalation channel")
	default:
		id, warns, err := w.Escalate(w.Settings.EscalateIssue, title, body)
		w.warnings = append(w.warnings, warns...)
		if err != nil {
			w.warn("escalation on #%d not sent: %v", w.Settings.EscalateIssue, err)
		} else {
			e.Note += " Escalation " + id + "."
		}
	}
	if err := Save(w.Root, *e); err != nil {
		w.warn("limits not recorded: %v", err)
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
