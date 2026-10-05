package backlog

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/tracker"
)

// newUmbrella is an umbrella over fake trackers, one per name, with the
// issues each lists; built[name] counts how often a sub-repo's tracker was made.
func newUmbrella(t *testing.T, cfg string, subs map[string][]Issue, order ...string) (*Umbrella, map[string]*fakeTracker, map[string]int) {
	t.Helper()
	fakes := map[string]*fakeTracker{}
	built := map[string]int{}
	var list []repos.Repo
	for _, name := range order {
		fakes[name] = &fakeTracker{Issues: subs[name], Milestones: []string{"M07 — Title"}}
		list = append(list, repos.Repo{Name: name, Rel: name, Path: "/umb/" + name})
	}
	u := &Umbrella{Cfg: mustDecode(t, cfg), CountProof: stubCountProof, Repos: list,
		NewTracker: func(dir string) (Tracker, error) {
			name := strings.TrimPrefix(dir, "/umb/")
			built[name]++
			return fakes[name], nil
		}}
	return u, fakes, built
}

func typed(n int, typ string, title string, extra ...string) Issue {
	return Issue{Number: n, Title: title, State: "open", Labels: append([]string{"type:" + typ}, extra...),
		URL: fmt.Sprintf("https://example.test/%d", n)}
}

func closedIssue(n int, typ, title, at string) Issue {
	is := typed(n, typ, title)
	is.State, is.ClosedAt, is.StateReason = "closed", at, "completed"
	return is
}

func twoRepos(t *testing.T) (*Umbrella, map[string]*fakeTracker, map[string]int) {
	return newUmbrella(t, `{}`, map[string][]Issue{
		"web": {typed(1, "feature", "Web feat", "size:Major"), typed(2, "bug", "Web bug", "p1"), typed(5, "task", "Web only"),
			closedIssue(3, "task", "Web done", "2026-09-02T10:00:00Z")},
		"api": {typed(1, "feature", "Api feat"), typed(4, "bug", "Api bug"), typed(6, "task", "Api task"),
			closedIssue(2, "bug", "Api done", "2026-09-03T10:00:00Z"), closedIssue(7, "task", "Api older", "2026-09-01T10:00:00Z")},
	}, "web", "api")
}

func TestUmbrellaRefs(t *testing.T) {
	u, _, _ := twoRepos(t)
	for ref, want := range map[string]string{
		"web:F1": "web:1", "web:1": "web:1", "web#1": "web:1", "api:#1": "api:1", "api#1": "api:1", "web:f1": "web:1",
		"B4": "api:4", "#4": "api:4", "4": "api:4", "T5": "web:5", "#6": "api:6",
	} {
		it, err := u.Get(ref)
		if err != nil {
			t.Errorf("%s: %v", ref, err)
			continue
		}
		if it.ID != want {
			t.Errorf("%s: ID %q, want %q", ref, it.ID, want)
		}
	}
	it, _ := u.Get("api:F1")
	if it.Type != "F" || it.Number != 1 || it.Fields.Get("repos") != "api" {
		t.Errorf("item %+v", it)
	}
}

func TestUmbrellaAmbiguousRef(t *testing.T) {
	u, _, _ := twoRepos(t)
	for _, ref := range []string{"F1", "#1", "1", "f1"} {
		_, err := u.Get(ref)
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: %v, want ErrInvalid", ref, err)
		}
		if !strings.Contains(err.Error(), "web:1, api:1") || !strings.Contains(err.Error(), "ambiguous across sub-repos") {
			t.Errorf("%s: message %q", ref, err)
		}
	}
	// closed items count as held, so api's closed bug 2 makes B2 ambiguous too;
	// a number only one sub-repo has is not
	if _, err := u.Get("B2"); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "web:2, api:2") {
		t.Errorf("B2: %v", err)
	}
	if it, err := u.Get("B4"); err != nil || it.ID != "api:4" {
		t.Errorf("B4: %v %v", it, err)
	}
	// every write verb stops at the ambiguity before touching a tracker
	_, fakes, _ := twoRepos(t)
	_ = fakes
	if _, err := u.Complete("F1", CompleteInput{Reason: "done", NoProof: true}); !errors.Is(err, ErrInvalid) {
		t.Errorf("complete: %v", err)
	}
}

