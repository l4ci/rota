package limits

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/jsonx"
)

var t0 = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

const noTimeMsg = "You've hit your usage limit.\n"

const limitMsg = "Claude usage limit reached. Your limit will reset at 3pm.\n"

// rig is a fake world: a clock, a registry under a temp dir, panes and
// recorded sends, transfers and escalations.
type rig struct {
	mu       sync.Mutex
	t        *testing.T
	root     string
	now      time.Time
	targets  []Target
	panes    map[string]string
	data     Data
	meters   map[string]Reading
	pick     string
	idle     map[string]string // account -> slot
	sent     []string          // "<session>: <prompt>"
	xfers    []string
	xferErr  error
	escal    []string
	notified []string
	set      Settings
	w        *Watcher
}

func newRig(t *testing.T) *rig {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o777); err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, root: root, now: t0, panes: map[string]string{}, meters: map[string]Reading{}, idle: map[string]string{},
		targets: []Target{{Session: Orchestrator, Pane: "p0", Orchestrator: true}},
		set: Settings{Mode: ModeSwitch, Margin: 60 * time.Second, Fallback: 30 * time.Minute, MaxResumes: 3,
			Prompt: "The usage limit has reset. Continue where you left off."}}
	return r
}

func (r *rig) build() *Watcher {
	r.w = New(Deps{
		Root: r.root, Settings: r.set, Now: func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now }, Loc: time.UTC,
		Targets:          func(context.Context) []Target { return r.targets },
		Capture:          func(_ context.Context, t Target) string { return r.panes[t.Session] },
		OrchestratorData: func(time.Time) Data { return r.data },
		Meter:            func(_ context.Context, a string) Reading { return r.meters[a] },
		PickAccount: func(_ context.Context, exclude string) (string, bool) {
			return r.pick, r.pick != "" && r.pick != exclude
		},
		IdleSlot: func(_ context.Context, a string) (string, bool) { s, ok := r.idle[a]; return s, ok },
		Transfer: func(_ context.Context, issue, to string) error {
			r.xfers = append(r.xfers, issue+"->"+to)
			for i := range r.targets { // the sender is parked, so idle
				if r.targets[i].Issue == issue {
					r.targets[i].Issue = ""
				}
			}
			return r.xferErr
		},
		Send: func(_ context.Context, t Target, p string) error {
			r.mu.Lock()
			r.sent = append(r.sent, t.Session+": "+p)
			r.mu.Unlock()
			return nil
		},
		Escalate: func(issue int, title, body string) (string, []string, error) {
			r.escal = append(r.escal, title)
			return "e1", nil, nil
		},
		Notify: func(title, body string) { r.notified = append(r.notified, title) },
	})
	return r.w
}

func (r *rig) step(sweep bool, ms ...Match) { r.w.Step(context.Background(), ms, sweep) }

func (r *rig) resumed() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sent)
}

func (r *rig) entries() []Entry { return Load(r.root) }

func (r *rig) only() Entry {
	r.t.Helper()
	es := r.entries()
	if len(es) != 1 {
		r.t.Fatalf("want one entry, got %+v", es)
	}
	return es[0]
}

func TestOrchestratorDataSleepsThenResumes(t *testing.T) {
	r := newRig(t)
	r.build()
	reset := t0.Add(90 * time.Second)
	r.data = Data{Window: WindowFiveHour, ResetsAt: reset, Limited: true, Known: true}
	r.step(false)
	e := r.only()
	if e.ID != "l1" || e.Status != StatusWaiting || e.Source != SourceData || e.Window != WindowFiveHour ||
		e.ResetsAt != Time(reset) || e.Action != ActionSleep || e.Session != Orchestrator {
		t.Fatalf("entry %+v", e)
	}
	r.now = reset.Add(30 * time.Second) // reset passed, margin not
	r.step(false)
	if len(r.sent) != 0 {
		t.Fatalf("prompt before reset plus margin: %v", r.sent)
	}
	r.now = reset.Add(60 * time.Second)
	r.step(false)
	if len(r.sent) != 1 || !strings.HasPrefix(r.sent[0], "orchestrator: The usage limit has reset") {
		t.Fatalf("sent %v", r.sent)
	}
	e = r.only()
	if e.Status != StatusResumed || e.Cycles != 1 || e.ResolvedAt != Time(r.now) {
		t.Fatalf("entry %+v", e)
	}
	// data no longer limited: nothing more happens
	r.data = Data{Known: true}
	r.step(false)
	if len(r.sent) != 1 || len(r.entries()) != 1 {
		t.Fatalf("extra work: %v %v", r.sent, r.entries())
	}
}

