package round

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/roundcfg"
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
