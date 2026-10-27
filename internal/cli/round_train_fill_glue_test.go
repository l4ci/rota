package cli

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/roundtick"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/verdict"
)

// The real tick runs against git, a round lease and a fake forge. With no full
// tier configured a real gate can report its refusal, but cannot land anything.
func fillTickFixture(t *testing.T, review string, reviewed, clocked bool, viewedState ...string) (*Ctx, string, func() roundtick.Result) {
	t.Helper()
	f := newScopeFx(t, "warn", "")
	gitIn(t, f.root, "branch", "-M", "main")
	gitIn(t, f.root, "checkout", "-b", "ben/1-fill")
	write(t, filepath.Join(f.root, "feature.txt"), "feature")
	gitIn(t, f.root, "add", "feature.txt")
	gitIn(t, f.root, "commit", "-m", "feature")
	gitIn(t, f.root, "checkout", "main")
	origin := filepath.Join(t.TempDir(), "origin.git")
	gitIn(t, f.root, "init", "--bare", origin)
	gitIn(t, f.root, "remote", "add", "origin", origin)
	gitIn(t, f.root, "push", "origin", "main", "ben/1-fill")
	r, err := f.deps.Git(context.Background(), f.root, "rev-parse", "ben/1-fill")
	if err != nil || r.ExitCode != 0 {
		t.Fatalf("head: %+v %v", r, err)
	}
	head := strings.TrimSpace(r.Stdout)
	body := "## Acceptance\n- [ ] x\n\n## Files\n- feature.txt\n"
	issue, _ := json.Marshal(map[string]any{"number": 1, "title": "Feature", "body": body, "labels": []string{}, "state": "OPEN"})
	f.issues["1"] = string(issue)
	write(t, filepath.Join(f.root, ".rota", "config.json"), `{"forge":{"provider":"github"},"issues":{"source":"github"},"ship":{"review":`+review+`,"mergeApproval":"none"},"autonomy":{"level":"auto"},"round":{"scope":"open","batch":1,"architectureEvery":0,"trainMax":4,"trainFillMinutes":20}}`)
	event := ""
	if clocked {
		event = `,"event":{"kind":"done","ts":"2026-10-27T08:54:00Z"}`
	}
	write(t, filepath.Join(f.root, ".rota", "workers.json"), `{"round":1,"slots":[{"name":"ben","task":"1","branch":"ben/1-fill","base":"main","state":"done","pr":"#11"`+event+`}]}`)
	previous := f.deps.trackerExec()
	f.deps.TrackerOptions = append(f.deps.TrackerOptions, tracker.WithExec(func(ctx context.Context, dir, name string, args []string, stdin []byte) ([]byte, []byte, int, error) {
		if len(args) >= 2 && args[0] == "pr" && (args[1] == "view" || args[1] == "list") {
			pr := map[string]any{"number": 11, "headRefName": "ben/1-fill", "headRefOid": head, "baseRefName": "main", "state": "OPEN", "body": "Closes #1\n\nDoor: two-way.", "url": "https://github.com/o/r/pull/11"}
			// The list can be an earlier open snapshot while a fresh PR view
			// sees that someone else has just closed or merged it.
			if args[1] == "view" && len(viewedState) > 0 {
				pr["state"] = viewedState[0]
			}
			var value any = pr
			if args[1] == "list" {
				value = []any{pr}
				for i := 2; i+1 < len(args); i++ {
					if args[i] == "--head" && args[i+1] != "ben/1-fill" || args[i] == "--state" && args[i+1] != "open" && args[i+1] != "all" {
						value = []any{}
					}
				}
			}
			b, err := json.Marshal(value)
			return b, nil, 0, err
		}
		return previous(ctx, dir, name, args, stdin)
	}, func(name string) (string, error) { return "/fake/" + name, nil }))
	f.deps.Getwd = func() (string, error) { return f.root, nil }
	f.deps.Getenv = func(string) string { return "" }
	pinNow(f.deps, "2026-10-27T09:00:00Z")
	if reviewed {
		at, _ := time.Parse(time.RFC3339, "2026-10-27T08:54:00Z")
		if _, err := verdict.AddBranch(f.root, "ben/1-fill", verdict.NewRecord(verdict.ReviewSpec, verdict.Pass, head, verdict.Body{}, at)); err != nil {
			t.Fatal(err)
		}
	}
	set, err := f.deps.roundSettings(f.root, roundConfigRequired)
	if err != nil {
		t.Fatal(err)
	}
	c := &Ctx{Deps: f.deps, ctx: context.Background(), Stderr: io.Discard, Path: "rota round tick"}
	return c, f.root, func() roundtick.Result {
		t.Helper()
		r, err := autopilotTick(c, f.root, set, "", scopePID)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
}

func TestAutopilotTrainFillPRStatePassThrough(t *testing.T) {
	for _, state := range []string{"CLOSED", "MERGED"} {
		t.Run(state, func(t *testing.T) {
			_, _, tick := fillTickFixture(t, `"light"`, false, true, state)
			r := tick()
			if len(r.Holding) != 0 {
				t.Fatalf("PR state refusal was held for fill: %+v", r)
			}
			handled := false
			for _, a := range r.Did {
				handled = handled || a.Action == "merge" || a.Action == "reconcile"
			}
			for _, item := range r.NeedsYou {
				if item.Kind == "awaiting-review" {
					t.Fatalf("PR state refusal was dropped as not ready: %+v", r)
				}
				handled = handled || item.Kind == "gate-failed"
			}
			if !handled {
				t.Fatalf("PR state was neither gated nor reconciled: %+v", r)
			}
		})
	}
}

func TestAutopilotTrainFillReadinessGlue(t *testing.T) {
	for _, tc := range []struct {
		name, policy                     string
		reviewed, clocked, holding, gate bool
	}{
		{"review disabled with done clock", `false`, false, true, true, false},
		{"reviewed fresh PR", `"light"`, true, true, true, false},
		{"missing required review", `"light"`, false, true, false, false},
		{"review disabled without clock", `false`, false, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, tick := fillTickFixture(t, tc.policy, tc.reviewed, tc.clocked)
			r := tick()
			if (len(r.Holding) == 1) != tc.holding {
				t.Fatalf("holding=%q, needs=%+v, want holding=%t", r.Holding, r.NeedsYou, tc.holding)
			}
			if tc.holding && r.Holding[0] != "holding train for main: 1/4 ready, oldest waited 6m of 20m" {
				t.Fatalf("holding=%q", r.Holding)
			}
			attempted := false
			for _, it := range r.NeedsYou {
				attempted = attempted || it.Kind == "gate-failed" && !strings.Contains(it.Why, "cannot read PR head")
			}
			for _, a := range r.Did {
				attempted = attempted || a.Action == "merge"
			}
			if attempted != tc.gate {
				t.Fatalf("gate attempted=%t, want %t; result=%+v", attempted, tc.gate, r)
			}
			if tc.holding {
				if r := tick(); len(r.Holding) != 0 {
					t.Fatalf("unchanged hold repeated: %q", r.Holding)
				}
			}
		})
	}
}