func slotRig(t *testing.T) *rig {
	r := newRig(t)
	r.targets = append(r.targets,
		Target{Session: "ben", Pane: "pb", Account: "a", Issue: "67"},
		Target{Session: "dana", Pane: "pd", Account: "b"})
	r.panes["ben"] = "working...\n" + limitMsg
	r.meters["a"] = Reading{Known: true, Cooling: true, ResetsAt: t0.Add(2 * time.Hour), Window: WindowFiveHour}
	r.pick = "b"
	r.idle["b"] = "dana"
	return r
}

func TestSlotSwitchesToIdleSlotOnAnotherAccount(t *testing.T) {
	r := slotRig(t)
	r.build()
	r.step(true)
	e := r.only()
	if len(r.xfers) != 1 || r.xfers[0] != "67->dana" {
		t.Fatalf("transfers %v", r.xfers)
	}
	if e.Status != StatusSwitched || e.Action != ActionSwitch || e.Account != "a" || e.To != "dana" ||
		e.Source != SourceData || e.ResolvedAt == "" || e.Session != "ben" {
		t.Fatalf("entry %+v", e)
	}
	// a switched entry waits for nothing
	r.now = t0.Add(5 * time.Hour)
	r.step(true)
	if len(r.sent) != 0 || len(r.entries()) != 1 {
		t.Fatalf("sent %v entries %v", r.sent, r.entries())
	}
}

func TestSlotSleepsWhenSwitchImpossible(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(r *rig)
		note   string
	}{
		"sleep mode":      {func(r *rig) { r.set.Mode = ModeSleep }, "limits.mode is sleep"},
		"no account":      {func(r *rig) { r.pick = "" }, "No usable account"},
		"no idle slot":    {func(r *rig) { delete(r.idle, "b") }, "No idle slot on account b"},
		"transfer failed": {func(r *rig) { r.xferErr = errors.New("boom") }, "transfer of 67 to dana failed: boom"},
	} {
		t.Run(name, func(t *testing.T) {
			r := slotRig(t)
			tc.mutate(r)
			r.build()
			r.step(true)
			e := r.only()
			if e.Status != StatusWaiting || e.Action != ActionSleep || e.To != "" || !strings.Contains(e.Note, tc.note) {
				t.Fatalf("entry %+v", e)
			}
			if e.ResetsAt != Time(t0.Add(2*time.Hour)) {
				t.Fatalf("resetsAt %s", e.ResetsAt)
			}
			// it then sleeps until the meter's reset, plus the margin
			r.now = t0.Add(2*time.Hour + 61*time.Second)
			r.step(false)
			if len(r.sent) != 1 || !strings.HasPrefix(r.sent[0], "ben: ") {
				t.Fatalf("sent %v", r.sent)
			}
		})
	}
}

func TestSlotWithFreeMeterIsNotLimited(t *testing.T) {
	r := slotRig(t)
	r.meters["a"] = Reading{Known: true}
	r.build()
	r.step(true)
	r.step(true)
	if len(r.entries()) != 0 {
		t.Fatalf("entries %v", r.entries())
	}
}

func TestTextWithoutDataParsesResetOrFallsBack(t *testing.T) {
	// no rateLimits and no meter: a pane match on the orchestrator
	r := newRig(t)
	r.build()
	r.step(false, Match{Session: Orchestrator, Line: "limit reached", Text: "x\n" + limitMsg})
	e := r.only()
	if e.Source != SourceText || e.ResetsAt != "2026-10-03T15:00:00Z" || e.Action != ActionSleep || e.Window != WindowUnknown {
		t.Fatalf("entry %+v", e)
	}
	r.now = time.Date(2026, 10, 3, 15, 1, 0, 0, time.UTC)
	r.step(false)
	if len(r.sent) != 1 || r.only().Status != StatusResumed {
		t.Fatalf("sent %v entry %+v", r.sent, r.only())
	}

	// no time in the text: the flat fallback
	r = newRig(t)
	r.build()
	r.step(false, Match{Session: Orchestrator, Text: "You've hit your usage limit. Try later."})
	e = r.only()
	if e.ResetsAt != Time(t0.Add(30*time.Minute)) || !strings.Contains(e.Note, "no reset time found") {
		t.Fatalf("entry %+v", e)
	}
}

