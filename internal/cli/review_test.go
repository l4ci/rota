package cli

import (
	"context"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/golden"
	"github.com/l4ci/rota/internal/tracker"
)

const reviewBacklog = `# Backlog

## Bugs

- **[B01] [P1] Fix the parser.** Detail: x
- **[B03] [P2] no period here**

## Features

- **[F02] [Major] Add the thing.** Related: [B01]

## Completed

- ~~**[T05] [P3] Old task.** Done 2025-01-02 [` + "`abc1234`" + `]~~
`

// reviewFeature builds a repo on main with a backlog and a feature branch
// "feat/x" carrying commits that cite items, and files that trip the
// scaffolding scan across two files and several hunks.
func reviewFeature(t *testing.T, repo string) {
	t.Helper()
	var body strings.Builder
	for i := 1; i <= 30; i++ {
		body.WriteString("line " + string(rune('a'+i%26)) + "\n")
	}
	write(t, filepath.Join(repo, "a.txt"), body.String())
	write(t, filepath.Join(repo, "b.txt"), "one\ntwo\n")
	write(t, filepath.Join(repo, "crlf.txt"), "x\r\ny\r\n")
	gitT(t, repo, "add", "a.txt", "b.txt", "crlf.txt")
	gitT(t, repo, "commit", "-q", "-m", "base files")
	gitT(t, repo, "checkout", "-q", "-b", "feat/x")

	lines := strings.Split(strings.TrimSuffix(body.String(), "\n"), "\n")
	lines[1] = "Task 12 is in flight"
	lines[20] = "a Placeholder here"
	lines = append(lines, "caféTask 3 stays unmatched", "tasks 5 no", "not yet wired up", "NOT YET WIRED")
	write(t, filepath.Join(repo, "a.txt"), strings.Join(lines, "\n")+"\n")
	write(t, filepath.Join(repo, "b.txt"), "one\nadded later\ntwo\n")
	write(t, filepath.Join(repo, "crlf.txt"), "x\r\ny\r\nz placeholder\r\n")
	write(t, filepath.Join(repo, "café.txt"), "task 1\n")
	gitT(t, repo, "add", "a.txt", "b.txt", "crlf.txt", "café.txt")
	gitT(t, repo, "commit", "-q", "-m", "Do work [B01] and [F02]", "-m", "Also [T05], [B03] and [B99].")
	write(t, filepath.Join(repo, "c.txt"), "extra\n")
	gitT(t, repo, "add", "c.txt")
	gitT(t, repo, "commit", "-q", "-m", "More [B01]")
	gitT(t, repo, "branch", "empty", "main")
	gitT(t, repo, "checkout", "-q", "feat/x")
}

// reviewProject is a plain repo (with .rota/BACKLOG.md) and an umbrella whose
// svc sub-repo has the same branches.
func reviewProject(t *testing.T) (plain, umb string) {
	t.Helper()
	// Fixed dates make the commit hashes the same on every run, so the
	// goldens can hold the hashes the retired helpers printed.
	t.Setenv("GIT_AUTHOR_DATE", "2020-01-01T00:00:00Z")
	t.Setenv("GIT_COMMITTER_DATE", "2020-01-01T00:00:00Z")
	plain = newRepo(t, t.TempDir(), "proj", "main")
	write(t, filepath.Join(plain, ".rota", "BACKLOG.md"), reviewBacklog)
	reviewFeature(t, plain)
	umb = umbrella(t)
	write(t, filepath.Join(umb, ".rota", "BACKLOG.md"), reviewBacklog)
	reviewFeature(t, filepath.Join(umb, "svc"))
	return plain, umb
}

// reviewCall is one verb run in the plain project or the umbrella; the goldens
// hold what the retired helpers (through the old test shim) answered.
type reviewCall struct {
	Name string
	Umb  bool
	Args []string
}

// reviewWant is an exit code and, on success, the data: the shape of the golden.
type reviewWant struct {
	RC   int `json:"rc"`
	Data any `json:"data"`
}