func TestUmbrellaUnknownRefs(t *testing.T) {
	u, _, _ := twoRepos(t)
	for _, ref := range []string{"F99", "web:99", "nope:F1", "nope#1", "web:", "not a ref", "", "T1"} {
		_, err := u.Get(ref)
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("%q: %v, want ErrNotFound", ref, err)
		}
	}
	// a wrong type letter in a qualified ref is not found, not a different item
	if _, err := u.Get("web:B1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("web:B1: %v", err)
	}
}

func TestUmbrellaScopeNarrowsBareRefs(t *testing.T) {
	u, _, _ := twoRepos(t)
	u.Scope = "api"
	if it, err := u.Get("F1"); err != nil || it.ID != "api:1" {
		t.Errorf("F1 in scope api: %v %v", it, err)
	}
	if _, err := u.Get("T5"); !errors.Is(err, ErrNotFound) {
		t.Errorf("web's T5 outside scope: %v", err)
	}
	// a qualified ref still reaches the other sub-repo
	if it, err := u.Get("web:T5"); err != nil || it.ID != "web:5" {
		t.Errorf("web:T5: %v %v", it, err)
	}
	items, _ := u.List(true)
	for _, it := range items {
		if !strings.HasPrefix(it.ID, "api:") {
			t.Errorf("scope api lists %s", it.ID)
		}
	}
}

func itemIDs(items []Item) string {
	var out []string
	for _, it := range items {
		out = append(out, it.ID)
	}
	return strings.Join(out, " ")
}

func TestUmbrellaListOrder(t *testing.T) {
	u, _, _ := twoRepos(t)
	open, err := u.List(false)
	if err != nil {
		t.Fatal(err)
	}
	// Bugs, Features, Tasks; inside a type the sub-repos in registry order, then by number.
	if got, want := itemIDs(open), "web:2 api:4 web:1 api:1 web:5 api:6"; got != want {
		t.Errorf("open: %s, want %s", got, want)
	}
	all, _ := u.List(true)
	// closed: newest first across sub-repos
	if got, want := itemIDs(all), "web:2 api:4 web:1 api:1 web:5 api:6 api:2 web:3 api:7"; got != want {
		t.Errorf("all: %s, want %s", got, want)
	}
	for _, it := range all[6:] {
		if !it.Closed {
			t.Errorf("%s not closed", it.ID)
		}
	}
	for i := 7; i < len(all); i++ {
		if all[i].ClosedAt == "" || all[i].ClosedAt > all[i-1].ClosedAt {
			t.Errorf("%s closed %q after %s closed %q", all[i].ID, all[i].ClosedAt, all[i-1].ID, all[i-1].ClosedAt)
		}
	}
}

func TestUmbrellaClosedTieKeepsRegistryOrder(t *testing.T) {
	u, _, _ := newUmbrella(t, `{}`, map[string][]Issue{
		"a": {closedIssue(3, "task", "A", "2026-09-02T10:00:00Z")},
		"b": {closedIssue(3, "task", "B", "2026-09-02T10:00:00Z")},
	}, "a", "b")
	all, _ := u.List(true)
	if got := itemIDs(all); got != "a:3 b:3" {
		t.Errorf("tie order %s", got)
	}
}

