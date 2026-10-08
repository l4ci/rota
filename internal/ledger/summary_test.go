package ledger

import (
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
)

var t0 = time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)

func at(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }

func hr(v float64) *jsonx.Object { return Detail("headroom", v) }

func row(t *testing.T, s Summary, issue, slot string) IssueRow {
	t.Helper()
	for _, r := range s.Issues {
		if r.Issue == issue && r.Slot == slot {
			return r
		}
	}
	t.Fatalf("no row %s/%s in %+v", issue, slot, s.Issues)
	return IssueRow{}
}

func TestSummaryRowAndTotals(t *testing.T) {
	es := []Entry{
		{TS: at(0), Kind: KindAssign, Round: 2, Issue: "12", Slot: "ben", Account: "work", Harness: "claude", Detail: hr(80)},
		{TS: at(1), Kind: KindAssign, Round: 2, Issue: "13", Slot: "dana", Account: "work", Harness: "claude", Detail: hr(78)},
		{TS: at(5), Kind: KindBounce, Round: 2, Issue: "12"},
		{TS: at(30), Kind: KindDone, Round: 2, Issue: "12", Slot: "ben", Account: "work", PR: "https://h/o/r/pull/7", Detail: hr(50)},
		{TS: at(31), Kind: KindGate, Round: 2, Issue: "12", Slot: "ben", PR: "https://h/o/r/pull/7", Detail: Detail("verdict", "pass")},
		{TS: at(31), Kind: KindMerge, Round: 2, Issue: "12", Slot: "ben", PR: "https://h/o/r/pull/7"},
		{TS: at(40), Kind: KindAssign, Round: 1, Issue: "99", Slot: "kit"}, // another round
	}
	s := Fold(es, nil, 2)
	if s.Round != 2 || len(s.Issues) != 2 {
		t.Fatalf("%+v", s)
	}
	r := row(t, s, "12", "ben")
	if r.Bounces != 1 || r.Gate != "pass" || !r.Merged || r.Outcome != OutcomeMerged || r.PR != "https://h/o/r/pull/7" {
		t.Errorf("row = %+v", r)
	}
	if r.WallSeconds == nil || *r.WallSeconds != 30*60 {
		t.Errorf("wall = %v", r.WallSeconds)
	}
	if r.QuotaShare == nil || *r.QuotaShare != 30 {
		t.Errorf("quota share = %v, want 30", r.QuotaShare)
	}
	open := row(t, s, "13", "dana")
	if open.Merged || open.DoneAt != nil || open.WallSeconds != nil || open.QuotaShare != nil {
		t.Errorf("open row = %+v", open)
	}
	if len(s.Slots) != 2 || len(s.Accounts) != 1 {
		t.Fatalf("slots %+v accounts %+v", s.Slots, s.Accounts)
	}
	a := s.Accounts[0]
	if a.Name != "work" || a.Issues != 2 || a.Merged != 1 || a.Bounces != 1 || a.QuotaShare == nil || *a.QuotaShare != 30 || a.WallSeconds != 30*60 {
		t.Errorf("account = %+v", a)
	}
}

func TestSummaryQuotaUnknownIsNeverZero(t *testing.T) {
	es := []Entry{
		{TS: at(0), Kind: KindAssign, Round: 1, Issue: "5", Slot: "ben", Harness: "codex"},
		{TS: at(9), Kind: KindDone, Round: 1, Issue: "5", Slot: "ben"},
		{TS: at(0), Kind: KindAssign, Round: 1, Issue: "6", Slot: "dana", Account: "a", Detail: hr(40)},
		{TS: at(9), Kind: KindDone, Round: 1, Issue: "6", Slot: "dana"}, // no end reading
		{TS: at(0), Kind: KindAssign, Round: 1, Issue: "7", Slot: "kit", Account: "a", Detail: hr(40)},
		{TS: at(9), Kind: KindDone, Round: 1, Issue: "7", Slot: "kit", Detail: hr(70)}, // window reset: not a spend
	}
	s := Fold(es, nil, 1)
	for _, k := range [][2]string{{"5", "ben"}, {"6", "dana"}, {"7", "kit"}} {
		if r := row(t, s, k[0], k[1]); r.QuotaShare != nil {
			t.Errorf("%v: quota share %v, want unknown", k, *r.QuotaShare)
		}
	}
	if !strings.Contains(s.Text(), "n/a") || strings.Contains(s.Text(), "0%") {
		t.Errorf("text must say n/a and never 0:\n%s", s.Text())
	}
	for _, a := range s.Accounts {
		if a.QuotaShare != nil {
			t.Errorf("account %s share %v", a.Name, *a.QuotaShare)
		}
	}
}

