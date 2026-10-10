package worker

import (
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/golden"
)

// paneFixtures are static pane texts covering every rule of the classifier,
// including the edge cases the rules are written around.
var paneFixtures = map[string]string{
	"blank":               "",
	"idle":                "  claude ready\n> \n",
	"done url":            "work\nROTA-DONE w1 https://github.com/o/r/pull/12\n",
	"done branch":         "ROTA-DONE w1 rota-worker/w1-t1\n",
	"done bare":           "ROTA-DONE w1\nnext line\n",
	"done multiline":      "ROTA-DONE w1\nsomething after\n",
	"blocked":             "ROTA-BLOCKED w1: which of A or B?\n",
	"blocked + done":      "ROTA-DONE w1 x\nROTA-BLOCKED w1: still a question\n",
	"blocked indent":      "   ROTA-BLOCKED   w1 :   spaced out  \n",
	"retry":               "API Error: Overloaded\nRetrying in 4 seconds\n",
	"api error":           "doing things\nAPI Error: 529 Overloaded\n",
	"crashed":             "Resume this session with: claude --resume abc\n",
	"limit":               "You've hit your usage limit. resets at 5pm\n",
	"limit funds":         "You've hit your limit\nAdd funds to continue\n",
	"limit approaching":   "Approaching usage limit\n",
	"limit reset":         "limit will reset at 3am\n",
	"limit prose":         "I read the rate limit code in limiter.go\n",
	"permission":          "Do you want to proceed?\n 1. Yes\n 2. No, and tell Claude what to do differently\n",
	"permission allow":    "Allow Bash(git push) to run?\n",
	"permission dont":     "Yes, and don't ask again for this\n",
	"sentinel over limit": "reached your usage limit\nROTA-DONE w1 https://gitlab.com/o/r/-/merge_requests/3\n",
	"unicode":             "héllo wörld — ünïcode ✓\n❯ \n",
	"crlf":                "ROTA-BLOCKED w1: windows\r\nline\r\n",
	"long limit line":     "usage limit reached " + strings.Repeat("x", 200) + "\n",
}

// TestClassify checks every pane fixture under every polled status against the
// state and evidence the retired shell classifier gave.
func TestClassify(t *testing.T) {
	statuses := []string{"", "idle", "working", "blocked", "done", "unknown", "gone"}
	got := map[string][2]string{} // "<pane>/<status>" -> state, evidence
	for name, text := range paneFixtures {
		for _, status := range statuses {
			state, evidence := Classify(text, false, 60, status)
			got[name+"/"+status] = [2]string{state, evidence}
		}
	}
	golden.Check(t, map[string]any{"panes": paneFixtures, "statuses": statuses, "argv": "--fixture <pane> --slot w1 [--status <status>]"}, got)
}

func TestClassifyMovementAndTailWindow(t *testing.T) {
	if st, ev := Classify("anything\n", true, 60, ""); st != StateBusy || ev != "pane changed between captures" {
		t.Errorf("moved: %s %q", st, ev)
	}
	// a sentinel outranks movement
	if st, _ := Classify("ROTA-DONE w1 rota-worker/w1\n", true, 60, "working"); st != StateDone {
		t.Errorf("sentinel vs movement: %s", st)
	}
	// LIMITED outranks movement
	if st, _ := Classify("usage limit reached\n", true, 60, ""); st != StateLimited {
		t.Errorf("limit vs movement: %s", st)
	}
	// only the last N lines count
	text := "ROTA-DONE w1 old\n" + strings.Repeat("noise\n", 80)
	if st, _ := Classify(text, false, 60, ""); st != StateIdle {
		t.Errorf("a sentinel scrolled out of the window still counted: %s", st)
	}
	if st, _ := Classify(text, false, 100, ""); st != StateDone {
		t.Errorf("a sentinel inside the window was missed: %s", st)
	}
}

func TestPollFixtureMode(t *testing.T) {
	fx := filepath.Join(t.TempDir(), "p.txt")
	os.WriteFile(fx, []byte("ROTA-BLOCKED w1: ?\n"), 0o644)
	res, err := PollFixture(fx, "", "", 0)
	if err != nil || len(res.Slots) != 1 || res.Slots[0].Name != "fixture" || res.Slots[0].State != StateBlocked || res.Changed {
		t.Errorf("%+v %v", res, err)
	}
	if res, _ = PollFixture(fx, "w3", "", 0); res.Slots[0].Name != "w3" {
		t.Errorf("name = %s", res.Slots[0].Name)
	}
	if _, err = PollFixture("/no/such/file", "", "", 0); exitOf(err) != exitcode.ExitUsage {
		t.Errorf("missing fixture: %v", err)
	}
}

