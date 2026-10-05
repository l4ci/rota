package backlog

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/golden"
	"github.com/l4ci/rota/internal/tracker"
)

// wStep is one backend call; recorded as golden input; the Python IssueBackend ran it.
type wStep struct {
	Op      string      `json:"op"`
	Ref     string      `json:"ref"`
	Kind    string      `json:"kind"`
	Text    string      `json:"text"`
	Field   string      `json:"field"`
	Value   string      `json:"value"`
	ID      string      `json:"id"`
	Reason  string      `json:"reason"`
	Note    string      `json:"note"`
	State   string      `json:"state"`
	Title   string      `json:"title"`
	Tag     string      `json:"tag"`
	Desc    string      `json:"desc"`
	NoProof bool        `json:"noproof"`
	Fields  [][2]string `json:"fields"`
	Body    *string     `json:"body"`
}

type wSeedIssue struct {
	Number      int      `json:"number"`
	Title       string   `json:"title"`
	Body        string   `json:"body"`
	Labels      []string `json:"labels"`
	Milestone   string   `json:"milestone"`
	State       string   `json:"state"`
	StateReason string   `json:"state_reason"`
	Assignees   []string `json:"assignees"`
}

type wSeedComment struct {
	Issue int    `json:"issue"`
	Body  string `json:"body"`
}

type wScenario struct {
	Cfg        string         `json:"cfg"`
	Limit      string         `json:"limit"`
	Issues     []wSeedIssue   `json:"issues"`
	Comments   []wSeedComment `json:"comments"`
	Milestones []string       `json:"milestones"`
	Steps      []wStep        `json:"steps"`
}

// writeErrKind is the normalized error class recorded with the Python harness.
func writeErrKind(err error) string {
	var te *tracker.Error
	switch {
	case errors.As(err, &te):
		return "tracker:" + strconv.Itoa(te.Code)
	case errors.Is(err, ErrProofMissing):
		return "proofmissing"
	case errors.Is(err, ErrNotFound) && strings.Contains(err.Error(), "not found on the tracker"):
		// Python raises TrackerError(1) for an unknown milestone; Go wraps ErrNotFound (exit 3 either way).
		return "tracker:1"
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrClosed):
		// Python raises LookupError for a closed item too; Go refuses it (ErrClosed).
		return "notfound"
	case errors.Is(err, ErrInvalid):
		return "invalid"
	}
	return "error:" + err.Error()
}

var statusType = map[string]string{"B": "bug", "F": "feature", "T": "task"}

func goStep(b *Issues, st wStep) map[string]any {
	ok := func(v any) map[string]any { return map[string]any{"k": "ok", "v": v} }
	bad := func(err error) map[string]any { return map[string]any{"k": writeErrKind(err)} }
	switch st.Op {
	case "create":
		in := CreateInput{Kind: st.Kind, Title: st.Title, Tag: st.Tag, Desc: st.Desc}
		for _, f := range st.Fields {
			in.Fields = append(in.Fields, Field{f[0], f[1]})
		}
		if st.Body != nil {
			in.Body, in.HasBody = []byte(*st.Body), true
		}
		r, err := b.Create(in)
		if err != nil {
			return bad(err)
		}
		return ok(r.Type + r.ID)
	case "set_field":
		v, err := b.SetField(st.Ref, st.Field, st.Value)
		if err != nil {
			return bad(err)
		}
		return ok(v)
	case "complete":
		v, err := b.Complete(st.Ref, CompleteInput{Commit: "abc1234", Date: "2026-01-02", Reason: st.Reason, Note: st.Note, NoProof: st.NoProof})
		if err != nil {
			return bad(err)
		}
		return ok(v)
	case "reopen":
		v, err := b.Reopen(st.Ref)
		if err != nil {
			return bad(err)
		}
		return ok(v)
	case "ready":
		v, err := b.Ready(st.Ref)
		if err != nil {
			return bad(err)
		}
		return ok(v)
	case "comments":
		v, err := b.Comments(st.Ref, st.Kind)
		if err != nil {
			return bad(err)
		}
		rows := []any{}
		for _, c := range v {
			rows = append(rows, map[string]any{"kind": c.Kind, "who": c.Who, "text": c.Text})
		}
		return ok(rows)
	case "comment_add":
		v, err := b.AddComment(st.Ref, st.Kind, st.Text)
		if err != nil {
			return bad(err)
		}
		return ok(v)
	case "note_get":
		v, found, err := b.NoteGet(st.Ref, st.Kind)
		if err != nil {
			return bad(err)
		}
		if !found {
			return ok(nil)
		}
		return ok(v)
	case "note_put":
		v, err := b.NotePut(st.Ref, st.Kind, st.Text)
		if err != nil {
			return bad(err)
		}
		return ok(v)
	case "note_rm":
		v, err := b.NoteRm(st.Ref, st.Kind)
		if err != nil {
			return bad(err)
		}
		return ok(v)
	case "claim":
		won, holder, err := b.Claim(st.Ref, st.ID)
		if err != nil {
			return bad(err)
		}
		return ok([]any{won, holder})
	case "release":
		v, err := b.Release(st.Ref, st.ID)
		if err != nil {
			return bad(err)
		}
		return ok(v)
	case "set_state":
		v, err := b.SetState(st.Ref, st.State)
		if err != nil {
			return bad(err)
		}
		return ok(v)
	case "status":
		s, err := b.Status(st.Ref)
		if err != nil {
			return bad(err)
		}
		rows := []any{}
		for _, c := range s.Comments {
			rows = append(rows, map[string]any{"kind": c.Kind, "who": c.Who, "text": c.Text})
		}
		return ok(map[string]any{"id": s.Type + s.ID, "title": s.Title, "type": statusType[s.Type], "status": s.Status,
			"state": s.State, "claim": s.Claim, "assignees": s.Assignees, "milestone": s.Milestone,
			"notes": s.Notes, "comments": rows})
	}
	panic("bad op " + st.Op)
}

