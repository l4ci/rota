package round

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

var bg = context.Background()

func sh(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// newRepo is a project root on main with one worktree per entry of branches
// (name -> branch); a branch with "+" suffix in the map value gets a commit.
func newRepo(t *testing.T, branches map[string]string, ahead ...string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sh(t, root, "init", "-q", "-b", "main", ".")
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "seed"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	sh(t, root, "add", "seed")
	sh(t, root, "commit", "-q", "-m", "seed")
	for name, br := range branches {
		wt := filepath.Join(root, ".worktrees", name)
		sh(t, root, "worktree", "add", "-q", "-b", br, wt, "main")
	}
	for _, name := range ahead {
		wt := filepath.Join(root, ".worktrees", name)
		if err := os.WriteFile(filepath.Join(wt, "work"), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		sh(t, wt, "add", "work")
		sh(t, wt, "commit", "-q", "-m", "work")
	}
	return root
}

func writeRegistry(t *testing.T, root string, slots ...*jsonx.Object) {
	t.Helper()
	err := worker.UpdateDoc(root, func(doc *jsonx.Object) {
		var l []any
		for _, s := range slots {
			l = append(l, s)
		}
		doc.Set("slots", l)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func slot(root, name, branch string, mod func(*jsonx.Object)) *jsonx.Object {
	s := worker.NewSlot(name, branch, filepath.Join(root, ".worktrees", name), "main", "").Raw()
	if mod != nil {
		mod(s)
	}
	return s
}

type fakeForge struct {
	prs      []tracker.PR
	states   map[int]string
	labelled []int
	prsErr   error
	added    []int
	addErr   error
}

func (f *fakeForge) OpenPRs(context.Context) ([]tracker.PR, error) { return f.prs, f.prsErr }
func (f *fakeForge) ClosedNumbers(body string) []int {
	var out []int
	for _, m := range regexp.MustCompile(`(?i)closes #(\d+)`).FindAllStringSubmatch(body, -1) {
		n, _ := strconv.Atoi(m[1])
		out = append(out, n)
	}
	return out
}
func (f *fakeForge) PRState(_ context.Context, n int) (string, error) {
	return f.states[n], nil
}
func (f *fakeForge) List(context.Context, tracker.ListFilter) ([]tracker.Issue, error) {
	var out []tracker.Issue
	for _, n := range f.labelled {
		out = append(out, tracker.Issue{Number: n})
	}
	return out, nil
}
func (f *fakeForge) AddLabels(_ context.Context, n int, _ []string, _ bool) error {
	f.added = append(f.added, n)
	return f.addErr
}

func env(agents []host.Agent, forge Forge) Env {
	e := Env{Git: worker.ExecGit, Base: "main", Forge: forge, HostName: "herdr"}
	if agents != nil {
		e.Snapshot = func(context.Context) ([]host.Agent, error) { return agents, nil }
	}
	return e
}

func kinds(fs []Finding) map[string][]string {
	m := map[string][]string{}
	for _, f := range fs {
		m[f.Slot] = append(m[f.Slot], f.Kind)
	}
	for _, v := range m {
		sort.Strings(v)
	}
	return m
}

// fixture builds one slot per drift kind.
func fixture(t *testing.T) (string, Env, *fakeForge) {
	root := newRepo(t, map[string]string{
		"ben": "park/ben", "dana": "dana/58-foo", "kit": "kit/56-bar", "finn": "finn/57-baz", "nia": "nia/60-new",
	}, "dana", "kit", "finn", "nia")
	writeRegistry(t, root,
		slot(root, "ben", "park/ben", func(s *jsonx.Object) { s.Set("handle", "w1:t1") }),
		slot(root, "kit", "kit/56-bar", func(s *jsonx.Object) { s.Set("handle", "w1:t3"); s.Set("task", "56") }),
		slot(root, "finn", "finn/57-baz", func(s *jsonx.Object) {
			s.Set("handle", "w1:t4")
			s.Set("pr", "https://github.com/o/r/pull/9")
		}),
		slot(root, "nia", "nia/60-new", func(s *jsonx.Object) { // healthy control
			s.Set("handle", "w1:t5")
			s.Set("pr", "https://github.com/o/r/pull/13")
		}),
	)
	forge := &fakeForge{
		prs:      []tracker.PR{{Number: 12, Branch: "kit/56-bar", URL: "https://github.com/o/r/pull/12"}, {Number: 13, Branch: "nia/60-new", URL: "https://github.com/o/r/pull/13"}},
		states:   map[int]string{9: "merged"},
		labelled: []int{56, 57, 60, 99},
	}
	agents := []host.Agent{
		{Tab: "w1:t3", Name: "kit", Cwd: filepath.Join(root, ".worktrees", "kit"), Status: "working"},
		{Tab: "w1:t4", Name: "finn", Cwd: filepath.Join(root, ".worktrees", "finn"), Status: "done"},
		{Tab: "w1:t5", Name: "nia", Cwd: filepath.Join(root, ".worktrees", "nia"), Status: "idle"},
		{Tab: "w2:t1", Name: "dana", Cwd: filepath.Join(root, ".worktrees", "dana"), Status: "working"},
		{Tab: "w3:t1", Name: "ghost", Cwd: filepath.Join(root, ".worktrees", "ghost"), Status: "working"},
		{Tab: "w4:t1", Name: "orchestrator", Cwd: root, Status: "working"},
	}
	return root, env(agents, forge), forge
}

func TestEachDriftKind(t *testing.T) {
	root, e, _ := fixture(t)
	rep, err := e.Status(bg, root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"ben":   {DeadTab},
		"dana":  {BranchNoPR, LabelMissing, UnregisteredWorktree},
		"kit":   {PRUnrecorded},
		"finn":  {PRStale},
		"ghost": {UnclaimedTab},
		"":      {LabelOrphan},
	}
	if got := kinds(rep.Findings); !reflect.DeepEqual(got, want) {
		t.Errorf("findings\n got %v\nwant %v", got, want)
	}
	if len(rep.Unavailable) != 0 {
		t.Errorf("unavailable = %v", rep.Unavailable)
	}
}

func TestRowsFromWorktreesAndSnapshot(t *testing.T) {
	root, e, _ := fixture(t)
	rep, _ := e.Status(bg, root)
	rows := map[string]Row{}
	for _, r := range rep.Rows {
		rows[r.Name] = r
	}
	d := rows["dana"]
	if d.Registered || d.Issue != "58" || d.Branch != "dana/58-foo" || d.HostState != "working" || d.Tab != "w2:t1" {
		t.Errorf("dana (unregistered, matched by cwd) = %+v", d)
	}
	k := rows["kit"]
	if k.Issue != "56" || k.PR != "https://github.com/o/r/pull/12" || k.PRState != "open" || k.Agent != "kit" {
		t.Errorf("kit = %+v", k)
	}
	if b := rows["ben"]; b.Issue != "" || b.HostState != "" || !reflect.DeepEqual(b.Drift, []string{DeadTab}) {
		t.Errorf("ben = %+v", b)
	}
	if f := rows["finn"]; f.PRState != "merged" {
		t.Errorf("finn prState = %q", f.PRState)
	}
	if _, ok := rows["orchestrator"]; ok {
		t.Error("an agent outside .worktrees/ must not be a row")
	}
}

func TestEmptyRegistryDerivesRowsFromWorktrees(t *testing.T) {
	root := newRepo(t, map[string]string{"ben": "park/ben", "dana": "dana/58-foo"})
	rep, err := env([]host.Agent{}, &fakeForge{}).Status(bg, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Rows) != 2 || rep.Rows[0].Name != "ben" || rep.Rows[1].Issue != "58" {
		t.Fatalf("rows = %+v", rep.Rows)
	}
	for _, r := range rep.Rows {
		if r.Registered {
			t.Errorf("%s registered from an empty registry", r.Name)
		}
	}
}

func TestUnavailableSourcesSkipTheirKinds(t *testing.T) {
	root, _, _ := fixture(t)
	rep, err := Env{Git: worker.ExecGit, Base: "main"}.Status(bg, root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rep.Unavailable, []string{SourceHost, SourceForge}) {
		t.Errorf("unavailable = %v", rep.Unavailable)
	}
	for _, f := range rep.Findings {
		if f.Kind != UnregisteredWorktree {
			t.Errorf("finding %s needs a source that is down", f.Kind)
		}
	}
	e := env([]host.Agent{}, &fakeForge{prsErr: errors.New("rate limited")})
	rep, _ = e.Status(bg, root)
	if !reflect.DeepEqual(rep.Unavailable, []string{SourceForge}) {
		t.Errorf("forge error: unavailable = %v", rep.Unavailable)
	}
}

func TestReconcileReportsWithoutApply(t *testing.T) {
	root, e, forge := fixture(t)
	before, _ := os.ReadFile(worker.RegistryPath(root))
	out, err := e.Reconcile(bg, root, false)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(worker.RegistryPath(root))
	if string(before) != string(after) || len(forge.added) != 0 {
		t.Error("reconcile without --apply wrote")
	}
	if len(out.Repaired) != 0 || len(out.Drift) != len(out.Report.Findings) || out.Clean() {
		t.Errorf("outcome = %+v", out)
	}
}

func TestReconcileApplyRepairsOnlyTheSafeKinds(t *testing.T) {
	root, e, forge := fixture(t)
	out, err := e.Reconcile(bg, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := kinds(out.Repaired), map[string][]string{
		"ben": {DeadTab}, "dana": {LabelMissing, UnregisteredWorktree}, "kit": {PRUnrecorded},
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("repaired\n got %v\nwant %v", got, want)
	}
	if got, want := kinds(out.Drift), map[string][]string{
		"dana": {BranchNoPR}, "finn": {PRStale}, "ghost": {UnclaimedTab}, "": {LabelOrphan},
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("remaining\n got %v\nwant %v", got, want)
	}
	if !reflect.DeepEqual(forge.added, []int{58}) {
		t.Errorf("labels added = %v", forge.added)
	}
	reg := worker.LoadRegistry(root)
	if b := reg.Slot("ben"); b.State() != "dead" || b.Handle() != "" {
		t.Errorf("ben = %v", b)
	}
	if k := reg.Slot("kit"); k.PR() != "https://github.com/o/r/pull/12" {
		t.Errorf("kit pr = %q", k.PR())
	}
	d := reg.Slot("dana")
	if d == nil || d.Task() != "58" || d.Branch() != "dana/58-foo" || d.Handle() != "w2:t1" {
		t.Errorf("dana = %v", d)
	}
	// A second pass finds the repaired kinds gone.
	again, _ := e.Reconcile(bg, root, false)
	for _, f := range again.Drift {
		switch f.Kind {
		case DeadTab, UnregisteredWorktree, PRUnrecorded:
			t.Errorf("%s still drifts after --apply", f.Kind)
		}
	}
}

func TestFailedRepairStaysInDrift(t *testing.T) {
	root, e, forge := fixture(t)
	forge.addErr = errors.New("boom")
	out, _ := e.Reconcile(bg, root, true)
	if got := kinds(out.Drift)["dana"]; !strings.Contains(strings.Join(got, ","), LabelMissing) {
		t.Errorf("label-missing left drift = %v", got)
	}
	if !strings.Contains(strings.Join(out.Report.Warnings, "\n"), "boom") {
		t.Errorf("warnings = %v", out.Report.Warnings)
	}
}

func TestIssueOfAndPRNumber(t *testing.T) {
	for _, c := range []struct{ task, branch, name, want string }{
		{"58", "x/1-y", "x", "58"}, {"#58", "", "x", "58"}, {"B07", "omar/58-a", "omar", "58"},
		{"", "park/omar", "omar", ""}, {"", "main", "omar", ""},
	} {
		if got := issueOf(c.task, c.branch, c.name); got != c.want {
			t.Errorf("issueOf(%q,%q) = %q, want %q", c.task, c.branch, got, c.want)
		}
	}
	for in, want := range map[string]int{"https://github.com/o/r/pull/9": 9, "#12": 12, "7": 7, "https://gitlab.com/o/r/-/merge_requests/3": 3, "omar/58": 0, "omar/58-x": 0} {
		if n, _ := prNumber(in); n != want {
			t.Errorf("prNumber(%q) = %d, want %d", in, n, want)
		}
	}
}

func TestOpenEscalationsAreReported(t *testing.T) {
	root, e, _ := fixture(t)
	esc := func(id, slot, status, deadline string) *jsonx.Object {
		o := jsonx.NewObject()
		o.Set("id", id)
		o.Set("kind", "issue")
		o.Set("number", 56)
		o.Set("slot", slot)
		o.Set("title", "q "+id)
		o.Set("commentId", "1")
		o.Set("sentAt", "2026-10-03T10:00:00Z")
		if deadline != "" {
			o.Set("deadline", deadline)
		}
		o.Set("notified", true)
		o.Set("status", status)
		return o
	}
	if err := worker.Update(root, jsonx.NewObject(), func(doc *jsonx.Object) {
		doc.Set("escalations", []any{
			esc("e1", "kit", "answered", ""),
			esc("e2", "kit", "pending", "2026-10-03T11:00:00Z"),
			esc("e3", "", "pending", ""),
		})
	}); err != nil {
		t.Fatal(err)
	}
	e.Now = func() time.Time { return time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC) }
	out, err := e.Reconcile(bg, root, false)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range out.Report.Escalations {
		got = append(got, r.Entry.ID+":"+r.Status)
	}
	if want := []string{"e2:timed-out", "e3:pending"}; !reflect.DeepEqual(got, want) {
		t.Errorf("escalations = %v, want %v", got, want)
	}
	for _, r := range out.Report.Rows {
		want := []string(nil)
		if r.Name == "kit" {
			want = []string{"e2"}
		}
		if !reflect.DeepEqual(r.Escalations, want) {
			t.Errorf("row %s escalations = %v, want %v", r.Name, r.Escalations, want)
		}
	}
	if kinds(out.Drift)["kit"] == nil || len(kinds(out.Drift)) != 6 {
		t.Errorf("escalations must not add drift: %v", kinds(out.Drift))
	}
}

// A parked slot owns no agent: every agent still running in its worktree is
// reported unclaimed, not just the first one (#212).
func TestParkedSlotAgentsAreAllUnclaimed(t *testing.T) {
	root := newRepo(t, map[string]string{"ben": "park/ben"})
	writeRegistry(t, root, slot(root, "ben", "park/ben", nil))
	wt := filepath.Join(root, ".worktrees", "ben")
	e := env([]host.Agent{
		{Tab: "w1:t5", Name: "rota-ben-t5", Cwd: wt, Status: "idle"},
		{Tab: "w1:t6", Name: "rota-ben-t6", Cwd: wt, Status: "idle"},
	}, &fakeForge{})
	rep, err := e.Status(bg, root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{"rota-ben-t5": {UnclaimedTab}, "rota-ben-t6": {UnclaimedTab}}
	if got := kinds(rep.Findings); !reflect.DeepEqual(got, want) {
		t.Errorf("findings\n got %v\nwant %v", got, want)
	}
}

// rawSlot edits one slot's record as JSON, for tests that stage a state the
// verbs would not write.
func rawSlot(root, name string, fn func(o *jsonx.Object)) error {
	return editSlot(root, name, func(s *worker.Slot) error { fn(s.Raw()); return nil })
}