func pollRegistry(t *testing.T, kind string) (string, *fakeHost) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 2, Base: "main"})
	f := &fakeHost{name: kind, inSession: true, panes: map[string][]string{}, status: map[string]string{}}
	return dir, f
}

func TestPollRecordsStateAndPRURL(t *testing.T) {
	dir, f := pollRegistry(t, "tmux")
	f.panes["w1"] = []string{"static\n", "static\nROTA-DONE w1 https://github.com/o/r/pull/9\n"}
	f.panes["w2"] = []string{"a\n", "a\n"}
	before, _ := os.ReadFile(RegistryPath(dir))
	res, err := envWith(f).Poll(bg, dir, PollOpts{Lines: 60})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Slots) != 2 || res.Slots[0] != (PollRow{"w1", StateDone, "https://github.com/o/r/pull/9"}) ||
		res.Slots[1] != (PollRow{"w2", StateIdle, "static pane, no sentinel"}) || !res.Changed {
		t.Errorf("%+v", res)
	}
	if got := slotField(t, dir, "w1", "state"); got != "done" {
		t.Errorf("state = %s", got)
	}
	if got := slotField(t, dir, "w1", "pr"); got != "https://github.com/o/r/pull/9" {
		t.Errorf("pr = %s", got)
	}
	if got := slotField(t, dir, "w2", "pr"); got != "<null>" {
		t.Errorf("pr = %s", got)
	}
	after, _ := os.ReadFile(RegistryPath(dir))
	if string(before) == string(after) {
		t.Error("registry unchanged")
	}
	// the same poll again changes nothing
	f.panes["w1"] = []string{"x\nROTA-DONE w1 https://github.com/o/r/pull/9\n", "x\nROTA-DONE w1 https://github.com/o/r/pull/9\n"}
	f.panes["w2"] = []string{"a\n", "a\n"}
	if res, _ = envWith(f).Poll(bg, dir, PollOpts{Lines: 60}); res.Changed {
		t.Error("an unchanged poll must report changed=false")
	}
}

// A branch name after ROTA-DONE must not become slot.pr: `gh pr merge` on a
// branch fails where the gate's local merge would have worked.
func TestPollOnlyURLShapedDoneBecomesThePR(t *testing.T) {
	dir, f := pollRegistry(t, "tmux")
	f.panes["w1"] = []string{"x\n", "ROTA-DONE w1 rota-worker/w1-t1\n"}
	f.panes["w2"] = []string{"x\n", "ROTA-DONE w2 https://example.com/not/a/pr\n"}
	envWith(f).Poll(bg, dir, PollOpts{Lines: 60})
	for _, s := range []string{"w1", "w2"} {
		if got := slotField(t, dir, s, "pr"); got != "<null>" {
			t.Errorf("%s pr = %s", s, got)
		}
		if got := slotField(t, dir, s, "state"); got != "done" {
			t.Errorf("%s state = %s", s, got)
		}
	}
}

func TestPollNamedSlotAndSettle(t *testing.T) {
	dir, f := pollRegistry(t, "tmux")
	f.panes["w2"] = []string{"a\n", "b\n"}
	res, err := envWith(f).Poll(bg, dir, PollOpts{Slot: "w2", Lines: 60})
	if err != nil || len(res.Slots) != 1 || res.Slots[0].State != StateBusy {
		t.Fatalf("%+v %v", res, err)
	}
	for _, c := range f.calls {
		if strings.Contains(c, "w1") {
			t.Errorf("polled an unnamed slot: %v", f.calls)
		}
	}
	if _, err = envWith(f).Poll(bg, dir, PollOpts{Slot: "w9"}); exitOf(err) != exitcode.ExitResolution {
		t.Errorf("unknown slot: %v", err)
	}
}

