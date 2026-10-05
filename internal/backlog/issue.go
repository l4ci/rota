package backlog

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/tracker"
)

// Issue is the tracker's view of one issue (hvlib_tracker _norm).
type Issue = tracker.Issue

// Tracker is the part of internal/tracker's Adapter the issue backend calls:
// exactly those methods, with the Adapter's own signatures. A missing issue is
// a *tracker.Error of KindNotFound, never an error message to parse.
type Tracker interface {
	Get(ctx context.Context, number int, withComments bool) (tracker.Issue, error)
	List(ctx context.Context, f tracker.ListFilter) ([]tracker.Issue, error)
	Create(ctx context.Context, title, body string, labels []string, milestone string) (int, error)
	Edit(ctx context.Context, number int, e tracker.IssueEdit) error
	EnsureLabels(ctx context.Context, names []string, autoCreate bool) error
	AddLabels(ctx context.Context, number int, labels []string, autoCreate bool) error
	RemoveLabels(ctx context.Context, number int, labels []string) error
	Close(ctx context.Context, number int, reason, comment string) error
	Reopen(ctx context.Context, number int) error
	AssignSelf(ctx context.Context, number int) error
	Comments(ctx context.Context, number int) ([]tracker.Comment, error)
	AddComment(ctx context.Context, number int, body string) (string, error)
	EditComment(ctx context.Context, number int, commentID, body string) error
	DeleteComment(ctx context.Context, number int, commentID string) error
	FindMilestone(ctx context.Context, rotaID string) (title string, ok bool, err error)
}

// A change to tracker.Adapter that breaks the subset fails the build here.
var _ Tracker = tracker.Adapter(nil)

// Issues is the backlog served from an issue tracker (IssueBackend in
// hvlib_backend.py). Rendered bullets keep the letter
// ("[F12]"); only Item.ID drops it, since an issue number is the identity.
type Issues struct {
	Cfg     any             // loaded config, for the label names
	Tracker Tracker         // where the issues come from
	Ctx     context.Context // for every tracker call; nil is context.Background()
	// CountProof counts the proof rows in an item's proof note, read through
	// Tracker. The row format belongs to the proof package, which imports
	// backlog, so Open injects it.
	CountProof func(text string) int
	Warn       func(string) // notices (duplicate tracking issues); nil drops them
	Repo       string       // umbrella sub-repo name, rendered as Repos:; "" otherwise. IDs stay plain numbers: the umbrella backend qualifies them ("repo:12") and resolves qualified refs, as in Python.
	// OnMissingMilestone is the umbrella hook (on_missing_milestone): called
	// with a rota milestone ID the tracker has no native milestone for, it
	// creates it and returns its title; ok false leaves the ID unknown.
	OnMissingMilestone func(mid string) (title string, ok bool, err error)
}

func (b *Issues) ctx() context.Context {
	if b.Ctx != nil {
		return b.Ctx
	}
	return context.Background()
}

// Name is "issues".
func (b *Issues) Name() string { return "issues" }

// Letter is the item type of an issue: the letter of the first type label it
// carries, else "T" (_letter).
func (b *Issues) Letter(is Issue) string {
	for _, c := range []struct{ letter, role string }{{"B", "types.bug"}, {"F", "types.feature"}, {"T", "types.task"}} {
		if slices.Contains(is.Labels, config.Label(b.Cfg, c.role)) {
			return c.letter
		}
	}
	return "T"
}

// IsMilestoneTracker reports whether the issue is a milestone tracking issue,
// which is not an item (_is_tracker).
func (b *Issues) IsMilestoneTracker(is Issue) bool {
	return slices.Contains(is.Labels, config.Label(b.Cfg, "milestoneTracker"))
}

// tag is the priority (bugs) or size (features) taken from the labels (_tag).
func (b *Issues) tag(is Issue, letter string) string {
	switch letter {
	case "B":
		pre := config.Label(b.Cfg, "priorityPrefix")
		for _, l := range is.Labels {
			rest, ok := strings.CutPrefix(l, pre)
			if ok && rest != "" && allDigits(rest) {
				return "P" + rest
			}
		}
	case "F":
		pre := config.Label(b.Cfg, "sizePrefix")
		for _, l := range is.Labels {
			if strings.HasPrefix(l, pre) && len(l) > len(pre) {
				return l[len(pre):]
			}
		}
	}
	return ""
}

func allDigits(s string) bool {
	for _, r := range s {
		if !pystr.IsDigit(r) {
			return false
		}
	}
	return true
}

