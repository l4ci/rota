package round

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// Phase-level tests of planAssign: plain inputs, no Board, Env or registry file.

func TestNoFreeSlotRefusal(t *testing.T) {
	set := roundcfg.Settings{Roster: []string{"ben", "dana"}, Slots: 2}
	reg := worker.ParseRegistry(`{"slots":[{"name":"ben"},{"name":"dana"}]}`)
	missing := worker.ParseRegistry(`{"slots":[{"name":"ben"}]}`)
	for _, tc := range []struct {
		name                 string
		reg                  worker.Registry
		skipped, unresolved  []string
		wantBy, wantContains string
	}{
		{"unresolved only", reg, nil, []string{"ben: x"}, blockedby.NoFreeSlot, "unresolved Core delivery: ben: x"},
		{"login wins over unresolved", reg, []string{"ben: nologin"}, []string{"dana: x"}, blockedby.NoLogin, "no login: ben: nologin"},
		{"unprovisioned slot", missing, nil, nil, blockedby.NoFreeSlot, "not provisioned (a reaped slot is gone): dana"},
		{"all busy", reg, nil, nil, blockedby.NoFreeSlot, "every roster slot is busy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := noFreeSlotRefusal(tc.reg, set, "#7", tc.skipped, tc.unresolved)
			if got.By != tc.wantBy || !strings.Contains(got.Msg, tc.wantContains) {
				t.Errorf("got %q (%s), want %s containing %q", got.Msg, got.By, tc.wantBy, tc.wantContains)
			}
		})
	}
}

func TestReadinessRefusal(t *testing.T) {
	ok := Readiness{ID: "#1", Checks: []Check{{Name: CheckTemplate, OK: true}}}
	overlap := Readiness{ID: "#2", Checks: []Check{{Name: CheckOverlap, OK: false}}}
	notReady := Readiness{ID: "#3", Checks: []Check{{Name: "deps", OK: false, Detail: []string{"#9 open"}}}}

	if got := readinessRefusal(ok, []Readiness{ok}); got != nil {
		t.Fatalf("all ready: got %v", got)
	}
	got := readinessRefusal(overlap, []Readiness{notReady})
	if got == nil || got.By != blockedby.Overlap || got.Readiness.ID != "#2" || !strings.Contains(got.Msg, "#2 is not ready") {
		t.Errorf("lead is checked first: %+v", got)
	}
	got = readinessRefusal(ok, []Readiness{ok, notReady})
	if got == nil || got.By != blockedby.NotReady || got.Readiness.ID != "#3" || !strings.Contains(got.Msg, "bundle member #3 is not ready") {
		t.Errorf("member refusal: %+v", got)
	}
}

func TestWaiveTemplatePassesOnlyTheTemplateCheck(t *testing.T) {
	r := Readiness{Checks: []Check{
		{Name: CheckTemplate, OK: false, Detail: []string{"missing ## Acceptance"}},
		{Name: CheckOverlap, OK: false, Detail: []string{"#4"}},
	}}
	waiveTemplate(&r)
	if !r.Checks[0].OK || len(r.Checks[0].Detail) != 0 {
		t.Errorf("template check not waived: %+v", r.Checks[0])
	}
	if r.Checks[1].OK || len(r.Checks[1].Detail) != 1 {
		t.Errorf("overlap check touched: %+v", r.Checks[1])
	}
}

func TestOpenPRRefusalWording(t *testing.T) {
	lead, member := openPRRefusal("#5", 12, false), openPRRefusal("#6", 13, true)
	if lead.By != blockedby.OpenPR || !strings.HasPrefix(lead.Msg, "#5 has open PR #12") {
		t.Errorf("lead: %+v", lead)
	}
	if !strings.HasPrefix(member.Msg, "bundle member #6 has open PR #13") {
		t.Errorf("member: %+v", member)
	}
}

func issueItem(n int) *backlog.Item {
	return &backlog.Item{ID: tracker.NumKey(n).String(), Number: n}
}