func TestUmbrellaMarkdown(t *testing.T) {
	u, _, _ := twoRepos(t)
	md, err := u.Markdown(20)
	if err != nil {
		t.Fatal(err)
	}
	want := `# Backlog

## Bugs

- **[B2] [P1] Web bug.** Repos: web
- **[B4] Api bug.** Repos: api

## Features

- **[F1] [Major] Web feat.** Repos: web
- **[F1] Api feat.** Repos: api

## Tasks

- **[T5] Web only.** Repos: web
- **[T6] Api task.** Repos: api

## Completed

- ~~**[B2] Api done.** Repos: api~~ Done 2026-09-03 [` + "`#2`" + `]
- ~~**[T3] Web done.** Repos: web~~ Done 2026-09-02 [` + "`#3`" + `]
- ~~**[T7] Api older.** Repos: api~~ Done 2026-09-01 [` + "`#7`" + `]
`
	if md != want {
		t.Errorf("markdown\n%s\nwant\n%s", md, want)
	}
	two, _ := u.Markdown(2)
	if strings.Contains(two, "Api older") || !strings.Contains(two, "Web done") {
		t.Errorf("limit 2: %s", two)
	}
	none, _ := u.Markdown(0)
	if strings.Contains(none, "Done 20") {
		t.Errorf("limit 0 shows done lines: %s", none)
	}
	all, _ := u.Markdown(-1)
	if !strings.Contains(all, "Api older") {
		t.Errorf("limit -1: %s", all)
	}
}

func TestUmbrellaWritesGoToTheOwner(t *testing.T) {
	u, fakes, _ := twoRepos(t)
	changed, err := u.Complete("api:B4", CompleteInput{Reason: "done", Commit: "abc1234", NoProof: true})
	if err != nil || !changed {
		t.Fatalf("complete: %v %v", changed, err)
	}
	if fakes["api"].Issues[1].State != "closed" || fakes["web"].Issues[1].State != "open" {
		t.Errorf("close hit the wrong tracker: api %v web %v", fakes["api"].Issues[1].State, fakes["web"].Issues[1].State)
	}
	for _, c := range fakes["web"].Calls {
		if c.Method != "get" && c.Method != "list" {
			t.Errorf("web saw write %v", c)
		}
	}
	// claim, state, comment, note, field and reopen route the same way
	if won, _, err := u.Claim("T5", "w1"); err != nil || !won {
		t.Fatalf("claim: %v %v", won, err)
	}
	if _, err := u.SetState("web:T5", "in-progress"); err != nil {
		t.Fatal(err)
	}
	if _, err := u.AddComment("T6", "question", "why?"); err != nil {
		t.Fatal(err)
	}
	if _, err := u.NotePut("web:2", "design", "a design"); err != nil {
		t.Fatal(err)
	}
	if _, err := u.SetField("api:1", "related", "B4"); err != nil {
		t.Fatal(err)
	}
	if _, err := u.Reopen("api:2"); err != nil {
		t.Fatal(err)
	}
	if got := fakes["api"].Issues[0].Body; !strings.Contains(got, "Related: B4") {
		t.Errorf("api #1 body %q", got)
	}
	if fakes["api"].Issues[3].State != "open" {
		t.Errorf("api #2 not reopened")
	}
	if !slices.Contains(fakes["web"].Issues[2].Labels, "in-progress") {
		t.Errorf("web #5 labels %v", fakes["web"].Issues[2].Labels)
	}
	for _, c := range fakes["api"].Calls {
		if c.Method == "add_comment" && c.Args[0] != 6 {
			t.Errorf("comment on %v", c.Args)
		}
	}
	rows, _ := u.Comments("T6", "")
	if len(rows) != 1 || rows[0].Kind != "question" {
		t.Errorf("comments %v", rows)
	}
	body, ok, _ := u.NoteGet("web:B2", "design")
	if !ok || body != "a design" {
		t.Errorf("note %q %v", body, ok)
	}
	if _, ok, _ := u.NoteGet("api:T6", "design"); ok {
		t.Errorf("design note leaked to api")
	}
	if rm, err := u.NoteRm("web:2", "design"); err != nil || !rm {
		t.Errorf("note rm %v %v", rm, err)
	}
	if won, holder, err := u.Claim("web:T5", "w2"); err != nil || won || holder != "w1" {
		t.Errorf("second claim: %v %q %v", won, holder, err)
	}
	if rel, err := u.Release("web:T5", "w1"); err != nil || !rel {
		t.Errorf("release: %v %v", rel, err)
	}
	if reasons, err := u.Ready("web:T5"); err != nil || len(reasons) != 2 {
		t.Errorf("ready: %v %v", reasons, err)
	}
}