func TestPollNotifiesOnTheTransitionOnly(t *testing.T) {
	dir, f := pollRegistry(t, "herdr")
	f.status["w1"] = "blocked"
	f.panes["w1"] = []string{"dialog\n", "dialog\n", "dialog\n", "dialog\n"}
	f.panes["w2"] = []string{"x\n", "x\n", "x\n", "x\n"}
	e := envWith(f)
	if _, err := e.Poll(bg, dir, PollOpts{Lines: 60}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Poll(bg, dir, PollOpts{Lines: 60}); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, "notify rota worker w1: NEEDS-PERMISSION") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("notifications = %d, want 1 (a poll loop must not ring every few seconds): %v", n, f.calls)
	}
	if got := slotField(t, dir, "w1", "state"); got != "needs-permission" {
		t.Errorf("state = %s", got)
	}
}

func TestPollHostFailureAndNoRegistry(t *testing.T) {
	dir := newProject(t, `{}`)
	f := tmuxFake()
	res, err := envWith(f).Poll(bg, dir, PollOpts{})
	if err != nil || len(res.Slots) != 0 || res.Changed {
		t.Errorf("no registry: %+v %v", res, err)
	}
	f.requireErr = fmt.Errorf("tmux is not installed")
	if _, err = envWith(f).Poll(bg, dir, PollOpts{}); exitOf(err) != exitcode.ExitUnavailable {
		t.Errorf("host missing: %v", err)
	}
}

// activeAt is the stall clock of `round reconcile`: dispatch stamps it, poll
// re-stamps it only when the classified state differs from the recorded one.
func TestActiveAtStampedByDispatchAndRestampedByPollOnAStateChange(t *testing.T) {
	dir, f := pollRegistry(t, "tmux")
	f.panes["w1"] = []string{"? for shortcuts\n"}
	const dispatched = "2026-10-02T15:04:05Z"
	if _, err := envWith(f).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "go\n"), Task: "T1"}); err != nil {
		t.Fatal(err)
	}
	if got := slotField(t, dir, "w1", "activeAt"); got != dispatched {
		t.Fatalf("dispatch stamps activeAt: %q", got)
	}
	if got := slotField(t, dir, "w2", "activeAt"); got != "<null>" {
		t.Errorf("a slot never dispatched has none: %q", got)
	}

	later := envWith(f)
	later.Now = func() time.Time { return time.Date(2026, 10, 2, 16, 0, 0, 0, time.UTC) }
	// busy -> busy: the pane moves, the state is the recorded one: no restamp.
	f.panes["w1"] = []string{"a\n", "b\n"}
	if _, err := later.Poll(bg, dir, PollOpts{Slot: "w1", Lines: 60}); err != nil {
		t.Fatal(err)
	}
	if got := slotField(t, dir, "w1", "activeAt"); got != dispatched {
		t.Errorf("an unchanged state must not restamp: %q", got)
	}
	// busy -> done: a change.
	f.panes["w1"] = []string{"x\n", "x\nROTA-DONE w1 https://github.com/o/r/pull/9\n"}
	if _, err := later.Poll(bg, dir, PollOpts{Slot: "w1", Lines: 60}); err != nil {
		t.Fatal(err)
	}
	if got := slotField(t, dir, "w1", "activeAt"); got != "2026-10-02T16:00:00Z" {
		t.Errorf("a state change restamps: %q", got)
	}
}

// TestClassifySentinelAfterReplyBullet pins #210: Claude Code v2.1.288 starts
// a reply with "● ", so a worker whose whole reply is the sentinel shows it
// after the bullet. The fixture is a real pane end (the input box and status
// line, whose usage glyphs must not read as a sentinel).
func TestClassifySentinelAfterReplyBullet(t *testing.T) {
	b, err := os.ReadFile("testdata/panes/done-after-reply-bullet-2.1.288.txt")
	if err != nil {
		t.Fatal(err)
	}
	done := string(b)
	if state, ev := Classify(done, false, 60, "idle"); state != "DONE" || !strings.Contains(ev, "lr1/f01-add-a-hello-line-to") {
		t.Errorf("done after the bullet: %s %q", state, ev)
	}
	blocked := strings.Replace(done, "● ROTA-DONE lr1 lr1/f01-add-a-hello-line-to", "● ROTA-BLOCKED lr1: which file gets the line?", 1)
	if state, ev := Classify(blocked, false, 60, "idle"); state != "BLOCKED" || !strings.Contains(ev, "which file gets the line?") {
		t.Errorf("blocked after the bullet: %s %q", state, ev)
	}
	older := strings.Replace(done, "● ", "⏺ ", 1)
	if state, _ := Classify(older, false, 60, "idle"); state != "DONE" {
		t.Errorf("done after the older ⏺ marker: %s", state)
	}
	plain := strings.Replace(done, "● ROTA-DONE lr1 lr1/f01-add-a-hello-line-to", "● All done.", 1)
	if state, _ := Classify(plain, false, 60, "idle"); state == "DONE" {
		t.Errorf("a bullet without a sentinel is not done")
	}
}