func TestTextOnTmuxIsFoundByCapture(t *testing.T) {
	r := newRig(t)
	r.panes[Orchestrator] = "stuff\n" + limitMsg
	r.build()
	r.step(true)
	if e := r.only(); e.Source != SourceText || e.Session != Orchestrator {
		t.Fatalf("entry %+v", e)
	}
	// already waiting: a second sweep adds nothing
	r.step(true)
	if len(r.entries()) != 1 {
		t.Fatalf("entries %v", r.entries())
	}
}

func TestOrchestratorNeverSwitches(t *testing.T) {
	r := slotRig(t)
	r.data = Data{Window: WindowSevenDay, ResetsAt: t0.Add(time.Hour), Limited: true, Known: true}
	r.panes["ben"] = ""
	r.build()
	r.step(true)
	e := r.only()
	if e.Session != Orchestrator || e.Action != ActionSleep || len(r.xfers) != 0 || !strings.Contains(e.Note, "never switches") {
		t.Fatalf("entry %+v xfers %v", e, r.xfers)
	}
}

func TestApproachingWarningIsNotALimit(t *testing.T) {
	r := newRig(t)
	r.build()
	r.step(false, Match{Session: Orchestrator, Text: "Approaching usage limit · resets at 5pm\n"})
	if len(r.entries()) != 0 {
		t.Fatalf("entries %v", r.entries())
	}
}

func TestOrchestratorDataBelowLimitDismissesText(t *testing.T) {
	r := newRig(t)
	r.data = Data{Known: true}
	r.build()
	r.step(false, Match{Session: Orchestrator, Text: limitMsg})
	if len(r.entries()) != 0 {
		t.Fatalf("entries %v", r.entries())
	}
}

func TestIdleSlotLimitIsIgnored(t *testing.T) {
	r := slotRig(t)
	r.targets[1].Issue = ""
	r.build()
	r.step(true)
	if len(r.entries()) != 0 {
		t.Fatalf("entries %v", r.entries())
	}
}

func TestMaxResumesFailsAndEscalates(t *testing.T) {
	for _, issue := range []int{0, 12} {
		r := newRig(t)
		r.set.MaxResumes, r.set.EscalateIssue = 2, issue
		r.build()
		pane := ""
		for cycle := 1; ; cycle++ {
			// still limited: a fresh message after whatever the pane held
			pane += noTimeMsg
			r.panes[Orchestrator] = pane
			r.step(true)
			e := r.only()
			if e.Status == StatusFailed {
				break
			}
			if cycle > 3 {
				t.Fatalf("never failed: %+v", e)
			}
			r.now = r.now.Add(31*time.Minute + time.Minute) // fallback sleep and margin
			r.step(false)
			pane += r.set.Prompt + "\n"
			r.now = r.now.Add(time.Minute)
		}
		e := r.only()
		if len(r.sent) != 2 || e.Cycles != 2 || e.Status != StatusFailed || e.ID != "l1" || !strings.Contains(e.Note, "still limited after 2 resume") {
			t.Fatalf("issue %d: sent %v entry %+v", issue, r.sent, e)
		}
		if r.w.Failed() != 1 {
			t.Fatalf("failed %d", r.w.Failed())
		}
		if issue == 0 && (len(r.notified) != 1 || len(r.escal) != 0) {
			t.Fatalf("notified %v escalated %v", r.notified, r.escal)
		}
		if issue != 0 && (len(r.escal) != 1 || len(r.notified) != 0 || !strings.Contains(e.Note, "Escalation e1")) {
			t.Fatalf("notified %v escalated %v note %q", r.notified, r.escal, e.Note)
		}
		// a failed session is left alone for a while
		r.step(true)
		if len(r.entries()) != 1 {
			t.Fatalf("entries %v", r.entries())
		}
	}
}

func TestAnsweredMessageDoesNotStartAnotherCycle(t *testing.T) {
	r := newRig(t)
	r.panes[Orchestrator] = noTimeMsg
	r.build()
	r.step(true)
	r.now = r.now.Add(35 * time.Minute)
	r.step(false)
	if r.only().Status != StatusResumed {
		t.Fatalf("entry %+v", r.only())
	}
	// the pane still shows the old message, then the typed prompt and work
	r.panes[Orchestrator] = noTimeMsg + r.set.Prompt + "\nworking on it\n"
	r.now = r.now.Add(time.Minute)
	r.step(true)
	if e := r.only(); e.Status != StatusResumed || e.Cycles != 1 {
		t.Fatalf("entry %+v", e)
	}
}

