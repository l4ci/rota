package worker

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/roundlease"
)

// ledgerNow is the clock of the ledger tests: every row must carry it.
var ledgerNow = time.Date(2026, 10, 3, 9, 8, 7, 0, time.UTC)

func ledgerProject(t *testing.T, slots string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gittest.Init(t, dir, "main")
	if err := os.MkdirAll(filepath.Join(dir, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".rota", "workers.json"), []byte(`{"slots":[`+slots+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func fakeLeaseEnv(alive ...int) roundlease.Env {
	live := map[int]bool{}
	for _, p := range alive {
		live[p] = true
	}
	return roundlease.Env{
		Host:      "h",
		Alive:     func(p int) bool { return live[p] },
		StartTime: func(p int) (uint64, bool) { return uint64(p) * 10, live[p] },
		Now:       func() time.Time { return time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC) },
	}
}

func TestLedgerRoundIsTheHeldLease(t *testing.T) {
	dir := ledgerProject(t, "")
	cd, err := rotastate.CommonDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	env := fakeLeaseEnv(100)
	le := Env{Lease: func() roundlease.Env { return env }}
	if n := LedgerRound(le, dir); n != 0 {
		t.Errorf("no lease: round %d, want 0", n)
	}
	if _, _, _, err := env.Acquire(cd, dir, roundlease.Holder{PID: 100}, 7); err != nil {
		t.Fatal(err)
	}
	if n := LedgerRound(le, dir); n != 7 {
		t.Errorf("held lease: round %d, want 7", n)
	}
	// The holder is gone: a stale lease is nobody's round.
	env = fakeLeaseEnv()
	if n := LedgerRound(le, dir); n != 0 {
		t.Errorf("stale lease: round %d, want 0", n)
	}
	// A registry round without a lease no longer counts.
	os.WriteFile(filepath.Join(dir, ".rota", "workers.json"), []byte(`{"round":4,"slots":[]}`), 0o644)
	if n := LedgerRound(le, dir); n != 0 {
		t.Errorf("registry round leaked into the ledger: %d", n)
	}
}

func TestLedgerNoteFillsSlotFromRegistry(t *testing.T) {
	dir := ledgerProject(t, `{"name":"ben","task":"12","kind":"claude","account":"work"}`)
	env := Env{Now: func() time.Time { return ledgerNow }}
	LedgerNote(env, dir, ledger.Entry{Kind: ledger.KindBounce, Issue: "12", Detail: ledger.Detail("count", 1)})
	LedgerNote(env, dir, ledger.Entry{Kind: ledger.KindGate, Issue: "12", Slot: "ben"})
	LedgerNote(env, dir, ledger.Entry{Kind: ledger.KindBounce, Issue: "99"})
	es, err := ledger.Load(dir)
	if err != nil || len(es) != 3 {
		t.Fatalf("%v %+v", err, es)
	}
	for _, e := range es[:2] {
		if e.Slot != "ben" || e.Account != "work" || e.Harness != harness.Claude || !e.TS.Equal(ledgerNow) {
			t.Errorf("%s entry = %+v", e.Kind, e)
		}
	}
	if e := es[2]; e.Slot != "" || e.Account != "" {
		t.Errorf("an issue no slot holds got a slot: %+v", e)
	}
}

func TestLedgerNoteReportsAWriteFailure(t *testing.T) {
	dir := ledgerProject(t, "")
	// A directory where the file belongs makes every append fail.
	if err := os.Mkdir(ledger.Path(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	LedgerNote(Env{Stderr: &out, Now: func() time.Time { return ledgerNow }}, dir, ledger.Entry{Kind: ledger.KindBounce, Issue: "12"}) // must not panic or fail the verb
	if !strings.Contains(out.String(), ledger.Path(dir)) || strings.Count(out.String(), "\n") != 1 {
		t.Errorf("stderr = %q, want one line naming %s", out, ledger.Path(dir))
	}
}

func TestPaneDoneRecordsHeadroomOnce(t *testing.T) {
	dir := ledgerProject(t, `{"name":"ben","task":"12","kind":"claude","account":"work","state":"busy"}`)
	t.Setenv("ROTA_ACCOUNT_USAGE_DIR", "")
	if err := os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{"round":{"accounts":[{"name":"work","configDir":"/w"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	acc := &Accounts{Fetch: func(context.Context, string, string) (*jsonx.Object, string) {
		v, _ := jsonx.Decode([]byte(`{"five_hour":{"utilization":35,"resets_at":null},"seven_day":{"utilization":10,"resets_at":null}}`))
		return v.(*jsonx.Object), ""
	}}
	row := PollRow{Name: "ben", State: StateDone, Evidence: "https://github.com/o/r/pull/9"}
	var dones []ledger.Entry
	for i := 0; i < 2; i++ { // the second pass sees the slot already done
		if err := UpdateSlots(dir, func(s *Slot) {
			prev := s.State()
			if _, err := recordRow(s, row, pollTarget{name: s.Name()}, time.Now(), LoadRegistryTolerant(dir)); err != nil {
				t.Fatal(err)
			}
			if d, ok := paneDone(prev, s, row); ok {
				dones = append(dones, d)
			}
		}); err != nil {
			t.Fatal(err)
		}
	}
	if len(dones) != 1 {
		t.Fatalf("%d done transitions, want 1", len(dones))
	}
	LedgerDone(Env{Now: func() time.Time { return ledgerNow }}, context.Background(), acc, dir, dones[0])
	es, _ := ledger.Load(dir)
	if len(es) != 1 || es[0].Kind != ledger.KindDone || es[0].Slot != "ben" || es[0].Issue != "12" || es[0].PR != row.Evidence {
		t.Fatalf("entries %+v", es)
	}
	if h, ok := es[0].DetailFloat("headroom"); !ok || h != 65 {
		t.Errorf("headroom = %v %v, want 65", h, ok)
	}
}

// On a pane host the poll is what records done; it snapshots the account's
// headroom there, so the summary can tell what the slot consumed. A second
// poll of the same pane records nothing again.
func TestPollDoneOnAPaneHostRecordsHeadroom(t *testing.T) {
	dir, f := pollRegistry(t, "tmux")
	if err := os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{"round":{"accounts":[{"name":"work","configDir":"/w"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateSlot(dir, "w1", func(s *Slot) { s.SetAccount("work", "/w") }); err != nil {
		t.Fatal(err)
	}
	f.panes["w1"] = []string{"x\n", "x\nROTA-DONE w1 https://github.com/o/r/pull/9\n"}
	f.panes["w2"] = []string{"a\n", "a\n"}
	e := envWith(f)
	e.Accounts = &Accounts{Fetch: func(context.Context, string, string) (*jsonx.Object, string) {
		v, _ := jsonx.Decode([]byte(`{"five_hour":{"utilization":35,"resets_at":null},"seven_day":{"utilization":10,"resets_at":null}}`))
		return v.(*jsonx.Object), ""
	}}
	for i := 0; i < 2; i++ {
		if _, err := e.Poll(bg, dir, PollOpts{Lines: 60}); err != nil {
			t.Fatal(err)
		}
	}
	es, _ := ledger.Load(dir)
	if len(es) != 1 || es[0].Kind != ledger.KindDone || es[0].Slot != "w1" || es[0].Account != "work" || es[0].PR != "https://github.com/o/r/pull/9" {
		t.Fatalf("entries %+v", es)
	}
	if h, ok := es[0].DetailFloat("headroom"); !ok || h != 65 {
		t.Errorf("headroom = %v %v, want 65", h, ok)
	}
}

// A best-of:2 pair holds one issue in two slots: a bounce names the slot that
// holds the bounced PR instead of landing on the first slot holding the issue.
func TestBounceEntryNamesTheSlotHoldingThePR(t *testing.T) {
	dir := ledgerProject(t, `{"name":"ben","task":"12","kind":"claude"},{"name":"dana","task":"12","kind":"claude"}`)
	if _, err := RecordBounce(Env{}, dir, "12", "dana", "h1"); err != nil {
		t.Fatal(err)
	}
	es, _ := ledger.Load(dir)
	if len(es) != 1 || es[0].Kind != ledger.KindBounce || es[0].Slot != "dana" {
		t.Fatalf("bounce entries %+v, want one for slot dana", es)
	}
}

// A PR gated by number after its slot moved on resolves to no slot, so the
// caller records the bounce with a blank one (never the PR number). The summary
// folds it into the issue's own row instead of listing a pseudo-slot.
func TestBounceByPRNumberAfterTheSlotMovedOnMakesNoPseudoSlot(t *testing.T) {
	dir := ledgerProject(t, `{"name":"ben","task":"13","kind":"claude"}`)
	env := Env{Now: func() time.Time { return ledgerNow }}
	ledgerAppend(env, dir, ledger.Entry{Kind: ledger.KindAssign, Issue: "12", Slot: "ben", PR: "847", Round: 0})
	if _, err := RecordBounce(env, dir, "12", "", "h1"); err != nil {
		t.Fatal(err)
	}
	es, _ := ledger.Load(dir)
	s := ledger.Fold(es, nil, 0)
	if len(s.Issues) != 1 || s.Issues[0].Slot != "ben" || s.Issues[0].Bounces != 1 {
		t.Fatalf("issue rows %+v, want one row for ben with the bounce", s.Issues)
	}
	for _, tot := range s.Slots {
		if tot.Name == "847" {
			t.Fatalf("pseudo-slot 847 in slot totals %+v", s.Slots)
		}
	}
}

func TestTrainLedgerStampsTheVerdictOnTheCulpritOnly(t *testing.T) {
	dir := ledgerProject(t, `{"name":"b1","branch":"b1","pr":"https://github.com/o/r/pull/1"},{"name":"b2","branch":"b2","pr":"https://github.com/o/r/pull/2"},{"name":"b3","branch":"b3","pr":"https://github.com/o/r/pull/3"}`)
	res := TrainResult{Verdict: GateVerifyFailed, Culprit: "b2", Landed: []string{"b1"},
		Members: []TrainMember{{Target: "b1"}, {Target: "b2", Culprit: true}, {Target: "b3"}}}
	trainLedger(Env{Now: func() time.Time { return ledgerNow }}, nil, dir, res, map[string]bool{})
	verdicts := map[string]string{}
	es, _ := ledger.Load(dir)
	for _, e := range es {
		verdicts[e.Slot] = e.DetailStr("verdict")
	}
	if len(es) != 2 || verdicts["b2"] != GateVerifyFailed || verdicts["b3"] != "" {
		t.Fatalf("entries %+v, want verify-failed on b2 only", es)
	}
}

func TestLedgerRoundSaysWhenTheLeaseCannotBeRead(t *testing.T) {
	dir := ledgerProject(t, "")
	cd, err := rotastate.CommonDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(roundlease.Path(cd), 0o755); err != nil { // a directory where the lease belongs: unreadable
		t.Fatal(err)
	}
	var out bytes.Buffer
	n := LedgerRound(Env{Stderr: &out}, dir)
	if n != 0 || !strings.Contains(out.String(), "round lease") || strings.Count(out.String(), "\n") != 1 {
		t.Errorf("round %d, stderr %q, want 0 and one line naming the round lease", n, out.String())
	}
}

// A verb that writes several entries reads the lease once.
func TestLedgerNotesOfOneVerbReadTheLeaseOnce(t *testing.T) {
	dir := ledgerProject(t, `{"name":"w1","branch":"w1","pr":"https://github.com/o/r/pull/1"}`)
	reads := 0
	env := fakeLeaseEnv(100)
	gateLedger(Env{Lease: func() roundlease.Env { reads++; return env }, Now: func() time.Time { return ledgerNow }}, &RoundMemo{}, dir, GateTarget{Name: "w1", Branch: "w1"}, GateResult{Verdict: GatePass})
	if reads != 1 {
		t.Errorf("gateLedger read the lease %d times, want 1", reads)
	}
}

// Gate and train rows take the env's clock: none reads the wall clock.
func TestGateAndTrainLedgerRowsTakeTheEnvClock(t *testing.T) {
	dir := ledgerProject(t, `{"name":"b1","branch":"b1","pr":"https://github.com/o/r/pull/1"},{"name":"b2","branch":"b2","pr":"https://github.com/o/r/pull/2"}`)
	env := Env{Now: func() time.Time { return ledgerNow }}
	gateLedger(env, nil, dir, GateTarget{Name: "b1", Branch: "b1"}, GateResult{Verdict: GatePass})
	trainLedger(env, nil, dir, TrainResult{Verdict: GateVerifyFailed, Culprit: "b2", Members: []TrainMember{{Target: "b2", Culprit: true}}}, map[string]bool{})
	es, _ := ledger.Load(dir)
	if len(es) != 3 { // gate, merge, train gate
		t.Fatalf("entries %+v", es)
	}
	for _, e := range es {
		if !e.TS.Equal(ledgerNow) {
			t.Errorf("%s row ts = %v, want %v", e.Kind, e.TS, ledgerNow)
		}
	}
}
