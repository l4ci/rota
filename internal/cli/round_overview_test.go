package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/tui"
	"github.com/l4ci/rota/internal/worker"
)

// overviewFixture is a round with every group populated: one slot working, one
// done with an open PR, a parked and a dead slot, a PR whose worker moved on,
// a merged one that must drop out, three support issues (waiting, active,
// unreadable) and two upcoming candidates.
func overviewFixture() roundSnap {
	return roundSnap{
		Host: "herdr",
		Slots: []round.Row{
			{Name: "ben", Issue: "101", Branch: "ben/101-work", HostState: "working", Tier: "standard", Model: "sonnet", Kind: "claude", PRTitle: "Work on 101"},
			{Name: "dana", Issue: "102", Branch: "dana/102-review", HostState: "idle", PR: "https://github.com/acme/project/pull/202", PRState: "open", Kind: "claude", Model: "opus",
				Event: &worker.Event{Kind: worker.EventDone}, PRTitle: "Review me"},
			{Name: "kit", Branch: "park/kit", HostState: "idle"},
			{Name: "nia", Issue: "103", Branch: "nia/103-work", HostState: "dead"},
		},
		Review: []round.QueuedPR{
			{Issue: "104", PR: "https://github.com/acme/project/pull/204", Branch: "ben/104-old", From: "ben"},
			{Issue: "105", PR: "https://github.com/acme/project/pull/205", From: "dana", State: "merged"},
		},
		Support: []round.SupportItem{
			{ID: "201", Title: "Support waits"},
			{ID: "202", Title: "Support works", Claim: "mac-ben"},
			{ID: "203", Title: "Support unreadable", Unknown: true},
		},
		SupportLoaded: true,
		Cands: []roundCand{
			{ID: "301", Title: "Ready upcoming", Ready: true},
			{ID: "302", Title: "Blocked upcoming", Why: []string{"overlaps #101 (ben)"}},
		},
		CandsLoaded: true,
	}
}

func overviewText(t *testing.T, s roundSnap, keys ...string) string {
	t.Helper()
	m, _ := press(newRoundScreen(s, nil, newClock().now, roundRefresh), keys...)
	return tui.Strip(m.Render(120, 40, tui.Style{}))
}

func TestRoundOverviewGroups(t *testing.T) {
	out := overviewText(t, overviewFixture())
	order := []string{"Active (2)", "PR review (2)", "Support queue (3)", "Upcoming (2)"}
	at := -1
	for _, h := range order {
		i := strings.Index(out, h)
		if i < 0 || i < at {
			t.Fatalf("group %q missing or out of order:\n%s", h, out)
		}
		at = i
	}
	for _, want := range []string{"#101", "Work on 101", "ben", "working", "claude/sonnet", "#102", "#202", "#104", "#204", "moved on", "#301", "ready", "#302", "blocked"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "#105") {
		t.Errorf("a merged PR must leave the review group:\n%s", out)
	}
	if strings.Contains(out, "park/kit") {
		t.Errorf("a parked slot is a count, not a row:\n%s", out)
	}
}

func TestRoundOverviewCounts(t *testing.T) {
	out := overviewText(t, overviewFixture())
	if !strings.Contains(out, "workers 4 known: 1 working · 1 in review · 1 parked (kit) · 1 unavailable") {
		t.Errorf("worker totals:\n%s", out)
	}
	if !strings.Contains(out, "support: 1 waiting · 1 active · 1 unknown (remote workers not reported)") {
		t.Errorf("support totals:\n%s", out)
	}
}

func TestRoundOverviewSupport(t *testing.T) {
	out := overviewText(t, overviewFixture())
	for _, want := range []string{"waiting", "active", "mac-ben", "unknown"} {
		if !strings.Contains(out, want) {
			t.Errorf("support rows lack %q:\n%s", want, out)
		}
	}
	// A failed claim read is an error line, never an empty queue.
	s := overviewFixture()
	s.Support, s.SupportLoaded, s.SupportErr = nil, false, "forge: boom"
	out = overviewText(t, s)
	if !strings.Contains(out, "support queue unavailable: forge: boom") || strings.Contains(out, "Support queue (0)") {
		t.Errorf("unavailable support source:\n%s", out)
	}
	// Not asked for at all (round.support empty): the group is absent.
	s = overviewFixture()
	s.Support, s.SupportLoaded = nil, false
	if out = overviewText(t, s); strings.Contains(out, "Support queue") && !strings.Contains(out, "loading") {
		t.Errorf("support group without a support machine:\n%s", out)
	}
}