func TestStartUpPassResumesEntryWhoseResetPassed(t *testing.T) {
	r := newRig(t)
	if _, err := Append(r.root, Entry{Session: Orchestrator, Window: WindowFiveHour, Source: SourceData,
		DetectedAt: Time(t0.Add(-time.Hour)), ResetsAt: Time(t0.Add(-10 * time.Minute)), Action: ActionSleep, Status: StatusWaiting}); err != nil {
		t.Fatal(err)
	}
	r.build()
	r.step(true)
	if len(r.sent) != 1 || r.only().Status != StatusResumed {
		t.Fatalf("sent %v entry %+v", r.sent, r.only())
	}
}

func TestResumeFailsWhenPaneIsGone(t *testing.T) {
	r := slotRig(t)
	r.set.Mode = ModeSleep
	r.build()
	r.step(true)
	r.targets = r.targets[:1] // ben's pane is gone
	r.now = t0.Add(3 * time.Hour)
	r.step(false)
	e := r.only()
	if e.Status != StatusFailed || !strings.Contains(e.Note, "pane is gone") || len(r.sent) != 0 {
		t.Fatalf("entry %+v", e)
	}
}

func TestIDsCountUp(t *testing.T) {
	r := slotRig(t)
	r.set.Mode = ModeSleep
	r.data = Data{Window: WindowFiveHour, ResetsAt: t0.Add(time.Hour), Limited: true, Known: true}
	r.build()
	r.step(true)
	es := r.entries()
	if len(es) != 2 || es[0].ID != "l1" || es[1].ID != "l2" {
		t.Fatalf("entries %+v", es)
	}
}

