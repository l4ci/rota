package backlog

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/golden"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/tracker"
)

// rrFake is the issue fake with open PRs, a merge and native milestones; every
// call is recorded under the Python adapter's name so the sequence compares
// with the Python IssueBackend's.
type rrFake struct {
	*trackertest.MS
	PRs        []tracker.PR
	HostCloses bool // a merge closes the issues the PR body closes, as a default-branch merge does
	merged     []string
}

const rrSHA = "0123456789abcdef0123456789abcdef01234567"

var rrClosing = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?):?\s+#(\d+)`)

func (f *rrFake) rec(m string, a ...any) error {
	f.Fake.Calls = append(f.Fake.Calls, trackertest.Call{Method: m, Args: append([]any{}, a...)})
	return f.Fake.Fail[m]
}

func (f *rrFake) OpenPRs(context.Context) ([]tracker.PR, error) {
	if err := f.rec("open_prs"); err != nil {
		return nil, err
	}
	return slices.Clone(f.PRs), nil
}

func (f *rrFake) ClosedNumbers(body string) []int {
	var out []int
	for _, m := range rrClosing.FindAllStringSubmatch(body, -1) {
		n, _ := strconv.Atoi(m[1])
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// PRFiles is the merge-approval gate's file list; the parity scenarios never
// gate, so it records nothing.
func (f *rrFake) PRFiles(context.Context, int) ([]string, error) { return nil, nil }

func (f *rrFake) PRMerge(_ context.Context, pr int) (string, error) {
	if err := f.rec("pr_merge", pr); err != nil {
		return "", err
	}
	i := slices.IndexFunc(f.PRs, func(p tracker.PR) bool { return p.Number == pr })
	if i < 0 {
		return "", &tracker.Error{Kind: tracker.KindFailed, Code: 1, Message: "no pull requests found"}
	}
	body := f.PRs[i].Body
	f.PRs = slices.Delete(f.PRs, i, i+1)
	f.merged = append(f.merged, strconv.Itoa(pr))
	if f.HostCloses {
		for _, n := range f.ClosedNumbers(body) {
			for j := range f.Fake.Issues {
				if is := &f.Fake.Issues[j]; is.Number == n && is.State == "open" {
					is.State, is.StateReason = "closed", "completed"
				}
			}
		}
	}
	return rrSHA, nil
}

func (f *rrFake) Milestones(_ context.Context, state string) ([]tracker.Milestone, error) {
	if err := f.rec("milestones", state); err != nil {
		return nil, err
	}
	return f.MS.Milestones(context.Background(), state)
}

func (f *rrFake) EditMilestone(ctx context.Context, n int, e tracker.MilestoneEdit) error {
	st := ""
	if e.State != nil {
		st = *e.State
	}
	if err := f.rec("edit_milestone", n, st); err != nil {
		return err
	}
	return f.MS.EditMilestone(ctx, n, e)
}

func (f *rrFake) IssuesInMilestone(_ context.Context, title, state string) ([]tracker.Issue, error) {
	if err := f.rec("issues_in_milestone", title, state); err != nil {
		return nil, err
	}
	var out []tracker.Issue
	for _, is := range f.Fake.Issues {
		if is.Milestone == title && (state == "all" || is.State == state) {
			is.Comments = nil
			out = append(out, is)
		}
	}
	return out, nil
}

// rrSeed is the scenario both sides start from.
type rrSeed struct {
	Cfg        string         `json:"cfg"`
	Milestones []string       `json:"milestones"`
	Issues     []wSeedIssue   `json:"issues"`
	Comments   []wSeedComment `json:"comments"`
	Native     []rrNative     `json:"native"`
	PRs        []rrPR         `json:"prs"`
	HostCloses bool           `json:"hostCloses"`
	Fail       map[string]int `json:"fail"`
	Steps      []rrStep       `json:"steps"`
}

type rrNative struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
}

type rrPR struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Branch string `json:"branch"`
	URL    string `json:"url"`
	Body   string `json:"body"`
}