func TestAutopilotTrainFillRejectsQueuedMissingReview(t *testing.T) {
	c, root, _ := fillTickFixture(t, `"light"`, true, true)
	if _, err := config.Set(root, config.RoundTrainMaxKey, "2"); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "branch", "dana/2-fill", "ben/1-fill")
	gitIn(t, root, "push", "origin", "dana/2-fill")
	r, err := c.deps().Git(context.Background(), root, "rev-parse", "dana/2-fill")
	if err != nil || r.ExitCode != 0 {
		t.Fatalf("head: %+v %v", r, err)
	}
	head := strings.TrimSpace(r.Stdout)
	addRegistryRows(t, root, map[string][]map[string]any{"prs": {{"branch": "dana/2-fill", "pr": "#12", "base": "main", "issue": "2"}}})
	previous := c.deps().trackerExec()
	c.Deps.TrackerOptions = append(c.Deps.TrackerOptions, tracker.WithExec(func(ctx context.Context, dir, name string, args []string, stdin []byte) ([]byte, []byte, int, error) {
		pr := map[string]any{"number": 12, "headRefName": "dana/2-fill", "headRefOid": head, "baseRefName": "main", "state": "OPEN", "body": "Closes #2", "url": "https://github.com/o/r/pull/12"}
		if len(args) >= 3 && args[0] == "pr" && args[1] == "view" && (args[2] == "12" || args[2] == "dana/2-fill") {
			b, err := json.Marshal(pr)
			return b, nil, 0, err
		}
		b, stderr, code, err := previous(ctx, dir, name, args, stdin)
		if err == nil && code == 0 && len(args) >= 2 && args[0] == "pr" && args[1] == "list" {
			include := true
			for i := 2; i+1 < len(args); i++ {
				if args[i] == "--head" && args[i+1] != "dana/2-fill" || args[i] == "--state" && args[i+1] != "open" && args[i+1] != "all" {
					include = false
				}
			}
			if include {
				var rows []map[string]any
				if err := json.Unmarshal(b, &rows); err != nil {
					t.Fatal(err)
				}
				rows = append(rows, pr)
				b, err = json.Marshal(rows)
			}
		}
		return b, stderr, code, err
	}, func(name string) (string, error) { return "/fake/" + name, nil }))
	set, err := c.deps().roundSettings(root, roundConfigRequired)
	if err != nil {
		t.Fatal(err)
	}
	result, err := autopilotTick(c, root, set, "", scopePID)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Holding) != 1 || result.Holding[0] != "holding train for main: 1/2 ready, oldest waited 6m of 20m" {
		t.Fatalf("queued missing review contributed to train: holding=%q needs=%+v", result.Holding, result.NeedsYou)
	}
	waiting := false
	for _, it := range result.NeedsYou {
		waiting = waiting || it.Kind == "awaiting-review" && it.Target == "#12"
	}
	if !waiting {
		t.Fatalf("missing queued review was not reported: %+v", result)
	}
}
