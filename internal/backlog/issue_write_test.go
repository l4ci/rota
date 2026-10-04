package backlog

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
)

func newIssues(t *testing.T, cfg string, issues ...Issue) (*Issues, *fakeTracker) {
	t.Helper()
	tr := &fakeTracker{Issues: issues, Milestones: []string{"M07 — Title", "M09"}}
	return &Issues{Cfg: mustDecode(t, cfg), Tracker: tr}, tr
}

// calls renders the recorded calls as "method(args)" lines.
func calls(tr *fakeTracker) string {
	var out []string
	for _, c := range tr.Calls {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.Encode(c.Args)
		out = append(out, c.Method+strings.TrimSpace(buf.String()))
	}
	return strings.Join(out, " ")
}

func wantCalls(t *testing.T, tr *fakeTracker, want string) {
	t.Helper()
	if got := calls(tr); got != want {
		t.Errorf("tracker calls\n got: %s\nwant: %s", got, want)
	}
}

func open(n int, labels ...string) Issue {
	return Issue{Number: n, Title: "Item", State: "open", Labels: labels}
}

func TestIssuesCreate(t *testing.T) {
	b, tr := newIssues(t, `{}`)
	res, err := b.Create(CreateInput{Kind: "bugs", Title: " Fix  it ", Tag: "P1", Desc: " why ",
		Fields: []Field{{"Milestone", "M07"}, {"Related", "F1"}}, Body: []byte("\nSee {ID}\n"), HasBody: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "1" || res.Type != "B" || res.Detail != "" {
		t.Fatalf("result %+v", res)
	}
	body := "why\n\nSee {ID}\n\n<!-- rota:fields\nRelated: F1\n-->"
	fixed := strings.ReplaceAll(body, "{ID}", "B1")
	wantCalls(t, tr, `find_milestone["M07"] ensure_labels[["type:bug","p1"],true] `+
		`create["Fix it","`+strings.ReplaceAll(body, "\n", `\n`)+`",["type:bug","p1"],"M07 — Title"] `+
		`edit[1,{"body":"`+strings.ReplaceAll(fixed, "\n", `\n`)+`"}]`)
	if got := tr.Issues[0]; got.Body != fixed || got.Milestone != "M07 — Title" {
		t.Fatalf("issue %+v", got)
	}

	// Features carry the size label, tasks the type label only; no {ID} means no edit.
	b, tr = newIssues(t, `{"issues": {"autoCreateLabel": false}}`)
	if _, err := b.Create(CreateInput{Kind: "features", Title: "F", Tag: "Major"}); err != nil {
		t.Fatal(err)
	}
	wantCalls(t, tr, `ensure_labels[["type:feature","size:Major"],false] create["F","",["type:feature","size:Major"],""]`)
}

func TestIssuesCreateErrors(t *testing.T) {
	b, tr := newIssues(t, `{}`)
	for name, c := range map[string]struct {
		in   CreateInput
		want error
	}{
		"bad kind":       {CreateInput{Kind: "x", Title: "t"}, ErrInvalid},
		"no title":       {CreateInput{Kind: "bugs", Title: " "}, ErrInvalid},
		"bad tag":        {CreateInput{Kind: "bugs", Title: "t", Tag: "Major"}, ErrInvalid},
		"bad field":      {CreateInput{Kind: "bugs", Title: "t", Fields: []Field{{"Nope", "x"}}}, ErrInvalid},
		"milestone fmt":  {CreateInput{Kind: "bugs", Title: "t", Fields: []Field{{"Milestone", "7"}}}, ErrInvalid},
		"milestone gone": {CreateInput{Kind: "bugs", Title: "t", Fields: []Field{{"Milestone", "M99"}}}, ErrNotFound},
	} {
		if _, err := b.Create(c.in); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	for _, c := range tr.Calls {
		if c.Method == "create" {
			t.Fatalf("a rejected capture created an issue: %v", c)
		}
	}
	if _, err := (&Issues{Cfg: mustDecode(t, `{}`)}).Create(CreateInput{Kind: "bugs", Title: "t"}); err == nil {
		t.Fatal("nil tracker must be an error")
	}
}

func TestIssuesSetField(t *testing.T) {
	blocked := Issue{Number: 2, Title: "T", State: "open", Body: "text\n\n<!-- rota:fields\nRelated: B1\nRepos: web\n-->", Milestone: "M07 — Title"}
	closed := Issue{Number: 3, Title: "T", State: "closed"}
	for _, c := range []struct {
		name, ref, field, value string
		changed                 bool
		err                     error
		calls                   string
	}{
		{"milestone same", "2", "milestone", "M07", false, nil, `get[2] find_milestone["M07"]`},
		{"milestone set", "1", "Milestone", " M09 ", true, nil, `get[1] find_milestone["M09"] edit[1,{"milestone":"M09"}]`},
		{"milestone clear", "2", "milestone", "", true, nil, `get[2] edit[2,{"remove_milestone":true}]`},
		{"milestone clear none", "1", "milestone", "", false, nil, `get[1]`},
		{"milestone bad", "1", "milestone", "x", false, ErrInvalid, `get[1]`},
		{"milestone gone", "1", "milestone", "M42", false, ErrNotFound, `get[1] find_milestone["M42"]`},
		{"block same", "2", "related", "B1", false, nil, `get[2]`},
		{"block replace keeps order", "2", "related", "F5,  B6", true, nil,
			`get[2] edit[2,{"body":"text\n\n<!-- rota:fields\nRelated: F5, B6\nRepos: web\n-->"}]`},
		{"block clear", "2", "repos", "", true, nil, `get[2] edit[2,{"body":"text\n\n<!-- rota:fields\nRelated: B1\n-->"}]`},
		{"block new", "1", "subsystem", "capture", true, nil, `get[1] edit[1,{"body":"<!-- rota:fields\nSubsystem: capture\n-->"}]`},
		{"detail", "1", "detail", "x", false, ErrInvalid, ``},
		{"unknown", "99", "related", "x", false, ErrNotFound, `get[99]`},
		{"closed", "3", "related", "x", false, ErrClosed, `get[3]`},
	} {
		b, tr := newIssues(t, `{}`, open(1), blocked, closed)
		got, err := b.SetField(c.ref, c.field, c.value)
		if !errors.Is(err, c.err) || (c.err == nil && err != nil) || got != c.changed {
			t.Errorf("%s: %v, %v; want %v, %v", c.name, got, err, c.changed, c.err)
		}
		wantCalls(t, tr, c.calls)
	}
}

func TestIssuesComplete(t *testing.T) {
	stale := open(1, "in-progress", "blocked", "keep")
	proof := Issue{Number: 2, Title: "T", State: "open"}
	b, tr := newIssues(t, `{}`, stale, proof, Issue{Number: 3, State: "closed"}, open(4, "blocked"))
	tr.Issues[1].Comments = nil
	// done, no proof
	_, err := b.Complete("1", CompleteInput{Commit: "abc", Reason: "done"})
	var ref *RefusedError
	if !errors.Is(err, ErrProofMissing) || !errors.As(err, &ref) || ref.BlockedBy != "proof missing" {
		t.Fatalf("no proof: %v", err)
	}
	wantCalls(t, tr, `get[1] comments[1]`)

	tr.Calls = nil
	if ok, err := b.Complete("1", CompleteInput{Commit: "abc", Reason: "done", NoProof: true, Note: "a\nb  c"}); !ok || err != nil {
		t.Fatal(ok, err)
	}
	wantCalls(t, tr, `get[1] remove_labels[1,["in-progress","blocked"]] close[1,"completed","Done in `+"`abc`"+` — a b c\n\n<!-- rota:done -->"]`)
	if is := tr.Issues[0]; is.State != "closed" || is.StateReason != "completed" || strings.Join(is.Labels, ",") != "keep" {
		t.Fatalf("%+v", is)
	}

	// done with a proof note: two rows in its Proof section.
	tr.Calls = nil
	tr.Issues[1].Comments = []tracker.Comment{{ID: "7", Body: "<!-- rota:proof -->\n## Proof\n- build · PASS · ok\n- tests · PASS · ok\n"}}
	if ok, err := b.Complete("#2", CompleteInput{Commit: "def", Reason: "done"}); !ok || err != nil {
		t.Fatal(ok, err)
	}
	wantCalls(t, tr, `get[2] comments[2] close[2,"completed","Done in `+"`def`"+`\n\n<!-- rota:done -->"]`)

	// dropped and handed-off close as not planned and skip the proof gate.
	for _, reason := range []string{"dropped", "handed-off"} {
		b, tr := newIssues(t, `{}`, open(5))
		if ok, err := b.Complete("5", CompleteInput{Commit: "x", Reason: reason, Note: "n"}); !ok || err != nil {
			t.Fatal(ok, err)
		}
		wantCalls(t, tr, `get[5] close[5,"not_planned","Closed: `+reason+` — n\n\n<!-- rota:closed -->"]`)
	}

	// blocked: label plus comment, no proof gate; blocked again is a no-op.
	b, tr = newIssues(t, `{}`, open(6))
	if ok, err := b.Complete("6", CompleteInput{Reason: "blocked", Note: "waiting"}); !ok || err != nil {
		t.Fatal(ok, err)
	}
	wantCalls(t, tr, `get[6] add_labels[6,["blocked"],true] add_comment[6,"Blocked — waiting\n\n<!-- rota:blocked -->"]`)
	tr.Calls = nil
	if ok, _ := b.Complete("6", CompleteInput{Reason: "blocked"}); ok {
		t.Fatal("already blocked must be a no-op")
	}
	wantCalls(t, tr, `get[6]`)

	// already closed, unknown, wrong letter
	b, tr = newIssues(t, `{}`, Issue{Number: 3, State: "closed"})
	if ok, err := b.Complete("3", CompleteInput{Reason: "done"}); ok || err != nil {
		t.Fatal(ok, err)
	}
	for _, ref := range []string{"9", "F3", "x"} {
		if _, err := b.Complete(ref, CompleteInput{Reason: "dropped"}); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v", ref, err)
		}
	}
}

func TestIssuesCompleteProofSeam(t *testing.T) {
	b, tr := newIssues(t, `{}`, open(1))
	var asked string
	b.ProofCount = func(id string) (int, error) { asked = id; return 1, nil }
	if ok, err := b.Complete("1", CompleteInput{Commit: "c", Reason: "done"}); !ok || err != nil || asked != "T1" {
		t.Fatal(ok, err, asked)
	}
	wantCalls(t, tr, `get[1] close[1,"completed","Done in `+"`c`"+`\n\n<!-- rota:done -->"]`)
}

func TestIssuesReopen(t *testing.T) {
	b, tr := newIssues(t, `{}`, Issue{Number: 1, State: "closed", Labels: []string{"not-planned", "blocked", "x"}},
		open(2, "blocked"), open(3, "x"))
	for _, c := range []struct {
		ref     string
		changed bool
		calls   string
	}{
		{"1", true, `get[1] reopen[1] remove_labels[1,["not-planned","blocked"]]`},
		{"2", true, `get[2] remove_labels[2,["blocked"]]`},
		{"3", false, `get[3]`},
	} {
		tr.Calls = nil
		if got, err := b.Reopen(c.ref); got != c.changed || err != nil {
			t.Errorf("%s: %v, %v", c.ref, got, err)
		}
		wantCalls(t, tr, c.calls)
	}
	if _, err := b.Reopen("9"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestIssuesReady(t *testing.T) {
	crit := Issue{Number: 1, State: "open", Body: "## Acceptance\n- [ ] ok"}
	bare := open(2)
	noted := open(3)
	noted.Comments = []tracker.Comment{{ID: "1", Body: "<!-- rota:plan -->\nsteps"}}
	b, tr := newIssues(t, `{}`, crit, bare, noted)
	tr.Issues[2].Comments = noted.Comments
	for _, c := range []struct {
		ref   string
		ready bool
		calls string
	}{
		{"1", true, `get[1] comments[1] comments[1]`},
		{"2", false, `get[2] comments[2] comments[2]`},
		{"3", true, `get[3] comments[3] comments[3]`},
	} {
		tr.Calls = nil
		got, err := b.Ready(c.ref)
		if err != nil || (len(got) == 0) != c.ready {
			t.Errorf("%s: %v, %v", c.ref, got, err)
		}
		wantCalls(t, tr, c.calls)
	}
}

func TestIssuesComments(t *testing.T) {
	b, tr := newIssues(t, `{}`, open(1))
	if _, err := b.AddComment("F1", "bogus", "x"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	id, err := b.AddComment("#1", "question", "Why?\r\nBecause.\n\n")
	if err != nil || id == "" {
		t.Fatal(id, err)
	}
	wantCalls(t, tr, `add_comment[1,"<!-- rota:comment question -->\nWhy?\nBecause."]`)
	tr.Issues[0].Comments = append(tr.Issues[0].Comments,
		tracker.Comment{ID: "9", Body: "plain chatter"}, tracker.Comment{ID: "10", Body: "<!-- rota:comment answer -->\n\nA\n\n", Author: "bob"})
	rows, err := b.Comments("1", "")
	if err != nil || len(rows) != 2 || rows[0] != (Comment{"fake-user", "question", "Why?\nBecause."}) || rows[1] != (Comment{"bob", "answer", "A"}) {
		t.Fatalf("%+v, %v", rows, err)
	}
	if rows, _ = b.Comments("1", "answer"); len(rows) != 1 {
		t.Fatalf("%+v", rows)
	}
	if _, err := b.Comments("1", "nope"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := b.Comments("5", ""); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestIssuesNotes(t *testing.T) {
	b, tr := newIssues(t, `{}`, open(1))
	if _, ok, err := b.NoteGet("1", "design"); ok || err != nil {
		t.Fatal(ok, err)
	}
	if changed, err := b.NotePut("1", "design", "line one\r\nline two\n\n"); !changed || err != nil {
		t.Fatal(changed, err)
	}
	if text, ok, _ := b.NoteGet("1", "design"); !ok || text != "line one\nline two" {
		t.Fatalf("%q", text)
	}
	tr.Calls = nil
	if changed, _ := b.NotePut("1", "design", "line one\nline two"); changed {
		t.Fatal("identical text must not write")
	}
	wantCalls(t, tr, `comments[1]`)
	if changed, _ := b.NotePut("1", "design", "other"); !changed {
		t.Fatal("changed text must write")
	}
	if changed, _ := b.NoteRm("1", "design"); !changed {
		t.Fatal("rm")
	}
	if changed, _ := b.NoteRm("1", "design"); changed {
		t.Fatal("rm twice")
	}
	for _, kind := range []string{"x", "plan:S", "plan:1"} {
		if _, err := b.NotePut("1", kind, "t"); !errors.Is(err, ErrInvalid) {
			t.Errorf("kind %q: %v", kind, err)
		}
	}
	if _, err := b.NotePut("1", "plan:S01", "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.NotePut("zz", "plan", "t"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestIssuesNoteSplit(t *testing.T) {
	t.Setenv("ROTA_NOTE_LIMIT", "100")
	b, tr := newIssues(t, `{}`, open(1))
	long := strings.Repeat("0123456789 abcdefghij\n", 12) + strings.Repeat("x", 250)
	if _, err := b.NotePut("1", "plan", long); err != nil {
		t.Fatal(err)
	}
	n := len(tr.Issues[0].Comments)
	if n < 3 {
		t.Fatalf("%d parts", n)
	}
	for i, c := range tr.Issues[0].Comments {
		if !strings.HasPrefix(c.Body, "<!-- rota:plan "+string(rune('1'+i))+"/"+string(rune('0'+n))+" -->\n") || len([]rune(c.Body)) > 100 {
			t.Fatalf("part %d: %q", i, c.Body)
		}
	}
	if got, _, _ := b.NoteGet("1", "plan"); got != strings.TrimRight(long, "\n") {
		t.Fatalf("round trip lost text: %q", got)
	}
	// shrinking deletes the surplus parts and edits the first in place
	tr.Calls = nil
	if changed, _ := b.NotePut("1", "plan", "short"); !changed || len(tr.Issues[0].Comments) != 1 {
		t.Fatalf("%d parts left", len(tr.Issues[0].Comments))
	}
	if tr.Calls[1].Method != "edit_comment" || tr.Calls[len(tr.Calls)-1].Method != "delete_comment" {
		t.Fatalf("calls %s", calls(tr))
	}
	// a line longer than a part is cut; an unparsable limit falls back to the default
	t.Setenv("ROTA_NOTE_LIMIT", "abc")
	if noteLimit() != noteLimitDefault {
		t.Fatal(noteLimit())
	}
	t.Setenv("ROTA_NOTE_LIMIT", "5")
	if noteLimit() != 80 {
		t.Fatal(noteLimit())
	}
}

func TestIssuesClaimReleaseState(t *testing.T) {
	b, tr := newIssues(t, `{}`, open(1, "needs-review", "keep"), Issue{Number: 2, State: "closed"})
	won, holder, err := b.Claim("1", "alice")
	if !won || holder != "alice" || err != nil {
		t.Fatal(won, holder, err)
	}
	wantCalls(t, tr, `get[1] comments[1] add_comment[1,"<!-- rota:claim alice -->\nClaimed by alice"] comments[1] `+
		`ensure_labels[["in-progress"],true] edit[1,{"add_labels":["in-progress"],"remove_labels":["needs-review"]}] assign_self[1]`)
	if is := tr.Issues[0]; strings.Join(is.Labels, ",") != "keep,in-progress" || len(is.Assignees) != 1 {
		t.Fatalf("%+v", is)
	}
	// the holder claiming again posts nothing and writes nothing
	tr.Calls = nil
	if won, _, _ := b.Claim("#1", "alice"); !won {
		t.Fatal("holder lost its own claim")
	}
	wantCalls(t, tr, `get[1] comments[1]`)
	// another claim loses, posting claim and release
	tr.Calls = nil
	won, holder, err = b.Claim("1", "bob")
	if won || holder != "alice" || err != nil {
		t.Fatal(won, holder, err)
	}
	wantCalls(t, tr, `get[1] comments[1] add_comment[1,"<!-- rota:claim bob -->\nClaimed by bob"] comments[1] add_comment[1,"<!-- rota:release bob -->"]`)
	if _, _, err := b.Claim("2", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("closed: %v", err)
	}
	// release: a stranger changes nothing; the holder posts the marker and drops the label
	tr.Calls = nil
	if ok, _ := b.Release("1", "carol"); ok {
		t.Fatal("stranger release")
	}
	wantCalls(t, tr, `get[1] comments[1]`)
	tr.Calls = nil
	if ok, _ := b.Release("1", "alice"); !ok {
		t.Fatal("release")
	}
	wantCalls(t, tr, `get[1] comments[1] add_comment[1,"<!-- rota:release alice -->"] remove_labels[1,["in-progress"]]`)

	// state: one edit leaves exactly one state label
	tr.Calls = nil
	if ok, err := b.SetState("1", "changes-requested"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	wantCalls(t, tr, `get[1] ensure_labels[["changes-requested"],true] edit[1,{"add_labels":["changes-requested"]}]`)
	tr.Calls = nil
	if ok, _ := b.SetState("1", "changes-requested"); ok {
		t.Fatal("same state must not write")
	}
	if ok, _ := b.SetState("1", "none"); !ok {
		t.Fatal("none")
	}
	if _, err := b.SetState("1", "bogus"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestIssuesStatus(t *testing.T) {
	is := Issue{Number: 4, Title: "**Do** it", State: "open", Labels: []string{"type:feature", "in-progress", "blocked"},
		Milestone: "M07 — Title", Assignees: []string{"alice"}}
	b, tr := newIssues(t, `{}`, is)
	tr.Issues[0].Comments = []tracker.Comment{
		{ID: "1", Body: "<!-- rota:claim a -->\nClaimed by a"},
		{ID: "2", Body: "<!-- rota:claim b -->\nClaimed by b"},
		{ID: "3", Body: "<!-- rota:design -->\nD"},
		{ID: "4", Body: "<!-- rota:plan 1/2 -->\nP"},
		{ID: "5", Body: "<!-- rota:design -->\nD2"},
		{ID: "6", Body: "<!-- rota:comment feedback -->\nnice", Author: "eve"},
		{ID: "7", Body: "<!-- rota:release a -->"},
	}
	st, err := b.Status("F4")
	if err != nil {
		t.Fatal(err)
	}
	want := Status{ID: "4", Type: "F", Title: "Do it", Status: "open", State: "in-progress,blocked", Claim: "b",
		Assignees: []string{"alice"}, Milestone: "M07", Notes: []string{"design", "plan"},
		Comments: []Comment{{"eve", "feedback", "nice"}}}
	if a, w := mustJSON(st), mustJSON(want); a != w {
		t.Fatalf("\n got %s\nwant %s", a, w)
	}
	wantCalls(t, tr, `get[4] comments[4]`)
}

func mustJSON(v any) string { raw, _ := json.Marshal(v); return string(raw) }

// A missing issue is the adapter's KindNotFound, and any other failure passes through.
func TestIssuesTrackerErrors(t *testing.T) {
	b, tr := newIssues(t, `{}`, open(1))
	if _, err := b.Get("7"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	boom := &tracker.Error{Kind: tracker.KindRateLimited, Code: 4, Message: "secondary rate limit"}
	tr.Fail = map[string]error{"get": boom}
	for _, call := range []func() error{
		func() error { _, err := b.Get("1"); return err },
		func() error { _, err := b.Complete("1", CompleteInput{Reason: "dropped"}); return err },
		func() error { _, _, err := b.Claim("1", "a"); return err },
		func() error { _, err := b.Status("1"); return err },
	} {
		if err := call(); !tracker.IsKind(err, tracker.KindRateLimited) || errors.Is(err, ErrNotFound) {
			t.Errorf("%v", err)
		}
	}
	tr.Fail = map[string]error{"close": boom}
	if _, err := b.Complete("1", CompleteInput{Reason: "dropped"}); !tracker.IsKind(err, tracker.KindRateLimited) {
		t.Fatal(err)
	}
}

func TestWorkflowFileMode(t *testing.T) {
	f := &File{Root: t.TempDir()}
	for _, err := range []error{
		func() error { _, err := f.Status("B01"); return err }(),
		func() error { _, _, err := f.NoteGet("B01", "plan"); return err }(),
		func() error { _, err := f.NotePut("B01", "plan", "x"); return err }(),
		func() error { _, err := f.NoteRm("B01", "plan"); return err }(),
	} {
		var ref *RefusedError
		if !errors.Is(err, ErrWrongBackend) || !errors.As(err, &ref) || ref.BlockedBy != "backend" || ref.Hint == "" {
			t.Errorf("%v", err)
		}
	}
}