func TestDisposition(t *testing.T) {
	asked := false
	park := func(ok bool, why string) func() (bool, string) {
		return func() (bool, string) { asked = true; return ok, why }
	}
	for _, tc := range []struct {
		name               string
		held, id           string
		parkable           func() (bool, string)
		resuming, queue    bool
		wantBy             string
		wantAsked, wantErr bool
	}{
		{name: "idle slot", held: "", id: "7", parkable: park(true, "")},
		{name: "holds the issue", held: "7", id: "#7", parkable: park(true, ""), resuming: true},
		{name: "other issue, done with PR", held: "3", id: "7", parkable: park(true, ""), queue: true, wantAsked: true},
		{name: "other issue, busy", held: "3", id: "7", parkable: park(false, "no PR"), wantBy: blockedby.SlotBusy, wantAsked: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asked = false
			resuming, queue, err := disposition(tc.held, tc.id, "ben", tc.parkable)
			if resuming != tc.resuming || queue != tc.queue || asked != tc.wantAsked {
				t.Errorf("resuming=%v queue=%v asked=%v", resuming, queue, asked)
			}
			var blk *BlockedError
			if tc.wantErr != errors.As(err, &blk) || (blk != nil && blk.By != tc.wantBy) {
				t.Errorf("err = %v, want blockedBy %q", err, tc.wantBy)
			}
		})
	}
}

func TestGateTouched(t *testing.T) {
	if got := gateTouched(true, "ben"); !slices.Equal(got, []string{"ben"}) {
		t.Errorf("forge: %v", got)
	}
	if got := gateTouched(false, "ben"); got != nil {
		t.Errorf("no forge: %v", got)
	}
}

