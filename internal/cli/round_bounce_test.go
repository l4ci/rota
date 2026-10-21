package cli

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/worker"
)

func TestRoundBounceCountsAndRefusesAtTheCap(t *testing.T) {
	deps := testDeps()
	dir := workerProject(t, `{"round":{"maxBounces":2}}`)

	for want := 1; want <= 2; want++ {
		code, out, _ := rotaInWith(t, deps, dir, "round", "bounce", "#31", "--head", string(rune('a'+want)), "--json")
		if d := data(t, out); code != 0 || d["bounces"] != float64(want) || d["max"] != float64(2) {
			t.Fatalf("bounce %d: %d %v", want, code, d)
		}
	}
	code, out, errOut := rotaInWith(t, deps, dir, "round", "bounce", "31", "--head", "z", "--json")
	if d := data(t, out); code != 4 || d["bounces"] != float64(2) || d["changed"] != false {
		t.Fatalf("at the cap: %d %v %s", code, d, errOut)
	}
	if !strings.Contains(errOut, "acceptance criteria") || !strings.Contains(errOut, "--cap-reason ambiguous-criteria") {
		t.Errorf("hint must name the audit step:\n%s", errOut)
	}
	if n := worker.LoadRegistryTolerant(dir).Bounces("31"); n != 2 {
		t.Errorf("a refused bounce must not count, registry says %d", n)
	}
	// The cap is per item.
	if code, _, _ := rotaInWith(t, deps, dir, "round", "bounce", "32"); code != 0 {
		t.Errorf("another item: %d", code)
	}
	if code, _, _ := rotaInWith(t, deps, dir, "round", "bounce"); code != 2 {
		t.Errorf("no item: %d, want 2", code)
	}
}

func TestRoundBounceCapZeroIsOff(t *testing.T) {
	deps := testDeps()
	dir := workerProject(t, `{"round":{"maxBounces":0}}`)
	for i := 0; i < 5; i++ {
		if code, _, _ := rotaInWith(t, deps, dir, "round", "bounce", "31"); code != 0 {
			t.Fatalf("bounce %d refused with the cap off: %d", i+1, code)
		}
	}
}

func TestRoundBounceSlotMustBeInThePool(t *testing.T) {
	deps := testDeps()
	dir := workerProject(t, `{}`)
	rotaInWith(t, deps, dir, "worker", "pool", "init", "--slots", "1", "--base", "main")

	code, _, errOut := rotaInWith(t, deps, dir, "round", "bounce", "31", "--slot", "w9", "--head", "a", "--json")
	if code != 4 || !strings.Contains(errOut, "w9") {
		t.Fatalf("unknown slot: %d %s", code, errOut)
	}
	if n := worker.LoadRegistryTolerant(dir).Bounces("31"); n != 0 {
		t.Errorf("a refused slot must write nothing, registry says %d bounces", n)
	}
	if code, _, errOut := rotaInWith(t, deps, dir, "round", "bounce", "31", "--slot", "w1", "--head", "a", "--json"); code != 0 {
		t.Fatalf("known slot: %d %s", code, errOut)
	}
	if n := worker.LoadRegistryTolerant(dir).Bounces("31"); n != 1 {
		t.Errorf("known slot must count, registry says %d", n)
	}
}

// A bundle bounces once, on its lead: a member is refused before it counts.
func TestRoundBounceBundleMember(t *testing.T) {
	deps := testDeps()
	dir := workerProject(t, `{}`)
	rotaInWith(t, deps, dir, "worker", "pool", "init", "--slots", "1", "--base", "main")
	slots := worker.LoadRegistryTolerant(dir).Slots()
	name := slots[0].Name()
	if _, err := worker.UpdateSlot(dir, name, func(s *worker.Slot) { s.SetTask("31"); s.SetBundle([]string{"32", "33"}) }); err != nil {
		t.Fatal(err)
	}
	if err := worker.Update(dir, func(d *worker.Doc) {
		d.QueuePR(worker.QueuedPR{Issue: "40", PR: "#9", From: "kit", Bundle: []string{"41"}})
	}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ member, lead, slot string }{{"#32", "31", name}, {"41", "40", "kit"}} {
		code, out, errOut := rotaInWith(t, deps, dir, "round", "bounce", c.member, "--json")
		d := data(t, out)
		if code != 4 || d["blockedBy"] != "bundle" || d["lead"] != c.lead || d["changed"] != false {
			t.Fatalf("%s: %d %v %s", c.member, code, d, errOut)
		}
		if !strings.Contains(errOut, "bundled under #"+c.lead+" (slot "+c.slot+")") || !strings.Contains(errOut, "rota round bounce "+c.lead) {
			t.Errorf("%s: %s", c.member, errOut)
		}
	}
	reg := worker.LoadRegistryTolerant(dir)
	if reg.Bounces("32") != 0 || reg.Bounces("41") != 0 {
		t.Error("a refused member bounce counts nothing")
	}
	// The lead counts as for any item.
	if code, _, _ := rotaInWith(t, deps, dir, "round", "bounce", "31", "--head", "a"); code != 0 {
		t.Errorf("the lead bounces: %d", code)
	}
	if n := worker.LoadRegistryTolerant(dir).Bounces("31"); n != 1 {
		t.Errorf("lead bounces %d", n)
	}
}
