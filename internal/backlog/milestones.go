package backlog

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/frontmatter"
	ms "github.com/l4ci/rota/internal/milestone"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/tracker"
)

// Milestones in issue mode (hvlib_backend.IssueBackend's milestone block): a
// milestone is a native milestone plus a tracking issue labelled
// milestone-tracker, whose body is the milestone plan, whose status:<s>
// label is the milestone status, and whose comments carry the slice plans
// (`plan:S<NN>` notes). rota milestone and rota plan use this, and so does the
// issue migration.

// MilestoneTracker is the part of tracker.Adapter the milestone calls need on
// top of Tracker: the native milestones.
type MilestoneTracker interface {
	Tracker
	Milestones(ctx context.Context, state string) ([]tracker.Milestone, error)
	CreateMilestone(ctx context.Context, title, description string) (int, error)
	EditMilestone(ctx context.Context, number int, e tracker.MilestoneEdit) error
}

// ErrMilestoneText is wrapped by MilestonePut when the text lacks frontmatter
// with `id: <mid>`.
var ErrMilestoneText = errors.New("milestone text needs frontmatter with its id")

// MilestoneStatuses are the legal milestone statuses.
var MilestoneStatuses = []string{"planned", "active", "shipped", "archived"}

const msStatusPrefix = "status:"

var (
	msTitleRe    = regexp.MustCompile(`\A(M\p{Nd}+)`)
	frontmatterM = regexp.MustCompile(`(?s)\A---\n(.*?)\n---[ \t]*(?:\n|\z)`)
	fmIDRe       = regexp.MustCompile(`(?m)^id:[ \t]*(\S+)`)
	fmDependsRe  = regexp.MustCompile(`(?m)^depends:[ \t]*(.*)$`)
	msAllRe      = regexp.MustCompile(`M\p{Nd}+`)
	msRestRe     = regexp.MustCompile(`\A[` + pystr.SpaceClass + `]*[\x{2014}\x{2013}-][` + pystr.SpaceClass + `]*(.*)`)
	sliceUnitRe  = regexp.MustCompile(`\Aplan:(S\p{Nd}+)\z`)
)

func (b *Issues) warn(msg string) {
	if b.Warn != nil {
		b.Warn(msg)
	}
}

func (b *Issues) milestoneTracker() (MilestoneTracker, error) {
	return capability[MilestoneTracker](b, noMilestoneSupport)
}

// parseMSTitle splits "M02 — Sharing" into "M02" and "Sharing". The ID must
// not run into a word character; rest is "" when the title has no dash part
// (_MS_TITLE_RE).
func parseMSTitle(title string) (id, rest string, ok bool) {
	t := pystr.Strip(title)
	loc := msTitleRe.FindStringSubmatchIndex(t)
	if loc == nil {
		return "", "", false
	}
	end := loc[3]
	if end < len(t) {
		if r, _ := utf8.DecodeRuneInString(t[end:]); pystr.IsWord(r) {
			return "", "", false
		}
	}
	if m := msRestRe.FindStringSubmatch(t[end:]); m != nil {
		rest = m[1]
	}
	return t[:end], rest, true
}

// trackingIssues is {MNN: issue} for every tracking issue, open and closed.
// The lowest open number wins per MNN (the lowest closed when none is open);
// duplicates are reported through Warn.
func (b *Issues) trackingIssues() (map[string]Issue, error) {
	tr, err := b.tracker()
	if err != nil {
		return nil, err
	}
	label := config.Label(b.Cfg, "milestoneTracker")
	list, err := tr.List(b.ctx(), tracker.ListFilter{State: "all", Labels: []string{label}})
	if err != nil {
		return nil, err
	}
	groups := map[string][]Issue{}
	var order []string
	for _, is := range list {
		if id, _, ok := parseMSTitle(is.Title); ok {
			if _, seen := groups[id]; !seen {
				order = append(order, id)
			}
			groups[id] = append(groups[id], is)
		}
	}
	out := map[string]Issue{}
	for _, mid := range order {
		g := groups[mid]
		sort.SliceStable(g, func(i, j int) bool {
			if (g[i].State != "open") != (g[j].State != "open") {
				return g[j].State != "open"
			}
			return g[i].Number < g[j].Number
		})
		out[mid] = g[0]
		if len(g) > 1 {
			nums := make([]int, len(g))
			for i, is := range g {
				nums[i] = is.Number
			}
			sort.Ints(nums)
			var parts []string
			for _, n := range nums {
				parts = append(parts, "#"+strconv.Itoa(n))
			}
			b.warn(fmt.Sprintf("%d tracking issues carry %s (%s); using #%d", len(g), mid, strings.Join(parts, ", "), g[0].Number))
		}
	}
	return out, nil
}