func TestOpenPRClaims(t *testing.T) {
	lead, member := issueItem(1), issueItem(2)
	prs := func(m map[int]tracker.PR) map[tracker.IssueKey]tracker.PR {
		out := map[tracker.IssueKey]tracker.PR{}
		for n, pr := range m {
			out[tracker.NumKey(n)] = pr
		}
		return out
	}
	listErr := errors.New("forge down")
	for _, tc := range []struct {
		name         string
		openPR       map[tracker.IssueKey]tracker.PR
		listErr      error
		accept       bool
		wantOpen     bool
		wantBy       string
		wantContains string
	}{
		{name: "none open", openPR: prs(nil)},
		{name: "lead has open PR", openPR: prs(map[int]tracker.PR{1: {Number: 9}}), wantBy: blockedby.OpenPR, wantContains: "1 has open PR #9"},
		{name: "member has open PR", openPR: prs(map[int]tracker.PR{2: {Number: 8}}), wantBy: blockedby.OpenPR, wantContains: "bundle member 2 has open PR #8"},
		{name: "lead refused before member", openPR: prs(map[int]tracker.PR{1: {Number: 9}, 2: {Number: 8}}), wantBy: blockedby.OpenPR, wantContains: "1 has open PR"},
		{name: "list error refuses", listErr: listErr, wantContains: "forge down"},
		{name: "list error tolerated on redo", listErr: listErr, accept: true},
		{name: "redo of own PR", openPR: prs(map[int]tracker.PR{1: {Number: 9, Branch: "ben/1-x"}}), accept: true, wantOpen: true},
		{name: "redo of another agent's PR", openPR: prs(map[int]tracker.PR{1: {Number: 9, Branch: "dana/1-x"}}), accept: true, wantBy: blockedby.OpenPR, wantContains: "owned by another agent"},
		{name: "redo, non-round branch", openPR: prs(map[int]tracker.PR{1: {Number: 9, Branch: "feature"}}), accept: true, wantOpen: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			open, err := openPRClaims(tc.openPR, tc.listErr, tc.accept, "ben", lead, []*backlog.Item{member})
			if open != tc.wantOpen {
				t.Errorf("hasOpenPR = %v", open)
			}
			if tc.wantContains == "" {
				if err != nil {
					t.Errorf("err = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantContains) {
				t.Fatalf("err = %v, want %q", err, tc.wantContains)
			}
			var blk *BlockedError
			if tc.wantBy != "" && (!errors.As(err, &blk) || blk.By != tc.wantBy) {
				t.Errorf("err = %v, want blockedBy %s", err, tc.wantBy)
			}
		})
	}
}

func TestClaimsRefusal(t *testing.T) {
	var calls []string
	base := func() claimIn {
		calls = nil
		return claimIn{
			agent: "ben", lead: issueItem(1), members: []*backlog.Item{issueItem(2)},
			staleClaim: func() error { calls = append(calls, "stale"); return nil },
			openPRs: func() (map[tracker.IssueKey]tracker.PR, error) {
				calls = append(calls, "openPRs")
				return nil, nil
			},
			crossCheck: func(backlog.Item) ([]string, error) { calls = append(calls, "cross"); return nil, nil },
		}
	}
	queued := &worker.QueuedPR{PR: "#5", From: "dana"}
	cross := func(it backlog.Item) ([]string, error) {
		calls = append(calls, "cross")
		if it.ID == "2" {
			return []string{"milestone M2"}, nil
		}
		return nil, nil
	}
	for _, tc := range []struct {
		name      string
		mod       func(*claimIn)
		wantBy    string
		wantCalls string
	}{
		{"clear", func(*claimIn) {}, "", "openPRs cross cross"},
		{"in review", func(in *claimIn) { in.queued = queued }, blockedby.Claimed, ""},
		{"in review but resuming", func(in *claimIn) { in.queued, in.resuming = queued, true }, "", ""},
		{"stale claim refused before open PRs", func(in *claimIn) {
			in.reclaimStale = "dana"
			in.staleClaim = func() error { calls = append(calls, "stale"); return blocked(blockedby.NotStale, "live") }
		}, blockedby.NotStale, "stale"},
		{"stale claim then open PRs", func(in *claimIn) { in.reclaimStale = "dana" }, "", "stale openPRs cross cross"},
		{"open PR before cross milestone", func(in *claimIn) {
			in.openPRs = func() (map[tracker.IssueKey]tracker.PR, error) {
				calls = append(calls, "openPRs")
				return map[tracker.IssueKey]tracker.PR{tracker.NumKey(1): {Number: 4}}, nil
			}
		}, blockedby.OpenPR, "openPRs"},
		{"cross milestone on a member", func(in *claimIn) { in.crossCheck = cross }, blockedby.CrossMilestone, "openPRs cross cross"},
		{"cross milestone accepted", func(in *claimIn) { in.crossCheck, in.acceptCross = cross, true }, "", "openPRs"},
		{"resuming skips open PRs and cross", func(in *claimIn) { in.resuming, in.crossCheck = true, cross }, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := base()
			tc.mod(&in)
			_, err := claimsRefusal(in)
			var blk *BlockedError
			switch {
			case tc.wantBy == "" && err != nil:
				t.Errorf("err = %v", err)
			case tc.wantBy != "" && (!errors.As(err, &blk) || blk.By != tc.wantBy):
				t.Errorf("err = %v, want blockedBy %s", err, tc.wantBy)
			}
			if got := strings.Join(calls, " "); got != tc.wantCalls {
				t.Errorf("lookups = %q, want %q", got, tc.wantCalls)
			}
		})
	}
}

