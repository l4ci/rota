package ledger

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
)

// Outcomes of an issue row beyond merged-or-not.
const (
	OutcomeMerged      = "merged"
	OutcomeClosed      = "closed" // the losing attempt of a best-of:2 issue
	OutcomeTransferred = "transferred"
	OutcomeBlocked     = "blocked"
)

// IssueRow is one assignment: an issue on a slot. A best-of:2 issue has two
// rows, a transferred one a row per slot. A PR gated by hand that no slot
// owns has a row with no slot and, when the gate did not know it, no issue.
type IssueRow struct {
	Issue, Slot, Harness, Account, PR string
	AssignedAt, DoneAt, MergedAt      *time.Time
	// WallSeconds runs from the assignment to done, else to the merge; nil
	// when either end is unknown.
	WallSeconds *int
	Bounces     int
	// Gate is the verdict of the last gate run on the row's PR.
	Gate    string
	Outcome string
	Merged  bool
	// HeadroomStart and HeadroomEnd are the account meter's headroom
	// percentage at assign and at done; nil when the meter did not read one
	// (codex, an unknown meter).
	HeadroomStart, HeadroomEnd *float64
	// QuotaShare is the headroom points spent between the two readings. nil
	// is unknown, never 0: a missing reading, or a window that reset in
	// between (headroom rose), claims nothing. The account's meter is shared
	// by every slot on it, so this includes their work in the same window.
	QuotaShare *float64
}

// QuotaBurn returns the headroom points spent between two readings. A missing
// reading or rising headroom (a quota window reset) leaves the burn unknown.
func QuotaBurn(start, current *float64) *float64 {
	if start == nil || current == nil || *current > *start {
		return nil
	}
	burn := *start - *current
	return &burn
}

// Total sums rows by slot or by account.
type Total struct {
	Name        string
	Issues      int
	Merged      int
	Bounces     int
	WallSeconds int
	// QuotaShare sums the known shares; nil when none is known.
	QuotaShare *float64
}

// AuditRow is a line of the gate audit log inside the round's time window.
type AuditRow struct {
	TS                       time.Time
	Gate, Verb, Target, Note string
}

// Summary is one round folded from the ledger and the gate audit log.
type Summary struct {
	Round    int
	Issues   []IssueRow
	Slots    []Total
	Accounts []Total
	Audit    []AuditRow
	// CapReasons counts the bounce-cap transfers by reason.
	CapReasons CapReasons
	// Idle is each slot's wait between a done and its next assign.
	Idle    []IdleRow
	Metrics RoundMetrics
}

// IdleRow is one stretch a slot sat done before the orchestrator assigned it
// again: the feed rule of #29 says it is free the moment its PR is open.
type IdleRow struct {
	Slot           string
	DoneAt, NextAt time.Time
	IdleSeconds    int
}

// CapReasons counts transfers made at the bounce cap: AmbiguousCriteria went
// to the human after the criteria audit, Tier went to a stronger worker. Fresh
// counts the review-relay transfers to a fresh context at round.freshAt, below
// the cap.
type CapReasons struct{ AmbiguousCriteria, Tier, Fresh int }

// prNum is the number a PR reference ends in (#7, a /pull/7 URL), 0 for none.
func prNum(pr string) int {
	pr = strings.TrimRight(strings.TrimSpace(pr), "/")
	if i := strings.LastIndexAny(pr, "/#"); i >= 0 {
		pr = pr[i+1:]
	}
	n, _ := strconv.Atoi(pr)
	return n
}

type folder struct{ rows []*IssueRow }

// find resolves an entry to its row: the issue and slot, else the PR number,
// else the issue's first row, else the slot's latest row.
func (f *folder) find(e Entry) *IssueRow {
	if e.Issue != "" && e.Slot != "" {
		for _, r := range f.rows {
			if r.Issue == e.Issue && r.Slot == e.Slot {
				return r
			}
		}
	}
	if n := prNum(e.PR); n != 0 {
		for _, r := range f.rows {
			if prNum(r.PR) == n {
				return r
			}
		}
	}
	if e.Issue != "" && e.Slot == "" {
		for _, r := range f.rows {
			if r.Issue == e.Issue {
				return r
			}
		}
	}
	if e.Issue == "" && e.Slot != "" {
		for i := len(f.rows) - 1; i >= 0; i-- {
			if f.rows[i].Slot == e.Slot {
				return f.rows[i]
			}
		}
	}
	return nil
}

