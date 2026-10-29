package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotastatetest"
	"github.com/l4ci/rota/internal/roundtick"
	"github.com/l4ci/rota/internal/roundwatch"
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

// nudgeRepo is a repo with n done slots, each on its own branch at HEAD with a
// review-spec verdict recorded there, so every PR counts as reviewed.
func nudgeRepo(t *testing.T, n int) string {
	t.Helper()
	root := gitRepo(t)
	head, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.TrimSpace(string(head))
	var slots []string
	for i := 1; i <= n; i++ {
		branch := fmt.Sprintf("w%d/%d-x", i, i)
		if out, err := exec.Command("git", "-C", root, "branch", branch).CombinedOutput(); err != nil {
			t.Fatalf("git branch: %v %s", err, out)
		}
		rec := verdict.NewRecord(verdict.ReviewSpec, verdict.Pass, sha, verdict.Body{}, time.Now())
		if _, err := verdict.AddBranch(root, verdict.BranchKey("", branch), rec); err != nil {
			t.Fatal(err)
		}
		slots = append(slots, fmt.Sprintf(`{"name":"w%d","branch":%q,"state":"done","base":"feat/x","pr":"https://github.com/o/r/pull/%d"}`, i, branch, 10+i))
	}
	write(t, filepath.Join(root, ".rota", "workers.json"), `{"slots":[`+strings.Join(slots, ",")+`]}`)
	return root
}

