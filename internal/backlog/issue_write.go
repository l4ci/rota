package backlog

import (
	"errors"
	ms "github.com/l4ci/rota/internal/milestone"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/tracker"
)

// The issue backend's lifecycle side: IssueBackend.create, set_field,
// complete, uncomplete, claim/release/set_state, notes, comments, status and
// ready_reasons in bin/hvlib_backend.py. The tracker calls happen in the order
// the Python makes them.

// stateRoles are the label roles status reports (_STATE_ROLES).
var stateRoles = []string{"inProgress", "needsReview", "changesRequested", "blocked"}

// StateRoleFor maps an `item state` word to its label role; "none" is "".
var StateRoleFor = map[string]string{
	"in-progress": "inProgress", "needs-review": "needsReview",
	"changes-requested": "changesRequested", "none": "",
}

// States are the words `item state --to` takes, in the order the old helper
// listed them.
var States = []string{"in-progress", "needs-review", "changes-requested", "none"}

// NoteKinds are the kinds of durable note (NOTE_KINDS).
var NoteKinds = []string{"proof", "design", "plan"}

// noteLimitDefault is the characters per marker comment (GitHub caps a
// comment at 65,536).
const noteLimitDefault = 60000

var (
	sliceKindRe = regexp.MustCompile(`\Aplan:S\p{Nd}+\z`)
)

func (b *Issues) tracker() (Tracker, error) {
	if b.Tracker == nil {
		return nil, errors.New("issues backend has no tracker")
	}
	return b.Tracker, nil
}

// AutoCreate reports issues.autoCreateLabel.
func (b *Issues) AutoCreate() bool {
	v, _ := config.Value(b.Cfg, "issues.autoCreateLabel")
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case interface{ String() string }: // json.Number
		f, err := strconv.ParseFloat(t.String(), 64)
		return err != nil || f != 0
	case []any:
		return len(t) > 0
	}
	return true
}

func (b *Issues) stateLabels(roles ...string) []string {
	if len(roles) == 0 {
		roles = stateRoles
	}
	out := make([]string, len(roles))
	for i, r := range roles {
		out[i] = config.Label(b.Cfg, r)
	}
	return out
}

// require is the issue behind ref and its "F12" spelling (_require).
func (b *Issues) require(ref string) (Issue, string, error) {
	is, ok, err := b.lookup(ref)
	if err != nil {
		return Issue{}, "", err
	}
	if !ok {
		return Issue{}, "", errf(ErrNotFound, "[%s] not found in the issue tracker", ref)
	}
	return is, b.Letter(is) + strconv.Itoa(is.Number), nil
}

// number is the issue number a ref spells, without asking the tracker (_number).
func (b *Issues) number(ref string) (int, error) {
	n, _, err := resolveItemRef(ref)
	if err != nil {
		return 0, errf(ErrNotFound, "%s", err.Error())
	}
	return n, nil
}

// ---- create ----------------------------------------------------------------

// milestoneTitle is the native milestone title for rota ID value ("M07")
// (_milestone_title). An ID the tracker does not know wraps ErrNotFound.
func (b *Issues) milestoneTitle(value string) (string, error) {
	value = pystr.Strip(value)
	if !(ms.LeadingID(value) == value && value != "") {
		return "", errf(ErrInvalid, "issue mode takes one milestone ID like M07, got '%s'", value)
	}
	title, ok, err := b.Tracker.FindMilestone(b.ctx(), value)
	if err != nil {
		return "", err
	}
	if !ok && b.OnMissingMilestone != nil {
		if title, ok, err = b.OnMissingMilestone(value); err != nil {
			return "", err
		}
	}
	if !ok {
		return "", errf(ErrNotFound, "milestone %s not found on the tracker — create it with /rota-vision (M07-S05)", value)
	}
	return title, nil
}