func TestSummaryNoLease(t *testing.T) {
	es := []Entry{
		{TS: at(0), Kind: KindAssign, Round: 0, Issue: "3", Slot: "ben", Harness: "claude"},
		{TS: at(4), Kind: KindDone, Round: 0, Issue: "3", Slot: "ben"},
	}
	s := Fold(es, nil, 0)
	if s.Round != 0 || len(s.Issues) != 1 || s.Issues[0].DoneAt == nil {
		t.Fatalf("%+v", s)
	}
	if !strings.HasPrefix(s.Text(), "Round 0") && !strings.Contains(s.Text(), "no round") {
		t.Errorf("heading: %s", s.Text())
	}
}

func TestSummaryBestOf(t *testing.T) {
	es := []Entry{
		{TS: at(0), Kind: KindAssign, Round: 1, Issue: "12", Slot: "ben", Account: "a"},
		{TS: at(0), Kind: KindAssign, Round: 1, Issue: "12", Slot: "dana", Account: "b"},
		{TS: at(20), Kind: KindDone, Round: 1, Issue: "12", Slot: "ben", PR: "#7"},
		{TS: at(22), Kind: KindDone, Round: 1, Issue: "12", Slot: "dana", PR: "#8"},
		{TS: at(25), Kind: KindPick, Round: 1, Issue: "12", Slot: "ben", PR: "#7", Detail: Detail("loser", "dana")},
		{TS: at(26), Kind: KindGate, Round: 1, Issue: "12", Slot: "ben", PR: "#7", Detail: Detail("verdict", "pass")},
		{TS: at(26), Kind: KindMerge, Round: 1, Issue: "12", Slot: "ben", PR: "#7"},
	}
	s := Fold(es, nil, 1)
	if len(s.Issues) != 2 {
		t.Fatalf("want two attempt rows: %+v", s.Issues)
	}
	if w, l := row(t, s, "12", "ben"), row(t, s, "12", "dana"); w.Outcome != OutcomeMerged || l.Outcome != OutcomeClosed || l.Merged {
		t.Errorf("winner %+v loser %+v", w, l)
	}
}

func TestSummaryExternalPR(t *testing.T) {
	es := []Entry{
		{TS: at(0), Kind: KindGate, Round: 1, PR: "https://h/o/r/pull/31", Detail: Detail("verdict", "pass")},
		{TS: at(0), Kind: KindMerge, Round: 1, PR: "https://h/o/r/pull/31"},
	}
	s := Fold(es, nil, 1)
	if len(s.Issues) != 1 {
		t.Fatalf("%+v", s.Issues)
	}
	if r := s.Issues[0]; r.Slot != "" || r.Issue != "" || r.PR != "https://h/o/r/pull/31" || !r.Merged || r.Gate != "pass" {
		t.Errorf("row = %+v", r)
	}
}