// The cursor follows an issue when its group changes underneath it.
func TestRoundOverviewSelection(t *testing.T) {
	s := overviewFixture()
	m, _ := press(newRoundScreen(s, nil, newClock().now, roundRefresh), "j", "j") // #101, #103, then #102 in PR review
	if out := tui.Strip(m.Render(120, 40, tui.Style{})); !strings.Contains(out, "› #102") {
		t.Fatalf("expected the cursor on #102:\n%s", out)
	}
	s.Slots[0].PR, s.Slots[0].PRState = "https://github.com/acme/project/pull/201", "open"
	s.Slots[0].Event = &worker.Event{Kind: worker.EventDone} // #101 moves to review, above #102 in the list
	rm := m.(*roundScreen)
	rm.box.put(s, nil)
	m, _ = m.Update(tui.Done{})
	if out := tui.Strip(m.Render(120, 40, tui.Style{})); !strings.Contains(out, "› #102") {
		t.Errorf("cursor left #102 when #101 changed groups:\n%s", out)
	}
}

func TestRoundOverviewNoColorKeepsLabels(t *testing.T) {
	m := newRoundScreen(overviewFixture(), nil, newClock().now, roundRefresh)
	plain := m.Render(120, 40, tui.Style{})
	if strings.Contains(plain, "\x1b") {
		t.Errorf("no-color frame has escapes: %q", plain)
	}
	colored := m.Render(120, 40, tui.Style{Color: true})
	if tui.Strip(colored) != plain {
		t.Errorf("colour changed the text:\n%s\nvs\n%s", tui.Strip(colored), plain)
	}
	if !strings.Contains(colored, "\x1b[36m›") {
		t.Errorf("selection is not the menu's cyan marker: %q", colored)
	}
}