func TestRunLeavesWaitingEntriesOnCancel(t *testing.T) {
	r := newRig(t)
	r.data = Data{Window: WindowFiveHour, ResetsAt: t0.Add(time.Hour), Limited: true, Known: true}
	r.build()
	ctx, cancel := context.WithCancel(context.Background())
	feed := make(chan Match)
	done := make(chan struct{})
	r.w.After = func(time.Duration) <-chan time.Time { return make(chan time.Time) }
	go func() { r.w.Run(ctx, feed); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for len(r.entries()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
	if e := r.only(); e.Status != StatusWaiting || len(r.sent) != 0 {
		t.Fatalf("entry %+v sent %v", e, r.sent)
	}
}

func TestRunStepsOnFeedAndTimer(t *testing.T) {
	r := newRig(t)
	r.build()
	tick := make(chan time.Time)
	r.w.After = func(time.Duration) <-chan time.Time { return tick }
	ctx, cancel := context.WithCancel(context.Background())
	feed := make(chan Match)
	done := make(chan struct{})
	go func() { r.w.Run(ctx, feed); close(done) }()
	feed <- Match{Session: Orchestrator, Text: limitMsg}
	for len(r.entries()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	r.mu.Lock()
	r.now = time.Date(2026, 10, 3, 15, 2, 0, 0, time.UTC)
	r.mu.Unlock()
	tick <- time.Time{}
	deadline := time.Now().Add(5 * time.Second)
	for r.resumed() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if len(r.sent) != 1 {
		t.Fatalf("sent %v", r.sent)
	}
}

func TestParseReset(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	berlin, _ := time.LoadLocation("Europe/Berlin")
	for _, tc := range []struct {
		text string
		loc  *time.Location
		want string
		ok   bool
	}{
		{"resets 3pm", time.UTC, "2026-10-03T15:00:00Z", true},
		{"limit will reset at 3:30pm", time.UTC, "2026-10-03T15:30:00Z", true},
		{"resets at 9am", time.UTC, "2026-10-04T09:00:00Z", true}, // already past today
		{"resets at 12am", time.UTC, "2026-10-04T00:00:00Z", true},
		{"resets at 15:45", time.UTC, "2026-10-03T15:45:00Z", true},
		{"resets 3pm (Europe/Berlin)", time.UTC, "2026-10-03T13:00:00Z", true},
		{"resets 3pm", berlin, "2026-10-03T13:00:00Z", true},
		{"resets Oct 5, 3pm", time.UTC, "2026-10-05T15:00:00Z", true},
		{"resets Oct 20, 3pm", time.UTC, "", false}, // more than 8 days ahead
		{"resets Oct 1, 3pm", time.UTC, "", false},  // next year, far ahead
		{"resets in 2 hours", time.UTC, "", false},
		{"resets at 25:00", time.UTC, "", false},
		{"resets at 13pm", time.UTC, "", false},
		{"resets 5", time.UTC, "", false},
		{"no time here", time.UTC, "", false},
	} {
		got, ok := ParseReset(tc.text, now, tc.loc)
		if ok != tc.ok || (ok && Time(got) != tc.want) {
			t.Errorf("ParseReset(%q) = %s, %v; want %s, %v", tc.text, Time(got), ok, tc.want, tc.ok)
		}
	}
}

func TestDataFromRateLimits(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	at := func(d time.Duration) string { return jsonNum(now.Add(d).Unix()) }
	for name, tc := range map[string]struct {
		in             string
		known, limited bool
		window         string
		reset          time.Duration
	}{
		"empty":      {``, false, false, "", 0},
		"free":       {`{"five_hour":{"used_percentage":40,"resets_at":` + at(time.Hour) + `}}`, true, false, "", 0},
		"five hour":  {`{"five_hour":{"used_percentage":100,"resets_at":` + at(time.Hour) + `}}`, true, true, WindowFiveHour, time.Hour},
		"over":       {`{"five_hour":{"used_percentage":104.5,"resets_at":` + at(time.Hour) + `}}`, true, true, WindowFiveHour, time.Hour},
		"later wins": {`{"five_hour":{"used_percentage":100,"resets_at":` + at(time.Hour) + `},"seven_day":{"used_percentage":100,"resets_at":` + at(48*time.Hour) + `}}`, true, true, WindowSevenDay, 48 * time.Hour},
		"past reset": {`{"five_hour":{"used_percentage":100,"resets_at":` + at(-time.Hour) + `}}`, true, false, "", 0},
		"no reset":   {`{"five_hour":{"used_percentage":100}}`, true, false, "", 0},
		"junk":       {`[1,2]`, false, false, "", 0},
	} {
		d := DataFromRateLimits(raw(tc.in), now)
		if d.Known != tc.known || d.Limited != tc.limited || d.Window != tc.window ||
			(tc.limited && !d.ResetsAt.Equal(now.Add(tc.reset))) {
			t.Errorf("%s: %+v", name, d)
		}
	}
}

func jsonNum(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestLoadSettings(t *testing.T) {
	cfg := config.Load(filepath.Join(t.TempDir(), "missing.json"))
	s, err := LoadSettings(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s.Mode != ModeSwitch || s.Margin != 60*time.Second || s.Fallback != 1800*time.Second || s.MaxResumes != 3 ||
		s.Prompt != "The usage limit has reset. Continue where you left off." || s.EscalateIssue != 0 {
		t.Fatalf("defaults %+v", s)
	}
	dir := t.TempDir()
	for key, val := range map[string]string{
		"mode": `"wait"`, "resumeMarginSeconds": `-1`, "fallbackSleepSeconds": `0`, "maxResumes": `0`, "resumePrompt": `"  "`,
	} {
		p := filepath.Join(dir, key+".json")
		os.WriteFile(p, []byte(`{"limits":{"`+key+`":`+val+`}}`), 0o666)
		if _, err := LoadSettings(config.Load(p)); err == nil || !strings.Contains(err.Error(), "limits."+key) {
			t.Errorf("%s=%s: %v", key, val, err)
		}
	}
	p := filepath.Join(dir, "ok.json")
	os.WriteFile(p, []byte(`{"limits":{"mode":"sleep","maxResumes":5},"orchestrator":{"escalateIssue":9}}`), 0o666)
	s, err = LoadSettings(config.Load(p))
	if err != nil || s.Mode != ModeSleep || s.MaxResumes != 5 || s.EscalateIssue != 9 {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestStoreRoundTripKeepsOtherKeys(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".rota"), 0o777)
	os.WriteFile(filepath.Join(root, ".rota", "workers.json"), []byte(`{"slots":[{"name":"ben"}],"escalations":[{"id":"e1"}]}`), 0o666)
	e, err := Append(root, Entry{Session: "ben", Window: WindowUnknown, Source: SourceText, DetectedAt: "x", Action: ActionSleep, Status: StatusWaiting})
	if err != nil || e.ID != "l1" {
		t.Fatalf("%+v %v", e, err)
	}
	e.Status, e.Cycles = StatusResumed, 1
	if err := Save(root, e); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, ".rota", "workers.json"))
	v, _ := jsonx.Decode(b)
	doc := v.(*jsonx.Object)
	for _, k := range []string{"slots", "escalations", "limits"} {
		if _, ok := doc.Get(k); !ok {
			t.Errorf("lost %s: %s", k, b)
		}
	}
	got := Load(root)
	if len(got) != 1 || got[0].Status != StatusResumed || got[0].Cycles != 1 {
		t.Fatalf("%+v", got)
	}
}