func TestAssessReadiness(t *testing.T) {
	lead, member := issueItem(1), issueItem(2)
	inFlight := []InFlight{{Slot: "ben", Issue: "1"}, {Slot: "ben", Issue: "#2"}, {Slot: "dana", Issue: "9"}}
	boom := errors.New("board down")
	var seen [][]string
	assess := func(failOn string) func(string, []InFlight) (Readiness, error) {
		seen = nil
		return func(id string, fl []InFlight) (Readiness, error) {
			var issues []string
			for _, f := range fl {
				issues = append(issues, f.Issue)
			}
			seen = append(seen, issues)
			r := Readiness{ID: id, Checks: []Check{{Name: CheckTemplate, OK: false}}}
			if id == failOn {
				return r, boom
			}
			return r, nil
		}
	}
	t.Run("own bundle leaves the in-flight list", func(t *testing.T) {
		l, b, err := assessReadiness(readinessIn{lead: lead, members: []*backlog.Item{member}, inFlight: inFlight, assess: assess("")})
		if err != nil || l.ID != "1" || len(b) != 1 || b[0].ID != "2" {
			t.Fatalf("l=%+v b=%+v err=%v", l, b, err)
		}
		for _, got := range seen {
			if !slices.Equal(got, []string{"9"}) {
				t.Errorf("in flight = %v, want only 9", got)
			}
		}
		if len(inFlight) != 3 {
			t.Errorf("caller's list changed: %v", inFlight)
		}
	})
	t.Run("template waived only when asked", func(t *testing.T) {
		l, _, _ := assessReadiness(readinessIn{lead: lead, inFlight: inFlight, assess: assess("")})
		if l.Checks[0].OK {
			t.Errorf("waived without the flag")
		}
		l, _, _ = assessReadiness(readinessIn{lead: lead, inFlight: inFlight, waiveTemplate: true, assess: assess("")})
		if !l.Checks[0].OK {
			t.Errorf("not waived")
		}
	})
	t.Run("lead error stops before members", func(t *testing.T) {
		_, b, err := assessReadiness(readinessIn{lead: lead, members: []*backlog.Item{member}, inFlight: inFlight, assess: assess("1")})
		if !errors.Is(err, boom) || b != nil || len(seen) != 1 {
			t.Errorf("err=%v b=%v lookups=%d", err, b, len(seen))
		}
	})
	t.Run("member error keeps the lead", func(t *testing.T) {
		l, b, err := assessReadiness(readinessIn{lead: lead, members: []*backlog.Item{member}, inFlight: inFlight, assess: assess("2")})
		if !errors.Is(err, boom) || b != nil || l.ID != "1" {
			t.Errorf("l=%+v b=%v err=%v", l, b, err)
		}
	})
}

func TestStartPoint(t *testing.T) {
	var calls []string
	in := func() startIn {
		calls = nil
		return startIn{
			slotBase:  "slotbase",
			milestone: func() (string, string, error) { calls = append(calls, "milestone"); return "", "", nil },
			freshBase: func(t string) (string, error) { calls = append(calls, "fresh:"+t); return "fresh-" + t, nil },
			fetchBase: func(b string) (string, error) { calls = append(calls, "fetch:"+b); return "origin/" + b, nil },
		}
	}
	boom := errors.New("fetch failed")
	for _, tc := range []struct {
		name                  string
		mod                   func(*startIn)
		from, base, wantCalls string
		warns                 []string
		wantErr               bool
	}{
		{"plain item", func(*startIn) {}, "", "", "milestone", nil, false},
		{"milestone branch", func(s *startIn) {
			s.milestone = func() (string, string, error) { calls = append(calls, "milestone"); return "m1", "heads up", nil }
		}, "origin/m1", "m1", "milestone fetch:m1", []string{"heads up"}, false},
		{"integration beats milestone", func(s *startIn) { s.integration = "int" }, "origin/int", "int", "fetch:int", nil, false},
		{"support without base uses slot base", func(s *startIn) { s.support = true }, "fresh-slotbase", "", "milestone fresh:slotbase", nil, false},
		{"support with integration base", func(s *startIn) { s.support, s.integration = true, "int" }, "fresh-int", "int", "fresh:int", nil, false},
		{"milestone error", func(s *startIn) {
			s.milestone = func() (string, string, error) { return "", "w", boom }
		}, "", "", "", nil, true},
		{"fetch error keeps base", func(s *startIn) {
			s.integration = "int"
			s.fetchBase = func(string) (string, error) { return "", boom }
		}, "", "int", "", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := in()
			tc.mod(&s)
			from, base, warns, err := startPoint(s)
			if from != tc.from || base != tc.base || !slices.Equal(warns, tc.warns) || (err != nil) != tc.wantErr {
				t.Errorf("from=%q base=%q warns=%v err=%v", from, base, warns, err)
			}
			if tc.wantCalls != "" && strings.Join(calls, " ") != tc.wantCalls {
				t.Errorf("calls = %q, want %q", strings.Join(calls, " "), tc.wantCalls)
			}
		})
	}
}
