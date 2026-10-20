package cli

import "testing"

func TestRoundAcceptExitCodes(t *testing.T) {
	deps := testDeps()
	dir := workerProject(t, `{}`)
	if code, _, errOut := rotaInWith(t, deps, dir, "round", "accept"); code != 2 {
		t.Errorf("no item: exit %d, want 2: %s", code, errOut)
	}
	if code, _, errOut := rotaInWith(t, deps, dir, "round", "accept", "B01"); code != 3 {
		t.Errorf("an unknown item exits 3, got %d: %s", code, errOut)
	}
}