// reviewCheck runs each call through the Go binary and compares the exit code
// and, on success, the data with the golden. It returns the data per call.
func reviewCheck(t *testing.T, plain, umb string, calls []reviewCall) []map[string]any {
	t.Helper()
	got := make([]reviewWant, len(calls))
	out := make([]map[string]any, len(calls))
	for i, c := range calls {
		dir := plain
		if c.Umb {
			dir = umb
		}
		o := trRun(t, dir, "", append(append([]string{}, c.Args...), "--json")...)
		env := envelope(t, o.stdout)
		got[i] = reviewWant{RC: o.code, Data: env["data"]}
		if o.code != 0 {
			if env["data"] != nil {
				t.Errorf("%s: failure carries data %v", c.Name, env["data"])
			}
			continue
		}
		out[i], _ = env["data"].(map[string]any)
	}
	golden.Check(t, calls, got)
	return out
}

func TestReviewScopeParity(t *testing.T) {
	plain, umb := reviewProject(t)
	got := reviewCheck(t, plain, umb, []reviewCall{
		{"plain", false, []string{"review", "scope", "feat/x"}},
		{"current branch", false, []string{"review", "scope"}},
		{"umbrella --repo", true, []string{"review", "scope", "--repo", "svc", "feat/x"}},
		{"umbrella no repo", true, []string{"review", "scope", "feat/x"}},
		{"missing", false, []string{"review", "scope", "nope"}},
		{"empty branch", false, []string{"review", "scope", "empty"}},
	})
	data := got[0]
	if data["commitCount"] != 2.0 {
		t.Errorf("commitCount %v", data["commitCount"])
	}
	intents := data["intents"].([]any)
	if len(intents) != 4 {
		t.Fatalf("intents %v", intents)
	}
	if b03 := intents[3].(map[string]any); b03["id"] != "B03" || b03["title"] != nil {
		t.Errorf("a bullet without a title must give null: %v", b03)
	}
	for _, args := range [][]string{{"review", "scope", "main"}, {"review", "scope", "main", "--repo", "svc"}} {
		dir := plain
		if len(args) > 3 {
			dir = umb
		}
		o := trRun(t, dir, "", append(args, "--json")...)
		if o.code != 1 || !strings.Contains(o.stdout+o.stderr, "cannot review base branch 'main' against itself") {
			t.Errorf("%v: %d %s%s", args, o.code, o.stdout, o.stderr)
		}
		if e := envelope(t, o.stdout); e["data"] != nil {
			t.Errorf("base branch carries data: %v", e)
		}
	}
	if o := trRun(t, umb, "", "review", "scope", "feat/x"); o.code != 2 {
		t.Errorf("umbrella without --repo: exit %d", o.code)
	}
	if o := trRun(t, plain, "", "review", "scope", "nope"); o.code != 3 {
		t.Errorf("missing branch: exit %d", o.code)
	}
	if o := trRun(t, plain, "", "review", "scope", "feat/x"); o.code != 0 || !strings.Contains(o.stdout, "feat/x vs main") {
		t.Errorf("text mode: %d %q", o.code, o.stdout)
	}
}

