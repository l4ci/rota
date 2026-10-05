package cli

import (
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
	if n := worker.LoadRegistry(dir).Bounces("31"); n != 2 {
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
