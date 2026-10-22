package round

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// supportHandoffFixture is main's repo with round.support "mac" and three
// support branches pushed to origin, each with an open PR: 612 handed to
// review, 614 still in progress, 615 handed to review but on a branch outside
// the support prefix's agent/issue shape. Only 612's remote-tracking ref is
// gone, as on a main machine that has not fetched since the support side
// pushed it.
func supportHandoffFixture(t *testing.T) (string, Env, *fakeRemote) {
	t.Helper()
	root, e, fb := unregFixture(t)
	origin := filepath.Join(gitTemp(t), "origin.git")
	sh(t, root, "clone", "-q", "--bare", root, origin)
	sh(t, root, "remote", "add", "origin", origin)
	for _, br := range []string{"mac-ben/612-thing", "mac-kit/614-busy", "mac-nia"} {
		wt := filepath.Join(t.TempDir(), "wt")
		sh(t, root, "worktree", "add", "-q", "-b", br, wt, "main")
		writeFile(t, filepath.Join(wt, "work-"+strings.ReplaceAll(br, "/", "-")), br)
		sh(t, wt, "add", ".")
		sh(t, wt, "commit", "-q", "-m", "support work")
		sh(t, root, "push", "-q", "origin", br)
		sh(t, root, "worktree", "remove", "--force", wt)
		sh(t, root, "branch", "-q", "-D", br)
	}
	sh(t, root, "update-ref", "-d", "refs/remotes/origin/mac-ben/612-thing")
	fb.add("614", "Busy", "M01", false, "## Acceptance\n- [ ] ok\n")
	fb.add("615", "Odd", "M01", false, "## Acceptance\n- [ ] ok\n")
	fb.prs = []tracker.PR{
		{Number: 70, Branch: "mac-ben/612-thing", URL: "https://github.com/o/r/pull/70", Body: "Closes #612"},
		{Number: 71, Branch: "mac-kit/614-busy", URL: "https://github.com/o/r/pull/71", Body: "Closes #614"},
		{Number: 72, Branch: "mac-nia", URL: "https://github.com/o/r/pull/72", Body: "Closes #615"},
	}
	fb.prViews = map[int]tracker.PRInfo{70: {Head: "mac-ben/612-thing", State: "OPEN"}}
	fb.labels = map[int][]string{612: {"needs-review"}, 614: {"in-progress"}, 615: {"needs-review"}}
	e.Round.AdoptPattern = ""
	e.Round.Support = "mac"
	return root, e, fb
}

// #1598 AC-6 (T5): main's reconcile lists a support PR handed to review on a
// `<round.support>-*/*` branch from the forge alone, and never a support
// branch still in flight. With round.support unset or on the support machine
// itself, nothing is listed (AC-1).
func TestReconcileUnregisteredSupportNeedsReview(t *testing.T) {
	root, e, _ := supportHandoffFixture(t)
	out, err := e.Reconcile(bg, root, false)
	if err != nil {
		t.Fatal(err)
	}
	got := unregistered(out.Drift)
	if len(got) != 1 || got[0].Issue != "612" || got[0].Key() != "mac-ben/612-thing" || got[0].Repair == "" || !strings.Contains(got[0].Detail, "#70") {
		t.Fatalf("want one adoptable finding for 612 on PR #70, got %+v", got)
	}

	// The user's own pattern matching support branches does not bypass the
	// review filter: 614 is still the support machine's.
	e.Round.AdoptPattern = "*/*"
	if out, _ = e.Reconcile(bg, root, false); len(unregistered(out.Drift)) != 1 {
		t.Errorf("round.adoptPattern */* reported an in-flight support branch: %+v", unregistered(out.Drift))
	}
	e.Round.AdoptPattern = ""

	e.Round.Role = roundcfg.RoleSupport
	if out, _ = e.Reconcile(bg, root, false); len(unregistered(out.Drift)) != 0 {
		t.Errorf("the support machine adopts nothing of its own: %+v", out.Drift)
	}
	e.Round.Role, e.Round.Support = "", ""
	if out, _ = e.Reconcile(bg, root, false); len(unregistered(out.Drift)) != 0 {
		t.Errorf("round.support unset lists no support branch: %+v", out.Drift)
	}
}

// #1598 AC-6 (T5): --apply fetches the support branch main has not seen yet and
// adopts it as a hostless slot recording the PR, which the merge gate then
// resolves like any adopted slot.
func TestReconcileApplyAdoptsSupportPR(t *testing.T) {
	root, e, _ := supportHandoffFixture(t)
	out, err := e.Reconcile(bg, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if r := unregistered(out.Repaired); len(r) != 1 || r[0].Issue != "612" {
		t.Fatalf("repaired %+v, drift %+v, warnings %v", out.Repaired, out.Drift, out.Report.Warnings)
	}
	var s *worker.Slot
	for _, x := range worker.LoadRegistryTolerant(root).Slots() {
		if x.Branch() == "mac-ben/612-thing" {
			s = x
		}
	}
	if s == nil || !s.IsExternal() || s.HeldID() != "612" || s.PR() != "https://github.com/o/r/pull/70" {
		t.Fatalf("want an external slot holding 612 with PR #70, got %v", s)
	}
	if r, _ := git.Exec(bg, root, "rev-parse", "--verify", "-q", "refs/heads/mac-ben/612-thing"); r.ExitCode != 0 {
		t.Error("no local branch for the adopted support PR")
	}
	tg, err := worker.LoadRegistryTolerant(root).GateTarget(s.Name())
	if err != nil || !tg.External || tg.Branch != "mac-ben/612-thing" || tg.PR != s.PR() {
		t.Errorf("the gate does not resolve the adopted slot: %+v %v", tg, err)
	}
}
