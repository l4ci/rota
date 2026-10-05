package ship

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/proof"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/verdict"
)

func TestBody(t *testing.T) {
	corpus := "- **[F01]** Add thing. GH: #7\n"
	g := &fakeGit{out: map[string]string{
		"log --no-merges --format=%s main..feat": "Add thing [F01]\n\nTidy",
		"log --no-merges --format=%B main..feat": "Add thing [F01]\n\nTidy",
	}}
	got, err := Body(g, "main", "feat", func() (TitleOf, error) { return CorpusTitles(corpus), nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Summary\n\n- Add thing [F01]\n- Tidy\n", "## Items resolved\n\n- [F01]", "Closes #7\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("body lacks %q:\n%s", want, got)
		}
	}
}

func TestBodyEvidence(t *testing.T) {
	g := &fakeGit{out: map[string]string{
		"log --no-merges --format=%s main..feat": "Add thing [F01]",
		"log --no-merges --format=%B main..feat": "Add thing [F01]\n\nRefs [F02]",
	}}
	proofs := func(id string) ([]proof.Row, error) {
		if id == "F01" {
			return []proof.Row{{Check: "unit | tests", Result: "PASS", Evidence: "go test ok"}}, nil
		}
		return nil, nil
	}
	got, err := Body(g, "main", "feat", func() (TitleOf, error) { return CorpusTitles(""), nil }, proofs)
	if err != nil {
		t.Fatal(err)
	}
	want := "## Evidence\n\n**[F01]**\n\n| Check | Result | Evidence |\n| --- | --- | --- |\n| unit \\| tests | PASS | go test ok |\n"
	if !strings.Contains(got, want) || strings.Contains(got, "[F02]**") {
		t.Errorf("body:\n%s", got)
	}
	none, _ := Body(g, "main", "feat", func() (TitleOf, error) { return CorpusTitles(""), nil }, func(string) ([]proof.Row, error) { return nil, nil })
	if strings.Contains(none, "## Evidence") {
		t.Errorf("empty evidence section:\n%s", none)
	}
}

func TestBodyNoCommits(t *testing.T) {
	g := &fakeGit{out: map[string]string{
		"log --no-merges --format=%s main..feat": "",
		"log --no-merges --format=%B main..feat": "",
	}}
	_, err := Body(g, "main", "feat", func() (TitleOf, error) { t.Error("corpus read"); return CorpusTitles(""), nil }, nil)
	var nc *NoCommitsError
	if !errors.As(err, &nc) || nc.Branch != "feat" {
		t.Fatalf("err = %v", err)
	}
}

type fakeForge struct {
	needsBase bool
	spec      tracker.PRSpec
	url       string
	err       error
}

func (f *fakeForge) Provider() string  { return "github" }
func (f *fakeForge) PRNeedsBase() bool { return f.needsBase }
func (f *fakeForge) PRCreate(_ context.Context, s tracker.PRSpec) (string, error) {
	f.spec = s
	return f.url, f.err
}

func prPorts(g Git, f Forge) PRPorts {
	return PRPorts{
		Git: g, Forge: f,
		Closes:  func([]string) (string, error) { return "Closes #4", nil },
		Verdict: func(string) error { return nil },
		Base:    func() (string, error) { return "main", nil },
	}
}

func TestOpenPR(t *testing.T) {
	g := &fakeGit{out: map[string]string{"worktree list --porcelain": "", "push -u origin feat": ""}}
	f := &fakeForge{needsBase: true, url: "https://example.test/pull/12"}
	pr, err := OpenPR(context.Background(), prPorts(g, f), PRRequest{Branch: "feat", Title: "T", Body: "B", Items: []string{"F01"}})
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 12 || pr.Provider != "github" || pr.URL != f.url {
		t.Errorf("pr = %+v", pr)
	}
	if f.spec.Body != "B\n\nCloses #4" || f.spec.Base != "main" || f.spec.Head != "feat" {
		t.Errorf("spec = %+v", f.spec)
	}
}

// A blocked branch or a bad item stops OpenPR before anything is pushed.
func TestOpenPRStopsBeforePush(t *testing.T) {
	boom := errors.New("boom")
	for name, edit := range map[string]func(*PRPorts){
		"closes":  func(p *PRPorts) { p.Closes = func([]string) (string, error) { return "", boom } },
		"verdict": func(p *PRPorts) { p.Verdict = func(string) error { return boom } },
		"base":    func(p *PRPorts) { p.Base = func() (string, error) { return "", boom } },
	} {
		g := &fakeGit{out: map[string]string{"worktree list --porcelain": "", "push -u origin feat": ""}}
		p := prPorts(g, &fakeForge{needsBase: true})
		edit(&p)
		if _, err := OpenPR(context.Background(), p, PRRequest{Branch: "feat"}); !errors.Is(err, boom) {
			t.Errorf("%s: err = %v", name, err)
		}
		if len(g.calls) != 0 {
			t.Errorf("%s: git ran: %v", name, g.calls)
		}
	}
}