// oneLine collapses whitespace runs to one space and strips (_one_line).
func oneLine(s string) string {
	return pystr.Strip(spaceRun.ReplaceAllString(s, " "))
}

var spaceRun = regexp.MustCompile(`[` + pystr.SpaceClass + `]+`)

// kv is one rendered field, in render order.
type kv struct{ name, value string }

// blockFields are the fields that live in the issue body block: everything the
// tracker does not supply (Milestone, Detail).
var blockFields = []string{"Related", "Repos", "Subsystem", "Captured", "Since"}

// milestone is the rota ID of the issue's native milestone ("M07" out of
// "M07 — Title"), the whole title when it has none, else the body block's.
func milestone(is Issue, block map[string]string) string {
	if title := oneLine(is.Milestone); title != "" {
		if m := milestoneRe.FindString(title); m != "" {
			return m
		}
		return title
	}
	return block["Milestone"]
}

var milestoneRe = regexp.MustCompile(`\AM\p{Nd}+`)

// fields renders the field values in canonical order, Milestone first (_fields).
func (b *Issues) fields(is Issue, block map[string]string) []kv {
	var out []kv
	if ms := milestone(is, block); ms != "" {
		out = append(out, kv{"Milestone", ms})
	}
	for _, name := range blockFields {
		if v := block[name]; v != "" {
			if name == "Related" {
				v = bracketIDs(v)
			}
			out = append(out, kv{name, v})
		}
	}
	if b.Repo != "" {
		for i := range out {
			if out[i].name == "Repos" {
				out[i].value = b.Repo
				return out
			}
		}
		out = append(out, kv{"Repos", b.Repo})
	}
	return out
}

// bracketIDs turns "F12, B03" into "[F12], [B03]" so Related matches the file
// grammar: a bracketed ID, (?<![\[\w])(IDPattern)(?![\]\w]).
func bracketIDs(v string) string {
	var out strings.Builder
	for i := 0; i < len(v); {
		r, n := utf8.DecodeRuneInString(v[i:])
		if n == 1 && strings.IndexByte(ItemLetters, v[i]) >= 0 {
			prev, _ := utf8.DecodeLastRuneInString(v[:i])
			j, digits := i+1, 0
			for j < len(v) {
				d, dn := utf8.DecodeRuneInString(v[j:])
				if !pystr.IsDigit(d) {
					break
				}
				j += dn
				digits++
			}
			next, _ := utf8.DecodeRuneInString(v[j:])
			before := i == 0 || (prev != '[' && !pystr.IsWord(prev))
			after := j == len(v) || (next != ']' && !pystr.IsWord(next))
			if digits > 0 && before && after {
				out.WriteString("[" + v[i:j] + "]")
				i = j
				continue
			}
		}
		out.WriteRune(r)
		i += n
	}
	return out.String()
}

// bulletInner is `**[F3] [Major] Title.** text Milestone: M07 ...` for one
// issue (_bullet_inner).
func (b *Issues) bulletInner(is Issue) string {
	letter := b.Letter(is)
	text, block, _ := ParseFieldsBlock(is.Body)
	title := oneLine(strings.ReplaceAll(is.Title, "*", ""))
	if title == "" {
		title = "(untitled)"
	}
	if last, _ := utf8.DecodeLastRuneInString(title); !strings.ContainsRune(".!?", last) {
		title += "."
	}
	head := "**[" + letter + strconv.Itoa(is.Number) + "] "
	if tag := b.tag(is, letter); tag != "" {
		head += "[" + tag + "] "
	}
	head += title + "**"

	desc := ""
	for _, p := range paraSplit.Split(text, -1) {
		if pystr.Strip(p) != "" {
			desc = oneLine(p)
			break
		}
	}
	if rs := []rune(desc); len(rs) > 200 {
		desc = pystr.Rstrip(string(rs[:199])) + "…"
	}
	parts := []string{head}
	if desc != "" {
		parts = append(parts, desc)
	}
	for _, f := range b.fields(is, block) {
		parts = append(parts, f.name+": "+f.value)
	}
	return strings.Join(parts, " ")
}

var paraSplit = regexp.MustCompile(`\n[` + pystr.SpaceClass + `]*\n`)

// closedDate is the date part of the issue's closed_at, the epoch when the
// tracker gave none.
func closedDate(is Issue) string {
	date := is.ClosedAt
	if date == "" {
		date = "1970-01-01"
	}
	if rs := []rune(date); len(rs) > 10 {
		date = string(rs[:10])
	}
	return date
}