// TrackerIssue is the tracking issue of milestone mid; the error wraps
// ErrNotFound when there is none.
func (b *Issues) TrackerIssue(mid string) (Issue, error) {
	all, err := b.trackingIssues()
	if err != nil {
		return Issue{}, err
	}
	is, ok := all[mid]
	if !ok {
		return Issue{}, errf(ErrNotFound, "milestone %s not found on the issue tracker", mid)
	}
	return is, nil
}

// NextMilestoneID is MNN with NN one above the highest M<digits> title prefix
// over every native milestone.
func (b *Issues) NextMilestoneID() (string, error) {
	mt, err := b.milestoneTracker()
	if err != nil {
		return "", err
	}
	found, err := mt.Milestones(b.ctx(), "all")
	if err != nil {
		return "", err
	}
	highest := 0
	for _, nm := range found {
		if id, _, ok := parseMSTitle(nm.Title); ok {
			if n, err := Atoi(id[1:]); err == nil {
				highest = max(highest, n)
			}
		}
	}
	return fmt.Sprintf("M%02d", highest+1), nil
}

// MilestoneAdd creates the native milestone and its tracking issue and
// returns the ID. An empty mid mints the next one.
func (b *Issues) MilestoneAdd(mid, title, summary string, depends []string, today string) (string, error) {
	mt, err := b.milestoneTracker()
	if err != nil {
		return "", err
	}
	if mid == "" {
		if mid, err = b.NextMilestoneID(); err != nil {
			return "", err
		}
	}
	native := mid + " — " + title
	labels := []string{config.Label(b.Cfg, "milestoneTracker"), msStatusPrefix + "planned"}
	if err := mt.EnsureLabels(b.ctx(), labels, b.AutoCreate()); err != nil {
		return "", err
	}
	if _, err := mt.CreateMilestone(b.ctx(), native, oneLine(summary)); err != nil {
		return "", err
	}
	body := RenderFieldsBlock(ms.StubOn(mid, title, summary, depends, today),
		[]string{"Depends"}, map[string]string{"Depends": strings.Join(depends, ", ")})
	if _, err := mt.Create(b.ctx(), native, body, labels, native); err != nil {
		return "", err
	}
	return mid, nil
}

func msStatusOf(is Issue) string {
	for _, l := range is.Labels {
		if strings.HasPrefix(l, msStatusPrefix) && has(MilestoneStatuses, l[len(msStatusPrefix):]) {
			return l[len(msStatusPrefix):]
		}
	}
	if is.State == "closed" {
		if is.StateReason == "not_planned" {
			return "archived"
		}
		return "shipped"
	}
	return "planned"
}

// MilestoneList is every milestone in ID order. ready is true when each
// dependency is shipped.
func (b *Issues) MilestoneList() ([]ms.Entry, error) {
	all, err := b.trackingIssues()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(all))
	for id := range all {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	rows := []ms.Entry{}
	shipped := map[string]bool{}
	for _, id := range ids {
		is := all[id]
		_, block, _ := ParseFieldsBlock(is.Body)
		_, rest, _ := parseMSTitle(is.Title)
		title := rest
		if title == "" {
			title = is.Title
		}
		r := ms.Entry{ID: id, Title: pystr.Strip(title), Status: msStatusOf(is), Depends: msAllRe.FindAllString(block["Depends"], -1)}
		if r.Depends == nil {
			r.Depends = []string{}
		}
		if r.Status == "shipped" {
			shipped[id] = true
		}
		rows = append(rows, r)
	}
	for i := range rows {
		rows[i].Ready = true
		for _, d := range rows[i].Depends {
			if !shipped[d] {
				rows[i].Ready = false
			}
		}
	}
	return rows, nil
}

// MilestoneShow is the milestone plan text: the issue body without its
// fields block, trailing newlines trimmed.
func (b *Issues) MilestoneShow(mid string) (string, error) {
	is, err := b.TrackerIssue(mid)
	if err != nil {
		return "", err
	}
	text, _, _ := ParseFieldsBlock(is.Body)
	return strings.TrimRight(text, "\n"), nil
}

