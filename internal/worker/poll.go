package worker

import (
	"context"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/host"
)

// Worker slot classification: the port of bin/hv-worker-poll.
//
// States: BUSY | IDLE | BLOCKED | DONE | DEAD | LIMITED | NEEDS-PERMISSION |
// UNKNOWN (herdr only).
//
// Liveness is decided by MOVEMENT, not by pattern-matching a spinner: the pane
// is captured twice, settle seconds apart, and a pane that changed is BUSY. A
// static pane is then classified by content.
//
// DONE and BLOCKED come from SENTINELS the worker contract requires it to
// print, not from inference:
//
//	ROTA-DONE <slot> <pr-url-or-branch>
//	ROTA-DONE <slot> issues:#a,#b      (a review item: filed issues, no PR)
//	ROTA-BLOCKED <slot>: <one question in plain language>
//
// LIMITED means the session hit its usage window; it routes to reassigning
// the slot to another account rather than re-dispatching onto the same one. If
// the pane offers "Add funds", that spends money and is never the
// orchestrator's to answer.
//
// DEAD is the one state with no sentinel, so it stays heuristic: a bare `API
// Error … Overloaded` on a static pane is a HEADSTONE, not a pulse (only
// `Retrying in` means a retry is in flight), and `Resume this session with`
// on a static pane is a crashed session.
//
// herdr adds a native agent state, read before the pane text: working is
// BUSY; blocked is NEEDS-PERMISSION (a dialog is up) unless the pane carries
// a ROTA-BLOCKED sentinel; idle/done fall to the text rules; unknown falls to
// the text rules and ends UNKNOWN, which is surfaced and never treated as
// finished; gone (no agent) is DEAD. Sentinels, `Retrying in` and LIMITED
// still outrank the native state, the same as they outrank movement.

// Slot states.
const (
	StateBusy            = "BUSY"
	StateIdle            = "IDLE"
	StateBlocked         = "BLOCKED"
	StateDone            = "DONE"
	StateDead            = "DEAD"
	StateLimited         = "LIMITED"
	StateNeedsPermission = "NEEDS-PERMISSION"
	StateUnknown         = "UNKNOWN"
)

// LimitPhrases are the usage-limit messages a session prints about itself, as
// case-insensitive regex sources (no flag prefix). `worker poll` classifies
// LIMITED with them, and the usage-limit watcher (D3) matches the same list,
// so there is one source.
var LimitPhrases = []string{
	`reached your usage limit`,
	`usage limit reached`,
	`You'?ve hit your (?:usage )?limit`,
	`limit (?:will )?reset[s]? at`,
	`Approaching (?:your )?usage limit`,
}

// LimitRegex is the alternation of LimitPhrases with the case-insensitive
// flag written in the regex itself, for engines that take one pattern string
// (herdr's pane.output_matched).
func LimitRegex() string { return "(?i)(?:" + strings.Join(LimitPhrases, "|") + ")" }

// LimitPatterns are the compiled LimitPhrases.
func LimitPatterns() []*regexp.Regexp { return limitPatterns }

var (
	// A sentinel may follow a reply marker: Claude Code v2.1.288 starts the
	// first line of every reply with "● " (older versions "⏺ "), Codex
	// 0.159.x with "• ".
	reBlocked = regexp.MustCompile(`(?m)^\s*(?:[●⏺•]\s*)?ROTA-BLOCKED\s+(\S+)\s*:\s*(.+)$`)
	reDone    = regexp.MustCompile(`(?m)^\s*(?:[●⏺•]\s*)?ROTA-DONE\s+(\S+)\s*(.*)$`)
	reRetry   = regexp.MustCompile(`Retrying in`)
	reFunds   = regexp.MustCompile(`Add funds`)
	reAPIErr  = regexp.MustCompile(`(API Error[^\n]*)`)
	reResume  = regexp.MustCompile(`Resume this session with`)
	// Heuristic, like DEAD: a limited session cannot print a sentinel. The
	// patterns are anchored to phrasing a session emits about ITSELF, not bare
	// words like "limit" that a worker reading source code would echo.
	limitPatterns = compileAll(`(?i)`, LimitPhrases)
	// A worker stopped at a permission prompt is STALLED, but looks exactly
	// like an idle one. Distinct from BLOCKED: the worker is not asking a
	// design question, it needs an approval.
	permissionPatterns = compileAll(`(?i)`, []string{
		`Do you want to (?:proceed|allow|make this edit)`,
		`and don'?t ask again`,
		`No, and tell Claude what to do differently`,
		`Allow .{0,40}\bto run\b`,
	})
	rePRURL = regexp.MustCompile(`^https?://\S+/(?:pull|merge_requests)/\d+\S*$`)
	// An architecture-review item produces issues, not code: its done line
	// names them in place of a PR, `issues:#139,#140`.
	reIssuesDone = regexp.MustCompile(`^issues:\s*#?\d+(?:\s*,\s*#?\d+)*$`)
	reDigits     = regexp.MustCompile(`\d+`)
)