type rrStep struct {
	Op    string   `json:"op"`
	PR    int      `json:"pr"`
	Items []string `json:"items"`
	Mid   string   `json:"mid"`
	Tag   string   `json:"tag"`
}

const rrTrackerBody = "---\nid: M07\ntitle: Seven\nstatus: active\n---\n\n# M07\n"

func rrIssues() []wSeedIssue {
	m := "M07 — Seven"
	return []wSeedIssue{
		{1, "Add export", "body", []string{"type:feature", "needs-review"}, m, "open", "", nil},
		{2, "Crash", "body", []string{"type:bug", "needs-review", "p1"}, m, "open", "", nil},
		{3, "M07 — Seven", rrTrackerBody, []string{"milestone-tracker", "status:active", "needs-review"}, m, "open", "", nil},
		{4, "Old task", "", []string{"type:task"}, m, "closed", "completed", nil},
		{5, "Dropped feature", "", []string{"type:feature"}, m, "closed", "not_planned", nil},
		{6, "Fixed bug", "", []string{"type:bug", "needs-review"}, m, "closed", "completed", nil},
		{7, "Wip", "", []string{"type:task", "in-progress"}, m, "open", "", nil},
		{8, "Plain open", "", []string{"type:task"}, m, "open", "", nil},
		{9, "Elsewhere", "", []string{"type:feature", "needs-review"}, "", "open", "", nil},
		{10, "Multi\n  spaced   title", "", []string{"type:feature", "released"}, m, "closed", "completed", nil},
		{11, "Review me", "", []string{"type:task", "needs-review"}, "", "open", "", nil},
		{12, "Blocked review", "", []string{"type:task", "changes-requested"}, m, "open", "", nil},
	}
}

func rrComments() []wSeedComment {
	return []wSeedComment{
		{1, "<!-- rota:proof -->\n## Proof\n- build · PASS · ok"},
		{2, "<!-- rota:proof -->\n## Proof\n- tests · PASS · ok"},
		{4, " Released in v1.2.0\n"},
		{6, "random"},
		{10, "<!-- rota:proof -->\n## Proof\n- x · PASS · ok"},
	}
}

func rrPRs() []rrPR {
	pr := func(n int, body string) rrPR {
		return rrPR{n, fmt.Sprintf("PR %d", n), fmt.Sprintf("feat/%d", n), fmt.Sprintf("https://example.test/pull/%d", n), body}
	}
	return []rrPR{
		pr(20, "Closes #1"),
		pr(21, "Fixes #2\nResolves #11"),
		pr(22, "Closes #3"),
		pr(23, "no links"),
		pr(24, "closes #9, closes #6"),
		pr(25, "Closes #1, closes #2"),
		pr(26, "Closes #8\nCloses #99"),
	}
}

func rrNativeSeed() []rrNative {
	return []rrNative{{1, "M07 — Seven", "open"}, {2, "M01 — Alpha", "closed"}}
}

