package worker

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/roundlease"
)

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
	leaseEnv = func() roundlease.Env { return env }
	t.Cleanup(func() { leaseEnv = roundlease.DefaultEnv })
	if n := LedgerRound(dir); n != 0 {
		t.Errorf("no lease: round %d, want 0", n)
	}
	if _, _, _, err := env.Acquire(cd, dir, roundlease.Holder{PID: 100}, 7); err != nil {
		t.Fatal(err)
	}
	if n := LedgerRound(dir); n != 7 {
		t.Errorf("held lease: round %d, want 7", n)
	}
	// The holder is gone: a stale lease is nobody's round.
	env = fakeLeaseEnv()
	if n := LedgerRound(dir); n != 0 {
		t.Errorf("stale lease: round %d, want 0", n)
	}
	// A registry round without a lease no longer counts.
	os.WriteFile(filepath.Join(dir, ".rota", "workers.json"), []byte(`{"round":4,"slots":[]}`), 0o644)
	if n := LedgerRound(dir); n != 0 {
		t.Errorf("registry round leaked into the ledger: %d", n)
	}
}

func TestLedgerNoteFillsSlotFromRegistry(t *testing.T) {
	dir := ledgerProject(t, `{"name":"ben","task":"12","kind":"claude","account":"work"}`)
	LedgerNote(dir, ledger.Entry{Kind: ledger.KindBounce, Issue: "12", Detail: ledger.Detail("count", 1)})
	LedgerNote(dir, ledger.Entry{Kind: ledger.KindGate, Issue: "12", Slot: "ben"})
	LedgerNote(dir, ledger.Entry{Kind: ledger.KindBounce, Issue: "99"})
	es, err := ledger.Load(dir)
	if err != nil || len(es) != 3 {
		t.Fatalf("%v %+v", err, es)
	}
	for _, e := range es[:2] {
		if e.Slot != "ben" || e.Account != "work" || e.Harness != "claude" || e.TS.IsZero() {
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
	r, w, _ := os.Pipe()
	old := os.Stderr
	os.Stderr = w
	LedgerNote(dir, ledger.Entry{Kind: ledger.KindBounce, Issue: "12"}) // must not panic or fail the verb
	w.Close()
	os.Stderr = old
	out, _ := io.ReadAll(r)
	if !strings.Contains(string(out), ledger.Path(dir)) || strings.Count(string(out), "\n") != 1 {
		t.Errorf("stderr = %q, want one line naming %s", out, ledger.Path(dir))
	}
}

func TestPaneDoneRecordsHeadroomOnce(t *testing.T) {
	dir := ledgerProject(t, `{"name":"ben","task":"12","kind":"claude","account":"work","state":"busy"}`)
	t.Setenv("ROTA_ACCOUNT_USAGE_DIR", "")
	if err := os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{"work":{"accounts":[{"name":"work","configDir":"/w"}]}}`), 0o644); err != nil {
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
			if err := recordRow(s, row, time.Now()); err != nil {
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
	LedgerDone(context.Background(), acc, dir, dones[0])
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
	if err := os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{"work":{"accounts":[{"name":"work","configDir":"/w"}]}}`), 0o644); err != nil {
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