// A pass that cannot read the support queue keeps the last rows and says they
// are old; it never replaces them with an empty queue.
func TestRoundOverviewPartialFailureKeepsLastRead(t *testing.T) {
	old := overviewFixture()
	m := newRoundScreen(old, nil, newClock().now, roundRefresh)
	bad := overviewFixture()
	bad.Support, bad.SupportLoaded, bad.SupportErr = nil, false, "forge: down"
	bad.Cands, bad.CandsErr = nil, "forge: down"
	rm := m
	rm.box.put(bad, nil)
	var next tui.Model
	next, _ = rm.Update(tui.Done{})
	out := tui.Strip(next.Render(120, 40, tui.Style{}))
	for _, want := range []string{"Support queue (3)", "#201", "support queue unavailable: forge: down (rows are from the last read)", "#301", "candidates unavailable: forge: down"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

// Rows carry text from the tracker and the registry: control sequences never
// reach the frame.
func TestRoundOverviewStripsControlCharacters(t *testing.T) {
	s := overviewFixture()
	s.Support[0].Title = "evil\x1b[2Jtitle"
	s.Slots[0].PRTitle = "x\x1b]0;t\x07y"
	s.Support[1].Claim = "mac\x1b[31m"
	m := newRoundScreen(s, nil, newClock().now, roundRefresh)
	for _, k := range [][]string{nil, {"j", "j", "j", "j", "j"}, {"j", "j", "j", "j", "j", "j"}} {
		m2, _ := press(m, k...)
		if out := m2.Render(120, 40, tui.Style{}); strings.ContainsRune(out, 0x1b) || strings.ContainsRune(out, 0x07) {
			t.Fatalf("a control character reached the frame: %q", out)
		}
	}
}

const (
	ghIssue = "https://github.com/acme/project/issues/101"
	ghPR    = "https://github.com/acme/project/pull/202"
	glIssue = "https://gitlab.example.com/group/sub/project/-/issues/9"
	glMR    = "https://gitlab.example.com/group/sub/project/-/merge_requests/7"
)

func linked(url string) string { return "\x1b]8;;" + url + "\x1b\\" }

func linksFixture() roundSnap {
	s := overviewFixture()
	s.IssueURLs = map[string]string{"101": ghIssue, "102": "https://github.com/acme/project/issues/102", "201": glIssue}
	return s
}

func TestRoundOverviewLinks(t *testing.T) {
	m := newRoundScreen(linksFixture(), nil, newClock().now, roundRefresh)
	linkStyle := tui.Style{Color: true, Links: true}
	out := m.Render(140, 40, linkStyle)
	// The issue and the PR are separate links with separate destinations.
	for _, url := range []string{ghIssue, ghPR, glIssue} {
		if !strings.Contains(out, linked(url)) {
			t.Errorf("no hyperlink to %s in %q", url, out)
		}
	}
	if strings.Count(out, linked(ghPR)) != 1 {
		t.Errorf("PR link must appear once, on its own cell: %q", out)
	}
	// Links change no layout: the stripped frame equals the link-free one.
	if plain := m.Render(140, 40, tui.Style{Color: true}); tui.Strip(out) != tui.Strip(plain) {
		t.Errorf("links changed the text:\n%s\nvs\n%s", tui.Strip(out), tui.Strip(plain))
	}
	// Narrow terminals still clip every line.
	for _, line := range strings.Split(m.Render(60, 30, linkStyle), "\n") {
		if n := tui.Width(line); n > 60 {
			t.Errorf("line is %d cells wide at width 60: %q", n, tui.Strip(line))
		}
	}
	// The detail pane carries the full URLs for terminals that render no links.
	sel, _ := press(m, "j", "j") // #102 in review
	if body := tui.Strip(sel.Render(140, 40, tui.Style{})); !strings.Contains(body, "issue: https://github.com/acme/project/issues/102") || !strings.Contains(body, "PR: "+ghPR) {
		t.Errorf("detail lacks the URLs:\n%s", body)
	}
}

func TestRoundOverviewLinksGitLabAndUnsafe(t *testing.T) {
	s := overviewFixture()
	s.Slots[0].PR, s.Slots[0].PRState = glMR, "open"
	s.IssueURLs = map[string]string{"101": glIssue, "103": "javascript:alert(1)"}
	out := newRoundScreen(s, nil, newClock().now, roundRefresh).Render(140, 40, tui.Style{Color: true, Links: true})
	if !strings.Contains(out, linked(glIssue)) || !strings.Contains(out, linked(glMR)) {
		t.Errorf("nested GitLab group URLs not linked: %q", out)
	}
	if strings.Contains(out, "javascript:") {
		t.Errorf("an unsafe issue URL reached the frame: %q", out)
	}
}

func TestRoundOverviewOpen(t *testing.T) {
	var opened []string
	fake := func(u string) error { opened = append(opened, u); return nil }
	run := func(m tui.Model, keys ...string) (tui.Model, tui.Cmd) {
		return press(m, keys...)
	}
	m := newRoundScreen(linksFixture(), nil, newClock().now, roundRefresh)
	m.open = fake
	// #101 is selected and has an issue URL but no PR.
	_, cmd := run(m, "i")
	if cmd.Exec == nil {
		t.Fatal("i started nothing")
	}
	_ = cmd.Exec()
	m2, cmd := run(m, "p")
	if cmd.Exec != nil {
		t.Error("p on a row without a PR started an open")
	}
	if out := tui.Strip(m2.Render(120, 40, tui.Style{})); !strings.Contains(out, "no PR link for this row") {
		t.Errorf("p without a PR must say so:\n%s", out)
	}
	// #102 (review) has both, and they are different pages.
	m3, _ := run(m, "j", "j")
	_, ci := run(m3, "i")
	_, cp := run(m3, "p")
	_ = ci.Exec()
	_ = cp.Exec()
	want := []string{ghIssue, "https://github.com/acme/project/issues/102", ghPR}
	if strings.Join(opened, " ") != strings.Join(want, " ") {
		t.Errorf("opened %v, want %v", opened, want)
	}
}

func TestRoundOverviewOpenFailureAndRefresh(t *testing.T) {
	m := newRoundScreen(linksFixture(), nil, newClock().now, roundRefresh)
	m.open = func(string) error { return errors.New("xdg-open: not found") }
	m1, cmd := press(m, "i")
	// The selection changes under a background refresh while the open runs: the
	// opener still gets the link the key was pressed on.
	rm := m1.(*roundScreen)
	next := linksFixture()
	next.Slots = next.Slots[1:]
	rm.box.put(next, nil)
	m2, _ := m1.Update(tui.Done{})
	err := cmd.Exec()
	if err == nil {
		t.Fatal("opener failure was swallowed")
	}
	m3, _ := m2.Update(tui.Done{Err: err, Tag: cmd.Tag})
	out := tui.Strip(m3.Render(140, 40, tui.Style{}))
	if !strings.Contains(out, "could not open "+ghIssue+": xdg-open: not found") {
		t.Errorf("open failure not shown:\n%s", out)
	}
	if strings.Contains(out, "refresh failed") {
		t.Errorf("an open failure must not read as a failed refresh:\n%s", out)
	}
	// Without an opener the key says so instead of doing nothing.
	m.open = nil
	m4, c := press(m, "i")
	if c.Exec != nil || !strings.Contains(tui.Strip(m4.Render(120, 40, tui.Style{})), "no browser") {
		t.Error("a screen without an opener must say so")
	}
}