func TestUmbrellaStatusID(t *testing.T) {
	u, _, _ := twoRepos(t)
	st, err := u.Status("api:F1")
	if err != nil {
		t.Fatal(err)
	}
	if st.ID != "api:1" || st.Type != "F" || st.Status != "open" {
		t.Errorf("status %+v", st)
	}
	if _, err := u.Status("F1"); !errors.Is(err, ErrInvalid) {
		t.Errorf("ambiguous status: %v", err)
	}
}

func TestUmbrellaReposFieldIsReadOnly(t *testing.T) {
	u, fakes, _ := twoRepos(t)
	for _, f := range []string{"repos", "Repos", "REPOS"} {
		if _, err := u.SetField("web:1", f, "api"); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", f, err)
		}
	}
	// refused before any lookup, even for an unknown item
	if _, err := u.SetField("nope:9", "repos", "x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown: %v", err)
	}
	if len(fakes["web"].Calls)+len(fakes["api"].Calls) != 0 {
		t.Errorf("tracker called: %v %v", fakes["web"].Calls, fakes["api"].Calls)
	}
}

func TestUmbrellaCreateRouting(t *testing.T) {
	in := func(fields ...Field) CreateInput {
		return CreateInput{Kind: "tasks", Title: "New", Fields: fields}
	}
	t.Run("repos field", func(t *testing.T) {
		u, fakes, _ := twoRepos(t)
		res, err := u.Create(in(Field{"Repos", " api "}, Field{"Related", "B4"}))
		if err != nil || res.ID != "api:8" || res.Type != "T" {
			t.Fatalf("%+v %v", res, err)
		}
		body := fakes["api"].Issues[len(fakes["api"].Issues)-1].Body
		if strings.Contains(body, "Repos:") || !strings.Contains(body, "Related: B4") {
			t.Errorf("body %q: Repos must not reach the fields block", body)
		}
		if len(fakes["web"].Issues) != 4 {
			t.Errorf("web gained an issue")
		}
	})
	t.Run("scope", func(t *testing.T) {
		u, _, _ := twoRepos(t)
		u.Scope = "web"
		if res, err := u.Create(in()); err != nil || res.ID != "web:6" {
			t.Fatalf("%+v %v", res, err)
		}
	})
	t.Run("scope matching the field", func(t *testing.T) {
		u, _, _ := twoRepos(t)
		u.Scope = "web"
		if res, err := u.Create(in(Field{"Repos", "web"})); err != nil || res.ID != "web:6" {
			t.Fatalf("%+v %v", res, err)
		}
	})
	t.Run("cwd", func(t *testing.T) {
		u, _, _ := twoRepos(t)
		u.CwdRepo = "api"
		if res, err := u.Create(in()); err != nil || res.ID != "api:8" {
			t.Fatalf("%+v %v", res, err)
		}
		// the field wins over the working directory
		if res, err := u.Create(in(Field{"Repos", "web"})); err != nil || res.ID != "web:6" {
			t.Fatalf("%+v %v", res, err)
		}
	})
	for name, c := range map[string]struct {
		fields []Field
		scope  string
		want   error
		msg    string
	}{
		"none":        {nil, "", ErrNotFound, "needs a target sub-repo"},
		"several":     {[]Field{{"Repos", "web,api"}}, "", ErrNotFound, "exactly one sub-repo"},
		"unknown":     {[]Field{{"Repos", "nope"}}, "", ErrNotFound, "unknown sub-repo 'nope'"},
		"conflict":    {[]Field{{"Repos", "web"}}, "api", ErrInvalid, "different sub-repos"},
		"blank names": {[]Field{{"Repos", " , "}}, "", ErrNotFound, "needs a target sub-repo"},
	} {
		t.Run(name, func(t *testing.T) {
			u, fakes, built := twoRepos(t)
			u.Scope = c.scope
			_, err := u.Create(in(c.fields...))
			if !errors.Is(err, c.want) || !strings.Contains(err.Error(), c.msg) {
				t.Fatalf("%v, want %v containing %q", err, c.want, c.msg)
			}
			if len(built) != 0 || len(fakes["web"].Calls)+len(fakes["api"].Calls) != 0 {
				t.Errorf("a tracker was touched: %v", built)
			}
		})
	}
	t.Run("validation after routing", func(t *testing.T) {
		u, _, _ := twoRepos(t)
		_, err := u.Create(CreateInput{Kind: "tasks", Title: "x", Tag: "P1", Fields: []Field{{"Repos", "web"}}})
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("bad tag: %v", err)
		}
	})
}