func (f *folder) row(e Entry) *IssueRow {
	if r := f.find(e); r != nil {
		return r
	}
	r := &IssueRow{Issue: e.Issue, Slot: e.Slot, Harness: e.Harness, Account: e.Account, PR: e.PR}
	f.rows = append(f.rows, r)
	return r
}

// Fold builds the summary of one round: the ledger entries of that round
// (round 0 is a solo round with no lease) and the audit lines that fall inside
// the time span its entries cover.
func Fold(entries []Entry, audit []*jsonx.Object, round int) Summary {
	s := Summary{Round: round}
	f := &folder{}
	var first, last time.Time
	doneAt := map[string]time.Time{} // slot -> its first done since the last assign
	for _, e := range entries {
		if e.Round != round {
			continue
		}
		switch {
		case e.Kind == KindDone && e.Slot != "" && !e.TS.IsZero():
			if _, open := doneAt[e.Slot]; !open { // a re-done after a bounce is not idle time
				doneAt[e.Slot] = e.TS
			}
		case e.Kind == KindAssign && e.Slot != "":
			if d, open := doneAt[e.Slot]; open && !e.TS.IsZero() {
				s.Idle = append(s.Idle, IdleRow{Slot: e.Slot, DoneAt: d, NextAt: e.TS, IdleSeconds: max(0, int(e.TS.Sub(d).Seconds()))})
			}
			delete(doneAt, e.Slot)
		}
		if !e.TS.IsZero() {
			if first.IsZero() || e.TS.Before(first) {
				first = e.TS
			}
			if e.TS.After(last) {
				last = e.TS
			}
		}
		ts := e.TS
		if e.Kind == KindTrain {
			continue // a train outcome has no issue or slot of its own
		}
		if e.Kind == KindBounce && e.Slot == "" && f.find(e) == nil {
			continue // no owner: a row would total under "-"
		}
		r := f.row(e)
		if r.Harness == "" {
			r.Harness = e.Harness
		}
		if r.Account == "" {
			r.Account = e.Account
		}
		if r.PR == "" {
			r.PR = e.PR
		}
		switch e.Kind {
		case KindAssign:
			if r.AssignedAt == nil {
				r.AssignedAt = &ts
				r.HeadroomStart = floatPtr(e, "headroom")
			}
		case KindDone:
			r.DoneAt = &ts
			r.HeadroomEnd = floatPtr(e, "headroom")
			r.Outcome = ""
		case KindBlocked:
			if !r.Merged {
				r.Outcome = OutcomeBlocked
			}
		case KindBounce:
			r.Bounces++
		case KindTransfer:
			switch e.DetailStr("capReason") {
			case CapAmbiguousCriteria:
				s.CapReasons.AmbiguousCriteria++
			case CapTier:
				s.CapReasons.Tier++
			case CapFresh:
				s.CapReasons.Fresh++
			}
			if from := e.DetailStr("from"); from != "" {
				for _, o := range f.rows {
					if o.Issue == e.Issue && o.Slot == from && !o.Merged {
						o.Outcome = OutcomeTransferred
					}
				}
			}
			if r.AssignedAt == nil {
				r.AssignedAt = &ts
			}
		case KindPick:
			for _, o := range f.rows {
				if o.Issue == e.Issue && o != r && !o.Merged {
					o.Outcome = OutcomeClosed
				}
			}
		case KindPark:
			if e.Detail != nil && jsonx.Bool(e.Detail, "merged") {
				r.Merged, r.Outcome = true, OutcomeMerged
			}
		case KindGate:
			v := e.DetailStr("verdict")
			if v != "" || jsonx.Bool(e.Detail, "train") { // a train member that passed has none, and drops an earlier gate's
				r.Gate = v
			}
		case KindMerge:
			r.Merged, r.Outcome = true, OutcomeMerged
			if r.MergedAt == nil {
				r.MergedAt = &ts // a repeated observation does not extend lead time
			}
		}
	}
	for _, r := range f.rows {
		end := r.DoneAt
		if end == nil {
			end = r.MergedAt
		}
		if r.AssignedAt != nil && end != nil {
			w := int(end.Sub(*r.AssignedAt).Seconds())
			r.WallSeconds = &w
		}
		r.QuotaShare = QuotaBurn(r.HeadroomStart, r.HeadroomEnd)
		s.Issues = append(s.Issues, *r)
	}
	s.Slots = totals(s.Issues, func(r IssueRow) string { return r.Slot })
	s.Accounts = totals(s.Issues, func(r IssueRow) string { return r.Account })
	s.Metrics = roundMetrics(s.Issues, entries, round)
	for _, a := range audit {
		ts, err := time.Parse(time.RFC3339, jsonx.Str(a, "ts"))
		if err != nil || first.IsZero() || ts.Before(first) || ts.After(last) {
			continue
		}
		s.Audit = append(s.Audit, AuditRow{TS: ts, Gate: jsonx.Str(a, "gate"), Verb: jsonx.Str(a, "verb"), Target: jsonx.Str(a, "target"), Note: jsonx.Str(a, "note")})
	}
	sort.SliceStable(s.Audit, func(i, j int) bool {
		if s.Audit[i].Target != s.Audit[j].Target {
			return s.Audit[i].Target < s.Audit[j].Target
		}
		return s.Audit[i].TS.Before(s.Audit[j].TS)
	})
	return s
}

