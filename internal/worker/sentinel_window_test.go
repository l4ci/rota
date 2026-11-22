package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/host"
)

const closedRelay = "<rota-relay round=\"66\" from=\"orchestrator\">\nbrief text\n</rota-relay>\n"

// AC-1 (#4093): a review worker's issues line after a closed relay, with no
// reply marker, is the worker's own done.
func TestClassifyIssuesDoneAfterClosedRelay(t *testing.T) {
	st, ev := ClassifyFor("claude", closedRelay+"filler\nROTA-DONE s1 issues:#12,#13\n", false, 60, "")
	if st != StateDone || ev != "issues:#12,#13" {
		t.Fatalf("got %s %q", st, ev)
	}
}

// AC-2: a DONE line the terminal soft-wrapped across two lines is joined.
func TestClassifyDoneSoftWrapped(t *testing.T) {
	const done = "ROTA-DONE s1 https://github.com/o/r/pull/4093"
	const width = 40
	text := strings.Repeat("─", width) + "\n" + done[:width] + "\n" + done[width:] + "\n❯\n"
	st, ev := ClassifyFor("claude", text, false, 60, "")
	if st != StateDone || ev != "https://github.com/o/r/pull/4093" {
		t.Fatalf("got %s %q", st, ev)
	}
}

// Review of #4140: a continuation is glued on only when the DONE line itself
// fills the pane width. Each case would join under an always-true width test.
func TestClassifyDoneNeverJoinsUnrelatedNextLine(t *testing.T) {
	const url = "ROTA-DONE s1 https://github.com/o/r/pull/1"
	border := strings.Repeat("─", 60)
	for name, tc := range map[string]struct{ text, want string }{
		"prompt after done":        {"● " + url + "\n❯\n", "https://github.com/o/r/pull/1"},
		"border after done":        {url + "\n" + strings.Repeat("─", len(url)) + "\n", "https://github.com/o/r/pull/1"},
		"prose after short done":   {border + "\nROTA-DONE s1 pr#1\nDone.\n", "pr#1"},
		"token after lone done":    {url + "\nDone.\n", "https://github.com/o/r/pull/1"},
		"reply marker after done":  {strings.Repeat("─", len(url)) + "\n" + url + "\n●\n", "https://github.com/o/r/pull/1"},
		"sentinel after done line": {strings.Repeat("─", len(url)) + "\n" + url + "\nROTA-DONE\n", "https://github.com/o/r/pull/1"},
	} {
		t.Run(name, func(t *testing.T) {
			st, ev := ClassifyFor("claude", tc.text, false, 60, "")
			if st != StateDone || ev != tc.want {
				t.Fatalf("got %s %q, want %q", st, ev, tc.want)
			}
		})
	}
}

// AC-4: a ROTA-DONE that was not accepted says why.
func TestNoSentinelEvidenceNamesTheReason(t *testing.T) {
	filler := strings.Repeat("● work\n", 70)
	for name, tc := range map[string]struct{ text, want string }{
		"nothing":        {"idle\n> \n", "static pane, no sentinel"},
		"outside window": {"ROTA-DONE s1 issues:#1\n" + filler, "outside the last 60 lines"},
		"relay echo":     {"<rota-relay round=\"1\" from=\"orchestrator\">\nROTA-DONE s1 issues:#1\n", "inside a relay echo"},
		"prefix":         {"I will print ROTA-DONE s1 x when finished\n", "not accepted"},
	} {
		t.Run(name, func(t *testing.T) {
			_, ev := ClassifyFor("claude", tc.text, false, 60, "")
			if !strings.Contains(ev, tc.want) {
				t.Fatalf("evidence %q lacks %q", ev, tc.want)
			}
		})
	}
}

type wideHost struct {
	*fakeHost
	recent, wide string
	asked        []int
}

func (h *wideHost) Capabilities() host.Capabilities { return host.CapabilitiesOf(h) }
func (h *wideHost) ReadPane(_ context.Context, _, _ string, lines int) (string, error) {
	h.asked = append(h.asked, lines)
	if lines > 60 {
		return h.wide, nil
	}
	return h.recent, nil
}

// AC-2: a DONE followed by more filler than the default window is found by
// the wider read, which is bounded by sentinelScanLines.
func TestPollWidensWindowForDoneBeyondIt(t *testing.T) {
	dir, f := pollRegistry(t, "herdr")
	filler := strings.Repeat("● work\n", 80)
	h := &wideHost{fakeHost: f, recent: strings.Repeat("● work\n", 60), wide: "ROTA-DONE w1 issues:#7\n" + filler}
	res, err := envWith(h).Poll(bg, dir, PollOpts{Slot: "w1", Lines: 60})
	if err != nil || len(res.Slots) != 1 || res.Slots[0].State != StateDone {
		t.Fatalf("poll: %+v, %v", res, err)
	}
	max := 0
	for _, n := range h.asked {
		if n > max {
			max = n
		}
	}
	if max != sentinelScanLines {
		t.Fatalf("widest read %d, want bound %d (asked %v)", max, sentinelScanLines, h.asked)
	}
}

// A pane unchanged since the last wider read is not read wide again.
func TestPollWidensOncePerUnchangedPane(t *testing.T) {
	dir, f := pollRegistry(t, "herdr")
	h := &wideHost{fakeHost: f, recent: strings.Repeat("● idle once\n", 60), wide: strings.Repeat("● nothing\n", 90)}
	for i := 0; i < 3; i++ {
		if _, err := envWith(h).Poll(bg, dir, PollOpts{Slot: "w1", Lines: 60}); err != nil {
			t.Fatal(err)
		}
	}
	wide := 0
	for _, n := range h.asked {
		if n == sentinelScanLines {
			wide++
		}
	}
	if wide != 1 {
		t.Fatalf("%d wide reads over 3 polls of an unchanged pane, want 1", wide)
	}
}