// Create captures one item as an issue (IssueBackend.create). The result's ID
// is the issue number; the "{ID}" placeholder in the body becomes "F42" (the
// type letter and the number), the spelling commits and PR bodies carry.
func (b *Issues) Create(in CreateInput) (CreateResult, error) {
	title, fields, err := checkCreate(in)
	if err != nil {
		return CreateResult{}, err
	}
	tr, err := b.tracker()
	if err != nil {
		return CreateResult{}, err
	}
	letter := kindLetter[in.Kind]
	role := map[string]string{"B": "types.bug", "F": "types.feature", "T": "types.task"}[letter]
	labels := []string{config.Label(b.Cfg, role)}
	if in.Tag != "" && letter == "B" {
		labels = append(labels, config.Label(b.Cfg, "priorityPrefix")+in.Tag[1:])
	} else if in.Tag != "" {
		labels = append(labels, config.Label(b.Cfg, "sizePrefix")+in.Tag)
	}
	msTitle := ""
	var names []string
	values := map[string]string{}
	for _, f := range fields {
		if f.Name == "Milestone" {
			if msTitle, err = b.milestoneTitle(f.Value); err != nil {
				return CreateResult{}, err
			}
			continue
		}
		names = append(names, f.Name)
		values[f.Name] = f.Value
	}
	if pystr.Strip(in.Since) != "" {
		if _, seen := values["Since"]; !seen {
			names = append(names, "Since")
		}
		values["Since"] = oneLine(in.Since)
	}
	var parts []string
	if d := pystr.Strip(in.Desc); d != "" {
		parts = append(parts, d)
	}
	if in.HasBody {
		if body := strings.Trim(string([]rune(string(in.Body))), "\n"); body != "" {
			parts = append(parts, body)
		}
	}
	full := RenderFieldsBlock(strings.Join(parts, "\n\n"), names, values)
	if err := tr.EnsureLabels(b.ctx(), labels, b.AutoCreate()); err != nil {
		return CreateResult{}, err
	}
	n, err := tr.Create(b.ctx(), title, full, labels, msTitle)
	if err != nil {
		return CreateResult{}, err
	}
	if strings.Contains(full, "{ID}") {
		body := strings.ReplaceAll(full, "{ID}", letter+strconv.Itoa(n))
		if err := tr.Edit(b.ctx(), n, tracker.IssueEdit{Body: &body}); err != nil {
			return CreateResult{}, err
		}
	}
	return CreateResult{ID: strconv.Itoa(n), Type: letter}, nil
}

// ---- set_field -------------------------------------------------------------

// SetField sets, replaces or clears a field of an open issue
// (IssueBackend.set_field): milestone is the native milestone, the others live
// in the body's fields block. A closed issue is a RefusedError wrapping
// ErrClosed, an unknown one ErrNotFound.
func (b *Issues) SetField(ref, field, value string) (bool, error) {
	field = strings.ToLower(field)
	ok := false
	for _, f := range SettableFields {
		ok = ok || (f == field && f != "detail")
	}
	if !ok {
		return false, errf(ErrInvalid, "%s is not a settable field; pick one of milestone/related/repos/subsystem", field)
	}
	tr, err := b.tracker()
	if err != nil {
		return false, err
	}
	is, found, err := b.lookup(ref)
	if err != nil {
		return false, err
	}
	if !found {
		return false, errf(ErrNotFound, "[%s] is not an open item on the issue tracker (unknown)", ref)
	}
	if is.State != "open" {
		return false, refused("closed item", ErrClosed, "[%s] is closed on the issue tracker", ref)
	}
	value = pystr.Strip(value)
	n := is.Number
	if field == "milestone" {
		if value == "" {
			if is.Milestone == "" {
				return false, nil
			}
			return true, tr.Edit(b.ctx(), n, tracker.IssueEdit{RemoveMilestone: true})
		}
		title, err := b.milestoneTitle(value)
		if err != nil {
			return false, err
		}
		if is.Milestone == title {
			return false, nil
		}
		return true, tr.Edit(b.ctx(), n, tracker.IssueEdit{Milestone: title})
	}
	name := strings.ToUpper(field[:1]) + field[1:]
	text, block, order := ParseFieldsBlock(is.Body)
	value = oneLine(value)
	if block[name] == value {
		return false, nil
	}
	values := map[string]string{}
	var names []string
	for _, k := range order {
		if k == name && value == "" {
			continue
		}
		names = append(names, k)
		values[k] = block[k]
	}
	if value != "" {
		if _, had := values[name]; !had {
			names = append(names, name)
		}
		values[name] = value
	}
	body := RenderFieldsBlock(text, names, values)
	return true, tr.Edit(b.ctx(), n, tracker.IssueEdit{Body: &body})
}

