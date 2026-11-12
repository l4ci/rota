package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/postgate"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/rotastatetest"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundtick"
)

func TestRoundTickObserveWithoutAuto(t *testing.T) {
	for _, level := range []string{"off", "roundtrip", "auto"} {
		t.Run(level, func(t *testing.T) {
			f := newScopeFx(t, "warn", "")
			if _, err := config.Set(f.root, "autonomy.level", level); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(rotatree.Workers(f.root))
			if err != nil {
				t.Fatal(err)
			}
			code, out, stderr := rotaInWith(t, f.deps, f.root, "--json", "round", "tick", "--observe", "--holder-pid", "4242", "--request-id", "observe-test")
			if code != 0 {
				t.Fatalf("observation must work under %s: exit %d: %s %s", level, code, out, stderr)
			}
			d := data(t, out)
			if d["observed"] != true || d["changed"] != false {
				t.Fatalf("not a read-only observation: %s", out)
			}
			if p, _ := d["proposed"].([]any); len(p) == 0 {
				t.Fatalf("missing proposals: %s", out)
			}
			if n, _ := d["notice"].(string); !strings.Contains(n, "not approval") {
				t.Errorf("missing the not-approval notice: %s", out)
			}
			if _, ok := d["unknown"].([]any); !ok {
				t.Errorf("missing the unknown list: %s", out)
			}
			if did, _ := d["did"].([]any); len(did) != 0 {
				t.Errorf("observation reports completed actions: %s", out)
			}
			cd, err := rotastatetest.CommonDir(f.root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(rotastate.File(cd, roundtick.StateFile)); !os.IsNotExist(err) {
				t.Errorf("observation saved autopilot state: %v", err)
			}
			after, err := os.ReadFile(rotatree.Workers(f.root))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("observation changed registry:\nbefore %s\nafter %s", before, after)
			}
			for _, name := range []string{"receipts.json", "gate-audit.jsonl"} {
				if _, err := os.Stat(filepath.Join(f.root, ".rota", name)); !os.IsNotExist(err) {
					t.Errorf("observation created %s: %v", name, err)
				}
			}
			code, out, stderr = rotaInWith(t, f.deps, f.root, "round", "tick", "--observe", "--holder-pid", "4242")
			if code != 0 || !strings.Contains(out, "proposed") || !strings.Contains(out, "not approval") {
				t.Fatalf("text observation: %d %s %s", code, out, stderr)
			}
		})
	}
}

// A held slot's worktree is scanned for overlap by the candidates pass. Under
// observation that scan must not take index.lock or refresh the index: every
// git status carries --no-optional-locks, nothing fetches, and the worktree's
// index bytes are unchanged.
func TestRoundTickObserveLeavesHeldWorktreeIndexAlone(t *testing.T) {
	f := newScopeFx(t, "warn", "")
	wt := filepath.Join(t.TempDir(), "ben")
	gittest.Run(t, f.root, "worktree", "add", "-q", "-b", "ben/1-holder", wt)
	write(t, filepath.Join(wt, "touched.txt"), "dirty")
	write(t, filepath.Join(f.root, ".rota", "workers.json"),
		`{"architectureReview":{"at":"2020-01-01T00:00:00Z"},"slots":[{"name":"ben","task":"1","branch":"ben/1-holder","worktree":"`+wt+`"},{"name":"dana","state":"idle"}]}`)
	index := strings.TrimSpace(gittest.Run(t, wt, "rev-parse", "--git-path", "index"))
	if !filepath.IsAbs(index) {
		index = filepath.Join(wt, index)
	}
	before, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	f.git = nil
	code, out, stderr := rotaInWith(t, f.deps, f.root, "--json", "round", "tick", "--observe", "--holder-pid", "4242")
	if code != 0 {
		t.Fatalf("observe exit %d: %s %s", code, out, stderr)
	}
	var statuses int
	for _, call := range f.git {
		fields := strings.Fields(call)
		if slices.Contains(fields, "fetch") {
			t.Errorf("observation fetched: %q", call)
		}
		if slices.Contains(fields, "status") {
			statuses++
			if fields[0] != "--no-optional-locks" {
				t.Errorf("observation ran git status that may write the index: %q", call)
			}
		}
	}
	if statuses == 0 {
		t.Fatalf("the held worktree was never scanned; the test proves nothing: %q", f.git)
	}
	after, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("observation rewrote the held worktree's index")
	}
}

// The no-mutation guarantee is structural: the observing world refuses every
// call that would change state, whatever the tick asks of it. One table lists
// each mutating World method; a new one must be added here or the seam's
// explicit implementation will not compile.
func TestObservingWorldRefusesMutations(t *testing.T) {
	o := observingWorld{&tickWorld{}}
	ctx := context.Background()
	refusals := map[string]func() error{
		"Landing": func() error { _, _, err := o.Landing(ctx, postgate.Request{}); return err },
		"Assign":  func() error { _, err := o.Assign(ctx, "#1"); return err },
		"Review.Mint": func() error {
			_, err := observingReviewer{}.Mint(ctx, round.Architecture{})
			return err
		},
		"FollowUp.Start": func() error {
			return o.FollowUp().Start(ctx, roundtick.FollowUp{}, "ben", false)
		},
	}
	for name, call := range refusals {
		if err := call(); !errors.Is(err, errReadOnlyWorld) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The audit line is dropped, not refused: the zero tickWorld would panic
	// if it wrote.
	o.Audit(roundtick.Action{})
}
