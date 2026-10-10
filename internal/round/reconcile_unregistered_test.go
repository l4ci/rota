package round

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/worker"
)

// unregFixture is a repo whose given branches carry a commit each, checked out
// in worktrees outside .worktrees (as a Codex-managed worktree is), with
// round.adoptPattern codex/*.
func unregFixture(t *testing.T, branches ...string) (string, Env, *fakeRemote) {
	t.Helper()
	root := newRepo(t, map[string]string{"ben": "ben/613-other"})
	for _, br := range branches {
		wt := filepath.Join(t.TempDir(), "wt")
		sh(t, root, "worktree", "add", "-q", "-b", br, wt, "main")
		if err := os.WriteFile(filepath.Join(wt, "work"), []byte(br), 0o644); err != nil {
			t.Fatal(err)
		}
		sh(t, wt, "add", "work")
		sh(t, wt, "commit", "-q", "-m", "work")
	}
	fb := &fakeRemote{states: map[int]string{}}
	fb.add("612", "Thing", "M01", false, filesBody)
	fb.add("613", "Other", "M01", false, "## Acceptance\n- [ ] ok\n\n## Files\n- docs/other.md\n")
	e := env(nil, fb.asForge())
	e.Board, e.Worker.Getenv, e.AdoptPattern = fb, noEnv, "codex/*"
	return root, e, fb
}

func unregistered(fs []Finding) []Finding {
	var out []Finding
	for _, f := range fs {
		if f.Kind == UnregisteredBranch {
			out = append(out, f)
		}
	}
	return out
}

func TestReconcileUnregisteredBranch(t *testing.T) {
	root, e, _ := unregFixture(t, "codex/612-thing", "other/9-nope")
	sh(t, root, "branch", "codex/merged-already", "main") // merged into base: not drift
	out, err := e.Reconcile(bg, root, false)
	if err != nil {
		t.Fatal(err)
	}
	got := unregistered(out.Drift)
	if len(got) != 1 || got[0].Issue != "612" || !strings.Contains(got[0].Detail, "codex/612-thing") {
		t.Fatalf("want one finding for 612, got %+v", got)
	}
	if len(worker.LoadRegistryTolerant(root).Slots()) != 0 {
		t.Fatal("a report-only reconcile registered a slot")
	}
	e.AdoptPattern = ""
	if out, _ = e.Reconcile(bg, root, false); len(unregistered(out.Drift)) != 0 {
		t.Errorf("an empty pattern turns the check off: %+v", out.Drift)
	}
}

func TestReconcileUnregisteredHeldBranchIsNotDrift(t *testing.T) {
	root, e, _ := unregFixture(t, "codex/612-thing")
	writeRegistry(t, root, slot(root, "ext-1", "codex/612-thing", func(o *jsonx.Object) {
		o.Set("kind", worker.KindExternal)
		o.Set("task", "612")
		o.Set("handle", "")
	}))
	out, err := e.Reconcile(bg, root, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := unregistered(out.Drift); len(got) != 0 {
		t.Errorf("a held branch is registered: %+v", got)
	}
}

func TestReconcileApplyAdopts(t *testing.T) {
	root, e, _ := unregFixture(t, "codex/612-thing", "codex/nonumber")
	out, err := e.Reconcile(bg, root, true)
	if err != nil {
		t.Fatal(err)
	}
	var adopted *worker.Slot
	for _, s := range worker.LoadRegistryTolerant(root).Slots() {
		if s.Branch() == "codex/612-thing" {
			adopted = s
		}
		if s.Branch() == "codex/nonumber" {
			t.Errorf("adopted a branch with no issue number: %s", s.Name())
		}
	}
	if adopted == nil || !adopted.IsExternal() || adopted.HeldID() != "612" {
		t.Fatalf("want an external slot holding 612, got %v", adopted)
	}
	// The tick audits and reports each finding as "<kind> <key>" (internal/cli/round_tick.go).
	r := unregistered(out.Repaired)
	if len(r) != 1 || r[0].Issue != "612" || r[0].Kind+" "+r[0].Key() != "unregistered-branch codex/612-thing" {
		t.Errorf("repaired: %+v", out.Repaired)
	}
	left := unregistered(out.Drift)
	if len(left) != 1 || !strings.Contains(left[0].Detail, "--issue") || left[0].Repair != "" {
		t.Errorf("a branch with no number stays listed with needs --issue: %+v", left)
	}
}

func TestReconcileApplySkipsOverlap(t *testing.T) {
	root, e, fb := unregFixture(t, "codex/612-thing")
	fb.details["613"] = filesBody // ben's item now clashes with 612
	writeRegistry(t, root, slot(root, "ben", "ben/613-other", func(o *jsonx.Object) { o.Set("task", "613") }))
	out, err := e.Reconcile(bg, root, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range worker.LoadRegistryTolerant(root).Slots() {
		if s.IsExternal() || s.Branch() == "codex/612-thing" {
			t.Fatalf("a blocking overlap adopted %s", s.Name())
		}
	}
	if len(unregistered(out.Drift)) != 1 || len(unregistered(out.Repaired)) != 0 {
		t.Errorf("the finding stays drift: drift %+v repaired %+v", out.Drift, out.Repaired)
	}
	warned := false
	for _, w := range out.Report.Warnings {
		warned = warned || (strings.Contains(w, "codex/612-thing") && strings.Contains(w, "overlap"))
	}
	if !warned {
		t.Errorf("want an overlap warning naming the branch: %v", out.Report.Warnings)
	}
}

func TestFindingKeyDistinguishesNumberlessBranches(t *testing.T) {
	root, e, _ := unregFixture(t, "codex/nonum-a", "codex/nonum-b")
	out, err := e.Reconcile(bg, root, false)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, f := range unregistered(out.Drift) {
		keys[f.Key()] = true
	}
	if len(keys) != 2 || !keys["codex/nonum-a"] || !keys["codex/nonum-b"] {
		t.Errorf("keys: %v", keys)
	}
}

func TestStatusSkipsTheBranchScan(t *testing.T) {
	root, e, _ := unregFixture(t, "codex/612-thing")
	rep, err := e.Status(bg, root)
	if err != nil {
		t.Fatal(err)
	}
	if got := unregistered(rep.Findings); len(got) != 0 {
		t.Errorf("plain status must not scan branches: %+v", got)
	}
}

// A corrupt registry is not an empty pool: Reconcile refuses instead of
// reporting every adoptable branch as unregistered (#712).
func TestReconcileRefusesACorruptRegistry(t *testing.T) {
	root, e, _ := unregFixture(t, "codex/612-thing")
	if err := os.WriteFile(worker.RegistryPath(root), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := e.Reconcile(bg, root, false)
	if err == nil {
		t.Fatalf("want an error on a corrupt registry, got findings %+v", unregistered(out.Drift))
	}
	if !errors.Is(err, fsio.ErrUnreadable) {
		t.Errorf("want fsio.ErrUnreadable, got %v", err)
	}
	if got := unregistered(out.Drift); len(got) != 0 {
		t.Errorf("no unregistered findings off a corrupt registry: %+v", got)
	}
}
