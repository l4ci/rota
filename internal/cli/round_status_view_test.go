package cli

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/golden"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/tui"
)

const rw, rh = 100, 30

type roundGoldenIn struct {
	Keys []string `json:"keys"`
	W    int      `json:"w"`
	H    int      `json:"h"`
}

type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time          { return f.t }
func (f *fakeClock) advance(d time.Duration) { f.t = f.t.Add(d) }

func newClock() *fakeClock { return &fakeClock{t: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)} }

// fixedSnap is a round in flight: two assigned slots, a parked one, a queued
// review and three candidates (one ready, two blocked for different reasons).
func fixedSnap() roundSnap {
	return roundSnap{
		Host: "herdr",
		Slots: []round.Row{
			{Name: "ben", Issue: "542", Branch: "ben/542-config-screen", PR: "https://github.com/l4ci/rota/pull/561", PRState: "open", HostState: "working",
				Tier: "standard", Model: "sonnet", Bounces: 1, PRTitle: "Add the config screen", Evidence: "go test ./internal/cli ok"},
			{Name: "dana", Issue: "543", Branch: "dana/543-projects-screen", HostState: "idle", Tier: "light", Model: "haiku"},
			{Name: "kit", Branch: "park/kit", HostState: "idle"},
		},
		Review: []round.QueuedPR{{Issue: "544", PR: "https://github.com/l4ci/rota/pull/559", Branch: "nia/544-palette-hub", From: "nia"}},
		Cands: []roundCand{
			{ID: "546", Title: "CLI UI 7: read-only views", Ready: true},
			{ID: "547", Title: "Cache the forge reads", Why: []string{"overlaps #542 (ben)"}},
			{ID: "548", Title: "Docs for --ui", Why: []string{"waiting on open PR #559", "deps: waits on #544"}},
		},
		CandsLoaded: true,
	}
}

func roundFrame(t *testing.T, m tui.Model) []string {
	t.Helper()
	lines := strings.Split(tui.Strip(m.Render(rw, rh, tui.Style{Color: true})), "\n")
	if len(lines) != rh {
		t.Errorf("frame has %d lines, want %d", len(lines), rh)
	}
	for i, l := range lines {
		if n := utf8.RuneCountInString(l); n > rw {
			t.Errorf("line %d is %d runes: %q", i, n, l)
		}
	}
	return lines
}

func press(m tui.Model, names ...string) (tui.Model, tui.Cmd) {
	var cmd tui.Cmd
	for _, n := range names {
		var k tui.Msg
		switch n {
		case "esc":
			k = tui.Key{Kind: tui.KeyEsc}
		case "down":
			k = tui.Key{Kind: tui.KeyDown}
		default:
			k = tui.Rune([]rune(n)[0])
		}
		m, cmd = m.Update(k)
	}
	return m, cmd
}

func TestRoundStatusScreenGolden(t *testing.T) {
	clk := newClock()
	load := func() (roundSnap, error) { return fixedSnap(), nil }
	for _, keys := range [][]string{nil, {"j", "j", "j", "j"}, {"j", "j", "j", "j", "j", "j"}, {"j", "j", "j", "j", "j", "j", "j"}} {
		m, _ := press(newRoundScreen(fixedSnap(), load, clk.now, roundRefresh), keys...)
		golden.Check(t, roundGoldenIn{keys, rw, rh}, roundFrame(t, m))
	}
}

func TestRoundStatusScreenLoadingCandidates(t *testing.T) {
	clk := newClock()
	s := fixedSnap()
	s.Cands, s.CandsLoaded = nil, false
	m := newRoundScreen(s, nil, clk.now, roundRefresh)
	golden.Check(t, roundGoldenIn{nil, rw, rh}, roundFrame(t, m))
}

func TestRoundStatusScreenQuit(t *testing.T) {
	for _, k := range []string{"q", "esc"} {
		m := newRoundScreen(fixedSnap(), nil, newClock().now, roundRefresh)
		if _, cmd := press(m, k); !cmd.Quit {
			t.Errorf("%s did not quit", k)
		}
	}
}

// runCmd does what the driver does with an Exec: run it, send Done.
func runCmd(m tui.Model, cmd tui.Cmd) tui.Model {
	var err error
	if cmd.Exec != nil {
		err = cmd.Exec()
	}
	m, _ = m.Update(tui.Done{Err: err})
	return m
}

func TestRoundStatusScreenRefresh(t *testing.T) {
	clk := newClock()
	passes := 0
	load := func() (roundSnap, error) {
		passes++
		s := fixedSnap()
		s.Slots[1].HostState = "working"
		return s, nil
	}
	var m tui.Model = newRoundScreen(fixedSnap(), load, clk.now, roundRefresh)

	// A Tick right after the screen opened does nothing.
	if _, cmd := m.Update(tui.Tick{}); cmd.Exec != nil {
		t.Fatal("a Tick at t+0 refreshed")
	}
	// A Tick one interval later refreshes.
	clk.advance(roundRefresh)
	m2, cmd := m.Update(tui.Tick{})
	if cmd.Exec == nil {
		t.Fatal("a Tick after the interval did not refresh")
	}
	if !strings.Contains(tui.Strip(m2.Render(rw, rh, tui.Style{})), "refreshing…") {
		t.Error("header does not say refreshing")
	}
	clk.advance(5 * time.Second) // the pass took as long as the interval
	m = runCmd(m2, cmd)
	if passes != 1 {
		t.Fatalf("passes = %d, want 1", passes)
	}
	if !strings.Contains(tui.Strip(m.Render(rw, rh, tui.Style{})), "updated 12:00:10") {
		t.Errorf("header not stamped with the clock:\n%s", tui.Strip(m.Render(rw, rh, tui.Style{})))
	}
	// The Tick that fell due while the pass ran must not start another at once.
	if _, cmd := m.Update(tui.Tick{}); cmd.Exec != nil {
		t.Fatal("a Tick right after a pass chased it")
	}
	// r refreshes whenever asked.
	if _, cmd := press(m, "r"); cmd.Exec == nil {
		t.Fatal("r did not refresh")
	}
}