func TestOpenPRPushFails(t *testing.T) {
	g := &fakeGit{out: map[string]string{"worktree list --porcelain": ""}, fail: map[string]bool{"push -u origin feat": true}}
	_, err := OpenPR(context.Background(), prPorts(g, &fakeForge{}), PRRequest{Branch: "feat"})
	var ge *GitError
	if !errors.As(err, &ge) || !strings.Contains(ge.Msg, "git push -u origin feat failed: fatal: boom") {
		t.Fatalf("err = %v", err)
	}
}

func mergePorts(g Git) MergePorts {
	return MergePorts{Git: g, Verdict: func(string) error { return nil }, Approve: func() error { return nil }}
}

func mergeGit() *fakeGit {
	return &fakeGit{out: map[string]string{
		"worktree list --porcelain": "",
		"checkout -q main":          "",
		"merge --no-ff feat -m msg": "",
		"branch -d feat":            "",
		"log -1 --format=%h":        "abc1234",
	}}
}

func TestMergeBranch(t *testing.T) {
	sha, err := MergeBranch(mergePorts(mergeGit()), "feat", "main", "msg")
	if err != nil || sha != "abc1234" {
		t.Fatalf("sha %q err %v", sha, err)
	}
}

func TestMergeBranchRefusals(t *testing.T) {
	var ref *Refusal
	if _, err := MergeBranch(mergePorts(mergeGit()), "main", "main", "msg"); !errors.As(err, &ref) || ref.By != "base branch" {
		t.Errorf("base: %v", err)
	}
	g := mergeGit()
	g.fail = map[string]bool{"merge --no-ff feat -m msg": true}
	g.out["merge --no-ff feat -m msg"] = ""
	// A conflict aborts the merge and refuses.
	cg := &conflictGit{fakeGit: g}
	if _, err := MergeBranch(mergePorts(cg), "feat", "main", "msg"); !errors.As(err, &ref) || ref.By != "conflict" {
		t.Errorf("conflict: %v", err)
	}
	if !contains(cg.calls, "merge --abort") {
		t.Errorf("no abort: %v", cg.calls)
	}
}

type conflictGit struct{ *fakeGit }