// ---- complete / reopen -----------------------------------------------------

// Complete closes the issue with the reason's tracker state
// (IssueBackend.complete): done closes as completed with a "Done in `<hash>`"
// comment; dropped and handed-off close as not planned with "Closed: <reason>";
// blocked leaves it open with the blocked label and a "Blocked" comment. Each
// comment ends with a rota marker line (blocked, done, closed).
// Closing clears the in-progress, needs-review, changes-requested and blocked
// labels. changed is false for an already closed (or already blocked) issue. A
// `done` close without a proof row is a RefusedError wrapping ErrProofMissing.
func (b *Issues) Complete(ref string, in CompleteInput) (bool, error) {
	tr, err := b.tracker()
	if err != nil {
		return false, err
	}
	is, itemID, err := b.require(ref)
	if err != nil {
		return false, err
	}
	if is.State == "closed" {
		return false, nil
	}
	n := is.Number
	suffix := ""
	if note := oneLine(in.Note); note != "" {
		suffix = " — " + note
	}
	if in.Reason == "blocked" {
		label := config.Label(b.Cfg, "blocked")
		for _, l := range is.Labels {
			if l == label {
				return false, nil
			}
		}
		if err := tr.AddLabels(b.ctx(), n, []string{label}, b.AutoCreate()); err != nil {
			return false, err
		}
		_, err := tr.AddComment(b.ctx(), n, "Blocked"+suffix+"\n\n"+marker.Line("blocked"))
		return err == nil, err
	}
	if in.Reason == "done" && !in.NoProof {
		count, err := b.proofCount(itemID)
		if err != nil {
			return false, err
		}
		if count == 0 {
			return false, refused("proof missing", ErrProofMissing,
				"[%s] no proof recorded, pass --no-proof to override", itemID)
		}
	}
	var stale []string
	for _, l := range b.stateLabels() {
		if has(is.Labels, l) {
			stale = append(stale, l)
		}
	}
	if len(stale) > 0 {
		if err := tr.RemoveLabels(b.ctx(), n, stale); err != nil {
			return false, err
		}
	}
	if in.Reason == "done" {
		return true, tr.Close(b.ctx(), n, "completed", "Done in `"+in.Commit+"`"+suffix+"\n\n"+marker.Line("done"))
	}
	return true, tr.Close(b.ctx(), n, "not_planned", "Closed: "+in.Reason+suffix+"\n\n"+marker.Line("closed"))
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Reopen reopens a closed issue (clearing not-planned and blocked) or unblocks
// an open one (IssueBackend.uncomplete); anything else is a no-op (false).
func (b *Issues) Reopen(ref string) (bool, error) {
	tr, err := b.tracker()
	if err != nil {
		return false, err
	}
	is, _, err := b.require(ref)
	if err != nil {
		return false, err
	}
	var stale []string
	for _, l := range b.stateLabels("notPlanned", "blocked") {
		if has(is.Labels, l) {
			stale = append(stale, l)
		}
	}
	if is.State == "closed" {
		if err := tr.Reopen(b.ctx(), is.Number); err != nil {
			return false, err
		}
	} else if !has(stale, config.Label(b.Cfg, "blocked")) {
		return false, nil
	}
	if len(stale) > 0 {
		if err := tr.RemoveLabels(b.ctx(), is.Number, stale); err != nil {
			return false, err
		}
	}
	return true, nil
}

// proofCount is the number of proof rows an item has, counted by the injected
// CountProof over its proof note.
func (b *Issues) proofCount(itemID string) (int, error) {
	if b.CountProof == nil {
		return 0, errors.New("backlog: no proof counter injected")
	}
	text, ok, err := b.NoteGet(itemID, "proof")
	if err != nil || !ok {
		return 0, err
	}
	return b.CountProof(text), nil
}

// ---- ready -----------------------------------------------------------------

// Ready lists what the issue lacks to be startable (IssueBackend.ready_reasons):
// acceptance criteria in the body, or a design or plan note.
func (b *Issues) Ready(ref string) ([]string, error) {
	is, _, err := b.require(ref)
	if err != nil {
		return nil, err
	}
	text, _, _ := ParseFieldsBlock(is.Body)
	note := false
	for _, kind := range []string{"design", "plan"} {
		body, _, err := b.NoteGet(ref, kind)
		if err != nil {
			return nil, err
		}
		if body != "" {
			note = true
			break
		}
	}
	return readyReasons(hasCriteria(text), note), nil
}

// ---- notes -----------------------------------------------------------------

func noteLimit() int {
	n, err := strconv.Atoi(pystr.Strip(os.Getenv("ROTA_NOTE_LIMIT")))
	if err != nil {
		return noteLimitDefault
	}
	return max(80, n)
}

func noteNorm(text string) string {
	return strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
}

func runes(s string) int { return utf8.RuneCountInString(s) }

// keepLines is str.splitlines(keepends=True).
func keepLines(s string) []string {
	var out []string
	start, i := 0, 0
	for i < len(s) {
		r, n := utf8.DecodeRuneInString(s[i:])
		i += n
		switch r {
		case '\n', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
		case '\r':
			if i < len(s) && s[i] == '\n' {
				i++
			}
		default:
			continue
		}
		out = append(out, s[start:i])
		start = i
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// noteParts is the comment bodies for text: one `<!-- rota:kind -->` comment, or
// numbered `<!-- rota:kind i/n -->` parts split on line boundaries (a line longer
// than a part is cut) (_note_parts).
func noteParts(kind, text string) []string {
	text = noteNorm(text)
	limit := noteLimit()
	single := marker.NoteHeader(kind, 1, 1)
	if runes(single)+runes(text) <= limit {
		return []string{single + text}
	}
	budget := limit - runes(marker.NoteHeader(kind, 99, 99))
	var chunks []string
	cur := ""
	for _, line := range keepLines(text) {
		for runes(line) > budget {
			if cur != "" {
				chunks = append(chunks, cur)
				cur = ""
			}
			rs := []rune(line)
			chunks = append(chunks, string(rs[:budget]))
			line = string(rs[budget:])
		}
		if runes(cur)+runes(line) > budget {
			chunks = append(chunks, cur)
			cur = ""
		}
		cur += line
	}
	chunks = append(chunks, cur)
	out := make([]string, len(chunks))
	for i, c := range chunks {
		out[i] = marker.NoteHeader(kind, i+1, len(chunks)) + c
	}
	return out
}

type notePart struct {
	idx  int
	c    tracker.Comment
	rest string
}

// noteComments is the `kind` note's comments in part order, with the text after
// each marker (_note_comments).
func (b *Issues) noteComments(n int, kind string) ([]notePart, error) {
	comments, err := b.Tracker.Comments(b.ctx(), n)
	if err != nil {
		return nil, err
	}
	var found []notePart
	for _, c := range comments {
		nt, ok := marker.ParseNote(c.Body)
		if !ok || nt.Kind != kind {
			continue
		}
		idx := 1
		if nt.Part != "" {
			if idx, err = Atoi(nt.Part); err != nil {
				return nil, err
			}
		}
		found = append(found, notePart{idx, c, nt.Rest})
	}
	sort.SliceStable(found, func(i, j int) bool {
		if found[i].idx != found[j].idx {
			return found[i].idx < found[j].idx
		}
		return idLess(found[i].c.ID, found[j].c.ID)
	})
	return found, nil
}

// idLess orders comment ids numerically when both are numbers.
func idLess(a, b string) bool {
	x, ea := strconv.Atoi(a)
	y, eb := strconv.Atoi(b)
	if ea == nil && eb == nil {
		return x < y
	}
	return a < b
}

func checkNoteKind(kind string) error {
	if has(NoteKinds, kind) || sliceKindRe.MatchString(kind) {
		return nil
	}
	return errf(ErrInvalid, "note kind must be one of %s (or plan:S<NN>)", strings.Join(NoteKinds, "/"))
}

// NoteGet is the `kind` note's text without trailing newlines; ok is false when
// the issue has no such note.
func (b *Issues) NoteGet(ref, kind string) (string, bool, error) {
	if err := checkNoteKind(kind); err != nil {
		return "", false, err
	}
	n, err := b.number(ref)
	if err != nil {
		return "", false, err
	}
	if _, err := b.tracker(); err != nil {
		return "", false, err
	}
	parts, err := b.noteComments(n, kind)
	if err != nil || len(parts) == 0 {
		return "", false, err
	}
	var all strings.Builder
	for _, p := range parts {
		all.WriteString(p.rest)
	}
	return noteNorm(all.String()), true, nil
}

// NotePut upserts the note: edits existing parts in place, adds missing ones,
// deletes surplus ones. It makes no write and returns false when the note
// already reads as text.
func (b *Issues) NotePut(ref, kind, text string) (bool, error) {
	if err := checkNoteKind(kind); err != nil {
		return false, err
	}
	n, err := b.number(ref)
	if err != nil {
		return false, err
	}
	tr, err := b.tracker()
	if err != nil {
		return false, err
	}
	existing, err := b.noteComments(n, kind)
	if err != nil {
		return false, err
	}
	want := noteParts(kind, text)
	changed := false
	for i, body := range want {
		if i < len(existing) {
			cur := strings.ReplaceAll(existing[i].c.Body, "\r\n", "\n")
			if i == len(want)-1 { // the tracker may trim trailing newlines
				cur, body = strings.TrimRight(cur, "\n"), strings.TrimRight(body, "\n")
			}
			if cur != body {
				if err := tr.EditComment(b.ctx(), n, existing[i].c.ID, body); err != nil {
					return changed, err
				}
				changed = true
			}
		} else {
			if _, err := tr.AddComment(b.ctx(), n, body); err != nil {
				return changed, err
			}
			changed = true
		}
	}
	for i := len(want); i < len(existing); i++ {
		if err := tr.DeleteComment(b.ctx(), n, existing[i].c.ID); err != nil {
			return changed, err
		}
		changed = true
	}
	return changed, nil
}

// NoteRm deletes every part of the note; false when there was none.
func (b *Issues) NoteRm(ref, kind string) (bool, error) {
	if err := checkNoteKind(kind); err != nil {
		return false, err
	}
	n, err := b.number(ref)
	if err != nil {
		return false, err
	}
	tr, err := b.tracker()
	if err != nil {
		return false, err
	}
	parts, err := b.noteComments(n, kind)
	if err != nil {
		return false, err
	}
	for _, p := range parts {
		if err := tr.DeleteComment(b.ctx(), n, p.c.ID); err != nil {
			return true, err
		}
	}
	return len(parts) > 0, nil
}

// ---- comments --------------------------------------------------------------

// AddComment appends a `<!-- rota:comment <kind> -->` comment and returns its id.
func (b *Issues) AddComment(ref, kind, text string) (string, error) {
	if !validCommentKind(kind) {
		return "", commentKindErr()
	}
	n, err := b.number(ref)
	if err != nil {
		return "", err
	}
	tr, err := b.tracker()
	if err != nil {
		return "", err
	}
	return tr.AddComment(b.ctx(), n, marker.CommentHeader(kind)+noteNorm(text))
}

// Comments lists the context comments oldest first, optionally only kind.
func (b *Issues) Comments(ref, kind string) ([]Comment, error) {
	if kind != "" && !validCommentKind(kind) {
		return nil, commentKindErr()
	}
	is, _, err := b.require(ref)
	if err != nil {
		return nil, err
	}
	comments, err := b.Tracker.Comments(b.ctx(), is.Number)
	if err != nil {
		return nil, err
	}
	return commentRows(comments, kind), nil
}

func commentRows(comments []tracker.Comment, kind string) []Comment {
	rows := []Comment{}
	for _, c := range comments {
		k, rest, ok := marker.ParseComment(c.Body)
		if !ok {
			continue
		}
		if kind == "" || kind == k {
			rows = append(rows, Comment{Who: c.Author, Kind: k, Text: strings.Trim(rest, "\n")})
		}
	}
	return rows
}

// ---- claim, release, state, status ------------------------------------------

// openClaims is the claim ids with no later release, in claim order; the
// earliest holds (_open_claims).
func openClaims(comments []tracker.Comment) []string {
	var held []string
	for _, c := range comments {
		verb, id, ok := marker.ParseClaim(c.Body)
		if !ok {
			continue
		}
		if verb == marker.KindClaim {
			if !has(held, id) {
				held = append(held, id)
			}
		} else if i := indexOf(held, id); i >= 0 {
			held = append(held[:i], held[i+1:]...)
		}
	}
	return held
}

func indexOf(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return -1
}

// applyState leaves only the want role's label (role "" for none) of
// in-progress, needs-review and changes-requested, in one edit. It reports
// whether it wrote (_apply_state).
func (b *Issues) applyState(is Issue, want string) (bool, error) {
	keep := ""
	if want != "" {
		keep = config.Label(b.Cfg, want)
	}
	var drop, add []string
	for _, l := range b.stateLabels("inProgress", "needsReview", "changesRequested") {
		if l != keep && has(is.Labels, l) {
			drop = append(drop, l)
		}
	}
	if keep != "" && !has(is.Labels, keep) {
		add = []string{keep}
	}
	if len(drop) == 0 && len(add) == 0 {
		return false, nil
	}
	if len(add) > 0 {
		if err := b.Tracker.EnsureLabels(b.ctx(), add, b.AutoCreate()); err != nil {
			return false, err
		}
	}
	return true, b.Tracker.Edit(b.ctx(), is.Number, tracker.IssueEdit{AddLabels: add, RemoveLabels: drop})
}

// Claim takes the item: it posts a claim marker, re-reads the comments, and the
// earliest open claim wins (IssueBackend.claim). A loser posts its release
// marker and changes no labels; the winner gets the in-progress label and the
// assignment. An unknown or closed item wraps ErrNotFound.
func (b *Issues) Claim(ref, claimID string) (won bool, holder string, err error) {
	tr, err := b.tracker()
	if err != nil {
		return false, "", err
	}
	is, _, err := b.require(ref)
	if err != nil {
		return false, "", err
	}
	if is.State != "open" {
		return false, "", errf(ErrNotFound, "[%s] is closed", ref)
	}
	n := is.Number
	held, err := b.heldClaims(n)
	if err != nil {
		return false, "", err
	}
	if !(len(held) > 0 && held[0] == claimID) {
		if _, err := tr.AddComment(b.ctx(), n, marker.Claim(claimID)+"\nClaimed by "+claimID); err != nil {
			return false, "", err
		}
		if held, err = b.heldClaims(n); err != nil {
			return false, "", err
		}
		if len(held) == 0 {
			return false, "", errors.New("claim comment not visible after posting")
		}
		if held[0] != claimID {
			if _, err := tr.AddComment(b.ctx(), n, marker.Release(claimID)); err != nil {
				return false, "", err
			}
			return false, held[0], nil
		}
	}
	wrote, err := b.applyState(is, "inProgress")
	if err != nil {
		return false, "", err
	}
	if wrote || len(is.Assignees) == 0 {
		if err := tr.AssignSelf(b.ctx(), n); err != nil {
			return false, "", err
		}
	}
	return true, claimID, nil
}

func (b *Issues) heldClaims(n int) ([]string, error) {
	comments, err := b.Tracker.Comments(b.ctx(), n)
	if err != nil {
		return nil, err
	}
	return openClaims(comments), nil
}

// Release posts the release marker and drops the in-progress label when no
// claim remains. It writes nothing and returns false when claimID holds no open
// claim.
func (b *Issues) Release(ref, claimID string) (bool, error) {
	tr, err := b.tracker()
	if err != nil {
		return false, err
	}
	is, _, err := b.require(ref)
	if err != nil {
		return false, err
	}
	held, err := b.heldClaims(is.Number)
	if err != nil {
		return false, err
	}
	if !has(held, claimID) {
		return false, nil
	}
	if _, err := tr.AddComment(b.ctx(), is.Number, marker.Release(claimID)); err != nil {
		return false, err
	}
	if len(held) == 1 {
		if label := config.Label(b.Cfg, "inProgress"); has(is.Labels, label) {
			if err := tr.RemoveLabels(b.ctx(), is.Number, []string{label}); err != nil {
				return true, err
			}
		}
	}
	return true, nil
}

// SetState leaves exactly one state label (in-progress, needs-review or
// changes-requested) or none; true when the tracker changed.
func (b *Issues) SetState(ref, state string) (bool, error) {
	role, ok := StateRoleFor[state]
	if !ok {
		return false, errf(ErrInvalid, "state must be one of %s", strings.Join(States, "/"))
	}
	if _, err := b.tracker(); err != nil {
		return false, err
	}
	is, _, err := b.require(ref)
	if err != nil {
		return false, err
	}
	return b.applyState(is, role)
}

// Status is the read-back of one issue and its comments (IssueBackend.status).
type Status struct {
	ID        string // the issue number
	Type      string // B | F | T
	Title     string
	Status    string // open | closed
	State     string // state labels, comma-joined; "" when none
	Claim     string // earliest open claim; "" when none
	Assignees []string
	Milestone string
	Notes     []string // note kinds present, in comment order
	Comments  []Comment
}

// Status reads the issue and its comments. An unknown item wraps ErrNotFound.
func (b *Issues) Status(ref string) (*Status, error) {
	if _, err := b.tracker(); err != nil {
		return nil, err
	}
	is, _, err := b.require(ref)
	if err != nil {
		return nil, err
	}
	comments, err := b.Tracker.Comments(b.ctx(), is.Number)
	if err != nil {
		return nil, err
	}
	_, block, _ := ParseFieldsBlock(is.Body)
	var state []string
	for _, l := range b.stateLabels() {
		if has(is.Labels, l) {
			state = append(state, l)
		}
	}
	held := openClaims(comments)
	notes := []string{}
	for _, c := range comments {
		nt, ok := marker.ParseNote(c.Body)
		if ok && !has(notes, nt.Kind) {
			notes = append(notes, nt.Kind)
		}
	}
	title := oneLine(strings.ReplaceAll(is.Title, "*", ""))
	if title == "" {
		title = "(untitled)"
	}
	st := &Status{
		ID: strconv.Itoa(is.Number), Type: b.Letter(is), Title: title, Status: is.State,
		State: strings.Join(state, ","), Assignees: append([]string{}, is.Assignees...),
		Milestone: milestone(is, block), Notes: notes, Comments: commentRows(comments, ""),
	}
	if len(held) > 0 {
		st.Claim = held[0]
	}
	return st, nil
}