func TestReviewBriefParity(t *testing.T) {
	plain, umb := reviewProject(t)
	// The retired helper's --repo form ran the scope step from inside the
	// sub-repo, where repos.json is gone, so it failed; the golden holds its
	// in-repo run, the same scenario. B2 (#55) edited the golden's closing
	// lines by hand: the brief asks for a JSON verdict block, not a last line.
	runs := []struct {
		Umb  bool
		Args []string
	}{
		{false, []string{"review", "brief", "feat/x"}},
		{true, []string{"review", "brief", "--repo", "svc", "feat/x"}},
	}
	var texts []string
	for _, c := range runs {
		dir := plain
		if c.Umb {
			dir = umb
		}
		o := trRun(t, dir, "", c.Args...)
		if o.code != 0 {
			t.Errorf("%v: exit %d", c.Args, o.code)
		}
		want := o.stdout
		texts = append(texts, want)
		o = trRun(t, dir, "", append(append([]string{}, c.Args...), "--json")...)
		data := envelope(t, o.stdout)["data"].(map[string]any)
		if data["brief"] != want || data["commitCount"] != 2.0 || data["base"] != "main" || data["branch"] != "feat/x" {
			t.Errorf("%v: data %v", c.Args, data)
		}
		if len(data) != 4 {
			t.Errorf("keys %v", data)
		}
	}
	golden.Check(t, runs, texts)
	reviewCheck(t, plain, umb, []reviewCall{
		{"shim parity", false, []string{"review", "brief", "feat/x"}},
		{"shim current", false, []string{"review", "brief"}},
		{"missing", false, []string{"review", "brief", "nope"}},
	})
	if o := trRun(t, umb, "", "review", "brief", "feat/x"); o.code != 2 {
		t.Errorf("umbrella without --repo: exit %d", o.code)
	}

	o := trRun(t, plain, "", "review", "brief", "main")
	if o.code != 1 || !strings.Contains(o.stderr, "cannot second-opinion base branch 'main' against itself") {
		t.Errorf("base: %d %s", o.code, o.stderr)
	}
	o = trRun(t, plain, "", "review", "brief", "empty")
	if o.code != 1 || !strings.Contains(o.stderr, "branch 'empty' has no commits beyond 'main'") {
		t.Errorf("empty: %d %s", o.code, o.stderr)
	}
	// A bullet without a title printed Python's None.
	if o := trRun(t, plain, "", "review", "brief", "feat/x"); !strings.Contains(o.stdout, "- [B03] None — ") {
		t.Errorf("missing None title line")
	}
}

func TestReviewScaffoldingParity(t *testing.T) {
	plain, umb := reviewProject(t)
	got := reviewCheck(t, plain, umb, []reviewCall{
		{"plain", false, []string{"review", "scaffolding", "feat/x"}},
		{"explicit base", false, []string{"review", "scaffolding", "feat/x", "--base", "main"}},
		{"current branch", false, []string{"review", "scaffolding"}},
		{"umbrella --repo", true, []string{"review", "scaffolding", "--repo", "svc", "feat/x"}},
		{"umbrella no repo", true, []string{"review", "scaffolding", "feat/x"}},
		{"missing branch", false, []string{"review", "scaffolding", "nope"}},
		{"missing base", false, []string{"review", "scaffolding", "feat/x", "--base", "nope"}},
		{"empty", false, []string{"review", "scaffolding", "empty"}},
	})
	findings := got[0]["findings"].([]any)
	if len(findings) < 6 {
		t.Fatalf("expected matches in several hunks and files: %v", findings)
	}
	files := map[string]bool{}
	for _, f := range findings {
		files[f.(map[string]any)["file"].(string)] = true
	}
	if !files["a.txt"] || !files["b.txt"] || !files["crlf.txt"] {
		t.Errorf("files %v", files)
	}
	if d := got[7]; len(d["findings"].([]any)) != 0 {
		t.Errorf("empty branch: %v", d)
	}

	// Text mode is the retired helper's stdout.
	o := trRun(t, plain, "", "review", "scaffolding", "feat/x")
	if o.code != 0 {
		t.Errorf("text mode: exit %d", o.code)
	}
	golden.Check(t, []string{"review", "scaffolding", "feat/x"}, o.stdout)
	if o := trRun(t, umb, "", "review", "scaffolding", "feat/x"); o.code != 2 {
		t.Errorf("umbrella no repo: exit %d", o.code)
	}
	if o := trRun(t, plain, "", "review", "scaffolding", "nope"); o.code != 3 {
		t.Errorf("missing branch: exit %d", o.code)
	}
}

// TestReviewScaffoldingUnicode pins the one place the port differs from
// Python's regex by construction: Go's \b is ASCII-only, so the port spells
// the boundary out; the retired helper's answer must still come out.
func TestReviewScaffoldingUnicode(t *testing.T) {
	plain, _ := reviewProject(t)
	var texts []string
	for _, f := range envelope(t, trRun(t, plain, "", "review", "scaffolding", "feat/x", "--json").stdout)["data"].(map[string]any)["findings"].([]any) {
		texts = append(texts, f.(map[string]any)["text"].(string))
	}
	for _, s := range texts {
		if strings.Contains(s, "éTask") || strings.Contains(s, "tasks 5") {
			t.Errorf("must not match %q", s)
		}
	}
}