// TestWatchResultNudgesAfterATickWithoutMergeItems: an autopilot tick that
// left no merge item still shows the train nudge when two reviewed PRs wait on
// one base, and shows none with one.
func TestWatchResultNudgesAfterATickWithoutMergeItems(t *testing.T) {
	for _, tc := range []struct {
		waiting int
		want    bool
	}{{2, true}, {1, false}} {
		root := nudgeRepo(t, tc.waiting)
		c := &Ctx{Deps: testDeps(), ctx: context.Background()}
		cd, err := rotastatetest.CommonDir(root)
		if err != nil {
			t.Fatal(err)
		}
		tick := roundtick.Result{}
		res := watchResult(c, root, cd, c.deps().LeaseEnv(), roundwatch.Result{Reason: roundwatch.ReasonChange}, &tick, "")
		got := strings.Contains(res.Text, "nudge\ttrain\t2 reviewed PRs wait on feat/x (#11 #12)\trota worker train 11 12 --base feat/x")
		if got != tc.want {
			t.Errorf("%d waiting: nudge present = %v, want %v\n%s", tc.waiting, got, tc.want, res.Text)
		}
		if _, has := res.Data.(*jsonx.Object).Get("trainNudge"); has != tc.want {
			t.Errorf("%d waiting: trainNudge key present = %v, want %v", tc.waiting, has, tc.want)
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
		rec := verdict.NewRecord(verdict.ReviewQuality, "PASS", sha, verdict.Body{}, nudgeReviewedAt)
		if _, err := verdict.AddBranch(dir, verdict.BranchKey("", b), rec); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

var nudgeReviewedAt = time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC)

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
		{PR: "https://example.test/o/r/pull/11", Base: "main", Reviewed: true, ReadySince: nudgeReviewedAt},
		{PR: "#13", Base: "main", Reviewed: false},
		{PR: "#12", Base: "cycle", Reviewed: true, ReadySince: nudgeReviewedAt},
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

// TestWaitingPRsFillsEmptyBaseFromConfiguredBase: the wiring from
// git.baseBranch through resolveBase to the filled base, with a non-main base.
// A done slot (no base) and a queued PR (explicit base) on it group into one train.
func TestWaitingPRsFillsEmptyBaseFromConfiguredBase(t *testing.T) {
	dir := nudgeProject(t, []string{"feat/x", "a/1-x", "b/2-y"}, []string{"a/1-x", "b/2-y"})
	os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{"git":{"baseBranch":"feat/x"}}`), 0o644)
	addRegistryRows(t, dir, map[string][]map[string]any{
		"slots": {{"name": "a", "state": "done", "branch": "a/1-x", "pr": "#11"}},
		"prs":   {{"branch": "b/2-y", "pr": "#12", "base": "feat/x"}},
	})
	got := waitingPRs(&Ctx{Deps: testDeps(), ctx: context.Background()}, dir)
	want := []waitingPR{
		{PR: "#11", Base: "feat/x", Reviewed: true, ReadySince: nudgeReviewedAt},
		{PR: "#12", Base: "feat/x", Reviewed: true, ReadySince: nudgeReviewedAt},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("waiting = %+v, want %+v", got, want)
	}
	if ns := trainNudges(got); len(ns) != 1 || ns[0].Base != "feat/x" || len(ns[0].PRs) != 2 {
		t.Fatalf("nudges = %+v, want one train of two on feat/x", ns)
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

func TestTrainBlockedOnFailNotConcerns(t *testing.T) {
	recWith := func(kind, v string, f ...verdict.Finding) verdict.Record {
		return verdict.Record{Kind: kind, Verdict: v, Sha: "bbbbbbb", Findings: f}
	}
	rec := func(kind, v string) verdict.Record { return verdict.Record{Kind: kind, Verdict: v, Sha: "bbbbbbb"} }
	cross := func(v string, f ...verdict.Finding) verdict.Record {
		return verdict.Record{Kind: verdict.ReviewCross, Verdict: v, Sha: "bbbbbbb", Harness: "codex", Model: "m", Findings: f}
	}
	for name, tc := range map[string]struct {
		recs []verdict.Record
		want bool
	}{
		"pass":                           {[]verdict.Record{rec(verdict.ReviewSpec, verdict.Pass)}, false},
		"concerns":                       {[]verdict.Record{rec(verdict.ReviewQuality, verdict.Concerns)}, false},
		"fail":                           {[]verdict.Record{rec(verdict.ReviewSpec, verdict.Fail)}, true},
		"spec fail after pass":           {[]verdict.Record{rec(verdict.ReviewQuality, verdict.Pass), rec(verdict.ReviewSpec, verdict.Fail)}, true},
		"none":                           {nil, false},
		"concerns with blocking finding": {[]verdict.Record{recWith(verdict.ReviewQuality, verdict.Concerns, verdict.Finding{Severity: "major", Title: "x"})}, true},
		"concerns with minor finding":    {[]verdict.Record{recWith(verdict.ReviewQuality, verdict.Concerns, verdict.Finding{Severity: "minor", Title: "x"})}, false},
		"cross pass":                     {[]verdict.Record{cross(verdict.Pass)}, false},
		"cross concerns, no blocker":     {[]verdict.Record{cross(verdict.Concerns, verdict.Finding{Severity: "minor", Title: "x"})}, false},
		"cross concerns with blocker":    {[]verdict.Record{cross(verdict.Concerns, verdict.Finding{Severity: "major", Title: "x"})}, true},
		"cross blocker then newer clean": {[]verdict.Record{cross(verdict.Concerns, verdict.Finding{Severity: "major", Title: "x"}), cross(verdict.Pass)}, false},
		"blocking finding then newer clean review": {[]verdict.Record{
			recWith(verdict.ReviewQuality, verdict.Concerns, verdict.Finding{Severity: "major", Title: "x"}),
			rec(verdict.ReviewSpec, verdict.Pass)}, false},
	} {
		root := t.TempDir()
		for _, r := range tc.recs {
			if _, err := verdict.AddBranch(root, "w1", r); err != nil {
				t.Fatal(err)
			}
		}
		if got := trainBlocked(verdict.Load(root), "w1"); got != tc.want {
			t.Errorf("%s: blocked %v want %v", name, got, tc.want)
		}
	}
}

// openPRForge is a forge whose open PR list is fixed.
type openPRForge struct {
	worker.Forge
	open []int
	err  error
}

func (f openPRForge) OpenPRs(context.Context) ([]tracker.PR, error) {
	var out []tracker.PR
	for _, n := range f.open {
		out = append(out, tracker.PR{Number: n})
	}
	return out, f.err
}

// TestWaitingPRsDropsMergedPRs: a PR the forge no longer lists as open (merged
// by gate or train) leaves the waiting list, so the nudge never names it. A
// forge that cannot answer filters nothing: the nudge is advice.
func TestWaitingPRsDropsMergedPRs(t *testing.T) {
	for name, tc := range map[string]struct {
		forge openPRForge
		want  []string
	}{
		"merged PR drops":       {openPRForge{open: []int{12}}, []string{"#12"}},
		"forge error keeps all": {openPRForge{err: errors.New("offline")}, []string{"#11", "#12"}},
	} {
		dir := nudgeProject(t, []string{"a/1-x", "b/2-y"}, []string{"a/1-x", "b/2-y"})
		if out, err := exec.Command("git", "-C", dir, "remote", "add", "origin", "https://github.com/o/r.git").CombinedOutput(); err != nil {
			t.Fatalf("git remote add: %v %s", err, out)
		}
		addRegistryRows(t, dir, map[string][]map[string]any{
			"slots": {{"name": "a", "state": "done", "branch": "a/1-x", "pr": "#11"}},
			"prs":   {{"branch": "b/2-y", "pr": "#12", "base": "main"}},
		})
		deps := testDeps()
		deps.WorkerEnv = func() worker.Env {
			return worker.Env{Forge: func(string, string, tracker.Settings) (worker.Forge, error) { return tc.forge, nil }}
		}
		var got []string
		for _, w := range waitingPRs(&Ctx{Deps: deps, ctx: context.Background()}, dir) {
			got = append(got, w.PR)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: waiting = %v, want %v", name, got, tc.want)
		}
	}
}

// TestWaitingPRsLeavesOutABouncedPR: a PR the gate sent back at its current head
// is not reviewed, though its verdict is on record; a push (new head) clears it.
func TestWaitingPRsLeavesOutABouncedPR(t *testing.T) {
	dir := nudgeProject(t, []string{"a/1-x", "b/2-y"}, []string{"a/1-x", "b/2-y"})
	head, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	addRegistryRows(t, dir, map[string][]map[string]any{
		"slots": {{"name": "a", "state": "done", "issue": "1", "branch": "a/1-x", "pr": "#11"}},
		"prs":   {{"issue": "2", "branch": "b/2-y", "pr": "#12", "base": "main"}},
	})
	for _, issue := range []string{"1", "2"} {
		if _, err := recordBounceVia(worker.Env{}, dir, issue, "", strings.TrimSpace(string(head))); err != nil {
			t.Fatal(err)
		}
	}
	got := waitingPRs(&Ctx{Deps: testDeps(), ctx: context.Background()}, dir)
	for _, w := range got {
		if w.Reviewed {
			t.Errorf("%s reviewed = true, want false (bounced at head)", w.PR)
		}
	}
	if len(got) != 2 {
		t.Fatalf("waiting = %+v, want two PRs", got)
	}
}

// TestWaitingPRsReviewedByHead: a bounce recorded at an older head no longer
// holds the PR back once it is pushed to, and a blocking verdict recorded at an
// older head leaves the PR unreviewed, not blocked-and-reviewed.
func TestWaitingPRsReviewedByHead(t *testing.T) {
	const oldSha = "0123456789abcdef0123456789abcdef01234567"
	for name, tc := range map[string]struct {
		reviewed []string
		setup    func(t *testing.T, dir string)
		want     bool
	}{
		"push clears the bounce": {
			reviewed: []string{"a/1-x"},
			setup: func(t *testing.T, dir string) {
				if _, err := recordBounceVia(worker.Env{}, dir, "1", "", oldSha); err != nil {
					t.Fatal(err)
				}
			},
			want: true,
		},
		"blocking verdict at an older head": {
			setup: func(t *testing.T, dir string) {
				rec := verdict.NewRecord(verdict.ReviewQuality, verdict.Concerns, oldSha,
					verdict.Body{Findings: []verdict.Finding{{Severity: "major", Title: "x"}}}, time.Now())
				if _, err := verdict.AddBranch(dir, verdict.BranchKey("", "a/1-x"), rec); err != nil {
					t.Fatal(err)
				}
			},
			want: false,
		},
	} {
		dir := nudgeProject(t, []string{"a/1-x"}, tc.reviewed)
		addRegistryRows(t, dir, map[string][]map[string]any{
			"slots": {{"name": "a", "state": "done", "issue": "1", "branch": "a/1-x", "pr": "#11"}},
		})
		tc.setup(t, dir)
		got := waitingPRs(&Ctx{Deps: testDeps(), ctx: context.Background()}, dir)
		if len(got) != 1 {
			t.Fatalf("%s: waiting = %+v, want one PR", name, got)
		}
		if got[0].Reviewed != tc.want {
			t.Errorf("%s: reviewed = %v, want %v", name, got[0].Reviewed, tc.want)
		}
	}
}