func floatPtr(e Entry, key string) *float64 {
	if v, ok := e.DetailFloat(key); ok {
		return &v
	}
	return nil
}

func totals(rows []IssueRow, key func(IssueRow) string) []Total {
	var out []Total
	idx := map[string]int{}
	for _, r := range rows {
		k := key(r)
		if k == "" {
			k = "-"
		}
		i, ok := idx[k]
		if !ok {
			i = len(out)
			idx[k] = i
			out = append(out, Total{Name: k})
		}
		t := &out[i]
		t.Issues++
		t.Bounces += r.Bounces
		if r.Merged {
			t.Merged++
		}
		if r.WallSeconds != nil {
			t.WallSeconds += *r.WallSeconds
		}
		if r.QuotaShare != nil {
			sum := *r.QuotaShare
			if t.QuotaShare != nil {
				sum += *t.QuotaShare
			}
			t.QuotaShare = &sum
		}
	}
	return out
}

// ---- rendering --------------------------------------------------------------

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func clock(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.UTC().Format("01-02 15:04")
}

func wall(sec *int) string {
	if sec == nil {
		return "-"
	}
	return dur(*sec)
}

func dur(sec int) string {
	d := time.Duration(sec) * time.Second
	if d >= time.Hour {
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), sec%60)
}

// share renders a quota share; unknown is n/a, never 0.
func share(p *float64) string {
	if p == nil {
		return "n/a"
	}
	return strconv.FormatFloat(*p, 'f', -1, 64) + "%"
}

func gateOutcome(r IssueRow) string {
	switch {
	case r.Outcome == OutcomeClosed, r.Outcome == OutcomeTransferred, r.Outcome == OutcomeBlocked:
		return r.Outcome
	case r.Merged:
		return "merged"
	}
	return "no"
}

// Text is the table `rota round summary` prints: a row per issue, totals per
// slot and per account, then the gate audit timeline by PR.
func (s Summary) Text() string {
	var b strings.Builder
	title := fmt.Sprintf("Round %d", s.Round)
	if s.Round == 0 {
		title += " (no round lease)"
	}
	fmt.Fprintln(&b, title)
	fmt.Fprint(&b, s.Metrics.text())
	if len(s.Issues) == 0 {
		fmt.Fprintln(&b, "no entries for this round")
		return b.String()
	}
	tw := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ISSUE\tSLOT\tHARNESS\tACCOUNT\tPR\tASSIGNED\tDONE\tWALL\tBOUNCES\tGATE\tMERGED\tQUOTA SHARE")
	for _, r := range s.Issues {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\t%s\n", orDash(r.Issue), orDash(r.Slot), orDash(r.Harness), orDash(r.Account),
			orDash(r.PR), clock(r.AssignedAt), clock(r.DoneAt), wall(r.WallSeconds), r.Bounces, orDash(r.Gate), gateOutcome(r), share(r.QuotaShare))
	}
	tw.Flush()
	fmt.Fprintln(&b, "\nquota share is the account headroom points spent between assign and done; n/a when no meter reads it (codex, unknown).")
	for _, g := range []struct {
		name string
		rows []Total
	}{{"Slots", s.Slots}, {"Accounts", s.Accounts}} {
		fmt.Fprintf(&b, "\n%s\n", g.name)
		tw = tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tISSUES\tMERGED\tBOUNCES\tWALL\tQUOTA SHARE")
		for _, t := range g.rows {
			fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%s\t%s\n", t.Name, t.Issues, t.Merged, t.Bounces, dur(t.WallSeconds), share(t.QuotaShare))
		}
		tw.Flush()
	}
	if c := s.CapReasons; c.AmbiguousCriteria+c.Tier+c.Fresh > 0 {
		fmt.Fprintf(&b, "\nBounce cap: %d ambiguous-criteria, %d tier, %d fresh\n", c.AmbiguousCriteria, c.Tier, c.Fresh)
	}
	if len(s.Idle) > 0 {
		fmt.Fprintf(&b, "\nIdle between done and next assign (median %s)\n", mins(s.medianIdle()))
		tw = tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "SLOT\tDONE\tNEXT ASSIGN\tIDLE")
		for _, r := range s.Idle {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Slot, clock(&r.DoneAt), clock(&r.NextAt), mins(r.IdleSeconds))
		}
		tw.Flush()
	}
	fmt.Fprintln(&b, "\nGate audit")
	if len(s.Audit) == 0 {
		fmt.Fprintln(&b, "none")
		return b.String()
	}
	tw = tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	for _, a := range s.Audit {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", orDash(a.Target), a.TS.UTC().Format("01-02 15:04"), a.Gate, a.Verb, a.Note)
	}
	tw.Flush()
	return b.String()
}