// a8Forge is an issue tracker with open PRs, a merge and native milestones,
// for the issue-only verbs (review queue, ship pr-merge, release ...).
type a8Forge struct {
	*trackertest.MS
	prs      []tracker.PR
	mergeErr error
	merged   []int
	files    map[int][]string // PRFiles answers, for the merge-approval gate
}

func (f *a8Forge) PRFiles(_ context.Context, pr int) ([]string, error) {
	return f.files[pr], nil
}

func (f *a8Forge) OpenPRs(context.Context) ([]tracker.PR, error) {
	if err := f.Fake.Fail["open_prs"]; err != nil {
		return nil, err
	}
	return slices.Clone(f.prs), nil
}

var a8Closing = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?):?\s+#(\d+)`)

func (f *a8Forge) ClosedNumbers(body string) []int {
	var out []int
	for _, m := range a8Closing.FindAllStringSubmatch(body, -1) {
		n, _ := strconv.Atoi(m[1])
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

func (f *a8Forge) PRMerge(_ context.Context, pr int, _ tracker.MergeOpts) (string, error) {
	if f.mergeErr != nil {
		return "", f.mergeErr
	}
	f.merged = append(f.merged, pr)
	f.prs = slices.DeleteFunc(f.prs, func(p tracker.PR) bool { return p.Number == pr })
	return "0123456789abcdef0123456789abcdef01234567", nil
}

func (f *a8Forge) IssuesInMilestone(_ context.Context, title, state string) ([]tracker.Issue, error) {
	var out []tracker.Issue
	for _, is := range f.Fake.Issues {
		if is.Milestone == title && (state == "all" || is.State == state) {
			out = append(out, is)
		}
	}
	return out, nil
}

func a8Fixture() *a8Forge {
	m := "M01 — One"
	issue := func(n int, title string, labels []string, milestone, state, reason string) tracker.Issue {
		return tracker.Issue{Number: n, Title: title, Labels: labels, Milestone: milestone, State: state, StateReason: reason,
			URL: "https://example.test/issues/" + strconv.Itoa(n)}
	}
	f := &a8Forge{MS: &trackertest.MS{Fake: &trackertest.Fake{Issues: []tracker.Issue{
		issue(1, "One", []string{"type:feature", "needs-review"}, m, "open", ""),
		issue(2, "Two", []string{"type:bug", "needs-review"}, m, "open", ""),
		{Number: 3, Title: m, Body: "---\nid: M01\ntitle: One\nstatus: active\n---\n\n# M01\n",
			Labels: []string{"milestone-tracker", "status:active"}, Milestone: m, State: "open"},
		issue(4, "Old", []string{"type:task"}, m, "closed", "completed"),
		issue(5, "Wip", []string{"type:feature", "in-progress"}, m, "open", ""),
		issue(6, "Plain", []string{"type:task"}, m, "open", ""),
		issue(7, "Fix crash", []string{"type:bug"}, m, "closed", "completed"),
		issue(8, "Dropped", []string{"type:feature"}, m, "closed", "not_planned"),
	}}, Native: []tracker.Milestone{{Number: 1, Title: m, State: "open"}}}}
	if _, err := f.Fake.AddComment(context.Background(), 1, "<!-- rota:proof -->\n## Proof\n- unit \u00b7 PASS \u00b7 ok"); err != nil {
		panic(err)
	}
	f.prs = []tracker.PR{
		{Number: 10, Title: "PR ten", Branch: "feat/ten", URL: "https://example.test/pull/10", Body: "Closes #1"},
		{Number: 11, Title: "PR eleven", Branch: "feat/eleven", URL: "https://example.test/pull/11", Body: "Fixes #2"},
		{Number: 12, Title: "PR twelve", Branch: "feat/twelve", URL: "https://example.test/pull/12", Body: "nothing"},
	}
	f.Fake.Calls = nil
	return f
}

// a8Run runs rota --json in root and returns the exit code, the data object and the error message.
func a8Run(t *testing.T, root string, args ...string) (int, map[string]any, string) {
	t.Helper()
	o := trRun(t, root, "", append([]string{"--json"}, args...)...)
	env := envelope(t, o.stdout)
	data, _ := env["data"].(map[string]any)
	msg := ""
	if e, ok := env["error"].(map[string]any); ok {
		msg, _ = e["message"].(string)
	}
	return o.code, data, msg
}

// a8Project is an issue-mode project served by f, with a git repo for --since.
// It returns the deps carrying f as the tracker.
func a8Project(t *testing.T, f *a8Forge) (string, *Deps) {
	t.Helper()
	root := newRepo(t, t.TempDir(), "proj", "main")
	write(t, filepath.Join(root, ".rota", "config.json"), issuesConfig)
	return root, withTracker(t, f)
}

// a8RunIn runs a8Run against a fresh a8Project served by f.
func a8RunIn(t *testing.T, f *a8Forge, args ...string) (int, map[string]any, string) {
	t.Helper()
	root, deps := a8Project(t, f)
	return a8RunWith(t, deps, root, args...)
}

// trRunIn runs trRun against a fresh a8Project served by f.
func trRunIn(t *testing.T, f *a8Forge, stdin string, args ...string) trOut {
	t.Helper()
	root, deps := a8Project(t, f)
	return trRunWith(t, deps, root, stdin, args...)
}

func a8FileProject(t *testing.T) string {
	t.Helper()
	root := newRepo(t, t.TempDir(), "proj", "main")
	write(t, filepath.Join(root, ".rota", "config.json"), `{"backlog":{"backend":"file"}}`)
	return root
}

func TestReviewQueue(t *testing.T) {
	f := a8Fixture()
	root, deps := a8Project(t, f)
	code, data, msg := a8RunWith(t, deps, root, "review", "queue")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, msg)
	}
	items := data["items"].([]any)
	var ids []string
	for _, it := range items {
		m := it.(map[string]any)
		ids = append(ids, m["id"].(string)+m["type"].(string))
	}
	// Needs-review issues only, lowest first; the id is the bare number (rule 11).
	if !reflect.DeepEqual(ids, []string{"1F", "2B"}) {
		t.Fatalf("items %v", ids)
	}
	first := items[0].(map[string]any)
	prs := first["prs"].([]any)
	pr := prs[0].(map[string]any)
	if first["number"] != float64(1) || first["title"] != "One" || len(prs) != 1 ||
		pr["number"] != float64(10) || pr["branch"] != "feat/ten" || pr["url"] != "https://example.test/pull/10" || pr["body"] != "Closes #1" {
		t.Fatalf("first %v", first)
	}
	// Key order is the contract's.
	o := trRunWith(t, deps, root, "", "--json", "review", "queue")
	if !strings.Contains(o.stdout, `"items": [{"id": "1", "type": "F", "number": 1, "title": "One", "prs": [{"number": 10, "title": "PR ten", "branch": "feat/ten"`) {
		t.Fatalf("key order: %s", o.stdout)
	}
	// Text mode: one line per item.
	if o := trRunWith(t, deps, root, "", "review", "queue"); o.code != 0 || o.stdout != "F1 One\nB2 Two\n" {
		t.Fatalf("text %+v", o)
	}
	// One issue list and one PR list.
	if len(f.Fake.Calls) < 2 {
		t.Fatalf("calls %v", f.Fake.Calls)
	}
}