// doneLine is the closed form of the bullet (_done_line).
func (b *Issues) doneLine(is Issue) string {
	date := closedDate(is)
	suffix := ""
	if is.StateReason == "not_planned" {
		suffix = " (dropped)"
	}
	return "- ~~" + b.bulletInner(is) + "~~ Done " + date + " [`#" + strconv.Itoa(is.Number) + "`]" + suffix
}

// openByLetter is the open item bullets by type letter, each section sorted by
// issue number.
func (b *Issues) openByLetter() (map[string][]string, error) {
	items, err := b.openIssues()
	if err != nil {
		return nil, err
	}
	out := map[string][]string{"B": nil, "F": nil, "T": nil}
	for _, is := range items {
		l := b.Letter(is)
		out[l] = append(out[l], "- "+b.bulletInner(is))
	}
	return out, nil
}

// items returns the tracker's issues in state without the milestone trackers.
func (b *Issues) items(state string) ([]Issue, error) {
	issues, err := b.Tracker.List(b.ctx(), tracker.ListFilter{State: state})
	if err != nil {
		return nil, err
	}
	var items []Issue
	for _, is := range issues {
		if !b.IsMilestoneTracker(is) {
			items = append(items, is)
		}
	}
	return items, nil
}

// openIssues is the open items sorted by issue number.
func (b *Issues) openIssues() ([]Issue, error) {
	items, err := b.items("open")
	if err != nil {
		return nil, err
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Number < items[j].Number })
	return items, nil
}

// closedIssues is the closed items, newest first.
func (b *Issues) closedIssues() ([]Issue, error) {
	items, err := b.items("closed")
	if err != nil {
		return nil, err
	}
	// reverse=True with a stable sort keeps the input order of equal keys.
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].ClosedAt != items[j].ClosedAt {
			return items[i].ClosedAt > items[j].ClosedAt
		}
		return items[i].Number > items[j].Number
	})
	return items, nil
}

// closedLines is the Done lines of the closed items, newest first.
func (b *Issues) closedLines() ([]string, error) {
	items, err := b.closedIssues()
	if err != nil {
		return nil, err
	}
	out := make([]string, len(items))
	for i, is := range items {
		out[i] = b.doneLine(is)
	}
	return out, nil
}

var sectionForLetter = map[string]string{"B": "Bugs", "F": "Features", "T": "Tasks"}

// Markdown renders the backlog as BACKLOG.md-shaped text: the open issues by
// type, then the newest closedLimit closed ones as Done lines (all when
// closedLimit is negative, none when 0). The usual caller passes 20.
func (b *Issues) Markdown(closedLimit int) (string, error) {
	if b.Tracker == nil {
		return "", errors.New("issues backend has no tracker")
	}
	sections, err := b.openByLetter()
	if err != nil {
		return "", err
	}
	var done []string
	if closedLimit != 0 {
		if done, err = b.closedLines(); err != nil {
			return "", err
		}
		if closedLimit > 0 && len(done) > closedLimit {
			done = done[:closedLimit]
		}
	}
	return renderBacklog(sections, done), nil
}

// renderBacklog is _render_backlog: the open bullets by type letter, then the
// Done lines, as BACKLOG.md-shaped text.
func renderBacklog(sections map[string][]string, done []string) string {
	out := []string{"# Backlog", ""}
	for _, l := range ItemLetters {
		bullets := sections[string(l)]
		out = append(out, "## "+sectionForLetter[string(l)], "")
		out = append(out, bullets...)
		if len(bullets) > 0 {
			out = append(out, "")
		}
	}
	out = append(out, "## Completed", "")
	out = append(out, done...)
	return strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
}

// lookup finds the issue behind ref, or reports not found for a malformed
// reference, an absent issue, a milestone tracking issue or a type-letter
// mismatch (_lookup). Other tracker failures are returned as they are.
func (b *Issues) lookup(ref string) (Issue, bool, error) {
	if b.Tracker == nil {
		return Issue{}, false, errors.New("issues backend has no tracker")
	}
	n, letter, err := resolveItemRef(ref)
	if err != nil {
		return Issue{}, false, nil
	}
	is, err := b.Tracker.Get(b.ctx(), n, false)
	if tracker.IsKind(err, tracker.KindNotFound) {
		return Issue{}, false, nil
	}
	if err != nil {
		return Issue{}, false, err
	}
	if b.IsMilestoneTracker(is) || (letter != "" && letter != b.Letter(is)) {
		return Issue{}, false, nil
	}
	return is, true, nil
}