func mins(sec int) string { return fmt.Sprintf("%d min", (sec+30)/60) }

func (s Summary) medianIdle() int {
	secs := make([]int, len(s.Idle))
	for i, r := range s.Idle {
		secs[i] = r.IdleSeconds
	}
	sort.Ints(secs)
	return secs[len(secs)/2]
}

// ---- JSON -------------------------------------------------------------------

func timeVal(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

func floatVal(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

// Object is the --json shape: {round, issues[], slots[], accounts[], audit[], capReasons, metrics}.
// Unknown values (times, headroom, quotaShare, wallSeconds) are null.
func (s Summary) Object() *jsonx.Object {
	issues := make([]any, 0, len(s.Issues))
	for _, r := range s.Issues {
		o := jsonx.NewObject()
		o.Set("issue", r.Issue)
		o.Set("slot", r.Slot)
		o.Set("harness", r.Harness)
		o.Set("account", r.Account)
		o.Set("pr", r.PR)
		o.Set("assignedAt", timeVal(r.AssignedAt))
		o.Set("doneAt", timeVal(r.DoneAt))
		o.Set("mergedAt", timeVal(r.MergedAt))
		if r.WallSeconds == nil {
			o.Set("wallSeconds", nil)
		} else {
			o.Set("wallSeconds", *r.WallSeconds)
		}
		o.Set("bounces", r.Bounces)
		o.Set("gate", r.Gate)
		o.Set("outcome", r.Outcome)
		o.Set("merged", r.Merged)
		o.Set("headroomStart", floatVal(r.HeadroomStart))
		o.Set("headroomEnd", floatVal(r.HeadroomEnd))
		o.Set("quotaShare", floatVal(r.QuotaShare))
		issues = append(issues, o)
	}
	tot := func(list []Total) []any {
		out := make([]any, 0, len(list))
		for _, t := range list {
			o := jsonx.NewObject()
			o.Set("name", t.Name)
			o.Set("issues", t.Issues)
			o.Set("merged", t.Merged)
			o.Set("bounces", t.Bounces)
			o.Set("wallSeconds", t.WallSeconds)
			o.Set("quotaShare", floatVal(t.QuotaShare))
			out = append(out, o)
		}
		return out
	}
	audit := make([]any, 0, len(s.Audit))
	for _, a := range s.Audit {
		o := jsonx.NewObject()
		o.Set("ts", a.TS.UTC().Format(time.RFC3339))
		o.Set("gate", a.Gate)
		o.Set("verb", a.Verb)
		o.Set("target", a.Target)
		o.Set("note", a.Note)
		audit = append(audit, o)
	}
	d := jsonx.NewObject()
	d.Set("round", s.Round)
	d.Set("issues", issues)
	d.Set("slots", tot(s.Slots))
	d.Set("accounts", tot(s.Accounts))
	d.Set("audit", audit)
	idle := make([]any, 0, len(s.Idle))
	for _, r := range s.Idle {
		o := jsonx.NewObject()
		o.Set("slot", r.Slot)
		o.Set("doneAt", r.DoneAt.UTC().Format(time.RFC3339))
		o.Set("nextAssignAt", r.NextAt.UTC().Format(time.RFC3339))
		o.Set("idleSeconds", r.IdleSeconds)
		idle = append(idle, o)
	}
	d.Set("idle", idle)
	cap := jsonx.NewObject()
	cap.Set(CapAmbiguousCriteria, s.CapReasons.AmbiguousCriteria)
	cap.Set(CapTier, s.CapReasons.Tier)
	cap.Set(CapFresh, s.CapReasons.Fresh)
	d.Set("capReasons", cap)
	d.Set("metrics", s.Metrics.object())
	return d
}