func goRunScenario(t *testing.T, s wScenario) map[string]any {
	t.Helper()
	t.Setenv("ROTA_NOTE_LIMIT", s.Limit)
	f := &trackertest.Fake{Milestones: s.Milestones}
	for _, i := range s.Issues {
		f.Issues = append(f.Issues, tracker.Issue{Number: i.Number, Title: i.Title, Body: i.Body, Labels: append([]string{}, i.Labels...),
			Milestone: i.Milestone, State: i.State, StateReason: i.StateReason, Assignees: append([]string{}, i.Assignees...),
			URL: fmt.Sprintf("https://example.test/issues/%d", i.Number)})
	}
	for _, c := range s.Comments {
		if _, err := f.AddComment(context.Background(), c.Issue, c.Body); err != nil {
			t.Fatal(err)
		}
	}
	f.Calls = nil
	b := &Issues{Cfg: mustDecode(t, s.Cfg), Tracker: f}
	var steps []any
	for _, st := range s.Steps {
		f.Calls = nil
		r := goStep(b, st)
		calls := []any{}
		for _, c := range f.Calls {
			calls = append(calls, map[string]any{"method": c.Method, "args": c.Args})
		}
		steps = append(steps, map[string]any{"result": r, "calls": calls})
	}
	final := []any{}
	for _, i := range f.Issues {
		cs := []any{}
		for _, c := range i.Comments {
			cs = append(cs, []any{c.ID, c.Body})
		}
		as := append([]string{}, i.Assignees...)
		final = append(final, []any{i.Number, i.Title, i.Body, i.Labels, i.Milestone, i.State, i.StateReason, as, cs})
	}
	return map[string]any{"steps": steps, "final": final}
}

func seedIssues() []wSeedIssue {
	return []wSeedIssue{
		{1, "Add export", "Export it.\n\n## Acceptance\n- [ ] works\n\n<!-- rota:fields\nRelated: B9\n-->", []string{"type:feature", "size:Major"}, "M07 — Seven", "open", "", nil},
		{2, "Crash on start", "plain", []string{"type:bug", "p1", "in-progress"}, "", "open", "", []string{"fake-user"}},
		{3, "M07 tracking", "", []string{"milestone-tracker"}, "", "open", "", nil},
		{4, "Old task", "done", []string{"type:task"}, "", "closed", "completed", nil},
		{5, "Dropped bug", "x", []string{"type:bug", "blocked", "not-planned"}, "", "closed", "not_planned", nil},
		{6, "No type", "line\r\n- [x] done\r\n", nil, "M01 — Alpha", "open", "", nil},
		{7, "Review me", "body", []string{"type:feature", "needs-review", "blocked"}, "", "open", "", nil},
		{8, "Fielded", "Text\n\n<!-- rota:fields\nSubsystem: web\nOther: keep\nRepos: a\n-->", []string{"type:task"}, "", "open", "", nil},
	}
}