func (c *conflictGit) Run(args ...string) (r git.Result, err error) {
	r, err = c.fakeGit.Run(args...)
	if strings.HasPrefix(strings.Join(args, " "), "merge --no-ff") {
		r.Stdout = "CONFLICT (content): Merge conflict in x\nAutomatic merge failed"
	}
	return r, err
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// The gates run before the branch is touched.
func TestMergeBranchGatesFirst(t *testing.T) {
	boom := errors.New("boom")
	g := mergeGit()
	p := mergePorts(g)
	p.Approve = func() error { return boom }
	if _, err := MergeBranch(p, "feat", "main", "msg"); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if len(g.calls) != 0 {
		t.Errorf("git ran: %v", g.calls)
	}
}

type fakeMerger struct {
	res backlog.MergeResult
	err error
}

func (f fakeMerger) MergePRGated(int, []string, backlog.MergeApprover) (backlog.MergeResult, error) {
	return f.res, f.err
}

func TestMergePR(t *testing.T) {
	m, err := MergePR(fakeMerger{res: backlog.MergeResult{SHA: "0123456789", Closed: []backlog.ItemRef{{ID: "F01", Type: ""}}}}, 3, nil, nil)
	if err != nil || m.SHA != "0123456" || len(m.Closed) != 1 {
		t.Fatalf("%+v %v", m, err)
	}
	var un *UnprovenError
	_, err = MergePR(fakeMerger{res: backlog.MergeResult{Unproven: []backlog.ItemRef{{ID: "F02"}}}}, 3, nil, nil)
	if !errors.As(err, &un) || len(un.IDs) != 1 || un.IDs[0] != "F02" {
		t.Errorf("unproven: %v", err)
	}
	var rf *PRMergeRefused
	_, err = MergePR(fakeMerger{err: &backlog.MergeFailedError{Err: errors.New("no")}}, 3, nil, nil)
	if !errors.As(err, &rf) {
		t.Errorf("refused: %v", err)
	}
	other := errors.New("other")
	if _, err = MergePR(fakeMerger{err: other}, 3, nil, nil); !errors.Is(err, other) {
		t.Errorf("passthrough: %v", err)
	}
}

func TestVerdictCheck(t *testing.T) {
	rec := verdict.Record{Kind: verdict.ReviewSpec, Verdict: verdict.Fail, Sha: "abc"}
	store := verdict.Store{Branches: map[string][]verdict.Record{"feat": {rec}}}
	g := &fakeGit{out: map[string]string{"rev-parse --short feat": "abc"}}
	err := VerdictCheck{Git: g, Store: store}.Block("feat")
	var vb *VerdictBlockedError
	if !errors.As(err, &vb) || vb.Stale {
		t.Fatalf("err = %v", err)
	}
	g.out["rev-parse --short feat"] = "def"
	if err = (VerdictCheck{Git: g, Store: store}).Block("feat"); !errors.As(err, &vb) || !vb.Stale {
		t.Errorf("stale: %v", err)
	}
	if err = (VerdictCheck{Git: g, Store: store}).Block("other"); err != nil {
		t.Errorf("unrecorded branch blocked: %v", err)
	}
}

func TestClearWorktree(t *testing.T) {
	list := "worktree /repo\nbranch refs/heads/main\n\nworktree /wt/feat\nbranch refs/heads/feat\n"
	g := &fakeGit{out: map[string]string{"worktree list --porcelain": list, "worktree remove /wt/feat": ""}}
	if err := ClearWorktree(g, "feat", nil); err != nil {
		t.Fatal(err)
	}
	if !contains(g.calls, "worktree remove /wt/feat") {
		t.Errorf("calls %v", g.calls)
	}
	// No worktree for the branch: nothing is removed.
	g = &fakeGit{out: map[string]string{"worktree list --porcelain": list}}
	if err := ClearWorktree(g, "nope", nil); err != nil || len(g.calls) != 1 {
		t.Errorf("err %v calls %v", err, g.calls)
	}
	// The on-disk fallback names a worktree git does not list.
	g = &fakeGit{out: map[string]string{"worktree list --porcelain": list, "worktree remove /disk": ""}}
	if err := ClearWorktree(g, "nope", func() string { return "/disk" }); err != nil || !contains(g.calls, "worktree remove /disk") {
		t.Errorf("err %v calls %v", err, g.calls)
	}
}

type fakeBackend struct {
	backlog.Backend
	items map[string]*backlog.Item
}

func (f fakeBackend) Get(ref string) (*backlog.Item, error) {
	if it, ok := f.items[ref]; ok {
		return it, nil
	}
	return nil, backlog.ErrNotFound
}

func TestClosesLines(t *testing.T) {
	b := fakeBackend{items: map[string]*backlog.Item{
		"F01":     {ID: "F01", Number: 4},
		"F02":     {ID: "F02", Number: 9},
		"api:F03": {ID: "api:F03", Number: 11},
		"web:F04": {ID: "web:F04", Number: 12},
	}}
	got, err := ClosesLines(b, "", []string{"F01", "F02"})
	if err != nil || got != "Closes #4\nCloses #9" {
		t.Fatalf("%q %v", got, err)
	}
	// In an umbrella an item qualified with another sub-repo is unknown.
	if got, err = ClosesLines(b, "api", []string{"api:F03"}); err != nil || got != "Closes #11" {
		t.Errorf("%q %v", got, err)
	}
	var ui *UnknownItemError
	if _, err = ClosesLines(b, "api", []string{"web:F04"}); !errors.As(err, &ui) || ui.Ref != "web:F04" {
		t.Errorf("cross-repo: %v", err)
	}
	if _, err = ClosesLines(b, "", []string{"F99"}); !errors.As(err, &ui) {
		t.Errorf("missing: %v", err)
	}
}

type fakeReopener struct {
	name    string
	changed bool
	opened  []string
}

func (f *fakeReopener) Name() string { return f.name }
func (f *fakeReopener) Reopen(id string) (bool, error) {
	f.opened = append(f.opened, id)
	return f.changed, nil
}

func TestRestore(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	backlogMD := "## Active\n\n- **[B01]** open\n\n## Completed\n\n- **[B02]** done\n"
	if err := os.WriteFile(filepath.Join(root, ".rota", "BACKLOG.md"), []byte(backlogMD), 0o644); err != nil {
		t.Fatal(err)
	}
	var warn strings.Builder
	r := &fakeReopener{name: "file", changed: true}
	if err := Restore(r, root, []string{"B01", "B02"}, &warn); err != nil {
		t.Fatal(err)
	}
	// B01 is already active in a file backlog: no reopen, a noop line.
	if len(r.opened) != 1 || r.opened[0] != "B02" {
		t.Errorf("opened %v", r.opened)
	}
	if want := "noop: [B01] already active in BACKLOG.md\n"; warn.String() != want {
		t.Errorf("warn %q", warn.String())
	}
}