func TestUmbrellaTrackersAreBuiltOnDemand(t *testing.T) {
	u, _, built := twoRepos(t)
	if _, err := u.Get("api:F1"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(built, map[string]int{"api": 1}) {
		t.Errorf("qualified ref built %v", built)
	}
	if _, err := u.Get("api:F1"); err != nil {
		t.Fatal(err)
	}
	if built["api"] != 1 {
		t.Errorf("tracker rebuilt: %v", built)
	}
	if _, err := u.List(false); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(built, map[string]int{"api": 1, "web": 1}) {
		t.Errorf("list built %v", built)
	}
	// a forge that cannot start fails the verbs that reach it, not the others
	boom := &tracker.Error{Kind: tracker.KindUnavailable, Message: "gh is not installed"}
	u2, _, _ := twoRepos(t)
	u2.NewTracker = func(dir string) (Tracker, error) {
		if dir == "/umb/web" {
			return nil, boom
		}
		return &fakeTracker{Issues: []Issue{typed(1, "feature", "x")}}, nil
	}
	if _, err := u2.Get("api:1"); err != nil {
		t.Errorf("api: %v", err)
	}
	if _, err := u2.Get("web:1"); !errors.Is(err, boom) {
		t.Errorf("web: %v", err)
	}
	if _, err := u2.Get("F1"); !errors.Is(err, boom) {
		t.Errorf("bare probe: %v", err)
	}
}

func TestUmbrellaTrackerFailureDuringProbe(t *testing.T) {
	u, fakes, _ := twoRepos(t)
	boom := &tracker.Error{Kind: tracker.KindRateLimited, Message: "secondary rate limit"}
	fakes["api"].Fail = map[string]error{"get": boom}
	if _, err := u.Get("T5"); !errors.Is(err, boom) {
		t.Errorf("a failing probe must surface, got %v", err)
	}
}

func milestoneTracker(n int, title, state string) Issue {
	return Issue{Number: n, Title: title, State: state, Labels: []string{"milestone-tracker"}}
}

func TestUmbrellaMilestoneCreatedOnFirstUse(t *testing.T) {
	u, fakes, _ := newUmbrella(t, `{}`, map[string][]Issue{
		"web": {milestoneTracker(9, "M05 — Alpha", "open"), typed(1, "task", "w")},
		"api": {typed(1, "task", "a"), typed(2, "task", "b")},
	}, "web", "api")
	fakes["web"].Milestones = []string{"M05 — Alpha"}
	fakes["api"].Milestones = nil

	if ch, err := u.SetField("api:1", "milestone", "M05"); err != nil || !ch {
		t.Fatalf("%v %v", ch, err)
	}
	if !reflect.DeepEqual(fakes["api"].Milestones, []string{"M05 — Alpha"}) {
		t.Fatalf("api milestones %v", fakes["api"].Milestones)
	}
	if fakes["api"].Issues[0].Milestone != "M05 — Alpha" {
		t.Errorf("issue milestone %q", fakes["api"].Issues[0].Milestone)
	}
	// the second assignment reuses it
	if _, err := u.SetField("api:2", "milestone", "M05"); err != nil {
		t.Fatal(err)
	}
	if len(fakes["api"].Milestones) != 1 {
		t.Errorf("duplicated: %v", fakes["api"].Milestones)
	}
	// the home repo already has it: nothing created there
	if _, err := u.SetField("web:1", "milestone", "M05"); err != nil || len(fakes["web"].Milestones) != 1 {
		t.Errorf("home: %v %v", err, fakes["web"].Milestones)
	}
	// capture with a milestone goes through the same hook
	fakes["api"].Milestones = nil
	if _, err := u.Create(CreateInput{Kind: "tasks", Title: "n", Fields: []Field{{"Repos", "api"}, {"Milestone", "M05"}}}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fakes["api"].Milestones, []string{"M05 — Alpha"}) {
		t.Errorf("create: %v", fakes["api"].Milestones)
	}
}

func TestUmbrellaMilestoneWithoutTrackingIssue(t *testing.T) {
	u, fakes, _ := newUmbrella(t, `{}`, map[string][]Issue{
		"web": {typed(1, "task", "w")},
		"api": {typed(1, "task", "a")},
	}, "web", "api")
	_, err := u.SetField("api:1", "milestone", "M99")
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "milestone M99 not found") {
		t.Fatalf("%v", err)
	}
	for _, c := range fakes["api"].Calls {
		if c.Method == "create_milestone" {
			t.Errorf("created %v", c)
		}
	}
	// M07 exists natively already
	if _, err := u.SetField("api:1", "milestone", "M07"); err != nil {
		t.Errorf("native: %v", err)
	}
}