func TestSummaryGateMatchesRowByPRNumber(t *testing.T) {
	es := []Entry{
		{TS: at(0), Kind: KindAssign, Round: 1, Issue: "12", Slot: "ben"},
		{TS: at(9), Kind: KindDone, Round: 1, Issue: "12", Slot: "ben", PR: "https://h/o/r/pull/7"},
		{TS: at(10), Kind: KindGate, Round: 1, PR: "#7", Detail: Detail("verdict", "stale")}, // queued gate: no slot
	}
	s := Fold(es, nil, 1)
	if len(s.Issues) != 1 || s.Issues[0].Gate != "stale" {
		t.Fatalf("%+v", s.Issues)
	}
}

func TestSummaryTransferMovesTheRow(t *testing.T) {
	es := []Entry{
		{TS: at(0), Kind: KindAssign, Round: 1, Issue: "12", Slot: "ben", Account: "a", Harness: "claude"},
		{TS: at(10), Kind: KindTransfer, Round: 1, Issue: "12", Slot: "dana", Detail: Detail("from", "ben")},
	}
	s := Fold(es, nil, 1)
	if len(s.Issues) != 2 {
		t.Fatalf("%+v", s.Issues)
	}
	if b, d := row(t, s, "12", "ben"), row(t, s, "12", "dana"); b.Outcome != OutcomeTransferred || d.AssignedAt == nil || !d.AssignedAt.Equal(at(10)) {
		t.Errorf("ben %+v dana %+v", b, d)
	}
}

func TestSummaryAudit(t *testing.T) {
	au := func(ts time.Time, target string) *jsonx.Object {
		o := jsonx.NewObject()
		o.Set("ts", ts.UTC().Format(time.RFC3339))
		o.Set("gate", "merge-approval")
		o.Set("verb", "gate")
		o.Set("target", target)
		o.Set("note", "ok")
		return o
	}
	es := []Entry{
		{TS: at(0), Kind: KindAssign, Round: 1, Issue: "12", Slot: "ben"},
		{TS: at(30), Kind: KindMerge, Round: 1, Issue: "12", Slot: "ben", PR: "#7"},
	}
	s := Fold(es, []*jsonx.Object{au(at(-60), "#3"), au(at(20), "#7"), au(at(10), "#7"), au(at(90), "#9")}, 1)
	if len(s.Audit) != 2 || s.Audit[0].Target != "#7" || !s.Audit[0].TS.Before(s.Audit[1].TS) {
		t.Fatalf("audit = %+v (only the round's window, by target then time)", s.Audit)
	}
	if !strings.Contains(s.Text(), "Gate audit") {
		t.Errorf("text lacks the Gate audit heading:\n%s", s.Text())
	}
}

func TestSummaryObjectShape(t *testing.T) {
	s := Fold([]Entry{{TS: at(0), Kind: KindAssign, Round: 1, Issue: "12", Slot: "ben", Account: "a", Harness: "claude", Detail: hr(70)}}, nil, 1)
	o := s.Object()
	for _, k := range []string{"round", "issues", "slots", "accounts", "audit"} {
		if _, ok := o.Get(k); !ok {
			t.Errorf("missing %q", k)
		}
	}
	b, err := jsonx.MarshalCompact(o)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"quotaShare": null`) {
		t.Errorf("unknown quota share must be null: %s", b)
	}
}

// A train member that passed its own step carries no verdict: it must not keep
// the stale an earlier gate recorded.
func TestSummaryTrainMemberDropsAnEarlierGateVerdict(t *testing.T) {
	es := []Entry{
		{TS: at(0), Kind: KindAssign, Round: 2, Issue: "12", Slot: "ben"},
		{TS: at(1), Kind: KindGate, Round: 2, Issue: "12", Slot: "ben", Detail: Detail("verdict", "stale")},
		{TS: at(2), Kind: KindGate, Round: 2, Issue: "12", Slot: "ben", Detail: Detail("verdict", "", "train", true, "culprit", "dana")},
	}
	if g := row(t, Fold(es, nil, 2), "12", "ben").Gate; g != "" {
		t.Errorf("gate = %q, want none for a member the train accepted", g)
	}
}
