package worker

import (
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/stamp"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/harness"
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
	res, err := PollFixture(fx, "", "", "", 0)
	if err != nil || len(res.Slots) != 1 || res.Slots[0].Name != "fixture" || res.Slots[0].State != StateBlocked || res.Changed {
		t.Errorf("%+v %v", res, err)
	}
	if res, _ = PollFixture(fx, "w3", "", "", 0); res.Slots[0].Name != "w3" {
		t.Errorf("name = %s", res.Slots[0].Name)
	}
	if _, err = PollFixture("/no/such/file", "", "", "", 0); exitOf(err) != exitcode.ExitUsage {
		t.Errorf("missing fixture: %v", err)
	}
}

func pollRegistry(t *testing.T, kind host.Kind) (string, *fakeHost) {
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

// A reader of the registry never sees done without the evidence the same done
// line carried: the state and the PR (or issues list) share one write.
func TestPollRecordsDoneAndEvidenceInOneWrite(t *testing.T) {
	for _, tc := range []struct{ line, field string }{
		{"ROTA-DONE w1 https://github.com/o/r/pull/9", "pr"},
		{"ROTA-DONE w1 issues:#4,#5", "issues"},
	} {
		dir, f := pollRegistry(t, "tmux")
		f.panes["w1"] = []string{"x\n", "x\n" + tc.line + "\n"}
		f.panes["w2"] = []string{"a\n", "a\n"}
		stop, bad := make(chan struct{}), make(chan string, 1)
		go func() {
			defer close(bad)
			for {
				select {
				case <-stop:
					return
				default:
				}
				reg, err := LoadRegistry(dir)
				if err != nil {
					continue
				}
				if s := reg.Slot("w1"); s != nil && s.State() == "done" && s.PR() == "" && len(s.Issues()) == 0 {
					bad <- "done recorded without evidence"
					return
				}
			}
		}()
		_, err := envWith(f).Poll(bg, dir, PollOpts{Lines: 60})
		close(stop)
		if err != nil {
			t.Fatal(err)
		}
		if msg, ok := <-bad; ok {
			t.Errorf("%s: %s", tc.field, msg)
		}
		reg := LoadRegistryTolerant(dir)
		if s := reg.Slot("w1"); s.State() != "done" || (s.PR() == "" && len(s.Issues()) == 0) {
			t.Errorf("%s: final state %s pr=%q issues=%v", tc.field, s.State(), s.PR(), s.Issues())
		}
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

// opencode-retry is a live capture of OpenCode 1.17.9 against an unreachable
// provider. The other OpenCode fixtures are built from the spike's description
// of the TUI (.rota/spikes/opencode-worker.md): the alternate-screen viewport,
// the user message echoed under a "┃" gutter, "esc interrupt" while a turn
// runs and the dialog and error texts it printed. They are not live captures:
// the provider keys were expired when this was written.
func opencodePane(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/panes/opencode-" + name + "-1.17.9.txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// OpenCode's wording is read on opencode slots only: a Claude or Codex worker
// that echoes it (cat of this repo's source, a fixture) keeps the state it had.
func TestClassifyOpenCodeVocabularyIsScopedToItsKind(t *testing.T) {
	for _, kind := range []string{"", harness.Claude, harness.Codex} {
		for _, name := range []string{"busy", "retry", "expired-key", "invalid-model"} {
			st, ev := ClassifyFor(kind, opencodePane(t, name), false, 60, "")
			if st != "IDLE" {
				t.Errorf("kind %q on opencode-%s: %s %q, want IDLE", kind, name, st, ev)
			}
		}
	}
	for name, want := range map[string]string{"expired-key": "LIMITED", "invalid-model": "BLOCKED", "busy": "BUSY", "retry": "BUSY"} {
		if st, ev := ClassifyFor(harness.OpenCode, opencodePane(t, name), false, 60, ""); st != want {
			t.Errorf("opencode on opencode-%s: %s %q, want %s", name, st, ev, want)
		}
	}
	// The gutter is an opencode reading too.
	if st, _ := ClassifyFor(harness.Claude, "  ┃  ROTA-DONE ben https://example.test/pr/1\n", false, 60, ""); st == "DONE" {
		t.Error("a guttered sentinel is not done on a claude pane")
	}
}

// The echoed brief names ROTA-DONE ben <pr-url>. On a line that starts with the
// gutter it is the user's text, not the worker's; a real URL after the gutter is.
func TestClassifyOpenCodeGutteredPlaceholderIsNotDone(t *testing.T) {
	for _, c := range []struct{ line, want string }{
		{"  ┃ ROTA-DONE ben <pr-url>", "IDLE"},
		{"  ┃  ROTA-DONE ben <pr-url> and stop.", "IDLE"},
		{"  ┃ ROTA-BLOCKED ben: <one question in plain language>", "IDLE"},
		{"  ┃ ROTA-DONE ben https://example.test/pr/1", "DONE"},
		{"  ┃ ROTA-BLOCKED ben: which file?", "BLOCKED"},
	} {
		if st, ev := ClassifyFor(harness.OpenCode, "  Working.\n"+c.line+"\n\n  tab agents  ctrl+p commands\n", false, 60, ""); st != c.want {
			t.Errorf("%q: %s %q, want %s", c.line, st, ev, c.want)
		}
	}
}

func TestClassifyOpenCode(t *testing.T) {
	// The echoed brief names ROTA-DONE ben <pr-url> and ROTA-BLOCKED ben:
	// <one question ...> as placeholders; neither is a sentinel.
	if state, ev := ClassifyFor(harness.OpenCode, opencodePane(t, "busy"), false, 60, ""); state != "BUSY" || !strings.Contains(ev, "esc interrupt") {
		t.Errorf("busy pane: %s %q", state, ev)
	}
	// A real capture: a provider that cannot be reached, OpenCode retrying.
	if state, ev := ClassifyFor(harness.OpenCode, opencodePane(t, "retry"), false, 60, ""); state != "BUSY" || !strings.Contains(ev, "retry") {
		t.Errorf("retry pane: %s %q", state, ev)
	}
	if state, ev := ClassifyFor(harness.OpenCode, opencodePane(t, "expired-key"), false, 60, ""); state != "LIMITED" || !strings.Contains(ev, "needs a human") {
		t.Errorf("expired key: %s %q", state, ev)
	}
	if state, ev := ClassifyFor(harness.OpenCode, opencodePane(t, "invalid-model"), false, 60, ""); state != "BLOCKED" || !strings.Contains(ev, "opencode-go/qwen3.6-plus") {
		t.Errorf("invalid model: %s %q", state, ev)
	}
	done := opencodePane(t, "done")
	if state, ev := ClassifyFor(harness.OpenCode, done, false, 60, ""); state != "DONE" || ev != "https://github.com/l4ci/rota/pull/600" {
		t.Errorf("done: %s %q", state, ev)
	}
	// The worker's reply may carry the gutter too.
	guttered := strings.Replace(done, "  ROTA-DONE ben https", "  ┃  ROTA-DONE ben https", 1)
	if state, ev := ClassifyFor(harness.OpenCode, guttered, false, 60, ""); state != "DONE" || ev != "https://github.com/l4ci/rota/pull/600" {
		t.Errorf("guttered done: %s %q", state, ev)
	}
	// The echo alone is not done, with or without its header in the viewport.
	echo := strings.Replace(done, "  ROTA-DONE ben https://github.com/l4ci/rota/pull/600\n", "  All finished.\n", 1)
	if state, _ := ClassifyFor(harness.OpenCode, echo, false, 60, ""); state == "DONE" || state == "BLOCKED" {
		t.Errorf("an echoed brief is not a sentinel: %s", state)
	}
	scrolled := echo[strings.Index(echo, "  ┃  You are a worker"):]
	if state, _ := ClassifyFor(harness.OpenCode, scrolled, false, 60, ""); state == "DONE" || state == "BLOCKED" {
		t.Errorf("an echo scrolled past its header is not a sentinel: %s", state)
	}
}

func TestLimitRegexIsTheAlternationOfTheClassifierPhrases(t *testing.T) {
	re := regexp.MustCompile(LimitRegex())
	if len(limitPatterns) != len(limitPhrases) {
		t.Fatalf("%d patterns for %d phrases", len(limitPatterns), len(limitPhrases))
	}
	for _, msg := range []string{"You've reached your usage limit", "Claude USAGE LIMIT REACHED", "Youve hit your usage limit", "Your limit will reset at 3pm", "Approaching your usage limit"} {
		if !re.MatchString(msg) {
			t.Errorf("%q not matched", msg)
		}
		if _, _, ok := FindLimit("", msg); ok == strings.HasPrefix(msg, "Approaching") {
			t.Errorf("%q: FindLimit ok=%v", msg, ok)
		}
	}
	if re.MatchString("the limit of the buffer") {
		t.Error("matched a bare limit")
	}
}

func TestParseIssuesDone(t *testing.T) {
	for in, want := range map[string][]string{
		"issues:#139":             {"#139"},
		"issues:#139,#140, #141":  {"#139", "#140", "#141"},
		"issues: 7,8":             {"#7", "#8"},
		"issues:PROJ-123":         {"PROJ-123"},
		"issues:#7, PROJ-8,ENG-9": {"#7", "PROJ-8", "ENG-9"},
	} {
		got, ok := ParseIssuesDone(in)
		if !ok || strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("%q: %v %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "rota-worker/w1", "https://github.com/o/r/pull/12", "issues:", "issues:#a", "issues:#1,", "issues:proj-1", "issues:PROJ-", "issues:#PROJ-1"} {
		if got, ok := ParseIssuesDone(in); ok {
			t.Errorf("%q parsed as %v", in, got)
		}
	}
}

func TestClassifyIssuesDoneKeys(t *testing.T) {
	st, ev := Classify("work\nROTA-DONE ben issues:PROJ-1,PROJ-2\n", false, 60, "")
	if st != StateDone || ev != "issues:PROJ-1,PROJ-2" {
		t.Errorf("%v %q", st, ev)
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

// TestClassifyDoneBeforeRelayIsNotDone pins #873: a bounce relay lands under the
// worker's old ROTA-DONE line, which stays in the pane. That sentinel predates
// the relay, so the slot is not done until the worker prints a new one.
func TestClassifyDoneBeforeRelayIsNotDone(t *testing.T) {
	relay := "--- ORCHESTRATOR (round 23) ---\n[ORCHESTRATOR RELAY — forwarded.]\n\nREVIEW https://github.com/l4ci/rota/pull/871 (2 items)\n\n"
	pane := "● ROTA-DONE ben https://github.com/l4ci/rota/pull/871\n\n" + relay
	st, ev := Classify(pane, false, 60, "idle")
	if st == StateDone {
		t.Fatalf("pre-relay sentinel read as done: %s %q", st, ev)
	}
	if st != StateIdle || ev != staleDoneEvidence {
		t.Errorf("static pane under a relay: %s %q", st, ev)
	}
	if st, _ := Classify(pane, true, 60, ""); st != StateBusy {
		t.Errorf("moving pane under a relay: %s", st)
	}
	// The worker's own sentinel after the relay is a fresh done.
	again := pane + "● fixed\n● ROTA-DONE ben https://github.com/l4ci/rota/pull/871\n"
	if st, ev := Classify(again, false, 60, "idle"); st != StateDone || ev != "https://github.com/l4ci/rota/pull/871" {
		t.Errorf("done after the relay: %s %q", st, ev)
	}
	// The forge fallback must not turn the stale row back into done.
	rows := []PollRow{{"w1", StateIdle, staleDoneEvidence}}
	w := newWorld(t, "")
	w.forge("listed", "1")
	if out, _ := w.env(false).withDefaults().promoteIdleWithPR(bg, w.dir, rows); out[0].State != StateIdle {
		t.Errorf("stale row promoted by branch head: %v", out[0])
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

// A blocked event moves the slot to blocked when it is sent, so the pane
// poll never sees a transition: the event itself must ring, once.
func TestPollNotifiesABlockedEventOnce(t *testing.T) {
	dir, f := pollRegistry(t, "herdr")
	f.panes["w1"] = []string{"x\n", "x\n", "x\n", "x\n", "x\n", "x\n"}
	f.panes["w2"] = []string{"x\n", "x\n", "x\n", "x\n", "x\n", "x\n"}
	send := func(body string) {
		if err := Update(dir, func(d *Doc) {
			s := d.Slot("w1")
			now := stamp.Format(time.Now())
			s.SetEvent(EventBlocked, body, now)
			s.MarkState("blocked", now)
		}); err != nil {
			t.Fatal(err)
		}
	}
	rings := func() (n int) {
		for _, c := range f.calls {
			if strings.HasPrefix(c, "notify rota worker w1: BLOCKED") {
				n++
			}
		}
		return n
	}
	e := envWith(f)
	send("A or B?")
	for i := 0; i < 2; i++ {
		if _, err := e.Poll(bg, dir, PollOpts{Lines: 60}); err != nil {
			t.Fatal(err)
		}
	}
	if n := rings(); n != 1 {
		t.Fatalf("notifications = %d, want 1 for one event: %v", n, f.calls)
	}
	send("C or D?")
	if _, err := e.Poll(bg, dir, PollOpts{Lines: 60}); err != nil {
		t.Fatal(err)
	}
	if n := rings(); n != 2 {
		t.Errorf("notifications = %d, want 2 after a second event", n)
	}
}

// FindLimit is the one definition of a hit limit: Classify and the limits
// watcher (limits.findLimit) both ask it.
func TestFindLimitIsTheOneDefinitionOfAHitLimit(t *testing.T) {
	for _, tc := range []struct {
		name, pane string
		limited    bool
		evidence   string
	}{
		{"approaching", "Approaching usage limit\n", false, ""},
		{"approaching your, with reset", "Approaching your usage limit · resets at 5pm\n", false, ""},
		{"hit limit", "You've reached your usage limit\n", true, "reached your usage limit"},
		{"add funds", "You've reached your usage limit\nAdd funds to continue\n", true, "Add funds"},
	} {
		_, _, found := FindLimit("", tc.pane)
		state, ev := Classify(tc.pane, false, 40, "")
		if found != tc.limited || (state == StateLimited) != tc.limited {
			t.Errorf("%s: FindLimit=%v Classify=%s, want limited=%v", tc.name, found, state, tc.limited)
		}
		if tc.limited && !strings.Contains(ev, tc.evidence) {
			t.Errorf("%s: evidence %q lacks %q", tc.name, ev, tc.evidence)
		}
	}
}

// The wiring from the registry to the classifier: slotTarget hands a slot's
// harness kind to ClassifyFor, so an opencode slot reads OpenCode's expired-key
// pane as limited and a claude slot showing the same text stays idle.
func TestPollReadsEachSlotsPaneByItsHarnessKind(t *testing.T) {
	dir, f := pollRegistry(t, "tmux")
	if err := Update(dir, func(d *Doc) { d.Slot("w1").Raw().Set("kind", harness.OpenCode) }); err != nil {
		t.Fatal(err)
	}
	pane := opencodePane(t, "expired-key")
	f.panes["w1"] = []string{pane, pane}
	f.panes["w2"] = []string{pane, pane}
	res, err := envWith(f).Poll(bg, dir, PollOpts{Lines: 60})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Slots) != 2 || res.Slots[0].State != StateLimited || res.Slots[1].State != StateIdle {
		t.Errorf("opencode slot w1, claude slot w2: %+v", res.Slots)
	}
}

// FindLimit reads a kind's own phrases on top of the shared ones, for that kind only.
func TestFindLimitReadsTheKindsOwnPhrases(t *testing.T) {
	const quota = "Error: insufficient quota\n"
	if _, _, ok := FindLimit(harness.OpenCode, quota); !ok {
		t.Error("opencode quota text is not a limit for an opencode slot")
	}
	for _, kind := range []string{"", harness.Claude, harness.Codex} {
		if _, _, ok := FindLimit(kind, quota); ok {
			t.Errorf("opencode quota text is a limit for kind %q", kind)
		}
	}
}

// The herdr prefilter must let a kind's own limit text through, or the
// watcher's per-kind reading never sees it.
func TestLimitRegexIncludesKindPhrases(t *testing.T) {
	if !regexp.MustCompile(LimitRegex()).MatchString("Error: insufficient quota") {
		t.Error("LimitRegex drops opencode quota text")
	}
}

// A limit or Add-funds phrase quoted by an echoed brief or relay is the
// orchestrator's text, not the session's: only what the worker prints after its
// reply marker counts (#883).
func TestClassifyLoginExpiredIsBlockedOnTheAccount(t *testing.T) {
	st, ev := ClassifyFor("claude", "  ⎿  Login expired · Please run /login\n", false, 0, "")
	if st != StateBlocked || !strings.Contains(ev, "round.accounts") || !strings.Contains(ev, "config.local.json") {
		t.Errorf("state %q evidence %q", st, ev)
	}
	// Another kind's pane that echoes the phrase is not logged out.
	if st, _ := ClassifyFor("codex", "Login expired\n", false, 0, ""); st == StateBlocked {
		t.Errorf("codex pane read as %q", st)
	}
}

// #1209: a pane that merely quotes "Please run /login" (a doc, a diff, a brief)
// is not logged out; only the "Login expired" status line is.
func TestClassifyQuotedLoginHintIsNotLoggedOut(t *testing.T) {
	st, _ := ClassifyFor("claude", "the docs say: Please run /login to sign in\n", false, 0, "")
	if st == StateBlocked {
		t.Errorf("quoted /login hint read as %q", st)
	}
}

// #1255: a logged-out pane showing only the bare status line (no "Login
// expired") reads BLOCKED, while the same words mid-line stay quoted text.
func TestClassifyBareNotLoggedInStatusIsBlocked(t *testing.T) {
	st, ev := ClassifyFor("claude", "Not logged in \u00b7 Please run /login\n", false, 0, "")
	if st != StateBlocked || !strings.Contains(ev, "round.accounts") {
		t.Errorf("bare status read as %q evidence %q", st, ev)
	}
	st, _ = ClassifyFor("claude", "the docs say: Not logged in \u00b7 Please run /login\n", false, 0, "")
	if st == StateBlocked {
		t.Errorf("quoted status read as %q", st)
	}
}

// #1156 x #1171: the background-task footer only overrides the final idle
// outcome, so a logged-out pane that carries it still reads BLOCKED.
func TestClassifyLoggedOutWithTaskFooterStaysBlocked(t *testing.T) {
	pane := "  ⎿  Login expired · Please run /login\n\n  ⏵⏵ bypass permissions on · 1 shell, 2 monitors\n"
	st, ev := ClassifyFor("claude", pane, false, 0, "")
	if st != StateBlocked || !strings.Contains(ev, "round.accounts") {
		t.Errorf("state %q evidence %q", st, ev)
	}
}

func TestClassifyLimitPhraseInEchoedBriefIsNotLimited(t *testing.T) {
	brief := "--- ORCHESTRATOR (round 24) ---\nYou are nia.\nOut of scope: The Add funds escalation behaviour.\nThe session says: You've reached your usage limit\n\n"
	st, ev := Classify(brief, false, 60, "idle")
	if st == StateLimited {
		t.Fatalf("echoed brief read as limited: %s %q", st, ev)
	}
	if st, _ := Classify(brief, true, 60, ""); st != StateBusy {
		t.Errorf("moving pane under an echoed brief: %s", st)
	}
	if _, _, ok := FindLimit("", StripRelays("", brief)); ok {
		t.Error("StripRelays left the echoed phrase in")
	}
	own := brief + "● working\nYou've reached your usage limit\nAdd funds to continue\n"
	st, ev = Classify(own, false, 60, "idle")
	if st != StateLimited || !strings.Contains(ev, "Add funds") {
		t.Errorf("phrases after the worker's reply marker: %s %q", st, ev)
	}
}

// A limit banner is printed by the harness, so it has no reply marker: it must
// still read as limited when it follows a signed brief that quoted the phrase.
func TestClassifyBareBannerAfterSignedBriefIsLimited(t *testing.T) {
	sig := "--- ROTA-SIG " + strings.Repeat("ab", 32) + " ---\n"
	brief := "--- ORCHESTRATOR (round 25) ---\nYou are kit.\nThe session says: You've hit your usage limit\n" + sig
	if st, ev := Classify(brief, false, 60, "idle"); st == StateLimited {
		t.Fatalf("signed brief alone read as limited: %q", ev)
	}
	if st, ev := Classify(brief+"You've hit your usage limit\n", false, 60, "idle"); st != StateLimited {
		t.Errorf("bare banner after the signature: %s %q", st, ev)
	}
}

// The evidence is sliced from the stripped text: a relay ahead of the limit
// line must not shift it.
func TestClassifyLimitEvidenceAfterRelay(t *testing.T) {
	pane := "--- ORCHESTRATOR (round 25) ---\n[relay] a long forwarded note that is dropped from the evidence offset\nmore relay text\n\n● working\nYou've reached your usage limit\n"
	st, ev := Classify(pane, false, 60, "idle")
	if st != StateLimited || !strings.Contains(ev, "usage limit") || strings.Contains(ev, "relay") {
		t.Errorf("evidence after a relay: %s %q", st, ev)
	}
}

// Claude Code echoes a submitted prompt as "❯ <first line>" with every further
// line indented two spaces. The echo is the orchestrator's; the first line
// back at column 0 (a harness banner) or a reply marker is the session's.
func TestClassifyClaudeEchoedBrief(t *testing.T) {
	brief := "❯ --- ORCHESTRATOR (round 25) ---\n  You are kit.\n  Out of scope: The Add funds escalation behaviour.\n\n  The session says: You've hit your usage limit\n"
	classify := func(pane string) (string, string) {
		return ClassifyFor("claude", pane, false, 60, "idle")
	}
	for name, pane := range map[string]string{
		"no reply yet": brief,
		"after reply":  brief + "\n● Reading the issue\n",
	} {
		if st, ev := classify(pane); st == StateLimited {
			t.Errorf("%s: echoed brief read as limited: %q", name, ev)
		}
	}
	// Without the Add-funds line: reFunds stays fail-closed on the whole tail.
	plain := "❯ --- ORCHESTRATOR (round 25) ---\n  You are kit.\n  The session says: You've hit your usage limit\n"
	banner := plain + "\nYou've hit your usage limit · resets 5pm\n"
	if st, ev := classify(banner); st != StateLimited || strings.Contains(ev, "Add funds") {
		t.Errorf("bare banner after the echo: %s %q", st, ev)
	}
	funds := banner + "Add funds to continue\n"
	if st, ev := classify(funds); st != StateLimited || !strings.Contains(ev, "Add funds") {
		t.Errorf("banner with Add funds after the echo: %s %q", st, ev)
	}
	// Add-funds only inside the echo, no limit anywhere: not limited.
	if st, ev := classify("❯ --- ORCHESTRATOR (round 25) ---\n  Add funds is out of scope.\n"); st == StateLimited {
		t.Errorf("Add funds only in the echo: %q", ev)
	}
}

func TestParseFindingDone(t *testing.T) {
	for in, want := range map[string]string{"finding:42": "42", "finding: 7": "7", "finding:IC_kwDO-9": "IC_kwDO-9"} {
		if got, ok := ParseFindingDone(in); !ok || got != want {
			t.Errorf("%q: %q %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "finding:", "finding:<comment-id>", "issues:#1", "https://github.com/o/r/pull/12", "finding:1 2"} {
		if got, ok := ParseFindingDone(in); ok {
			t.Errorf("%q parsed as %q", in, got)
		}
	}
}

// A busy session whose scrollback quotes the limit or "Add funds" phrases in a
// diff, prose, a code listing, a conflict hunk or tool output is working, not
// limited: only the harness's own dialog counts (#1212).
func TestClassifyQuotedLimitTextIsNotLimited(t *testing.T) {
	spinner := "\n✻ Running tests… (12s · esc to interrupt)\n\n❯ \n  ⏵⏵ bypass permissions on\n"
	for name, quoted := range map[string]string{
		"diff added":   "    412 +\tif reFunds.MatchString(tail) { // You've hit your usage limit: Add funds\n",
		"diff removed": "    413 -\t\treturn StateLimited, \"usage limit reached; pane offers 'Add funds'\"\n",
		"numbered":     "    417  +\t\t\treturn StateLimited, \"usage limit reached; pane offers 'Add funds'\"\n",
		"tool output":  "  ⎿  internal/worker/poll.go:417: usage limit reached; pane offers 'Add funds'\n",
		"prose":        "  The classifier matches \"You've hit your usage limit\" and \"Add funds\" as plain text.\n",
		"code listing": "    return StateLimited, \"usage limit reached; pane offers 'Add funds'\"\n",
		"conflict":     "    <<<<<<< HEAD\n    msg := \"You've hit your usage limit\"\n    =======\n    msg := \"Add funds\"\n    >>>>>>> main\n",
	} {
		for _, native := range []string{"idle", "working"} {
			pane := "● Update(internal/worker/poll.go)\n" + quoted + spinner
			if st, ev := ClassifyFor("claude", pane, false, 60, native); st != StateBusy {
				t.Errorf("%s/%s: want BUSY, got %s %q", name, native, st, ev)
			}
			// Idle, with the worker's last reply quoting the text above the input box.
			idle := "● " + strings.TrimLeft(quoted, " +-\t") + "\n❯ \n  ⏵⏵ bypass permissions on\n"
			if st, ev := ClassifyFor("claude", idle, false, 60, native); st == StateLimited {
				t.Errorf("%s/%s idle: quoted text read as limited: %q", name, native, ev)
			}
		}
	}
	// A real dialog rendered the way Claude Code shows it, under a ⎿ gutter, stays limited.
	for name, dialog := range map[string]string{
		"gutter": "● Update(internal/worker/poll.go)\n\n  ⎿  You've hit your usage limit · resets 5pm\n     Add funds to continue\n",
		"bare":   "● Update(internal/worker/poll.go)\n  ⎿  Done\n\nYou've hit your usage limit · resets 5pm\n\nAdd funds to continue\n",
	} {
		pane := dialog + "\n❯ \n  ⏵⏵ bypass permissions on\n"
		if st, ev := ClassifyFor("claude", pane, false, 60, "idle"); st != StateLimited || !strings.Contains(ev, "Add funds") {
			t.Errorf("real dialog %s: %s %q", name, st, ev)
		}
	}
	// The harness's own dialog still reads as limited, even after a diff.
	pane := "+\tx := \"usage limit reached\"\n\nYou've hit your usage limit · resets 5pm\nAdd funds to continue\n"
	if st, ev := ClassifyFor("claude", pane, false, 60, "idle"); st != StateLimited || !strings.Contains(ev, "Add funds") {
		t.Errorf("real dialog after a diff: %s %q", st, ev)
	}
}

// Claude Code separates the paragraphs of a reply with blank lines and indents
// every one after the first, code blocks included. A later paragraph quoting
// the limit phrase is the worker's prose, idle or busy (#1212).
func TestClassifyMultiParagraphReplyQuotingLimitIsNotLimited(t *testing.T) {
	footer := "\n❯ \n  ⏵⏵ bypass permissions on\n"
	spinner := "\n✻ Running tests… (12s · esc to interrupt)\n"
	for name, reply := range map[string]string{
		"prose": "● The classifier matched quoted text.\n\n  It reads \"You've hit your usage limit\" as a dialog.\n\n  Then it offers \"Add funds\".\n",
		"code":  "● Here is the hunk:\n\n  if reFunds.MatchString(tail) {\n    return \"You've hit your usage limit · Add funds\"\n  }\n",
	} {
		for _, busy := range []bool{false, true} {
			pane := reply
			if busy {
				pane += spinner
			}
			pane += footer
			for _, native := range []string{"idle", "working"} {
				if st, ev := ClassifyFor("claude", pane, false, 60, native); st == StateLimited {
					t.Errorf("%s busy=%v native=%s: read as limited: %q", name, busy, native, ev)
				}
			}
		}
	}
}

// A custom statusline under the footer is frame: the spinner above it still
// marks the pane working, so tool output quoting the phrase is not a limit.
func TestClassifyStatuslineUnderFooterIsFrame(t *testing.T) {
	pane := "● Bash(grep -n 'usage limit' internal/worker/poll.go)\n" +
		"  ⎿  poll.go:417: You've hit your usage limit · Add funds to continue\n" +
		"\n✻ Running… (3s · esc to interrupt)\n\n" +
		"╭──────────────╮\n│ ❯            │\n╰──────────────╯\n" +
		"  ⏵⏵ bypass permissions on\n  opus · ctx 40% · $1.20\n"
	for _, native := range []string{"idle", "working"} {
		if st, ev := ClassifyFor("claude", pane, false, 60, native); st == StateLimited {
			t.Errorf("native=%s: statusline pane read as limited: %q", native, ev)
		}
	}
}

// Claude Code renders a hit limit as "⎿" lines under the last reply, so an idle
// pane keeps those lines. The trade-off, by design: an idle pane whose last
// block is tool output quoting the phrase reads limited too.
func TestClassifyIdleGutterLinesUnderLastReplyAreDialog(t *testing.T) {
	footer := "\n❯ \n  ⏵⏵ bypass permissions on\n"
	for name, pane := range map[string]string{
		"limit error": "● Update(internal/worker/poll.go)\n  ⎿  You've hit your usage limit · resets 5pm\n",
		"tool output": "● Bash(grep -n limit poll.go)\n  ⎿  poll.go:417: You've hit your usage limit\n",
	} {
		if st, ev := ClassifyFor("claude", pane+footer, false, 60, "idle"); st != StateLimited {
			t.Errorf("%s: want LIMITED, got %s %q", name, st, ev)
		}
	}
}

// The region's edges: the dialog reads as limited with no input box, with only
// a footer, with a boxed input and with a statusline under the footer.
func TestClassifyRealDialogAtEachRegionEdge(t *testing.T) {
	dialog := "● Done.\n\nYou've hit your usage limit · resets 5pm\n"
	for name, frame := range map[string]string{
		"no input box": "",
		"footer only":  "  ⏵⏵ bypass permissions on\n",
		"boxed input":  "\n╭────╮\n│ ❯  │\n╰────╯\n  ? for shortcuts\n",
		"statusline":   "\n╭────╮\n│ ❯  │\n╰────╯\n  ⏵⏵ bypass permissions on\n  opus · ctx 40% · $1.20\n",
	} {
		if st, ev := ClassifyFor("claude", dialog+frame, false, 60, "idle"); st != StateLimited {
			t.Errorf("%s: want LIMITED, got %s %q", name, st, ev)
		}
	}
	// The status area under the footer is the harness's too.
	pane := "● Done.\n\n❯ \n  ⏵⏵ bypass permissions on\n  You've hit your usage limit · resets 5pm\n"
	if st, ev := ClassifyFor("claude", pane, false, 60, "idle"); st != StateLimited {
		t.Errorf("status area: want LIMITED, got %s %q", st, ev)
	}
}

// Claude Code 2.1.288 draws its input box as two rules and puts the statusline
// between the lower rule and the footer (testdata done-after-reply-bullet). The
// spinner above the box still marks the pane working.
func TestClassifyStatuslineAboveFooterIsFrame(t *testing.T) {
	rule := strings.Repeat("─", 40) + "\n"
	frame := "\n" + rule + "❯\n" + rule +
		"  Opus 5.5 on kit/204-agent-names | 567k / 1.0m | 44% remain\n" +
		"  current: ○○○○○○○○○○ 6%  | weekly: ●●○○○○○○○○ 20%\n" +
		"  resets 8:20pm           | resets Oct 9, 8:00pm\n" +
		"  ⏵⏵ bypass permissions on (shift+tab to cycle) · PR #208\n"
	grep := "● Bash(grep -n 'usage limit' internal/worker/poll.go)\n" +
		"  ⎿  poll.go:417: You've hit your usage limit · Add funds to continue\n"
	busy := grep + "\n✻ Running… (3s · esc to interrupt)\n" + frame
	if st, ev := ClassifyFor("claude", busy, false, 60, "working"); st != StateBusy {
		t.Errorf("busy pane: want BUSY, got %s %q", st, ev)
	}
	limited := "● Done.\n\nYou've hit your usage limit · resets 5pm\n" + frame
	if st, ev := ClassifyFor("claude", limited, false, 60, "idle"); st != StateLimited {
		t.Errorf("real dialog: want LIMITED, got %s %q", st, ev)
	}
}

// Codex indents its footer under an unindented prompt line. Only the indented
// run straight under the last reply is reply content, so a limit printed above
// the prompt is still dialog, and a later paragraph of the reply is not.
func TestClassifyCodexPaneLimitAbovePrompt(t *testing.T) {
	footer := "\n› Ask Codex to do anything\n\n  GPT-6-Sol default fast · 1 warning · f2 to view\n"
	reply := "• Fixed the classifier.\n\n  It used to read \"You've hit your usage limit\" as a dialog.\n"
	if st, ev := ClassifyFor("codex", reply+footer, false, 60, "idle"); st == StateLimited {
		t.Errorf("codex prose read as limited: %q", ev)
	}
	limit := reply + "\n■ You've hit your usage limit. Try again in 2 hours.\n" + footer
	if st, ev := ClassifyFor("codex", limit, false, 60, "idle"); st != StateLimited {
		t.Errorf("codex limit: want LIMITED, got %s %q", st, ev)
	}
}