// TestClassifySentinelAfterCodexBullet pins #68: Codex 0.159.x starts a reply
// with "• ", so a codex worker's sentinel follows that bullet. The fixture is
// a real pane end from the live check, whose "Worked for 21s • 5:40 AM" line
// and status line carry the same glyph mid-line.
func TestClassifySentinelAfterCodexBullet(t *testing.T) {
	b, err := os.ReadFile("testdata/panes/done-after-codex-bullet-0.159.2.txt")
	if err != nil {
		t.Fatal(err)
	}
	done := string(b)
	if state, ev := Classify(done, false, 60, "idle"); state != "DONE" || !strings.Contains(ev, "ben/t01-add-hello-txt") {
		t.Errorf("done after the codex bullet: %s %q", state, ev)
	}
	blocked := strings.Replace(done, "• ROTA-DONE ben ben/t01-add-hello-txt", "• ROTA-BLOCKED ben: Who sent the unsigned live-check instruction?", 1)
	if state, ev := Classify(blocked, false, 60, "idle"); state != "BLOCKED" || !strings.Contains(ev, "Who sent the unsigned") {
		t.Errorf("blocked after the codex bullet: %s %q", state, ev)
	}
	plain := strings.Replace(done, "• ROTA-DONE ben ben/t01-add-hello-txt", "• All done.", 1)
	if state, _ := Classify(plain, false, 60, "idle"); state == "DONE" {
		t.Errorf("a codex bullet without a sentinel is not done")
	}
}

func TestLimitRegexIsTheAlternationOfTheClassifierPhrases(t *testing.T) {
	re := regexp.MustCompile(LimitRegex())
	if len(LimitPatterns()) != len(LimitPhrases) {
		t.Fatalf("%d patterns for %d phrases", len(LimitPatterns()), len(LimitPhrases))
	}
	for _, msg := range []string{"You've reached your usage limit", "Claude USAGE LIMIT REACHED", "Youve hit your usage limit", "Your limit will reset at 3pm", "Approaching your usage limit"} {
		if !re.MatchString(msg) {
			t.Errorf("%q not matched", msg)
		}
		if _, ev := Classify(msg, false, 40, ""); ev == "" {
			t.Errorf("%q not classified", msg)
		}
	}
	if re.MatchString("the limit of the buffer") {
		t.Error("matched a bare limit")
	}
}