func rrScenarios() []rrSeed {
	queue := rrStep{Op: "queue"}
	merge := func(pr int, items ...string) rrStep {
		s := rrStep{Op: "merge", PR: pr}
		if items != nil {
			s.Items = items
		}
		return s
	}
	gate := func(mid string) rrStep { return rrStep{Op: "gate", Mid: mid} }
	notes := func(mid string) rrStep { return rrStep{Op: "notes", Mid: mid} }
	closeOut := func(mid string) rrStep { return rrStep{Op: "close", Mid: mid, Tag: "v1.2.0"} }
	base := func(steps ...rrStep) rrSeed {
		return rrSeed{Cfg: defaultCfg, Issues: rrIssues(), Comments: rrComments(), Native: rrNativeSeed(), PRs: rrPRs(), Steps: steps}
	}
	var out []rrSeed
	out = append(out, base(queue, merge(20), queue, merge(21), merge(99), merge(23), merge(22), merge(24),
		merge(20), merge(23, "F6"), merge(23, "#99"), merge(23, "B1"), merge(23, "garbage"), merge(26, "#8"),
		merge(25, "1", "#2"), merge(25), queue))
	out = append(out, base(gate("M07"), gate("M99"), gate("M01"), notes("M07"), notes("M99"), closeOut("M07"), closeOut("M07"),
		gate("M07"), notes("M07"), closeOut("M01"), closeOut("M99")))
	host := base(merge(20), merge(25), queue)
	host.HostCloses = true
	out = append(out, host)
	for _, fail := range []map[string]int{{"open_prs": 3}, {"list": 4}, {"pr_merge": 1}, {"issues_in_milestone": 3}, {"milestones": 3}} {
		s := base(queue, merge(20), gate("M07"), notes("M07"), closeOut("M07"))
		s.Fail = fail
		out = append(out, s)
	}
	custom := base(queue, merge(20), merge(21), gate("M07"), notes("M07"), closeOut("M07"), queue)
	custom.Cfg = `{"issues": {"labels": {"types": {"bug": "kind/bug", "feature": "kind/feature", "task": "kind/task"}, "milestoneTracker": "tracker", "needsReview": "ready-for-review", "released": "shipped"}}}`
	repl := map[string]string{"type:bug": "kind/bug", "type:feature": "kind/feature", "type:task": "kind/task", "milestone-tracker": "tracker", "needs-review": "ready-for-review", "released": "shipped"}
	for i := range custom.Issues {
		for j, l := range custom.Issues[i].Labels {
			if r, ok := repl[l]; ok {
				custom.Issues[i].Labels[j] = r
			}
		}
	}
	return append(out, custom)
}