// Get returns the item behind ref ("12", "#12" or "F12"). The error wraps
// ErrNotFound when there is no such item. Fields.Detail is the issue URL.
func (b *Issues) Get(ref string) (*Item, error) {
	is, ok, err := b.lookup(ref)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, ref)
	}
	return b.item(is), nil
}

// List returns the open items by type (Bugs, Features, Tasks), each sorted by
// issue number, then, with includeClosed, the closed ones newest first: the
// order Markdown renders. Milestone tracking issues are not items. One tracker
// call per state.
func (b *Issues) List(includeClosed bool) ([]Item, error) {
	if b.Tracker == nil {
		return nil, errors.New("issues backend has no tracker")
	}
	open, err := b.openIssues()
	if err != nil {
		return nil, err
	}
	var out []Item
	for _, l := range ItemLetters {
		for _, is := range open {
			if b.Letter(is) == string(l) {
				out = append(out, *b.item(is))
			}
		}
	}
	if includeClosed {
		closed, err := b.closedIssues()
		if err != nil {
			return nil, err
		}
		for _, is := range closed {
			out = append(out, *b.item(is))
		}
	}
	return out, nil
}

// item builds the Item for an issue; Get and List share it.
func (b *Issues) item(is Issue) *Item {
	_, block, _ := ParseFieldsBlock(is.Body)
	letter := b.Letter(is)
	it := &Item{
		ID:     strconv.Itoa(is.Number),
		Type:   letter,
		Tag:    b.tag(is, letter),
		Title:  pystr.Strip(strings.TrimRight(oneLine(strings.ReplaceAll(is.Title, "*", "")), ".")),
		Closed: is.State == "closed",
		Number: is.Number,
		URL:    is.URL,
	}
	if it.Title == "" {
		it.Title = "(untitled)"
	}
	for _, f := range b.fields(is, block) {
		it.Fields.set(strings.ToLower(f.name), f.value)
	}
	it.Fields.Detail = is.URL
	if it.Closed {
		it.ClosedAt = closedDate(is)
		it.Reason = "done"
		if is.StateReason == "not_planned" {
			it.Reason = "dropped"
		}
		it.Line = b.doneLine(is)
	} else {
		it.Line = "- " + b.bulletInner(is)
	}
	return it
}

// Detail returns the issue body without its fields block; ok is false when
// the issue is unknown or the text is blank.
func (b *Issues) Detail(ref string) (string, bool, error) {
	is, ok, err := b.lookup(ref)
	if err != nil || !ok {
		return "", false, err
	}
	text, _, _ := ParseFieldsBlock(is.Body)
	if pystr.Strip(text) == "" {
		return "", false, nil
	}
	return text, true, nil
}

var (
	fieldLineRe = regexp.MustCompile(`\A([A-Za-z]+):[ \t]*(.*?)[ \t]*\z`)
)

// ParseFieldsBlock splits an issue body into its text and the trailing
// "<!-- rota:fields ... -->" comment (or the legacy hv:fields one), one
// "Name: value" per line (parse_fields_block). order lists the field names in
// block order; a name that repeats keeps its first position and its last
// value. Without a block the text is the body (CRLF turned into LF) and fields
// is empty.
func ParseFieldsBlock(body string) (text string, fields map[string]string, order []string) {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	fields = map[string]string{}
	text, block, ok := marker.SplitFields(body)
	if !ok {
		return body, fields, nil
	}
	for _, line := range strings.Split(block, "\n") {
		m := fieldLineRe.FindStringSubmatch(line)
		if m == nil || m[2] == "" {
			continue
		}
		if _, seen := fields[m[1]]; !seen {
			order = append(order, m[1])
		}
		fields[m[1]] = m[2]
	}
	return text, fields, order
}

// RenderFieldsBlock is the inverse of ParseFieldsBlock: it appends the block
// to text, writing the fields named in names (in that order) whose value is
// not blank once whitespace is collapsed (render_fields_block). With no such
// field the block is left out. It round-trips exactly for text without
// trailing newlines.
func RenderFieldsBlock(text string, names []string, values map[string]string) string {
	text = strings.TrimRight(text, "\n")
	var lines []string
	for _, k := range names {
		if v := oneLine(values[k]); v != "" {
			lines = append(lines, k+": "+v)
		}
	}
	if len(lines) == 0 {
		return text
	}
	block := marker.FieldsBlock(lines)
	if text == "" {
		return block
	}
	return text + "\n\n" + block
}
