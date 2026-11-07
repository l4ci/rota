package round

import (
	"context"
	"errors"
	"github.com/l4ci/rota/internal/git"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

func TestReconcileProposedRepairs(t *testing.T) {
	root, e, _ := supersededPRFixture(t)
	out, err := e.Reconcile(bg, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Proposed) != 1 || out.Proposed[0].Kind != PRSuperseded {
		t.Fatalf("proposals = %+v, want one superseded PR repair", out.Proposed)
	}
}

func TestInspectReconcilePreservesStateAndAgreesWithApply(t *testing.T) {
	root, e, forge := fixture(t)
	e.Round.StallMinutes = 1 // includes the shared git-status probe
	diskBefore := inspectionFiles(t, root)
	before, err := os.ReadFile(worker.RegistryPath(root))
	if err != nil {
		t.Fatal(err)
	}
	out, err := e.InspectReconcile(bg, root)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(worker.RegistryPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) || len(forge.added) != 0 || len(out.Repaired) != 0 {
		t.Fatalf("inspection mutated state: registry changed=%v labels=%v repaired=%v", string(after) != string(before), forge.added, out.Repaired)
	}
	if diskAfter := inspectionFiles(t, root); !reflect.DeepEqual(diskBefore, diskAfter) {
		t.Fatal("inspection changed repository files, worktree indexes or execution state")
	}
	want := map[string][]string{"ben": {DeadTab}, "dana": {LabelMissing, UnregisteredWorktree}, "kit": {PRUnrecorded}, "finn": {PRStale}}
	if got := kinds(out.Proposed); !reflect.DeepEqual(got, want) {
		t.Fatalf("proposed=%v, want %v", got, want)
	}
	if len(out.Drift) != len(out.Report.Findings) {
		t.Fatalf("inspection lost drift: %+v", out)
	}
	applied, err := e.Reconcile(bg, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out.Proposed, applied.Proposed) || !reflect.DeepEqual(out.Proposed, applied.Repaired) {
		t.Fatalf("inspection and execution disagree: proposed=%+v applied=%+v", out.Proposed, applied)
	}
}

func TestInspectReconcileUnavailableSourcesAreNotProposed(t *testing.T) {
	for _, read := range []string{"open PRs", "recorded PR state", "labels"} {
		t.Run(read, func(t *testing.T) {
			root, e, fr := supersededPRFixture(t)
			switch read {
			case "open PRs":
				fr.prsErr = errors.New("forge down")
			case "recorded PR state":
				fr.stateErrs = map[int]error{7: errors.New("forge down")}
			case "labels":
				e.Forge = closedListFails{e.Forge}
			}
			out, err := e.InspectReconcile(bg, root)
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Proposed) != 0 || len(out.Repaired) != 0 {
				t.Fatalf("unavailable source proposed repairs: %+v", out)
			}
			if got := worker.LoadRegistryTolerant(root).Slot("ben").PR(); got != "https://github.com/o/r/pull/7" {
				t.Fatalf("inspection changed PR: %s", got)
			}
			if read == "labels" && len(kinds(out.Drift)["ben"]) == 0 {
				t.Fatal("partial source failure discarded known drift")
			}
		})
	}
}

func TestInspectArchitectureFactsPreservesRegistry(t *testing.T) {
	for _, seeded := range []bool{false, true} {
		name := "unseeded"
		if seeded {
			name = "seeded"
		}
		t.Run(name, func(t *testing.T) {
			root, e, be, set := archFixture(t, 1, []tracker.Issue{closedIssue(1, "feature", "2026-10-01T00:00:00Z")})
			set.ArchitectureIdleMinHours = 24
			if seeded {
				seedReview(t, root)
			}
			before, err := os.ReadFile(worker.RegistryPath(root))
			if err != nil {
				t.Fatal(err)
			}
			facts, ready, err := e.InspectArchitectureFacts(bg, root, be, set, nil)
			if err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(worker.RegistryPath(root))
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("inspection wrote architecture seed or cache")
			}
			decision := DecideReview(facts, ready)
			if seeded {
				if facts.Count != 1 || !decision.Due || decision.Trigger != TriggerThreshold {
					t.Fatalf("seeded facts=%+v decision=%+v", facts, decision)
				}
			} else {
				if facts.Count != 0 || facts.Since != "2026-10-04T12:00:00Z" || !facts.Cooling || decision.Due {
					t.Fatalf("unseeded facts=%+v decision=%+v", facts, decision)
				}
			}
			actual, actualReady, err := e.ArchitectureFacts(bg, root, be, set, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(facts, actual) || ready != actualReady {
				t.Fatalf("inspect facts=%+v/%v execute=%+v/%v", facts, ready, actual, actualReady)
			}
		})
	}
}

func TestInspectReconcileDoesNotFetchStaleClaimActivity(t *testing.T) {
	root := newRepo(t, nil)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	fr := &fakeRemote{claims: map[string]string{"7": "mac-ben@3"}, claimTimes: map[string]map[string]time.Time{"7": {"mac-ben@3": now.Add(-5 * time.Hour)}}}
	fr.add("7", "Abandoned task", "", false, "")
	fr.items["7"].Type = "tasks"
	fr.items["7"].Labels = []string{"claimed:mac-ben@mac"}
	e := env(nil, fr.asForge())
	e.Board = fr
	e.Worker = wk(worker.Env{Now: func() time.Time { return now }})
	e.Round.ClaimStaleMinutes = 240
	fetches := 0
	e.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		switch args[0] {
		case "ls-remote":
			return git.Result{Stdout: "abcdef\trefs/heads/mac-ben/7-work\n"}, nil
		case "fetch":
			fetches++
			return git.Result{}, nil
		case "log":
			return git.Result{Stdout: now.Add(-5 * time.Hour).Format(time.RFC3339)}, nil
		}
		return git.Exec(ctx, dir, args...)
	}
	out, err := e.InspectReconcile(bg, root)
	if err != nil {
		t.Fatal(err)
	}
	if fetches != 0 {
		t.Fatalf("inspection fetched %d remote branches", fetches)
	}
	if !strings.Contains(strings.Join(out.Report.Warnings, "\n"), "stale-claim age unavailable during observation") {
		t.Fatalf("unknown remote claim age not reported: %v", out.Report.Warnings)
	}
	for _, f := range out.Report.Findings {
		if f.Kind == StaleClaimKind {
			t.Fatalf("unknown branch age asserted stale: %+v", f)
		}
	}
}

// Compare complete repository contents, including git indexes and refs. File
// access times are deliberately absent: reading naturally updates them.
func inspectionFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[path] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