func rrBackend(t *testing.T, s rrSeed) (*Issues, *rrFake) {
	t.Helper()
	ms := &trackertest.MS{Fake: &trackertest.Fake{}}
	f := &rrFake{MS: ms, HostCloses: s.HostCloses}
	for _, i := range s.Issues {
		f.Fake.Issues = append(f.Fake.Issues, tracker.Issue{Number: i.Number, Title: i.Title, Body: i.Body, Labels: slices.Clone(i.Labels),
			Milestone: i.Milestone, State: i.State, StateReason: i.StateReason, Assignees: slices.Clone(i.Assignees),
			URL: fmt.Sprintf("https://example.test/issues/%d", i.Number)})
	}
	for _, c := range s.Comments {
		if _, err := f.Fake.AddComment(context.Background(), c.Issue, c.Body); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range s.Native {
		f.MS.Native = append(f.MS.Native, tracker.Milestone{Number: n.Number, Title: n.Title, State: n.State})
	}
	for _, p := range s.PRs {
		f.PRs = append(f.PRs, tracker.PR{Number: p.Number, Title: p.Title, Branch: p.Branch, URL: p.URL, Body: p.Body})
	}
	f.Fake.Fail = map[string]error{}
	for m, code := range s.Fail {
		f.Fake.Fail[m] = &tracker.Error{Kind: tracker.KindFailed, Code: code, Message: m + " failed"}
	}
	f.Fake.Calls = nil
	return &Issues{Cfg: mustDecode(t, s.Cfg), Tracker: f}, f
}

func rrStepGo(b *Issues, st rrStep) map[string]any {
	ok := func(v any) map[string]any { return map[string]any{"k": "ok", "v": v} }
	bad := func(err error) map[string]any { return map[string]any{"k": writeErrKind(err)} }
	switch st.Op {
	case "queue":
		rows, err := b.ReviewQueue()
		if err != nil {
			return bad(err)
		}
		out := []any{}
		for _, r := range rows {
			prs := []any{}
			for _, p := range r.PRs {
				prs = append(prs, map[string]any{"number": p.Number, "title": p.Title, "branch": p.Branch, "url": p.URL, "body": p.Body})
			}
			out = append(out, map[string]any{"id": r.Type + r.ID, "number": r.Number, "title": r.Title, "prs": prs})
		}
		return ok(out)
	case "merge":
		res, err := b.MergePR(st.PR, st.Items)
		if err != nil {
			return bad(err)
		}
		var sha any
		if res.SHA != "" {
			sha = res.SHA
		}
		refs := func(rs []ItemRef) []any {
			out := []any{}
			for _, r := range rs {
				out = append(out, r.Ref())
			}
			return out
		}
		return ok([]any{sha, refs(res.Closed), refs(res.Unproven)})
	case "gate":
		blocked, warn, err := b.ReleaseGate(st.Mid)
		if err != nil {
			return bad(err)
		}
		bl, w := []any{}, []any{}
		for _, x := range blocked {
			bl = append(bl, []any{x.Issue.Number, x.Label})
		}
		for _, is := range warn {
			w = append(w, is.Number)
		}
		return ok([]any{bl, w})
	case "notes":
		secs, err := b.ReleaseNotes(st.Mid)
		if err != nil {
			return bad(err)
		}
		out := map[string]any{}
		for _, s := range secs {
			rows := []any{}
			for _, r := range s.Rows {
				rows = append(rows, []any{r.Title, r.Number})
			}
			out[s.Name] = rows
		}
		return ok(out)
	case "close":
		n, _, err := b.ReleaseClose(st.Mid, st.Tag)
		if err != nil {
			return bad(err)
		}
		return ok(n)
	}
	panic("bad op " + st.Op)
}

func rrRun(t *testing.T, s rrSeed) map[string]any {
	t.Helper()
	b, f := rrBackend(t, s)
	var steps []any
	for _, st := range s.Steps {
		f.Fake.Calls = nil
		r := rrStepGo(b, st)
		calls := []any{}
		for _, c := range f.Fake.Calls {
			calls = append(calls, map[string]any{"method": c.Method, "args": c.Args})
		}
		steps = append(steps, map[string]any{"result": r, "calls": calls})
	}
	final := []any{}
	for _, i := range f.Fake.Issues {
		cs := []any{}
		for _, c := range i.Comments {
			cs = append(cs, []any{c.ID, c.Body})
		}
		final = append(final, []any{i.Number, i.Title, i.Body, i.Labels, i.Milestone, i.State, i.StateReason, append([]string{}, i.Assignees...), cs})
	}
	native, prs := []any{}, []any{}
	for _, n := range f.MS.Native {
		native = append(native, []any{n.Number, n.State})
	}
	for _, p := range f.PRs {
		prs = append(prs, p.Number)
	}
	return map[string]any{"steps": steps, "final": final, "native": native, "prs": prs}
}

// The Go backend, run on the recorded scenarios, must return what the Python IssueBackend did:
// the same results, the same tracker calls in the same order and the same
// issues, milestones and PRs left behind.
func TestIssuesReleaseReviewMatchPython(t *testing.T) {
	scen := rrScenarios()
	var got []any
	for _, s := range scen {
		got = append(got, rrRun(t, s))
	}
	golden.Check(t, map[string]any{"input": scen}, got)
}

func rrOne(t *testing.T) (*Issues, *rrFake) {
	t.Helper()
	b, f := rrBackend(t, rrSeed{Cfg: defaultCfg, Issues: rrIssues(), Comments: rrComments(), Native: rrNativeSeed(), PRs: rrPRs()})
	return b, f
}

func rrIssue(f *rrFake, n int) tracker.Issue {
	for _, is := range f.Fake.Issues {
		if is.Number == n {
			return is
		}
	}
	return tracker.Issue{}
}

func TestIssuesReviewQueue(t *testing.T) {
	b, f := rrOne(t)
	rows, err := b.ReviewQueue()
	if err != nil {
		t.Fatal(err)
	}
	// Needs-review, open, not the tracking issue (3), lowest number first.
	var ids []string
	for _, r := range rows {
		ids = append(ids, r.Type+r.ID)
	}
	if !reflect.DeepEqual(ids, []string{"F1", "B2", "F9", "T11"}) {
		t.Fatalf("queue %v", ids)
	}
	if n := len(rows[0].PRs); n != 2 || rows[0].PRs[0].Number != 20 || rows[0].PRs[1].Number != 25 {
		t.Fatalf("F1 prs %v", rows[0].PRs)
	}
	if rows[2].PRs == nil || len(rows[2].PRs) != 1 || rows[3].PRs[0].Number != 21 {
		t.Fatalf("prs %v / %v", rows[2].PRs, rows[3].PRs)
	}
	// One issue list and one PR list.
	if got := []string{f.Fake.Calls[0].Method, f.Fake.Calls[1].Method}; len(f.Fake.Calls) != 2 || got[0] != "list" || got[1] != "open_prs" {
		t.Fatalf("calls %v", f.Fake.Calls)
	}
}

func TestIssuesMergePR(t *testing.T) {
	b, f := rrOne(t)
	res, err := b.MergePR(20, nil)
	if err != nil || res.SHA != rrSHA || len(res.Closed) != 1 || res.Closed[0].Ref() != "F1" {
		t.Fatalf("merge: %+v %v", res, err)
	}
	is := rrIssue(f, 1)
	if is.State != "closed" || is.StateReason != "completed" || slices.Contains(is.Labels, "needs-review") {
		t.Fatalf("F1 %+v", is)
	}
	last := is.Comments[len(is.Comments)-1].Body
	if last != "Done in `0123456`\n\n<!-- rota:done -->" {
		t.Fatalf("close comment %q", last)
	}
	// Unproven T11: nothing is merged, the item flips and gets the feedback.
	res, err = b.MergePR(21, nil)
	if err != nil || res.SHA != "" || len(res.Unproven) != 1 || res.Unproven[0].Ref() != "T11" {
		t.Fatalf("unproven: %+v %v", res, err)
	}
	if slices.Contains(f.merged, "21") {
		t.Fatal("PR 21 was merged")
	}
	is = rrIssue(f, 11)
	want := "<!-- rota:comment feedback -->\nPR 21 not merged: no proof recorded for T11. Add proof with rota proof add, then run the review again."
	if !slices.Contains(is.Labels, "changes-requested") || slices.Contains(is.Labels, "needs-review") ||
		is.Comments[len(is.Comments)-1].Body != want {
		t.Fatalf("T11 %+v", is)
	}
	if _, err := b.MergePR(99, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown PR: %v", err)
	}
	if _, err := b.MergePR(23, []string{"#99"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown item: %v", err)
	}
	// A PR that is not open is not found with --items too (the contract), and
	// nothing reaches the forge's merge.
	if _, err := b.MergePR(99, []string{"F1"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("PR not open with --items: %v", err)
	}
	if slices.Contains(f.merged, "99") {
		t.Fatal("PR 99 reached the merge")
	}
}

func TestIssuesMergePRHostClosed(t *testing.T) {
	b, f := rrBackend(t, rrSeed{Cfg: defaultCfg, Issues: rrIssues(), Comments: rrComments(), Native: rrNativeSeed(), PRs: rrPRs(), HostCloses: true})
	res, err := b.MergePR(20, nil)
	if err != nil || len(res.Closed) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	// The host closed F1; the state label it left behind is removed, and the
	// backend closes nothing itself.
	is := rrIssue(f, 1)
	if is.State != "closed" || slices.Contains(is.Labels, "needs-review") {
		t.Fatalf("F1 %+v", is)
	}
	for _, c := range f.Fake.Calls {
		if c.Method == "close" {
			t.Fatalf("closed through the tracker: %v", f.Fake.Calls)
		}
	}
}

func TestIssuesMergePRRefused(t *testing.T) {
	b, f := rrOne(t)
	f.Fake.Fail = map[string]error{"pr_merge": &tracker.Error{Kind: tracker.KindFailed, Code: 1, Message: "gh pr merge 23: not mergeable"}}
	_, err := b.MergePR(23, nil)
	var mf *MergeFailedError
	if !errors.As(err, &mf) || mf.PR != 23 {
		t.Fatalf("want MergeFailedError, got %v", err)
	}
	f.Fake.Fail = map[string]error{"pr_merge": &tracker.Error{Kind: tracker.KindUnavailable, Code: 3, Message: "gh missing"}}
	if _, err = b.MergePR(23, nil); errors.As(err, &mf) {
		t.Fatalf("an unavailable forge is not a refused merge: %v", err)
	}
}

func TestIssuesReleaseGate(t *testing.T) {
	b, _ := rrOne(t)
	blocked, warn, err := b.ReleaseGate("M07")
	if err != nil {
		t.Fatal(err)
	}
	var bl []string
	for _, x := range blocked {
		bl = append(bl, fmt.Sprintf("%d:%s", x.Issue.Number, x.Label))
	}
	var w []int
	for _, is := range warn {
		w = append(w, is.Number)
	}
	if !reflect.DeepEqual(bl, []string{"1:needs-review", "2:needs-review", "7:in-progress", "12:changes-requested"}) || !reflect.DeepEqual(w, []int{8}) {
		t.Fatalf("blocked %v warn %v", bl, w)
	}
	if _, _, err := b.ReleaseGate("M99"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown milestone: %v", err)
	}
}

func TestIssuesReleaseNotes(t *testing.T) {
	b, _ := rrOne(t)
	secs, err := b.ReleaseNotes("M07")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]NoteRow{}
	for _, s := range secs {
		got[s.Name] = s.Rows
	}
	if names := []string{secs[0].Name, secs[1].Name, secs[2].Name}; !reflect.DeepEqual(names, []string{"New", "Fixed", "Changed"}) ||
		!reflect.DeepEqual(got["New"], []NoteRow{{"Multi spaced title", 10}}) ||
		!reflect.DeepEqual(got["Fixed"], []NoteRow{{"Fixed bug", 6}}) ||
		!reflect.DeepEqual(got["Changed"], []NoteRow{{"Old task", 4}}) {
		t.Fatalf("notes %+v", secs)
	}
}

func TestIssuesReleaseClose(t *testing.T) {
	b, f := rrOne(t)
	n, changed, err := b.ReleaseClose("M07", "v1.2.0")
	if err != nil || n != 3 || !changed {
		t.Fatalf("close: %d %v %v", n, changed, err)
	}
	// 4 already carries its comment, 10 its label; each gets only what is missing.
	if is := rrIssue(f, 4); !slices.Contains(is.Labels, "released") || len(is.Comments) != 1 {
		t.Fatalf("#4 %+v", is)
	}
	if is := rrIssue(f, 10); len(is.Comments) != 2 || is.Comments[1].Body != "Released in v1.2.0\n\n<!-- rota:released -->" {
		t.Fatalf("#10 %+v", is)
	}
	if is := rrIssue(f, 5); slices.Contains(is.Labels, "released") {
		t.Fatalf("not-planned #5 released")
	}
	if f.MS.Native[0].State != "closed" || rrIssue(f, 3).State != "closed" {
		t.Fatalf("milestone not closed: %+v %+v", f.MS.Native, rrIssue(f, 3))
	}
	// A second run writes nothing.
	f.Fake.Calls = nil
	n, changed, err = b.ReleaseClose("M07", "v1.2.0")
	if err != nil || n != 3 || changed {
		t.Fatalf("second run: %d %v %v", n, changed, err)
	}
	for _, c := range f.Fake.Calls {
		switch c.Method {
		case "add_labels", "add_comment", "edit", "close", "reopen", "remove_labels", "edit_milestone":
			t.Fatalf("second run wrote: %v", f.Fake.Calls)
		}
	}
}

// rrUmbrella is two sub-repos seeded like rrOne; only gh (home, first
// registered) holds the M07 tracking issue #3.
func rrUmbrella(t *testing.T) (*Umbrella, map[string]*rrFake) {
	t.Helper()
	fakes := map[string]*rrFake{}
	for _, name := range []string{"gh", "gl"} {
		issues := rrIssues()
		if name == "gl" {
			issues = slices.DeleteFunc(issues, func(i wSeedIssue) bool { return i.Number == 3 })
		}
		_, f := rrBackend(t, rrSeed{Cfg: defaultCfg, Issues: issues, Comments: rrComments(), Native: rrNativeSeed(), PRs: rrPRs()})
		fakes[name] = f
	}
	u := &Umbrella{Cfg: mustDecode(t, defaultCfg),
		Repos:      []repos.Repo{{Name: "gh", Rel: "gh", Path: "/umb/gh"}, {Name: "gl", Rel: "gl", Path: "/umb/gl"}},
		NewTracker: func(dir string) (Tracker, error) { return fakes[strings.TrimPrefix(dir, "/umb/")], nil }}
	return u, fakes
}

func TestUmbrellaReleaseReview(t *testing.T) {
	u, f := rrUmbrella(t)
	q, err := u.ReviewQueue()
	if err != nil || len(q) == 0 || q[0].ID != "gh:1" || q[0].Repo != "gh" || q[len(q)-1].Repo != "gl" ||
		!strings.HasPrefix(q[len(q)-1].ID, "gl:") {
		t.Fatalf("queue %+v %v", q, err)
	}
	u.Scope = "gl"
	if q, _ := u.ReviewQueue(); len(q) == 0 || q[0].Repo != "gl" {
		t.Fatalf("--repo gl queue %+v", q)
	}

	u.Scope = ""
	if _, err := u.MergePR(20, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("merge without --repo: %v", err)
	}
	// Scope S: with no --repo, the working directory's sub-repo is the one.
	u.CwdRepo = "gl"
	if res, err := u.MergePR(25, nil); err != nil || len(res.Closed) != 2 || res.Closed[0].ID != "gl:1" ||
		!slices.Contains(f["gl"].merged, "25") || slices.Contains(f["gh"].merged, "25") {
		t.Fatalf("merge from the gl cwd: %+v %v", res, err)
	}
	if notes, err := u.ReleaseNotes("M07"); err != nil || len(notes) != 3 {
		t.Fatalf("notes from the gl cwd: %v %v", notes, err)
	}
	u.CwdRepo = ""
	u.Scope = "gl"
	if _, err := u.MergePR(20, []string{"gh:1"}); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "belongs to gh") {
		t.Fatalf("cross-repo item: %v", err)
	}
	res, err := u.MergePR(20, []string{"gl:1"})
	if err != nil || len(res.Closed) != 1 || res.Closed[0].ID != "gl:1" || !slices.Contains(f["gl"].merged, "20") || slices.Contains(f["gh"].merged, "20") {
		t.Fatalf("merge gl: %+v %v", res, err)
	}

	// gl has no tracking issue of its own; its gate and notes still read its milestone.
	if blocked, _, err := u.ReleaseGate("M07"); err != nil || len(blocked) == 0 {
		t.Fatalf("gate gl: %v %v", blocked, err)
	}
	// Closing out gl closes its native milestone only; the milestone ships with the last sub-repo.
	if _, changed, err := u.ReleaseClose("M07", "v1.0.0"); err != nil || !changed {
		t.Fatalf("close gl: %v %v", changed, err)
	}
	if f["gl"].MS.Native[0].State != "closed" || f["gh"].MS.Native[0].State != "open" || rrIssue(f["gh"], 3).State != "open" {
		t.Fatalf("after gl: gl %v gh %v tracker %v", f["gl"].MS.Native, f["gh"].MS.Native, rrIssue(f["gh"], 3).State)
	}
	u.Scope = "gh"
	if _, _, err := u.ReleaseClose("M07", "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	if f["gh"].MS.Native[0].State != "closed" || rrIssue(f["gh"], 3).State != "closed" {
		t.Fatalf("after gh: gh %v tracker %v", f["gh"].MS.Native, rrIssue(f["gh"], 3).State)
	}
	if _, changed, err := u.ReleaseClose("M07", "v1.0.0"); err != nil || changed {
		t.Fatalf("re-run: changed %v %v", changed, err)
	}
}