func TestReviewQueueEmpty(t *testing.T) {
	f := a8Fixture()
	f.Fake.Issues = f.Fake.Issues[2:]
	root, deps := a8Project(t, f)
	code, data, _ := a8RunWith(t, deps, root, "review", "queue")
	if items, ok := data["items"].([]any); code != 0 || !ok || len(items) != 0 {
		t.Fatalf("exit %d data %v", code, data)
	}
}

func TestReviewQueueExits(t *testing.T) {
	// File backend: backend failure, exit 1 for a read-only verb.
	code, data, _ := a8Run(t, a8FileProject(t), "review", "queue")
	if code != 1 || data["blockedBy"] != "backend" || data["changed"] != false {
		t.Fatalf("file: exit %d data %v", code, data)
	}
	// Tracker failures: 5 unavailable, 6 rate-limited.
	for kind, want := range map[tracker.Kind]int{tracker.KindUnavailable: 5, tracker.KindFailed: 5, tracker.KindRateLimited: 6} {
		f := a8Fixture()
		f.Fake.Fail = map[string]error{"list": &tracker.Error{Kind: kind, Code: 3, Message: "boom"}}
		if code, _, _ := a8RunIn(t, f, "review", "queue"); code != want {
			t.Errorf("list failure %v: exit %d, want %d", kind, code, want)
		}
	}
	f := a8Fixture()
	f.Fake.Fail = map[string]error{"open_prs": &tracker.Error{Kind: tracker.KindUnavailable, Code: 3, Message: "boom"}}
	if code, _, _ := a8RunIn(t, f, "review", "queue"); code != 5 {
		t.Errorf("PR list failure: exit %d", code)
	}
	if code, _, _ := a8RunIn(t, a8Fixture(), "review", "queue", "extra"); code != 2 {
		t.Errorf("extra argument: exit %d", code)
	}
}

