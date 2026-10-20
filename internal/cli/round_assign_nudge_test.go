package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotastate"
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
	cd, err := rotastate.CommonDir(dir)
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