func seedComments() []wSeedComment {
	return []wSeedComment{
		{1, "<!-- rota:proof -->\n## Proof\n- build · PASS · ok\n- tests · PASS · ok"},
		{1, "<!-- rota:design 2/2 -->\nB part\n"},
		{1, "<!-- rota:design 1/2 -->\nA part\n"},
		{1, "<!-- rota:claim agent-a -->\nClaimed by agent-a"},
		{1, "<!-- rota:comment question -->\nWhy?\nmore"},
		{1, "random chatter"},
		{2, "<!-- rota:claim agent-b -->\nClaimed by agent-b"},
		{2, "<!-- rota:claim agent-a -->\nClaimed by agent-a"},
		{2, "<!-- rota:release agent-b -->"},
		{2, "<!-- rota:comment answer -->\r\nBecause."},
		{6, "<!-- rota:plan:S01 -->\nplan one"},
		{7, "<!-- rota:proof -->\n## Proof\nnone yet"},
		{7, "<!-- rota:plan -->\nthe plan\r\n"},
	}
}

// translate renames the seed labels for a config that renames them.
func translate(s wScenario, repl map[string]string) wScenario {
	out := s
	out.Issues = nil
	for _, i := range s.Issues {
		var ls []string
		for _, l := range i.Labels {
			if r, ok := repl[l]; ok {
				l = r
			}
			ls = append(ls, l)
		}
		i.Labels = ls
		out.Issues = append(out.Issues, i)
	}
	return out
}

var writeRefs = []string{"1", "#2", "F1", "B2", "T6", "7", "F7", "8", "99", "x", "3", "4", "5", "b2", "T2", "6", "1", "2", "7", "8"}

func genStep(rng *rand.Rand) wStep {
	ops := []string{"create", "set_field", "complete", "reopen", "ready", "comments", "comment_add", "note_get", "note_put", "note_rm", "claim", "release", "set_state", "status"}
	st := wStep{Op: pick(rng, ops), Ref: pick(rng, writeRefs)}
	switch st.Op {
	case "create":
		st.Kind = pick(rng, []string{"bugs", "features", "tasks", "bugs", "features", "stories"})
		st.Title = pick(rng, []string{"New thing", "  spaced   title ", "Ends!", "", "Café — ü*"})
		switch st.Kind {
		case "bugs":
			st.Tag = pick(rng, []string{"", "P1", "P3", "P9"})
		case "features":
			st.Tag = pick(rng, []string{"", "Major", "Cosmetic", "huge"})
		default:
			st.Tag = pick(rng, []string{"", "", "P1"})
		}
		st.Desc = pick(rng, []string{"", "Short desc.", "  padded  "})
		fieldSets := [][][2]string{nil, {{"Milestone", "M07"}}, {{"Milestone", "M99"}}, {{"Milestone", "7"}},
			{{"Related", "B1, F2"}, {"Subsystem", "web"}}, {{"Bogus", "x"}}, {{"Repos", ""}}, {{"Milestone", "M01"}, {"Captured", "2026-05-09"}},
			{{"Related", "a"}, {"Related", "b"}}}
		st.Fields = fieldSets[rng.Intn(len(fieldSets))]
		if rng.Intn(2) == 0 {
			st.Body = ptr(pick(rng, []string{"Body {ID} text\n", "# {ID}\n\n- [ ] ac\n", "ünï\r\ntext", "\n\nx\n\n", ""}))
		}
	case "set_field":
		st.Field = pick(rng, []string{"milestone", "related", "repos", "subsystem", "detail", "Related", "bogus", "milestone", "related"})
		st.Value = pick(rng, []string{"M07", "M01", "", "M99", "bad", "B1, F2", "  web  ", "x\ny", "B9"})
	case "complete":
		st.Reason = pick(rng, []string{"done", "done", "dropped", "handed-off", "blocked", "other"})
		st.NoProof = rng.Intn(3) == 0
		st.Note = pick(rng, []string{"", "ship it", "multi\nline  note"})
	case "comment_add":
		st.Kind = pick(rng, []string{"question", "answer", "decision", "feedback", "bogus"})
		st.Text = pick(rng, []string{"hello", "multi\r\nline\n\n", "ünï", " "})
	case "note_get", "note_put", "note_rm":
		st.Kind = pick(rng, []string{"proof", "design", "plan", "plan:S01", "plan:S1x", "bogus", "design"})
		st.Text = pick(rng, []string{"## Proof\n- a\n- b", "plain text\n\n", "", "x\r\ny\rz", longText(rng)})
	case "claim", "release":
		st.ID = pick(rng, []string{"agent-a", "agent-b", "w1"})
	case "set_state":
		st.State = pick(rng, []string{"in-progress", "needs-review", "changes-requested", "none", "bogus"})
	case "comments":
		st.Kind = pick(rng, []string{"", "question", "answer", "bogus"})
	}
	return st
}