// MilestoneStatus moves milestone mid to planned, active, shipped or
// archived: it swaps the status label, syncs the body frontmatter, closes
// (shipped: completed, archived: not planned) or reopens the tracking issue,
// and does the same to the native milestone.
func (b *Issues) MilestoneStatus(mid, status string) error {
	if !has(MilestoneStatuses, status) {
		return errf(ErrInvalid, "status must be one of: %s", strings.Join(MilestoneStatuses, " "))
	}
	mt, err := b.milestoneTracker()
	if err != nil {
		return err
	}
	is, err := b.TrackerIssue(mid)
	if err != nil {
		return err
	}
	n := is.Number
	label := msStatusPrefix + status
	var stale []string
	for _, l := range is.Labels {
		if strings.HasPrefix(l, msStatusPrefix) && l != label {
			stale = append(stale, l)
		}
	}
	if !has(is.Labels, label) {
		if err := mt.AddLabels(b.ctx(), n, []string{label}, b.AutoCreate()); err != nil {
			return err
		}
	}
	if len(stale) > 0 {
		if err := mt.RemoveLabels(b.ctx(), n, stale); err != nil {
			return err
		}
	}
	text, block, order := ParseFieldsBlock(is.Body)
	if newText, _ := frontmatter.UpdateField(text, "status", status); newText != text {
		body := RenderFieldsBlock(newText, order, block)
		if err := mt.Edit(b.ctx(), n, tracker.IssueEdit{Body: &body}); err != nil {
			return err
		}
	}
	wantClosed := status == "shipped" || status == "archived"
	reason := "completed"
	if status == "archived" {
		reason = "not_planned"
	}
	if is.State == "closed" && (!wantClosed || is.StateReason != reason) {
		if err := mt.Reopen(b.ctx(), n); err != nil {
			return err
		}
		is.State = "open"
	}
	if wantClosed && is.State == "open" {
		if err := mt.Close(b.ctx(), n, reason, ""); err != nil {
			return err
		}
	}
	nm, err := b.nativeMilestone(mt, mid, is)
	if err != nil {
		return err
	}
	want := "open"
	if wantClosed {
		want = "closed"
	}
	if nm != nil && nm.State != want {
		return mt.EditMilestone(b.ctx(), nm.Number, tracker.MilestoneEdit{State: &want})
	}
	return nil
}

// nativeMilestone is the native milestone of mid: the issue's own, else the
// one whose title starts with mid, open ones first.
func (b *Issues) nativeMilestone(mt MilestoneTracker, mid string, is Issue) (*tracker.Milestone, error) {
	found, err := mt.Milestones(b.ctx(), "all")
	if err != nil {
		return nil, err
	}
	for i := range found {
		if is.Milestone != "" && found[i].Title == is.Milestone {
			return &found[i], nil
		}
	}
	var hits []tracker.Milestone
	for _, nm := range found {
		t := pystr.Strip(nm.Title)
		if strings.HasPrefix(t, mid) {
			if r, _ := utf8.DecodeRuneInString(t[len(mid):]); t[len(mid):] == "" || !pystr.IsWord(r) {
				hits = append(hits, nm)
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].State != "closed" && hits[j].State == "closed" })
	if len(hits) == 0 {
		return nil, nil
	}
	return &hits[0], nil
}

// MilestonePut replaces the milestone plan text of the tracking issue. The
// text must carry frontmatter with `id: <mid>` (else ErrMilestoneText);
// `status` follows the label and `depends`, when present, updates the
// Depends field.
func (b *Issues) MilestonePut(mid, text string) error {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	fm := frontmatterM.FindStringSubmatch(text)
	var idm []string
	if fm != nil {
		idm = fmIDRe.FindStringSubmatch(fm[1])
	}
	if idm == nil || idm[1] != mid {
		return errf(ErrMilestoneText, "milestone text needs frontmatter with 'id: %s'", mid)
	}
	is, err := b.TrackerIssue(mid)
	if err != nil {
		return err
	}
	mt, err := b.milestoneTracker()
	if err != nil {
		return err
	}
	_, block, order := ParseFieldsBlock(is.Body)
	if dm := fmDependsRe.FindStringSubmatch(fm[1]); dm != nil {
		if _, ok := block["Depends"]; !ok {
			order = append(order, "Depends")
		}
		block["Depends"] = strings.Join(msAllRe.FindAllString(dm[1], -1), ", ")
	}
	text, _ = frontmatter.UpdateField(text, "status", msStatusOf(is))
	body := RenderFieldsBlock(text, order, block)
	return mt.Edit(b.ctx(), is.Number, tracker.IssueEdit{Body: &body})
}