// ParseIssuesDone reads the evidence of `ROTA-DONE <slot> issues:#a,#b` into
// the issue refs (`#a`, `#b`). ok is false for any other evidence.
func ParseIssuesDone(evidence string) (refs []string, ok bool) {
	ev := strings.TrimSpace(evidence)
	if !reIssuesDone.MatchString(ev) {
		return nil, false
	}
	for _, n := range reDigits.FindAllString(ev, -1) {
		refs = append(refs, "#"+n)
	}
	return refs, true
}

func compileAll(prefix string, pats []string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(pats))
	for i, p := range pats {
		out[i] = regexp.MustCompile(prefix + p)
	}
	return out
}

// clip is Python's s[:n] on characters.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// Classify decides one slot's state from its pane text. native is herdr's
// agent_status (or `gone`), empty under tmux, which has none. moved says the
// pane changed between the two captures.
func Classify(text string, moved bool, lines int, native string) (state, evidence string) {
	all := splitLines(text)
	if lines > 0 && len(all) > lines { // [-0:] is the whole list in Python
		all = all[len(all)-lines:]
	}
	tail := strings.Join(all, "\n")

	// Sentinels win over everything, including movement: a worker that printed
	// ROTA-DONE and is still rendering its own output is finished, not busy.
	if m := reBlocked.FindStringSubmatch(tail); m != nil {
		return StateBlocked, strings.TrimSpace(m[2])
	}
	if m := reDone.FindStringSubmatch(tail); m != nil {
		return StateDone, strings.TrimSpace(m[2])
	}
	// `Retrying in` is the only positive proof a retry is in flight. Check it
	// before the DEAD patterns so a live retry is never read as a corpse.
	if reRetry.MatchString(tail) {
		return StateBusy, "retry in flight"
	}
	// LIMITED is checked BEFORE movement: a session that hit its usage limit may
	// still animate a prompt, and reading that as BUSY strands the orchestrator
	// waiting for work that cannot resume until the window resets.
	for _, re := range limitPatterns {
		if m := re.FindString(tail); m != "" {
			// The "Add funds" prompt spends real money and is NEVER the
			// orchestrator's to answer: flag it so the caller escalates.
			if reFunds.MatchString(tail) {
				return StateLimited, "usage limit reached; pane offers 'Add funds' — needs a human, do NOT answer"
			}
			return StateLimited, clip(strings.TrimSpace(m), 120)
		}
	}
	if native == "working" {
		return StateBusy, "herdr: agent working"
	}
	if moved {
		return StateBusy, "pane changed between captures"
	}
	if native == "blocked" {
		return StateNeedsPermission, "herdr: agent blocked on a dialog — answer it in the slot's tab, or widen work.workerCommand"
	}
	if native == "gone" {
		return StateDead, "herdr: no agent in the slot's tab — the session exited"
	}
	// Static pane from here down.
	if m := reAPIErr.FindStringSubmatch(tail); m != nil {
		return StateDead, clip(strings.TrimSpace(m[1]), 120)
	}
	if reResume.MatchString(tail) {
		return StateDead, "crashed session offering resume"
	}
	for _, re := range permissionPatterns {
		if re.MatchString(tail) {
			return StateNeedsPermission, "stopped at a permission prompt — approve in the pane, or widen work.workerCommand"
		}
	}
	if native == "unknown" {
		return StateUnknown, "herdr cannot classify the agent — inspect the tab; not proof it finished"
	}
	return StateIdle, "static pane, no sentinel"
}

// PollRow is one slot's verdict.
type PollRow struct {
	Name, State, Evidence string
}

// missingSentinelNote marks a row promoted to DONE from the forge.
const missingSentinelNote = "no ROTA-DONE sentinel; PR found by branch head"