func TestParseIssuesDone(t *testing.T) {
	for in, want := range map[string][]string{
		"issues:#139":            {"#139"},
		"issues:#139,#140, #141": {"#139", "#140", "#141"},
		"issues: 7,8":            {"#7", "#8"},
	} {
		got, ok := ParseIssuesDone(in)
		if !ok || strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("%q: %v %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "rota-worker/w1", "https://github.com/o/r/pull/12", "issues:", "issues:#a", "issues:#1,"} {
		if got, ok := ParseIssuesDone(in); ok {
			t.Errorf("%q parsed as %v", in, got)
		}
	}
}

func TestClassifyIssuesDone(t *testing.T) {
	st, ev := Classify("work\nROTA-DONE ben issues:#139,#140\n", false, 60, "")
	if st != StateDone || ev != "issues:#139,#140" {
		t.Errorf("%s %q", st, ev)
	}
}

// A review item's done line names the issues it filed; Poll records them on the
// slot and leaves slot.pr empty.
func TestPollRecordsIssuesDone(t *testing.T) {
	dir, f := pollRegistry(t, "tmux")
	f.panes["w1"] = []string{"x\n", "ROTA-DONE w1 issues:#139,#140\n"}
	f.panes["w2"] = []string{"a\n", "a\n"}
	if _, err := envWith(f).Poll(bg, dir, PollOpts{Lines: 60}); err != nil {
		t.Fatal(err)
	}
	if got := LoadRegistryTolerant(dir).Slot("w1").Issues(); strings.Join(got, ",") != "#139,#140" {
		t.Errorf("issues = %v", got)
	}
	if got := slotField(t, dir, "w1", "pr"); got != "<null>" {
		t.Errorf("pr = %s", got)
	}
	if got := slotField(t, dir, "w1", "state"); got != "done" {
		t.Errorf("state = %s", got)
	}
}

// A worker that opened a PR but printed no URL leaves the slot without one;
// poll records the open PR headed by the slot's branch (#210).
func TestOpenPRsByHeadRecordsOnlyUnrecordedSlots(t *testing.T) {
	w := newWorld(t, "")
	w.forge("listed", "1")
	reg := LoadRegistryTolerant(w.dir)
	polled := map[string]PollRow{"w1": {Name: "w1", State: StateIdle}}
	if got := w.env(false).withDefaults().openPRsByHead(bg, w.dir, reg, polled)["w1"]; got != ghURL {
		t.Errorf("w1 -> %q", got)
	}
	// an unpolled slot, or one that already records its PR, needs no listing
	w.forge("listError", "must not be called")
	if got := w.env(false).withDefaults().openPRsByHead(bg, w.dir, reg, nil); got != nil {
		t.Errorf("unpolled: %v", got)
	}
	w.setSlot(ghURL, "")
	if got := w.env(false).withDefaults().openPRsByHead(bg, w.dir, LoadRegistryTolerant(w.dir), polled); got != nil {
		t.Errorf("recorded: %v", got)
	}
}

// An idle slot with no sentinel whose branch heads an open PR is DONE with that
// PR and says the sentinel was missing (#227). No PR, or a listing that fails,
// leaves it idle (stuck) as before.
func TestPromoteIdleWithPR(t *testing.T) {
	idle := func() []PollRow { return []PollRow{{"w1", StateIdle, "static pane, no sentinel"}} }
	w := newWorld(t, "")

	w.forge("listed", "1")
	rows, notes := w.env(false).withDefaults().promoteIdleWithPR(bg, w.dir, idle())
	if rows[0] != (PollRow{"w1", StateDone, ghURL}) || notes["w1"] != missingSentinelNote {
		t.Errorf("open PR: %+v %v", rows, notes)
	}

	w.forge("listed", "0")
	rows, notes = w.env(false).withDefaults().promoteIdleWithPR(bg, w.dir, idle())
	if rows[0].State != StateIdle || notes != nil {
		t.Errorf("no PR: %+v %v", rows, notes)
	}

	w.forge("listError", "boom")
	rows, notes = w.env(false).withDefaults().promoteIdleWithPR(bg, w.dir, idle())
	if rows[0].State != StateIdle || notes != nil {
		t.Errorf("lookup error: %+v %v", rows, notes)
	}

	// only an idle row is looked at
	w.forge("listed", "1")
	busy := []PollRow{{"w1", StateBusy, "x"}}
	if rows, notes = w.env(false).withDefaults().promoteIdleWithPR(bg, w.dir, busy); rows[0].State != StateBusy || notes != nil {
		t.Errorf("busy: %+v %v", rows, notes)
	}
}

// TestClassifyLastSentinelWins pins #321: a ROTA-DONE printed after an answered
// ROTA-BLOCKED (and its relay) is the slot's state, and sentinel text quoted in
// a relay is not the worker's own signal.
func TestClassifyLastSentinelWins(t *testing.T) {
	relay := "--- ORCHESTRATOR (round 5) ---\n[ORCHESTRATOR RELAY — forwarded.]\n\nPush is allowed now.\nROTA-BLOCKED ben: git push to origin is denied\n\n"
	pane := "● ROTA-BLOCKED ben: git push to origin is denied\n\n" + relay +
		"● pushing\n● ROTA-DONE ben https://github.com/l4ci/rota/pull/314\n"
	if st, ev := Classify(pane, false, 60, "idle"); st != StateDone || ev != "https://github.com/l4ci/rota/pull/314" {
		t.Errorf("blocked, relay, done: %s %q", st, ev)
	}
	// A relay that quotes a blocked line, with no later worker output, is not blocked.
	quoted := "● working\n" + relay
	if st, _ := Classify(quoted, false, 60, "idle"); st == StateBlocked {
		t.Errorf("relay-quoted sentinel counted as the worker's: %s", st)
	}
	// A done line followed by a fresh blocked line stays blocked.
	again := "● ROTA-DONE ben x\n● ROTA-BLOCKED ben: new question\n"
	if st, ev := Classify(again, false, 60, "idle"); st != StateBlocked || ev != "new question" {
		t.Errorf("done then blocked: %s %q", st, ev)
	}
}

// A slot herdr cannot classify carries herdr's own explanation; a failing
// explain leaves today's evidence and the poll still succeeds.
func TestPollUnknownCarriesExplainExcerpt(t *testing.T) {
	dir, f := pollRegistry(t, "herdr")
	f.status["w1"], f.status["w2"] = "unknown", "unknown"
	f.explain = map[string]string{"w1": "no recognised agent process / foreground is vim"}
	f.panes["w1"] = []string{"x\n", "x\n"}
	f.panes["w2"] = []string{"x\n", "x\n"}
	res, err := envWith(f).Poll(bg, dir, PollOpts{Lines: 60})
	if err != nil {
		t.Fatal(err)
	}
	const base = "herdr cannot classify the agent — inspect the tab; not proof it finished"
	if got := res.Slots[0]; got.State != StateUnknown || got.Evidence != base+": herdr explain: no recognised agent process / foreground is vim" {
		t.Errorf("w1 = %+v", got)
	}
	if got := res.Slots[1]; got.State != StateUnknown || got.Evidence != base {
		t.Errorf("a failing explain must leave the evidence unchanged: %+v", got)
	}
}

// The review cursor starts when the slot reports done: comments and bot posts
// from before the PR was handed back are never review input. A re-done after a
// relay keeps the cursor the relay moved.
func TestPollBaselinesTheReviewCursorAtDone(t *testing.T) {
	dir, f := pollRegistry(t, "tmux")
	f.panes["w1"] = []string{"x\n", "x\nROTA-DONE w1 https://github.com/o/r/pull/9\n"}
	f.panes["w2"] = []string{"a\n", "a\n"}
	if _, err := envWith(f).Poll(bg, dir, PollOpts{Lines: 60}); err != nil {
		t.Fatal(err)
	}
	seen := slotField(t, dir, "w1", "reviewSeen")
	if seen == "<null>" || seen == "" {
		t.Fatalf("no baseline at done: %q", seen)
	}
	if got := slotField(t, dir, "w2", "reviewSeen"); got != "<null>" {
		t.Errorf("a busy slot got a baseline: %q", got)
	}
	if _, err := UpdateSlot(dir, "w1", func(s *Slot) { s.SetReviewSeen("2026-10-08T10:00:00Z") }); err != nil {
		t.Fatal(err)
	}
	f.panes["w1"] = []string{"y\nROTA-DONE w1 https://github.com/o/r/pull/9\n", "y\nROTA-DONE w1 https://github.com/o/r/pull/9\n"}
	f.panes["w2"] = []string{"a\n", "a\n"}
	if _, err := envWith(f).Poll(bg, dir, PollOpts{Lines: 60}); err != nil {
		t.Fatal(err)
	}
	if got := slotField(t, dir, "w1", "reviewSeen"); got != "2026-10-08T10:00:00Z" {
		t.Errorf("a later poll moved the cursor: %q", got)
	}
}

// The pane's last ROTA-DONE outlives its issue: a slot that took the next issue
// would read the previous PR back as its own while that PR waits in `prs` (#648).
func TestPollDoesNotRecordAPRAnotherRecordHolds(t *testing.T) {
	dir, f := pollRegistry(t, "tmux")
	const url = "https://github.com/o/r/pull/9"
	if err := Update(dir, func(d *Doc) { d.QueuePR(QueuedPR{Issue: "12", PR: url, From: "w1", Branch: "w1/12-x"}) }); err != nil {
		t.Fatal(err)
	}
	f.panes["w1"] = []string{"x\nROTA-DONE w1 " + url + "\n", "x\nROTA-DONE w1 " + url + "\n"}
	f.panes["w2"] = []string{"a\n", "a\n"}
	if _, err := envWith(f).Poll(bg, dir, PollOpts{Lines: 60}); err != nil {
		t.Fatal(err)
	}
	if got := slotField(t, dir, "w1", "pr"); got != "<null>" {
		t.Errorf("a queued PR is not the slot's: pr = %s", got)
	}
}
