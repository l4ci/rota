package worker

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/landing"
)

// footBody is long enough that git still calls an edited, moved copy a rename.
const footBody = "a\n-\n-\n-\n-\n-\n-\n-\n-\n-\n-\n-\n-\n-\n-\n-\n-\n-\n-\n-\nz\n"

// seedBoth puts file on main and merges it into w1, both pushed.
func seedBoth(t *testing.T, w *world, file string) {
	t.Helper()
	gittest.Run(t, w.worker, "checkout", "-q", "main")
	os.WriteFile(filepath.Join(w.worker, file), []byte(footBody), 0o644)
	gittest.Run(t, w.worker, "add", file)
	gittest.Run(t, w.worker, "commit", "-q", "-m", "seed "+file)
	gittest.Run(t, w.worker, "push", "-q", "origin", "main")
	gittest.Run(t, w.worker, "checkout", "-q", "w1")
	gittest.Run(t, w.worker, "merge", "-q", "origin/main", "-m", "sync")
}

// pushW1 commits everything in the worker tree on w1 and tells the forge.
func pushW1(t *testing.T, w *world, msg string) {
	t.Helper()
	gittest.Run(t, w.worker, "commit", "-q", "-am", msg)
	gittest.Run(t, w.worker, "push", "-q", "origin", "w1")
	w.forge("sha", gittest.Run(t, w.worker, "rev-parse", "HEAD"))
}

func staleNote(res landing.GateResult) string {
	for _, n := range res.Notes {
		if strings.HasPrefix(n, "STALE-MERGE") {
			return n
		}
	}
	return ""
}

func TestGateAndTrainShareRenameFootprint(t *testing.T) {
	w := newWorld(t, ghURL)
	seedBoth(t, w, "old.txt")
	gittest.Run(t, w.worker, "mv", "old.txt", "new.txt")
	pushW1(t, w, "rename")
	// main makes the same move, so the merge is clean and the gate notes every
	// path both sides changed.
	gittest.Run(t, w.worker, "checkout", "-q", "main")
	gittest.Run(t, w.worker, "mv", "old.txt", "new.txt")
	gittest.Run(t, w.worker, "commit", "-q", "-m", "main renames")
	gittest.Run(t, w.worker, "push", "-q", "origin", "main")

	res, err := w.gate(false, GateOpts{CheckOnly: true})
	if err != nil || res.Verdict != landing.GateFresh {
		t.Fatalf("%+v %v", res, err)
	}
	if note := staleNote(res); !strings.Contains(note, "new.txt") || !strings.Contains(note, "old.txt") {
		t.Errorf("gate footprint lacks a name of the rename: %q", note)
	}

	e := w.env(false)
	e.Ctx, e.Git = bg, git.Exec
	foots, err := e.trainFootprints(w.dir, "origin/main", []landing.TrainMember{{Target: "w1"}},
		func(landing.TrainMember) string { return "origin/w1" })
	if err != nil {
		t.Fatal(err)
	}
	got := slices.Clone(foots[0].paths)
	slices.Sort(got)
	if want := []string{"new.txt", "old.txt", "work.txt"}; !slices.Equal(got, want) {
		t.Errorf("train footprint %q, want %q", got, want)
	}
}

func TestTrainPlanChecksRenameAgainstEdit(t *testing.T) {
	w := trainWorld(t, "false")
	trainWrite(t, w, "old.txt", strings.TrimSuffix(footBody, "\n"))
	for _, b := range []string{"b1", "b2"} {
		gittest.Run(t, w.dir, "branch", b, "main")
	}
	gittest.Run(t, w.dir, "checkout", "-q", "b1")
	gittest.Run(t, w.dir, "mv", "old.txt", "new.txt")
	trainWrite(t, w, "new.txt", "one"+strings.TrimSuffix(footBody[1:], "\n"))
	gittest.Run(t, w.dir, "checkout", "-q", "b2")
	trainWrite(t, w, "old.txt", "two"+strings.TrimSuffix(footBody[1:], "\n"))
	gittest.Run(t, w.dir, "checkout", "-q", "main")
	os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"),
		[]byte(`{"slots":[{"name":"b1","branch":"b1"},{"name":"b2","branch":"b2"}]}`), 0o644)

	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2"}, Plan: true})
	if err != nil || res.Plan == nil {
		t.Fatalf("%+v %v", res, err)
	}
	if c := res.Plan.Conflicts; len(c) != 1 || strings.Join(c[0].Paths, ",") != "old.txt" {
		t.Fatalf("conflicts: %+v, landable %v", c, res.Plan.Landable)
	}
}

func TestGateSharedPathsGlobMatchesNonASCII(t *testing.T) {
	const file = "über.txt"
	w := newWorld(t, ghURL)
	seedBoth(t, w, file)
	os.WriteFile(filepath.Join(w.worker, file), []byte("A\n"+footBody[2:]), 0o644)
	pushW1(t, w, "edit the top")
	gittest.Run(t, w.worker, "checkout", "-q", "main")
	os.WriteFile(filepath.Join(w.worker, file), []byte(footBody[:len(footBody)-2]+"Z\n"), 0o644)
	gittest.Run(t, w.worker, "commit", "-q", "-am", "edit the bottom")
	gittest.Run(t, w.worker, "push", "-q", "origin", "main")

	// Without the glob the note names the path byte for byte; with it, nothing.
	for _, c := range []struct {
		cfg  string
		want bool
	}{
		{`{"test":{"full":["true"]}}`, true},
		{`{"test":{"full":["true"]},"round":{"sharedPaths":["ü*.txt"]}}`, false},
	} {
		w.setConfig(c.cfg)
		res, err := w.gate(false, GateOpts{CheckOnly: true})
		if err != nil || res.Verdict != landing.GateFresh {
			t.Fatalf("%+v %v", res, err)
		}
		if note := staleNote(res); strings.Contains(note, "both sides changed "+file) != c.want {
			t.Errorf("config %s: note %q, want the shared path named: %v", c.cfg, note, c.want)
		}
	}
}