func TestRoundStatusScreenRefreshKeepsSelectionAndShowsFailure(t *testing.T) {
	clk := newClock()
	fail := false
	load := func() (roundSnap, error) {
		if fail {
			return roundSnap{}, errors.New("forge: boom")
		}
		s := fixedSnap()
		s.Slots = append([]round.Row{{Name: "amy", Branch: "park/amy"}}, s.Slots...) // a row above shifts the indexes
		return s, nil
	}
	m, _ := press(newRoundScreen(fixedSnap(), load, clk.now, roundRefresh), "j") // dana
	m2, cmd := press(m, "r")
	m = runCmd(m2, cmd)
	if out := tui.Strip(m.Render(rw, rh, tui.Style{})); !strings.Contains(out, "▸ dana") {
		t.Errorf("selection moved off dana:\n%s", out)
	}
	fail = true
	m2, cmd = press(m, "r")
	m = runCmd(m2, cmd)
	out := tui.Strip(m.Render(rw, rh, tui.Style{}))
	if !strings.Contains(out, "refresh failed: forge: boom") || !strings.Contains(out, "amy") {
		t.Errorf("a failed refresh must keep the old rows and say so:\n%s", out)
	}
}

func TestSnapFromData(t *testing.T) {
	slot := jsonx.NewObject()
	slot.Set("name", "ben")
	slot.Set("issue", "542")
	slot.Set("pr", "https://github.com/l4ci/rota/pull/561")
	slot.Set("model", "sonnet")
	slot.Set("bounces", 2)
	slot.Set("drift", []any{"pr-stale"})
	rev := jsonx.NewObject()
	rev.Set("issue", "544")
	rev.Set("from", "nia")
	d := jsonx.NewObject()
	d.Set("slots", []any{slot})
	d.Set("review", []any{rev})
	d.Set("host", "herdr")
	d.Set("unavailable", []any{"forge"})
	got := snapFromData(d)
	if len(got.Slots) != 1 || got.Slots[0].Name != "ben" || got.Slots[0].Bounces != 2 || got.Slots[0].Drift[0] != "pr-stale" ||
		len(got.Review) != 1 || got.Review[0].From != "nia" || got.Host != "herdr" || got.Notes[0] != "forge unavailable" {
		t.Fatalf("snap = %+v", got)
	}
}

func TestRoundStatusScreenStripsControlCharacters(t *testing.T) {
	s := fixedSnap()
	s.Slots[0].PRTitle = "evil\x1b[2Jtitle\x07"
	s.Slots[0].Evidence = "line\x1b]0;x\x07 two"
	s.Cands[1].Title = "t\x1b[31mred"
	s.Notes = []string{"warn\x1bX"}
	m := newRoundScreen(s, nil, newClock().now, roundRefresh)
	for _, k := range [][]string{nil, {"j", "j", "j", "j", "j"}} {
		m2, _ := press(m, k...)
		if out := m2.Render(rw, rh, tui.Style{}); strings.ContainsRune(out, 0x1b) || strings.ContainsRune(out, 0x07) {
			t.Fatalf("a control character reached the frame: %q", out)
		}
	}
}

func TestRoundScreenBurnColumn(t *testing.T) {
	s := fixedSnap()
	h := 62.0
	s.Slots[0].Burn = &h
	m := newRoundScreen(s, nil, newClock().now, roundRefresh)
	out := strings.Join(roundFrame(t, m), "\n")
	if !strings.Contains(out, "burn") || !strings.Contains(out, "62%") || !strings.Contains(out, "n/a") {
		t.Errorf("burn column missing:\n%s", out)
	}
}

func TestFillBurnFromLedger(t *testing.T) {
	dir := workerProject(t, `{}`)
	for _, e := range []ledger.Entry{
		{Kind: ledger.KindAssign, Issue: "12", Slot: "ben", Account: "work", Harness: "claude", Detail: ledger.Detail("headroom", 80.0)},
		{Kind: ledger.KindAssign, Issue: "13", Slot: "dana", Account: "work", Harness: "codex"},
	} {
		if err := ledger.Append(dir, e); err != nil {
			t.Fatal(err)
		}
	}
	rows := []round.Row{{Name: "ben", Issue: "12"}, {Name: "dana", Issue: "13"}, {Name: "kit"}}
	c := &Ctx{Deps: testDeps()}
	fillBurn(c, dir, rows)
	if rows[0].Burn == nil || *rows[0].Burn != 80 || rows[1].Burn != nil || rows[2].Burn != nil {
		t.Errorf("burn = %v %v %v", rows[0].Burn, rows[1].Burn, rows[2].Burn)
	}
}
