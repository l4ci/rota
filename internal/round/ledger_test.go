package round

import (
	"context"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/worker"
)

func ledgerKinds(t *testing.T, root string) []ledger.Entry {
	t.Helper()
	es, err := ledger.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return es
}

func lastOf(es []ledger.Entry, kind string) *ledger.Entry {
	for i := len(es) - 1; i >= 0; i-- {
		if es[i].Kind == kind {
			return &es[i]
		}
	}
	return nil
}

func TestAssignAppendsLedger(t *testing.T) {
	f := newAssignFixture(t)
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatal(err)
	}
	e := lastOf(ledgerKinds(t, f.root), ledger.KindAssign)
	if e == nil {
		t.Fatal("no assign entry in ledger.jsonl")
	}
	if e.Issue != "12" || e.Slot != "ben" || e.Harness != "claude" || e.Round != 1 || e.TS.IsZero() {
		t.Errorf("entry = %+v", e)
	}
	if _, ok := e.DetailFloat("headroom"); ok {
		t.Error("no meter configured: headroom must be absent, not 0")
	}
	// A repeated assign resumes the same item and is not a second assignment.
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, x := range ledgerKinds(t, f.root) {
		if x.Kind == ledger.KindAssign {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d assign entries after a resume, want 1", n)
	}
}

func TestAssignRefusedAppendsNothing(t *testing.T) {
	f := newAssignFixture(t)
	if _, err := f.assign("14", "ben", nil); err == nil {
		t.Fatal("not-ready item was assigned")
	}
	if ledger.Exists(f.root) {
		t.Error("a refused assign wrote the ledger")
	}
}

func TestReportAppendsLedger(t *testing.T) {
	f := soloAssign(t)
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatal(err)
	}
	url := "https://github.com/o/r/pull/9"
	if _, err := f.env.ReportSlot(bg, f.root, ReportOpts{Slot: "ben", State: "done", PR: url}); err != nil {
		t.Fatal(err)
	}
	e := lastOf(ledgerKinds(t, f.root), ledger.KindDone)
	if e == nil || e.Slot != "ben" || e.Issue != "12" || e.PR != url || e.Round != 1 {
		t.Fatalf("done entry = %+v", e)
	}
	// Re-reporting the same state is not a second event; idle is no event.
	f.env.ReportSlot(bg, f.root, ReportOpts{Slot: "ben", State: "done", PR: url})
	f.env.ReportSlot(bg, f.root, ReportOpts{Slot: "ben", State: "idle"})
	n := 0
	for _, x := range ledgerKinds(t, f.root) {
		if x.Kind == ledger.KindDone {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d done entries, want 1", n)
	}
	if _, err := f.env.ReportSlot(bg, f.root, ReportOpts{Slot: "ben", State: "blocked"}); err != nil {
		t.Fatal(err)
	}
	if lastOf(ledgerKinds(t, f.root), ledger.KindBlocked) == nil {
		t.Error("no blocked entry")
	}
}

func TestTransferAppendsLedger(t *testing.T) {
	f := newMoveFx(t)
	if _, err := f.transfer("12", "dana", nil); err != nil {
		t.Fatal(err)
	}
	e := lastOf(ledgerKinds(t, f.root), ledger.KindTransfer)
	if e == nil || e.Issue != "12" || e.Slot != "dana" || e.DetailStr("from") != "ben" {
		t.Fatalf("transfer entry = %+v", e)
	}
}

func TestPickAppendsLedger(t *testing.T) {
	f := newPickFx(t)
	f.bothHavePRs(t)
	if _, err := f.pick("#7", "cleaner diff"); err != nil {
		t.Fatal(err)
	}
	e := lastOf(ledgerKinds(t, f.root), ledger.KindPick)
	if e == nil || e.Issue != "12" || e.Slot != "ben" || e.PR != "#7" || e.DetailStr("loser") != "dana" {
		t.Fatalf("pick entry = %+v", e)
	}
}

func TestWindDownAppendsParkLedger(t *testing.T) {
	f := newAssignFixture(t)
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatal(err)
	}
	f.verifyWith(t, `["true"]`)
	if _, err := f.windDown(nil); err != nil {
		t.Fatal(err)
	}
	var parks []ledger.Entry
	for _, x := range ledgerKinds(t, f.root) {
		if x.Kind == ledger.KindPark {
			parks = append(parks, x)
		}
	}
	if len(parks) != 1 || parks[0].Slot != "ben" || parks[0].Issue != "12" || parks[0].Round != 1 {
		t.Fatalf("park entries = %+v", parks)
	}
}

func TestBounceAppendsLedger(t *testing.T) {
	f := newAssignFixture(t)
	if _, err := worker.RecordBounce(f.root, "12", "", "abc"); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.RecordBounce(f.root, "12", "", "abc"); err != nil { // same head: not counted
		t.Fatal(err)
	}
	var n int
	for _, x := range ledgerKinds(t, f.root) {
		if x.Kind == ledger.KindBounce && x.Issue == "12" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d bounce entries, want 1", n)
	}
}

type ctxKey struct{}

// A solo done reads the account's headroom through the Env's Accounts and the
// caller's context, not a private fetcher: a test injects the meter, and a
// cancelled command cancels the fetch.
func TestReportHeadroomUsesInjectedAccountsAndContext(t *testing.T) {
	f := soloAssign(t)
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatal(err)
	}
	f.config(t, `{"work":{"accounts":[{"name":"work","configDir":"/w"}]}}`)
	if _, err := worker.UpdateSlot(f.root, "ben", func(s *worker.Slot) { s.SetAccount("work", "/w") }); err != nil {
		t.Fatal(err)
	}
	var seen any
	f.env.Accounts = &worker.Accounts{Fetch: func(ctx context.Context, _, _ string) (*jsonx.Object, string) {
		seen = ctx.Value(ctxKey{})
		v, _ := jsonx.Decode([]byte(`{"five_hour":{"utilization":25,"resets_at":null},"seven_day":{"utilization":5,"resets_at":null}}`))
		return v.(*jsonx.Object), ""
	}}
	ctx := context.WithValue(bg, ctxKey{}, "caller")
	if _, err := f.env.ReportSlot(ctx, f.root, ReportOpts{Slot: "ben", State: "done", PR: "https://github.com/o/r/pull/9"}); err != nil {
		t.Fatal(err)
	}
	if seen != "caller" {
		t.Errorf("the meter fetch did not get the caller's context: %v", seen)
	}
	e := lastOf(ledgerKinds(t, f.root), ledger.KindDone)
	if h, ok := e.DetailFloat("headroom"); e == nil || !ok || h != 75 {
		t.Fatalf("done entry = %+v headroom %v %v, want 75", e, h, ok)
	}
}

// `round report --state limited` is a limit event: it writes a limited entry.
func TestReportLimitedAppendsLedger(t *testing.T) {
	f := soloAssign(t)
	if _, err := f.assign("12", "ben", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.env.ReportSlot(bg, f.root, ReportOpts{Slot: "ben", State: "limited"}); err != nil {
		t.Fatal(err)
	}
	e := lastOf(ledgerKinds(t, f.root), ledger.KindLimited)
	if e == nil || e.Slot != "ben" || e.Issue != "12" || e.Harness != "claude" {
		t.Fatalf("limited entry = %+v", e)
	}
}
