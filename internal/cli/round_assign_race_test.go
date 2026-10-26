package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/worker"
)

func TestRoundAssignRaceExitsFourWithSlotBusy(t *testing.T) {
	f := newScopeFx(t, "warn", "")
	realGit := f.deps.Git
	fired := false
	f.deps.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		// Assignment has planned against the idle slot when it looks up the
		// attempt's inherited branch tip. Complete the competing assign here.
		if !fired && strings.Join(args, " ") == "check-ref-format --branch dana/2-candidate" {
			fired = true
			code, out, errOut := rotaInWith(t, f.deps, f.root, "--json", "round", "assign", "3", "--agent", "dana", "--holder-pid", "4242")
			if code != 0 {
				t.Fatalf("first assign exit %d: %s %s", code, out, errOut)
			}
		}
		return realGit(ctx, dir, args...)
	}
	code, out, errOut := rotaInWith(t, f.deps, f.root, "--json", "round", "assign", "2", "--agent", "dana", "--holder-pid", "4242")
	if !fired {
		t.Fatal("competing assign did not run")
	}
	if code != 4 {
		t.Fatalf("later assign exit %d, want 4: %s %s", code, out, errOut)
	}
	if d := data(t, out); d["blockedBy"] != "slot-busy" || d["changed"] != false {
		t.Errorf("refusal data: %v", d)
	}
	if held := worker.LoadRegistryTolerant(f.root).Slot("dana").HeldID(); held != "3" {
		t.Errorf("slot holds %q, want winner 3", held)
	}
}
