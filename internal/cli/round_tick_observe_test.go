package cli

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/rotastatetest"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/roundtick"
	"github.com/l4ci/rota/internal/worker"
)

// Exercise a repair that calls the host and rewrites the registry. An empty
// round cannot distinguish InspectReconcile from an applying Reconcile.
func TestRoundTickObserveReconcileDoesNotRepair(t *testing.T) {
	for _, mode := range []string{"adapter", "tick"} {
		t.Run(mode, func(t *testing.T) {
			f := newScopeFx(t, "warn", "")
			if err := worker.Update(f.root, func(d *worker.Doc) { d.Slot("ben").SetHandle("w1:t1") }); err != nil {
				t.Fatal(err)
			}
			var repairs int
			build := f.deps.RoundEnv
			f.deps.RoundEnv = func(ctx context.Context, root string, opts RoundOpts) round.Env {
				e := build(ctx, root, opts)
				e.HostName = host.Herdr
				e.Snapshot = func(context.Context) ([]host.Agent, error) {
					return []host.Agent{{Name: host.AgentName("ben", "w1:t1"), Tab: "w2:t2", Status: "working"}}, nil
				}
				e.FollowTab = func(context.Context, string, string, string) error {
					repairs++
					return nil
				}
				return e
			}
			before, err := os.ReadFile(rotatree.Workers(f.root))
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			w := &tickWorld{root: f.root, renv: f.deps.roundEnv(ctx, f.root), observe: true}
			if mode == "adapter" {
				out, err := w.Reconcile(ctx)
				if err != nil {
					t.Fatal(err)
				}
				hasMovedTab := func(f round.Finding) bool { return f.Kind == round.TabMoved && f.Slot == "ben" }
				if !slices.ContainsFunc(out.Proposed, hasMovedTab) || !slices.ContainsFunc(out.Drift, hasMovedTab) || len(out.Repaired) != 0 {
					t.Errorf("inspection must propose the repair and retain drift without applying: %+v", out)
				}
			} else {
				code, out, stderr := rotaInWith(t, f.deps, f.root, "--json", "round", "tick", "--observe", "--holder-pid", "4242")
				if code != 0 {
					t.Fatalf("observe exit %d: %s %s", code, out, stderr)
				}
				proposals, _ := data(t, out)["proposed"].([]any)
				if !slices.ContainsFunc(proposals, func(p any) bool {
					m := p.(map[string]any)
					return m["action"] == "reconcile" && m["target"] == "tab-moved ben"
				}) {
					t.Errorf("observation did not propose the moved-tab repair: %s", out)
				}
			}
			after, err := os.ReadFile(rotatree.Workers(f.root))
			if err != nil {
				t.Fatal(err)
			}
			if repairs != 0 || string(before) != string(after) {
				t.Fatalf("observation applied a repair: host repairs=%d, registry changed=%v", repairs, string(before) != string(after))
			}
			// Positive control: the same drift really does reach the applying
			// adapter when execution is requested.
			w.observe = false
			applied, err := w.Reconcile(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if repairs != 1 || !slices.ContainsFunc(applied.Repaired, func(f round.Finding) bool { return f.Kind == round.TabMoved && f.Slot == "ben" }) {
				t.Fatalf("execution did not exercise the repair adapter: repairs=%d outcome=%+v", repairs, applied)
			}
		})
	}
}

func TestRoundTickObserveReviewLoopDoesNotRelay(t *testing.T) {
	f := &reviewForge{comments: []string{"please rename x"}}
	c, root, _ := reviewCtx(t, f)
	h := &cliHost{inSession: true}
	useHost(c.Deps, h)
	if err := worker.Update(root, func(d *worker.Doc) { d.Slot("nia").SetHandle("w1:t1") }); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(rotatree.Workers(root))
	if err != nil {
		t.Fatal(err)
	}
	w := &tickWorld{c: c, root: root, set: roundcfg.Settings{ReviewLoop: roundcfg.ReviewLoopAuto, MaxBounces: 3}, observe: true}
	got := w.ReviewLoop("nia")
	if !got.Proposed || got.Relayed || got.Detail != "1 from rev "+prURL {
		t.Errorf("observation must report pending input as a proposal: %+v", got)
	}
	after, err := os.ReadFile(rotatree.Workers(root))
	if err != nil {
		t.Fatal(err)
	}
	if len(h.calls) != 0 || len(f.posted) != 0 || string(before) != string(after) {
		t.Fatalf("observation relayed review input: host=%v posted=%v registry changed=%v", h.calls, f.posted, string(before) != string(after))
	}
}

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