func TestUmbrellaTrackingIssuePick(t *testing.T) {
	// open beats closed, then the lower number; a title that only starts like the ID is no match
	u, fakes, _ := newUmbrella(t, `{"issues": {"homeRepo": "api"}}`, map[string][]Issue{
		"web": {typed(1, "task", "w")},
		"api": {milestoneTracker(4, "M05 — Closed one", "closed"), milestoneTracker(8, "M05 — Later open", "open"),
			milestoneTracker(6, "M05 — First open", "open"), milestoneTracker(2, "M055 — Other", "open")},
	}, "web", "api")
	fakes["web"].Milestones = nil
	if _, err := u.SetField("web:1", "milestone", "M05"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fakes["web"].Milestones, []string{"M05 — First open"}) {
		t.Errorf("created %v", fakes["web"].Milestones)
	}
	for in, want := range map[string]string{"M05": "M05", " M5 — x": "M5", "M05x": "", "M05_": "", "M7-x": "M7", "Plain": "", "M": ""} {
		if got := msTitleID(in); got != want {
			t.Errorf("msTitleID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUmbrellaHomeRepoMustBeRegistered(t *testing.T) {
	u, _, _ := newUmbrella(t, `{"issues": {"homeRepo": "ghost"}}`, map[string][]Issue{"web": {typed(1, "task", "w")}}, "web")
	_, err := u.SetField("web:1", "milestone", "M99")
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "issues.homeRepo 'ghost' is not a registered sub-repo") {
		t.Fatalf("%v", err)
	}
}

func TestUmbrellaDetail(t *testing.T) {
	is := typed(1, "task", "x")
	is.Body = "Some detail.\n\n<!-- rota:fields\nRelated: B1\n-->"
	u, _, _ := newUmbrella(t, `{}`, map[string][]Issue{"web": {is}, "api": {typed(1, "task", "y")}}, "web", "api")
	if text, ok, err := u.Detail("web:1"); err != nil || !ok || text != "Some detail." && !strings.HasPrefix(text, "Some detail.") {
		t.Errorf("%q %v %v", text, ok, err)
	}
	if _, ok, err := u.Detail("api:1"); err != nil || ok {
		t.Errorf("blank body: %v %v", ok, err)
	}
	if _, ok, err := u.Detail("nope:1"); err != nil || ok {
		t.Errorf("unknown: %v %v", ok, err)
	}
}

func TestOpenRowsQualifiesUmbrellaIDs(t *testing.T) {
	u, _, _ := twoRepos(t)
	rows, _, ok, err := OpenRows(u)
	if err != nil || !ok {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rows {
		got = append(got, r.ID+"|"+r.Key+"|"+r.Repo)
	}
	want := []string{"web:2|B2|web", "api:4|B4|api", "web:1|F1|web", "api:1|F1|api", "web:5|T5|web", "api:6|T6|api"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows %v", got)
	}
	f1 := rows[2]
	for ref, want := range map[string]bool{"web:1": true, "web:F1": true, "web:#1": true, "web#1": true, "F1": true, "#1": true, "1": true, "api:1": false, "web:2": false} {
		if got := f1.IssueMatches(map[string]bool{ref: true}); got != want {
			t.Errorf("web F1 matches %q = %v, want %v", ref, got, want)
		}
	}
}

func TestListingClustersStaySubRepoLocal(t *testing.T) {
	// both sub-repos have an F1 related to their own B2; the clusters must not merge
	u, _, _ := newUmbrella(t, `{}`, map[string][]Issue{
		"web": {withFields(typed(1, "feature", "Web one"), "Related: B2"), typed(2, "bug", "Web two")},
		"api": {withFields(typed(1, "feature", "Api one"), "Related: B2"), typed(2, "bug", "Api two")},
	}, "web", "api")
	rows, md, _, err := OpenRows(u)
	if err != nil {
		t.Fatal(err)
	}
	l := BuildListing(rows, md, nil, "")
	// components sort on (key, repo), so the tie between the two B2s goes by name
	want := [][]string{{"api:2", "api:1"}, {"web:2", "web:1"}}
	if !reflect.DeepEqual(l.Clusters, want) {
		t.Errorf("clusters %v, want %v", l.Clusters, want)
	}
	if !strings.Contains(l.Text(), "- [B2] Web two ↔ [F1] Web one") || !strings.Contains(l.Text(), "- [B2] Api two ↔ [F1] Api one") {
		t.Errorf("text:\n%s", l.Text())
	}
	if !strings.Contains(l.Text(), "| web:1 | Web one.") && !strings.Contains(l.Text(), "web:1") {
		t.Errorf("ID cells not qualified:\n%s", l.Text())
	}
}

func withFields(is Issue, lines ...string) Issue {
	is.Body = "<!-- rota:fields\n" + strings.Join(lines, "\n") + "\n-->"
	return is
}

func TestListingActiveStreamsHideOnlyTheirOwnRepoRow(t *testing.T) {
	u, _, _ := twoRepos(t)
	rows, md, _, err := OpenRows(u)
	if err != nil {
		t.Fatal(err)
	}
	features := func(active ...Active) string {
		var out []string
		for _, r := range BuildListing(rows, md, active, "").Features {
			out = append(out, r.ID)
		}
		return strings.Join(out, " ")
	}
	for name, c := range map[string]struct {
		active Active
		want   string
	}{
		"bare in api":         {Active{Branch: "b", Repo: "api", Items: []string{"F1"}}, "web:1"},
		"bare in web":         {Active{Branch: "b", Repo: "web", Items: []string{"1"}}, "api:1"},
		"bare hash":           {Active{Branch: "b", Repo: "web", Items: []string{"#1"}}, "api:1"},
		"bare without repo":   {Active{Branch: "b", Items: []string{"F1"}}, ""},
		"qualified":           {Active{Branch: "b", Items: []string{"web:F1"}}, "api:1"},
		"qualified hash":      {Active{Branch: "b", Repo: "api", Items: []string{"web#1"}}, "api:1"},
		"qualified id":        {Active{Branch: "b", Items: []string{"api:1"}}, "web:1"},
		"other repo's number": {Active{Branch: "b", Repo: "api", Items: []string{"B2"}}, "web:1 api:1"},
	} {
		if got := features(c.active); got != c.want {
			t.Errorf("%s: features %q, want %q", name, got, c.want)
		}
	}
}
