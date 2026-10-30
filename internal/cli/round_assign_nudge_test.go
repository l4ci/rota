package cli

import (
	"context"
	"github.com/l4ci/rota/internal/gittest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotastatetest"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/roundwatch"
)

// assignNudgeRepo is a file-backend repo whose only slot is done with a PR,
// with a ready candidate in the backlog when withCandidate is set.
func assignNudgeRepo(t *testing.T, withCandidate bool) string {
	t.Helper()
	dir := verdictRepo(t)
	for f, c := range map[string]string{"BACKLOG.md": "# TODO\n\n## Tasks\n\n## Completed\n", "counters.json": "{}\n"} {
		if err := os.WriteFile(filepath.Join(dir, ".rota", f), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if withCandidate {
		floorMinor(t, dir)
		body := writeBody(t, t.TempDir(), `{"findings":[{"severity":"minor","title":"rename the helper"}]}`)
		if code, _, errOut := rotaIn(t, dir, "verdict", "add", "--kind", "review-quality", "--verdict", "FAIL", "--body-file", body, "--file-deferred", "--pr", "#77"); code != 0 {
			t.Fatalf("file candidate: %d %s", code, errOut)
		}
	}
	write(t, filepath.Join(dir, ".rota", "workers.json"),
		`{"slots":[{"name":"w1","branch":"w1/1-x","state":"done","pr":"https://github.com/o/r/pull/11"},{"name":"w2","branch":"w2/2-y","state":"busy"}]}`)
	return dir
}

func watchedSlot(t *testing.T, dir, slot string) Result {
	t.Helper()
	c := &Ctx{Deps: testDeps(), ctx: context.Background()}
	cd, err := rotastatetest.CommonDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	res := roundwatch.Result{Reason: roundwatch.ReasonChange, Slot: &roundwatch.SlotNews{Slot: slot, State: "done"}}
	return watchResult(c, dir, cd, roundlease.DefaultEnv(), res, nil, "")
}

// AC-1/AC-2 (#1060): a done slot with a PR returns while a candidate is ready:
// the watch names the candidate and the assign command; with none, nothing extra.
func TestWatchNudgesToAssignBeforeGating(t *testing.T) {
	for _, tc := range []struct {
		name          string
		withCandidate bool
		slot          string
		want          bool
	}{
		{"candidate left", true, "w1", true},
		{"no candidates", false, "w1", false},
		{"slot not done", true, "w2", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := watchedSlot(t, assignNudgeRepo(t, tc.withCandidate), tc.slot)
			got := strings.Contains(res.Text, "assign before you gate") && strings.Contains(res.Text, "rota round assign ")
			if got != tc.want {
				t.Errorf("nudge present = %v, want %v\n%s", got, tc.want, res.Text)
			}
			if _, has := res.Data.(*jsonx.Object).Get("assignNudge"); has != tc.want {
				t.Errorf("assignNudge key present = %v, want %v", has, tc.want)
			}
		})
	}
}

func TestTrainNudgeAssignsFreeSlotFirst(t *testing.T) {
	for _, withCandidate := range []bool{true, false} {
		t.Run(map[bool]string{true: "ready candidate", false: "no candidate"}[withCandidate], func(t *testing.T) {
			dir := assignNudgeRepo(t, withCandidate)
			write(t, filepath.Join(dir, ".rota", "config.json"), `{"round":{"roster":["w1","w2"],"slots":2,"scope":"open"}}`)
			// The train wake has no returning slot: w1 was already free.
			write(t, filepath.Join(dir, ".rota", "workers.json"), `{"slots":[{"name":"w1","state":"idle"},{"name":"w2","state":"busy"}]}`)
			addRegistryRows(t, dir, map[string][]map[string]any{"prs": {
				{"pr": "#11", "branch": "a/1-x", "base": "main"},
				{"pr": "#12", "branch": "b/2-x", "base": "main"},
			}})
			for _, branch := range []string{"a/1-x", "b/2-x"} {
				gittest.Run(t, dir, "branch", branch)
				if code, _, errOut := rotaIn(t, dir, "verdict", "add", branch, "--kind", "review-spec", "--verdict", "PASS"); code != 0 {
					t.Fatalf("review: %d %s", code, errOut)
				}
			}
			c := &Ctx{Deps: testDeps(), ctx: context.Background()}
			cd, err := rotastatetest.CommonDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			res := watchResult(c, dir, cd, roundlease.DefaultEnv(), roundwatch.Result{Reason: roundwatch.ReasonChange}, nil, "")
			trainAt := strings.Index(res.Text, "rota worker train 11 12 --base main")
			if trainAt < 0 {
				t.Fatalf("missing train: %s", res.Text)
			}
			assignAt := strings.Index(res.Text, "assign first: rota round assign T01 --agent w1")
			if withCandidate && (assignAt < 0 || assignAt > trainAt) {
				t.Errorf("assignment must precede train: %s", res.Text)
			}
			if !withCandidate && strings.Contains(res.Text, "assign first:") {
				t.Errorf("unexpected assignment: %s", res.Text)
			}
			rows, _ := res.Data.(*jsonx.Object).Get("trainNudge")
			row := rows.([]any)[0].(*jsonx.Object)
			assignment, has := row.Get("assignFirst")
			if has != withCandidate {
				t.Fatalf("JSON assignFirst present = %v, want %v", has, withCandidate)
			}
			if has {
				cmd, _ := assignment.(*jsonx.Object).Get("command")
				if cmd != "rota round assign T01 --agent w1" {
					t.Errorf("JSON assignment command = %v", cmd)
				}
			} else if res.Text != "nudge\ttrain\t2 reviewed PRs wait on main (#11 #12)\trota worker train 11 12 --base main" {
				t.Errorf("no-candidate nudge changed: %s", res.Text)
			}
		})
	}
}

// AC-1 (#1077): round wait carries the same assign nudge as the watch, in text
// and in data, and only while a candidate is ready.
func TestRoundWaitNudgesToAssignBeforeGating(t *testing.T) {
	for _, tc := range []struct {
		name          string
		withCandidate bool
		json          bool
		want          bool
	}{
		{"candidate left, text", true, false, true},
		{"candidate left, json", true, true, true},
		{"no candidates, text", false, false, false},
		{"no candidates, json", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps := testDeps()
			dir := workerProject(t, `{}`)
			rotaInWith(t, deps, dir, "worker", "pool", "init", "--slots", "1", "--base", "main")
			useHost(deps, &doneHost{cliHost: cliHost{inSession: true}})
			for f, c := range map[string]string{"BACKLOG.md": "# TODO\n\n## Tasks\n\n## Completed\n", "counters.json": "{}\n"} {
				if err := os.WriteFile(filepath.Join(dir, ".rota", f), []byte(c), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.withCandidate {
				floorMinor(t, dir)
				body := writeBody(t, t.TempDir(), `{"findings":[{"severity":"minor","title":"rename the helper"}]}`)
				if code, _, errOut := rotaIn(t, dir, "verdict", "add", "--kind", "review-quality", "--verdict", "FAIL", "--body-file", body, "--file-deferred", "--pr", "#77"); code != 0 {
					t.Fatalf("file candidate: %d %s", code, errOut)
				}
			}
			brief := filepath.Join(t.TempDir(), "b.md")
			os.WriteFile(brief, []byte("hello\n"), 0o644)
			if code, _, _ := rotaInWith(t, deps, dir, "worker", "dispatch", "w1", "--body-file", brief); code != 0 {
				t.Fatalf("dispatch: %d", code)
			}
			if tc.json {
				code, out, _ := rotaInWith(t, deps, dir, "round", "wait", "--settle", "0", "--json")
				d := data(t, out)
				if code != 0 || d["slot"] != "w1" {
					t.Fatalf("wait: %d %v", code, d)
				}
				if _, has := d["assignNudge"]; has != tc.want {
					t.Errorf("assignNudge key present = %v, want %v: %v", has, tc.want, d)
				}
				return
			}
			_, text, _ := rotaInWith(t, deps, dir, "round", "wait", "--settle", "0")
			if got := strings.Contains(text, "assign before you gate") && strings.Contains(text, "rota round assign "); got != tc.want {
				t.Errorf("text nudge = %v, want %v\n%s", got, tc.want, text)
			}
		})
	}
}

// doneHost's pane ends on the worker's done sentinel with a PR URL.
type doneHost struct{ cliHost }

func (h *doneHost) Capture(context.Context, string, string, int) string {
	return "ROTA-DONE w1 https://github.com/o/r/pull/11\n"
}

func (h *doneHost) Capabilities() host.Capabilities { return host.CapabilitiesOf(h) }