func ptr(s string) *string { return &s }

func longText(rng *rand.Rand) string {
	var b strings.Builder
	for i := 0; i < 3+rng.Intn(8); i++ {
		b.WriteString(strings.Repeat(pick(rng, []string{"word ", "é", "ü€ ", "abc"}), 1+rng.Intn(12)))
		b.WriteString(pick(rng, []string{"\n", "\n", "\n\n", "\r\n", "\r"}))
	}
	return b.String()
}

func noteStep(op, kind, text string) wStep {
	return wStep{Op: op, Ref: "1", Kind: kind, Text: text}
}

func handcrafted() []wScenario {
	base := func(limit string, steps ...wStep) wScenario {
		return wScenario{Cfg: defaultCfg, Limit: limit, Issues: seedIssues(), Comments: seedComments(), Milestones: []string{"M07 — Seven", "M01 — Alpha"}, Steps: steps}
	}
	long := strings.Repeat("alpha beta é gamma\n", 30) + "last line without newline"
	oneLine := strings.Repeat("x", 300)
	var out []wScenario
	// 82, not 80: the limit was 80 when the marker was "<!-- hv:" (#236); the
	// two extra runes of "<!-- rota:" keep the recorded chunking.
	out = append(out, base("82",
		noteStep("note_put", "proof", long), noteStep("note_get", "proof", ""), noteStep("note_put", "proof", long),
		noteStep("note_put", "proof", "short\n"), noteStep("note_get", "proof", ""),
		noteStep("note_put", "proof", long+long), noteStep("note_put", "proof", oneLine), noteStep("note_get", "proof", ""),
		noteStep("note_rm", "proof", ""), noteStep("note_rm", "proof", ""),
		noteStep("note_put", "plan:S01", long), noteStep("note_put", "plan:S02", "a\rb\r\nc\n\n"), noteStep("note_get", "plan:S01", ""),
		noteStep("note_put", "design", long), noteStep("note_put", "design", "tiny"), noteStep("note_get", "design", ""),
		noteStep("note_put", "design", "ü"+strings.Repeat("€", 100)), noteStep("note_get", "design", ""),
		noteStep("note_put", "proof", ""), noteStep("note_get", "proof", ""), noteStep("note_put", "plan", "a b\u0085c\fd\ve"+long),
		wStep{Op: "status", Ref: "1"}))
	out = append(out, base("",
		noteStep("note_put", "proof", long), noteStep("note_put", "proof", long), noteStep("note_put", "proof", "## Proof\n- one\n"),
		wStep{Op: "complete", Ref: "1", Reason: "done"}, wStep{Op: "complete", Ref: "1", Reason: "done"},
		wStep{Op: "reopen", Ref: "1"}, wStep{Op: "reopen", Ref: "1"}, wStep{Op: "status", Ref: "1"}))
	// claims
	out = append(out, base("",
		wStep{Op: "claim", Ref: "1", ID: "agent-a"}, wStep{Op: "claim", Ref: "1", ID: "agent-b"},
		wStep{Op: "release", Ref: "1", ID: "agent-b"}, wStep{Op: "release", Ref: "1", ID: "agent-a"}, wStep{Op: "status", Ref: "1"},
		wStep{Op: "claim", Ref: "2", ID: "agent-a"}, wStep{Op: "claim", Ref: "2", ID: "agent-c"}, wStep{Op: "claim", Ref: "8", ID: "w1"},
		wStep{Op: "claim", Ref: "8", ID: "w1"}, wStep{Op: "status", Ref: "8"}, wStep{Op: "release", Ref: "8", ID: "w1"},
		wStep{Op: "claim", Ref: "4", ID: "w1"}, wStep{Op: "claim", Ref: "3", ID: "w1"}, wStep{Op: "claim", Ref: "F8", ID: "w1"}))
	// blocked / complete variants
	out = append(out, base("",
		wStep{Op: "complete", Ref: "7", Reason: "blocked", Note: "waits"}, wStep{Op: "complete", Ref: "7", Reason: "blocked"},
		wStep{Op: "reopen", Ref: "7"}, wStep{Op: "complete", Ref: "7", Reason: "dropped", Note: "n"},
		wStep{Op: "complete", Ref: "7", Reason: "done"}, wStep{Op: "reopen", Ref: "7"}, wStep{Op: "reopen", Ref: "5"},
		wStep{Op: "complete", Ref: "2", Reason: "handed-off", Note: "a\nb"}, wStep{Op: "complete", Ref: "8", Reason: "done"},
		wStep{Op: "complete", Ref: "8", Reason: "done", NoProof: true}, wStep{Op: "complete", Ref: "3", Reason: "done", NoProof: true}))
	// create with {ID}
	out = append(out, base("",
		wStep{Op: "create", Kind: "bugs", Title: "Bug {ID}", Tag: "P2", Desc: "d", Fields: [][2]string{{"Milestone", "M07"}, {"Related", "F1"}}, Body: ptr("see {ID} and {ID}\n")},
		wStep{Op: "create", Kind: "features", Title: "Feat", Tag: "Minor"},
		wStep{Op: "create", Kind: "tasks", Title: "Task", Fields: [][2]string{{"Milestone", "M42"}}},
		wStep{Op: "set_field", Ref: "9", Field: "milestone", Value: "M01"}, wStep{Op: "set_field", Ref: "9", Field: "milestone", Value: ""},
		wStep{Op: "set_field", Ref: "9", Field: "related", Value: "B1"}, wStep{Op: "set_field", Ref: "9", Field: "related", Value: ""},
		wStep{Op: "set_field", Ref: "4", Field: "related", Value: "x"}, wStep{Op: "set_field", Ref: "3", Field: "related", Value: "x"}))
	// custom config
	custom := base("")
	custom.Cfg = customCfg
	custom = translate(custom, map[string]string{"type:bug": "kind/bug", "type:feature": "kind/feature", "type:task": "kind/task", "milestone-tracker": "tracker", "p1": "prio-1", "size:Major": "effort:L"})
	custom.Steps = []wStep{{Op: "status", Ref: "2"}, {Op: "status", Ref: "3"}, {Op: "create", Kind: "bugs", Title: "B", Tag: "P2"},
		{Op: "create", Kind: "features", Title: "F", Tag: "Major"}, {Op: "set_state", Ref: "1", State: "needs-review"}, {Op: "claim", Ref: "1", ID: "me"}}
	out = append(out, custom)
	wip := base("")
	wip.Cfg = `{"issues": {"autoCreateLabel": false, "labels": {"inProgress": "wip", "needsReview": "nr", "blocked": "stuck"}}}`
	wip = translate(wip, map[string]string{"in-progress": "wip", "needs-review": "nr", "blocked": "stuck"})
	wip.Steps = []wStep{{Op: "set_state", Ref: "2", State: "needs-review"}, {Op: "set_state", Ref: "2", State: "none"}, {Op: "set_state", Ref: "7", State: "in-progress"},
		{Op: "complete", Ref: "7", Reason: "blocked"}, {Op: "reopen", Ref: "7"}, {Op: "claim", Ref: "6", ID: "z"}, {Op: "complete", Ref: "2", Reason: "dropped"}, {Op: "status", Ref: "7"}}
	out = append(out, wip)
	return out
}

func TestIssueWritesMatchPython(t *testing.T) {
	rng := rand.New(rand.NewSource(77))
	scen := handcrafted()
	cfgs := []struct {
		cfg  string
		repl map[string]string
	}{
		{defaultCfg, nil},
		{customCfg, map[string]string{"type:bug": "kind/bug", "type:feature": "kind/feature", "type:task": "kind/task", "milestone-tracker": "tracker"}},
	}
	for i := 0; i < 45; i++ {
		c := cfgs[i%2]
		s := wScenario{Cfg: c.cfg, Issues: seedIssues(), Comments: seedComments(), Milestones: []string{"M07 — Seven", "M01 — Alpha", "M02"}}
		if i%5 == 0 {
			s.Limit = "82"
		}
		s = translate(s, c.repl)
		for j := 0; j < 6+rng.Intn(5); j++ {
			s.Steps = append(s.Steps, genStep(rng))
		}
		scen = append(scen, s)
	}
	var got []any
	for _, s := range scen {
		got = append(got, goRunScenario(t, s))
	}
	golden.Check(t, map[string]any{"input": scen}, got)
}