// promoteIdleWithPR turns an IDLE row with no sentinel into DONE when the open
// PR headed by the slot's own branch exists: the worker opened its PR and
// stopped on a prose summary. A slot with no such PR, or a forge that cannot
// be asked, stays IDLE (stuck), as before. notes names each promoted slot.
func (e Env) promoteIdleWithPR(ctx context.Context, root string, rows []PollRow) (out []PollRow, notes map[string]string) {
	idle := map[string]PollRow{}
	for _, r := range rows {
		if r.State == StateIdle {
			idle[r.Name] = r
		}
	}
	if len(idle) == 0 {
		return rows, nil
	}
	reg := LoadRegistry(root)
	openPRs := e.openPRsByHead(ctx, root, reg, idle)
	for i, r := range rows {
		s := reg.Slot(r.Name)
		if _, ok := idle[r.Name]; !ok || s == nil || s.Branch() == "" || s.Branch() == "park/"+s.Name() {
			continue
		}
		if url := openPRs[s.Branch()]; url != "" {
			rows[i] = PollRow{r.Name, StateDone, url}
			if notes == nil {
				notes = map[string]string{}
			}
			notes[r.Name] = missingSentinelNote
		}
	}
	return rows, notes
}

// PollOpts are the flags of `rota worker poll`.
type PollOpts struct {
	Slot   string
	Settle time.Duration // default 3s
	Lines  int           // default 60
}

// PollResult is the verdict list and whether the registry changed.
type PollResult struct {
	Slots   []PollRow
	Notes   map[string]string // slot -> why its state was not read from a sentinel
	Changed bool
}

// PollFixture classifies a pane text file instead of talking to a host. It
// exists so the classifier is testable without a live session. It writes
// nothing.
func PollFixture(path, slot, status string, lines int) (PollResult, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return PollResult{}, fail(exitcode.ExitUsage, "fixture not found: "+path)
	}
	if slot == "" {
		slot = "fixture"
	}
	if lines <= 0 {
		lines = 60
	}
	st, ev := Classify(string(b), false, lines, status)
	return PollResult{Slots: []PollRow{{slot, st, ev}}}, nil
}

// Poll classifies the worker slots through the host and records each slot's
// state in .rota/workers.json, plus the PR URL from `ROTA-DONE <slot> <pr-url>` in
// slot.pr, so the gate merges through the PR instead of falling back to a
// local merge. A slot that newly turns BLOCKED or NEEDS-PERMISSION raises a
// host notification (herdr only).
func (e Env) Poll(ctx context.Context, root string, o PollOpts) (PollResult, error) {
	e = e.withDefaults()
	if o.Settle < 0 {
		o.Settle = 0
	}
	if o.Lines <= 0 {
		o.Lines = 60
	}
	if err := SoloRefusal(root, "rota round report records a subagent's state"); err != nil {
		return PollResult{}, err
	}
	h := e.NewHost(e.hostKind(root))
	if err := h.Require(); err != nil {
		return PollResult{}, fail(exitcode.ExitUnavailable, err.Error())
	}
	reg := LoadRegistry(root)
	if o.Slot != "" && reg.Slot(o.Slot) == nil {
		return PollResult{}, fail(exitcode.ExitResolution, fmt.Sprintf("slot '%s' is not in the pool", o.Slot))
	}
	var targets []pollTarget
	for _, s := range reg.Slots() {
		if o.Slot != "" && s.Name() != o.Slot {
			continue
		}
		targets = append(targets, slotTarget(s))
	}
	if len(targets) == 0 {
		return PollResult{}, nil
	}
	before, _ := os.ReadFile(RegistryPath(root))

	rows, _ := e.classify(ctx, h, targets, o.Settle, o.Lines)
	rows, notes := e.promoteIdleWithPR(ctx, root, rows)
	for i, r := range rows {
		// Notify on the transition only: a poll loop re-reading a stuck slot
		// must not ring every few seconds.
		if (r.State == StateBlocked || r.State == StateNeedsPermission) && targets[i].prev != strings.ToLower(r.State) {
			h.Notify(ctx, fmt.Sprintf("rota worker %s: %s", r.Name, r.State), r.Evidence)
		}
	}

	byName := map[string]PollRow{}
	for _, r := range rows {
		byName[r.Name] = r
	}
	openPRs := e.openPRsByHead(ctx, root, reg, byName)
	var rowErr error
	if err := UpdateSlots(root, func(s *Slot) {
		r, ok := byName[s.Name()]
		if !ok {
			return
		}
		if err := recordRow(s, r, e.Now()); err != nil && rowErr == nil {
			rowErr = err
		}
		// A worker can open its PR and never print the sentinel (or print it
		// without a URL): the slot would stay busy and the gate would not find
		// the PR. The open PR headed by the slot's own branch is the record.
		if s.PR() == "" && s.Branch() != "" && s.Branch() != "park/"+s.Name() {
			if url := openPRs[s.Branch()]; url != "" {
				s.SetPR(url)
			}
		}
	}); err != nil {
		return PollResult{}, err
	}
	if rowErr != nil {
		return PollResult{}, rowErr
	}
	after, _ := os.ReadFile(RegistryPath(root))
	return PollResult{Slots: rows, Notes: notes, Changed: string(before) != string(after)}, nil
}