// ---- slice plans: plan:S<NN> notes on the tracking issue ---------------------

// SlicePlan is one slice plan note.
type SlicePlan struct{ Milestone, Unit, Text string }

func (b *Issues) trackingNumber(mid string) (string, error) {
	is, err := b.TrackerIssue(mid)
	return strconv.Itoa(is.Number), err
}

type slicePart struct {
	idx  int
	id   string
	rest string
}

// slicePartsOf groups the slice-plan comments of issue n by unit.
func (b *Issues) slicePartsOf(n int) (map[string][]slicePart, error) {
	tr, err := b.tracker()
	if err != nil {
		return nil, err
	}
	comments, err := tr.Comments(b.ctx(), n)
	if err != nil {
		return nil, err
	}
	parts := map[string][]slicePart{}
	for _, c := range comments {
		body := strings.ReplaceAll(c.Body, "\r\n", "\n")
		m := markerRe.FindStringSubmatchIndex(body)
		if m == nil {
			continue
		}
		sm := sliceUnitRe.FindStringSubmatch(body[m[2]:m[3]])
		if sm == nil {
			continue
		}
		idx := 1
		if m[4] >= 0 {
			if idx, err = Atoi(body[m[4]:m[5]]); err != nil {
				return nil, err
			}
		}
		parts[sm[1]] = append(parts[sm[1]], slicePart{idx, c.ID, body[m[1]:]})
	}
	return parts, nil
}

func unitLess(a, b string) bool {
	x, ea := Atoi(a[1:])
	y, eb := Atoi(b[1:])
	if ea == nil && eb == nil && x != y {
		return x < y
	}
	return a < b
}

// SliceUnits is the sorted SNN units that have a plan note on the tracking
// issue of mid.
func (b *Issues) SliceUnits(mid string) ([]string, error) {
	is, err := b.TrackerIssue(mid)
	if err != nil {
		return nil, err
	}
	parts, err := b.slicePartsOf(is.Number)
	if err != nil {
		return nil, err
	}
	units := make([]string, 0, len(parts))
	for u := range parts {
		units = append(units, u)
	}
	sort.Slice(units, func(i, j int) bool { return unitLess(units[i], units[j]) })
	return units, nil
}

// SlicePlans is every slice plan note, ordered by milestone then unit. mid ""
// lists every milestone; a milestone with no tracking issue is skipped.
func (b *Issues) SlicePlans(mid string) ([]SlicePlan, error) {
	all, err := b.trackingIssues()
	if err != nil {
		return nil, err
	}
	var ms []string
	if mid == "" {
		for id := range all {
			ms = append(ms, id)
		}
		sort.Strings(ms)
	} else {
		ms = []string{mid}
	}
	out := []SlicePlan{}
	for _, m := range ms {
		is, ok := all[m]
		if !ok {
			continue
		}
		parts, err := b.slicePartsOf(is.Number)
		if err != nil {
			return nil, err
		}
		units := make([]string, 0, len(parts))
		for u := range parts {
			units = append(units, u)
		}
		sort.Slice(units, func(i, j int) bool { return unitLess(units[i], units[j]) })
		for _, u := range units {
			ps := parts[u]
			sort.SliceStable(ps, func(i, j int) bool {
				if ps[i].idx != ps[j].idx {
					return ps[i].idx < ps[j].idx
				}
				return idLess(ps[i].id, ps[j].id)
			})
			var text strings.Builder
			for _, p := range ps {
				text.WriteString(p.rest)
			}
			out = append(out, SlicePlan{m, u, noteNorm(text.String())})
		}
	}
	return out, nil
}

// SliceGet is the slice plan text; ok is false when there is none.
func (b *Issues) SliceGet(mid, unit string) (string, bool, error) {
	n, err := b.trackingNumber(mid)
	if err != nil {
		return "", false, err
	}
	return b.NoteGet(n, "plan:"+unit)
}

// SlicePut writes the slice plan note; false when it already read as text.
func (b *Issues) SlicePut(mid, unit, text string) (bool, error) {
	n, err := b.trackingNumber(mid)
	if err != nil {
		return false, err
	}
	return b.NotePut(n, "plan:"+unit, text)
}

// SliceRm deletes the slice plan note; false when there was none.
func (b *Issues) SliceRm(mid, unit string) (bool, error) {
	n, err := b.trackingNumber(mid)
	if err != nil {
		return false, err
	}
	return b.NoteRm(n, "plan:"+unit)
}