// Umbrella issue mode is not ported: exit 71, with and without --repo.
func TestReviewQueueUmbrella(t *testing.T) {
	_, umb := reviewProject(t)
	// The backend rule comes first: a file-backend umbrella is refused.
	if o := trRun(t, umb, "", "review", "queue", "--repo", "svc"); o.code != 1 {
		t.Errorf("file backend: exit %d, want 1\n%s%s", o.code, o.stdout, o.stderr)
	}
	if o := trRun(t, umb, "", "review", "queue", "--repo", "nope"); o.code != 3 {
		t.Errorf("unknown --repo: exit %d, want 3\n%s%s", o.code, o.stdout, o.stderr)
	}
	write(t, filepath.Join(umb, ".rota", "config.json"), `{"backlog":{"backend":"issues"}}`)
	// Past the checks it reaches the sub-repos' trackers; with no forge here that is exit 5.
	if o := trRun(t, umb, "", "review", "queue"); o.code != 5 {
		t.Errorf("exit %d, want 5\n%s%s", o.code, o.stdout, o.stderr)
	}
	if o := trRun(t, umb, "", "review", "queue", "--repo", "svc"); o.code != 5 {
		t.Errorf("--repo: exit %d, want 5\n%s%s", o.code, o.stdout, o.stderr)
	}
}

// Issue mode: refs come from closing keywords and the branch name, resolved
// through the tracker; an unknown ref stays listed with no intent.
func TestReviewScopeIssueMode(t *testing.T) {
	root := newRepo(t, t.TempDir(), "proj", "main")
	write(t, filepath.Join(root, ".rota", "config.json"), issuesConfig)
	write(t, filepath.Join(root, "a.txt"), "a\n")
	gitT(t, root, "add", "a.txt")
	gitT(t, root, "commit", "-q", "-m", "base")
	gitT(t, root, "checkout", "-q", "-b", "kit/7-export")
	write(t, filepath.Join(root, "a.txt"), "b\n")
	gitT(t, root, "add", "a.txt")
	gitT(t, root, "commit", "-q", "-m", "Add export", "-m", "Closes #9, closes #99. Mentions [B01].")
	deps := withTracker(t, issueFixture())

	o := trRunWith(t, deps, root, "", "review", "scope", "--json")
	if o.code != 0 {
		t.Fatalf("exit %d %s%s", o.code, o.stdout, o.stderr)
	}
	d := envelope(t, o.stdout)["data"].(map[string]any)
	if !reflect.DeepEqual(d["referencedIds"], []any{"#7", "#9", "#99"}) {
		t.Fatalf("referencedIds %v", d["referencedIds"])
	}
	in := d["intents"].([]any)
	if len(in) != 2 {
		t.Fatalf("intents %v", in)
	}
	i0 := in[0].(map[string]any)
	if i0["id"] != "#7" || i0["type"] != "F" || i0["title"] != "Add export" || i0["entry"] == "" {
		t.Errorf("intent #7: %v", i0)
	}
	if i1 := in[1].(map[string]any); i1["id"] != "#9" || i1["type"] != "B" {
		t.Errorf("intent #9: %v", i1)
	}

	b := trRunWith(t, deps, root, "", "review", "brief")
	if b.code != 0 || !strings.Contains(b.stdout, "- #7 Add export — ") || strings.Contains(b.stdout, "[#7]") {
		t.Errorf("brief: %d\n%s%s", b.code, b.stdout, b.stderr)
	}
}
