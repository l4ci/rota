package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/verdict"
)

func TestTrainNudgeNamesTwoReviewedPRsOnOneBase(t *testing.T) {
	ns := trainNudges([]waitingPR{
		{PR: "https://example.test/o/r/pull/12", Base: "main", Reviewed: true},
		{PR: "#9", Base: "main", Reviewed: true},
		{PR: "#30", Base: "main", Reviewed: false},
		{PR: "#31", Base: "cycle", Reviewed: true},
	})
	if len(ns) != 1 {
		t.Fatalf("nudges = %+v, want one (main)", ns)
	}
	if got, want := ns[0].Command(), "rota worker train 9 12 --base main"; got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
	if txt := ns[0].Text(); !strings.Contains(txt, "#9 #12") || !strings.Contains(txt, "rota worker train") {
		t.Errorf("text names neither PRs nor command: %q", txt)
	}
}

func TestTrainNudgeAbsentWithOneReviewedPR(t *testing.T) {
	for name, w := range map[string][]waitingPR{
		"one waiting":            {{PR: "#9", Base: "main", Reviewed: true}},
		"second unreviewed":      {{PR: "#9", Base: "main", Reviewed: true}, {PR: "#10", Base: "main"}},
		"two on different bases": {{PR: "#9", Base: "main", Reviewed: true}, {PR: "#10", Base: "cycle", Reviewed: true}},
		"none":                   nil,
	} {
		if ns := trainNudges(w); len(ns) != 0 {
			t.Errorf("%s: nudges = %+v, want none", name, ns)
		}
	}
}

func TestTrainNudgeGroupsEmptyBaseWithDefault(t *testing.T) {
	w := fillDefaultBase([]waitingPR{
		{PR: "#9", Base: "", Reviewed: true},
		{PR: "#10", Base: "main", Reviewed: true},
	}, "main")
	ns := trainNudges(w)
	if len(ns) != 1 || ns[0].Base != "main" || len(ns[0].PRs) != 2 {
		t.Fatalf("nudges = %+v, want one train of two on main", ns)
	}
}

// nudgeProject is a worker project on main with a branch per name, each at the
// current head. A branch in reviewed carries a PASS review verdict at that head.
func nudgeProject(t *testing.T, branches, reviewed []string) string {
	t.Helper()
	dir := workerProject(t, `{}`)
	var sha string
	for _, b := range branches {
		c := exec.Command("git", "branch", b)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git branch %s: %v %s", b, err, out)
		}
	}
	c := exec.Command("git", "rev-parse", "HEAD")
	c.Dir = dir
	out, err := c.Output()
	if err != nil {
		t.Fatal(err)
	}
	sha = strings.TrimSpace(string(out))
	for _, b := range reviewed {
		rec := verdict.NewRecord(verdict.ReviewQuality, "PASS", sha, verdict.Body{}, time.Now())
		if _, err := verdict.AddBranch(dir, verdict.BranchKey("", b), rec); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// addRegistryRows merges rows into .rota/workers.json, creating it when absent.
func addRegistryRows(t *testing.T, dir string, rows map[string][]map[string]any) {
	t.Helper()
	path := filepath.Join(dir, ".rota", "workers.json")
	reg := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &reg); err != nil {
			t.Fatal(err)
		}
	}
	for key, list := range rows {
		cur, _ := reg[key].([]any)
		for _, r := range list {
			cur = append(cur, r)
		}
		reg[key] = cur
	}
	b, _ := json.Marshal(reg)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWaitingPRsReadsDoneSlotsAndQueue(t *testing.T) {
	dir := nudgeProject(t, []string{"a/1-x", "b/2-y", "c/3-z", "d/4-w", "e/5-v"}, []string{"a/1-x", "b/2-y", "d/4-w"})
	addRegistryRows(t, dir, map[string][]map[string]any{
		"slots": {
			{"name": "a", "state": "done", "branch": "a/1-x", "pr": "https://example.test/o/r/pull/11"},
			{"name": "c", "state": "done", "branch": "c/3-z", "pr": "#13"},
			{"name": "d", "state": "busy", "branch": "d/4-w", "pr": "#14"},
			{"name": "e", "state": "done", "branch": "e/5-v"},
		},
		"prs": {
			{"branch": "b/2-y", "pr": "#12", "base": "cycle"},
			{"branch": "gone/9-q", "pr": "#19", "base": "main"},
			{"branch": "x/8-p"},
		},
	})
	got := waitingPRs(&Ctx{Deps: testDeps(), ctx: context.Background()}, dir)
	want := []waitingPR{
		{PR: "https://example.test/o/r/pull/11", Base: "main", Reviewed: true},
		{PR: "#13", Base: "main", Reviewed: false},
		{PR: "#12", Base: "cycle", Reviewed: true},
		{PR: "#19", Base: "main", Reviewed: false},
	}
	if len(got) != len(want) {
		t.Fatalf("waiting = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("waiting[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestWaitingPRsEmptyWithoutRegistry(t *testing.T) {
	dir := nudgeProject(t, nil, nil)
	if got := waitingPRs(&Ctx{Deps: testDeps(), ctx: context.Background()}, dir); len(got) != 0 {
		t.Fatalf("waiting = %+v, want none", got)
	}
}

// TestRoundWaitCarriesTheTrainNudge: wait prints the nudge line and sets
// trainNudge in its data only when two reviewed PRs share a base.
func TestRoundWaitCarriesTheTrainNudge(t *testing.T) {
	setup := func(reviewed []string) (*Deps, string) {
		deps := testDeps()
		dir := nudgeProject(t, []string{"a/1-x", "b/2-y"}, reviewed)
		rotaInWith(t, deps, dir, "worker", "pool", "init", "--slots", "1", "--base", "main")
		useHost(deps, &cliHost{inSession: true})
		brief := filepath.Join(t.TempDir(), "b.md")
		os.WriteFile(brief, []byte("hello\n"), 0o644)
		if code, _, _ := rotaInWith(t, deps, dir, "worker", "dispatch", "w1", "--body-file", brief); code != 0 {
			t.Fatalf("dispatch: %d", code)
		}
		addRegistryRows(t, dir, map[string][]map[string]any{"prs": {
			{"branch": "a/1-x", "pr": "#21", "base": "main"},
			{"branch": "b/2-y", "pr": "#22", "base": "main"},
		}})
		return deps, dir
	}

	deps, dir := setup([]string{"a/1-x", "b/2-y"})
	code, out, _ := rotaInWith(t, deps, dir, "round", "wait", "--settle", "0", "--json")
	d := data(t, out)
	rows, _ := d["trainNudge"].([]any)
	if code != 0 || len(rows) != 1 {
		t.Fatalf("wait: %d %v, want one trainNudge row", code, d)
	}
	if cmd := rows[0].(map[string]any)["command"]; cmd != "rota worker train 21 22 --base main" {
		t.Errorf("command = %v", cmd)
	}
	deps, dir = setup([]string{"a/1-x", "b/2-y"})
	_, text, _ := rotaInWith(t, deps, dir, "round", "wait", "--settle", "0")
	if !strings.Contains(text, "nudge\ttrain\t2 reviewed PRs wait on main (#21 #22)") {
		t.Errorf("text lacks the nudge line:\n%s", text)
	}

	deps, dir = setup([]string{"a/1-x"})
	code, out, _ = rotaInWith(t, deps, dir, "round", "wait", "--settle", "0", "--json")
	d = data(t, out)
	if _, ok := d["trainNudge"]; code != 0 || ok {
		t.Fatalf("one unreviewed PR: %d %v, want no trainNudge", code, d)
	}
}