// openPRsByHead maps head branch to PR URL for the open PRs, read only when a
// polled slot records no PR. Best effort: with no forge, no origin or a failing
// listing the poll records what the panes say and nothing more.
func (e Env) openPRsByHead(ctx context.Context, root string, reg Registry, polled map[string]PollRow) map[string]string {
	need := false
	for _, s := range reg.Slots() {
		if _, ok := polled[s.Name()]; ok && s.PR() == "" && s.Branch() != "" && s.Branch() != "park/"+s.Name() {
			need = true
		}
	}
	if !need {
		return nil
	}
	if _, code := e.git(root, "remote", "get-url", "origin"); code != 0 {
		return nil
	}
	cfg := config.Load(filepath.Join(root, ".rota", "config.json"))
	f, err := e.Forge(e.detectProvider(root, ""), root, cfg)
	if err != nil {
		return nil
	}
	prs, err := f.OpenPRs(ctx)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, p := range prs {
		out[p.Branch] = p.URL
	}
	return out
}

// seenKey is what `round wait` remembers of a slot it returned: the state and
// its evidence, so a new ROTA-BLOCKED question or limit line is news again.
func seenKey(state, evidence string) string {
	return strings.ToLower(state) + "\t" + evidence
}

// alwaysNews are the states `round wait` returns every time it sees them. The
// orchestrator answers them in the pane at once (a permission prompt) or
// reclaims the slot (dead); an answer typed in the pane re-arms nothing, and
// the generic evidence of a permission prompt cannot tell two prompts apart.
func alwaysNews(state string) bool {
	return state == StateNeedsPermission || state == StateDead
}

// recordRow writes one classified row into its slot, the way every writer of
// a pane's state does (Poll, Wait). A row different from `seen` drops it: the
// slot moved on, so its next arrival is news again.
func recordRow(s *Slot, r PollRow, now time.Time) error {
	// A state change is the registry's only record of activity that is not a
	// commit or an edit: `round reconcile` reads it as the stall clock.
	if err := s.MarkState(r.State, stamp(now)); err != nil {
		return err
	}
	if s.Seen() != seenKey(r.State, r.Evidence) {
		s.ClearSeen()
	}
	// Only a URL-shaped ROTA-DONE argument becomes slot.pr. The contract
	// allows a bare branch name there, and handing a branch to `gh pr
	// merge` fails where the gate's local merge would have worked.
	if r.State == StateDone && rePRURL.MatchString(r.Evidence) {
		s.SetPR(r.Evidence)
	}
	// A review item reports the issues it filed instead; `round assign` frees
	// the slot on them and closes the item.
	if r.State == StateDone {
		if refs, ok := ParseIssuesDone(r.Evidence); ok {
			s.SetIssues(refs)
		}
	}
	return nil
}

// pollTarget is one slot to classify: its name, host handle, the state the
// registry last recorded for it and the state `round wait` last returned for it.
type pollTarget struct{ name, handle, prev, seen string }

func slotTarget(s *Slot) pollTarget {
	return pollTarget{s.Name(), s.PaneHandle(), s.State(), s.Seen()}
}

// classify reads each target's pane twice, settle apart, and classifies it.
// It touches no file: Poll and Wait record the result.
// First capture for every slot, then settle once, then the second capture, so
// N slots cost one settle interval, not N.
//
// settling reports a slot that is BUSY only because its pane moved while the
// host's own status was not `working` (herdr idle or done, or no native
// status). The host sends no event when such a pane comes to rest, so a
// caller woken by events must re-classify it on its own (#211: herdr's
// scrollback reads differ for a moment after a turn ends).
func (e Env) classify(ctx context.Context, h host.Host, targets []pollTarget, settle time.Duration, lines int) (rows []PollRow, settling bool) {
	first := map[string]string{}
	for _, t := range targets {
		first[t.name] = h.Capture(ctx, t.name, t.handle, lines)
	}
	e.Sleep(settle)
	for _, t := range targets {
		second := h.Capture(ctx, t.name, t.handle, lines)
		native := h.Status(ctx, t.name, t.handle)
		moved := first[t.name] != second
		st, ev := Classify(second, moved, lines, native)
		if st == StateBusy && moved && native != "working" {
			settling = true
		}
		rows = append(rows, PollRow{t.name, st, ev})
	}
	return rows, settling
}

// IsPRURL reports whether s is a PR or MR URL, the shape `worker poll` stores
// in slot.pr.
func IsPRURL(s string) bool { return rePRURL.MatchString(s) }
