package main

// Go-only scenarios for the item verbs in issue mode (#48). Each scenario
// builds a git project whose origin resolves to github or gitlab, seeds the
// stateful fake forge (test/fakes/fake_tracker.py), runs the Go binary, and
// compares exit code, envelope and the final fake-forge database with the
// frozen record in testdata/frozen (keyed by scenario name).
//
// Safety: the shared TestMain refuses to run unless gh resolves to test/fakes
// (TestIssueFakesFirst checks glab too); every run has its own
// FAKE_TRACKER_DB under t.TempDir.
//
// Behaviour rationale worth keeping:
//  - ids are the issue number plus the type letter (contract rule 11).
//  - data.changed reports whether the tracker changed (isc.changed).
//  - item field set on a closed issue is 4, on --name detail 4 (backend).
//  - a milestone that is not M<digits> is usage, 2; an empty note body is 2.
//  - the loser of a claim gets failure data {blockedBy: "claimed", changed:
//    true} (#106: its claim and release were posted).
//  - comment add / note resolve the issue first (3 for a milestone tracker or
//    a wrong type letter); note show of an absent note has data.exists false.
//  - read-only verbs under the wrong backend exit 1 (#106); claim/release on
//    an unknown item on the file backend is 3; item note add there is 4.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type isc struct {
	name     string
	remote   string // "" github, "gitlab", "none" (no origin)
	file     bool   // file backend project, no tracker
	cfg      string // config.json override
	argv     []string
	in       string
	env      []string
	want     int
	changed  *bool // Go's data.changed on success
	textSame bool  // also record the text-mode stdout (read-only verbs)
	check    func(t *testing.T, e envl, db map[string]any)
}

func yes() *bool { b := true; return &b }
func no() *bool  { b := false; return &b }

const issueCfg = `{"backlog": {"backend": "issues"}, "issues": {"retryWaitSeconds": 0}}`

func cm(id int, body string) map[string]any {
	return map[string]any{"id": id, "body": body, "author": "fake-user"}
}

func iss(n int, title, body string, labels []string, state string, comments ...map[string]any) map[string]any {
	cs := []any{}
	for _, c := range comments {
		cs = append(cs, c)
	}
	lb := []any{}
	for _, l := range labels {
		lb = append(lb, l)
	}
	i := map[string]any{"number": n, "title": title, "body": body, "labels": lb, "milestone": nil,
		"state": state, "state_reason": nil, "closed_at": nil, "assignees": []any{}, "comments": cs}
	if state == "closed" {
		i["state_reason"], i["closed_at"] = "completed", "2026-01-01T00:00:00Z"
	}
	return i
}

// seedDB is the fixture forge: #1 feature with criteria and a native
// milestone, #2 bug, #3 milestone tracker, #4 closed task, #5 bug with a proof
// note, a design note and a question, #6 feature claimed by "other", #7 task
// claimed by "me", #8 blocked bug, #9 dropped bug, #10 bare task, #11 task with
// a three-part plan note.
func seedDB() map[string]any {
	i1 := iss(1, "Add export", "Export it.\n\n## Acceptance\n- [ ] works\n\n<!-- rota:fields\nRelated: B2\n-->",
		[]string{"type:feature", "size:Major"}, "open")
	i1["milestone"] = []any{"M07 — Title", 1}
	i6 := iss(6, "Parallel work", "x", []string{"type:feature", "in-progress"}, "open",
		cm(4, "<!-- rota:claim other -->\nClaimed by other"))
	i6["assignees"] = []any{"fake-user"}
	i9 := iss(9, "Dropped bug", "x", []string{"type:bug", "not-planned"}, "closed")
	i9["state_reason"] = "not_planned"
	return map[string]any{
		"next_issue": 12, "next_milestone": 2, "next_comment": 20, "next_mr": 1,
		"labels": []any{}, "prs": []any{},
		"milestones": []any{map[string]any{"number": 1, "title": "M07 — Title", "description": "", "state": "open"}},
		"issues": []any{
			i1,
			iss(2, "Crash on start", "boom", []string{"type:bug", "p1"}, "open"),
			iss(3, "M07 tracking", "", []string{"milestone-tracker"}, "open"),
			iss(4, "Old task", "x", []string{"type:task"}, "closed"),
			iss(5, "Noted bug", "x", []string{"type:bug"}, "open",
				cm(1, "<!-- rota:proof -->\n## Proof\n- build · PASS · ok"),
				cm(2, "<!-- rota:design -->\nthe design"),
				cm(3, "<!-- rota:comment question -->\nWhy?\nmore")),
			i6,
			iss(7, "Mine", "x", []string{"type:task", "needs-review"}, "open",
				cm(5, "<!-- rota:claim me -->\nClaimed by me")),
			iss(8, "Blocked bug", "x", []string{"type:bug", "blocked"}, "open"),
			i9,
			iss(10, "Bare task", "", []string{"type:task"}, "open"),
			iss(11, "Planned", "x", []string{"type:task"}, "open",
				cm(6, "<!-- rota:plan 1/3 -->\nline1\n"), cm(7, "<!-- rota:plan 2/3 -->\nline2\n"), cm(8, "<!-- rota:plan 3/3 -->\nline3")),
		},
	}
}

func writeDB(t *testing.T, db map[string]any) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tracker.json")
	raw, _ := json.Marshal(db)
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// readDB loads a fake DB with the volatile closed_at stamps normalized.
func readDB(t *testing.T, p string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var db map[string]any
	if err := json.Unmarshal(raw, &db); err != nil {
		t.Fatalf("%s: %v", p, err)
	}
	for _, i := range db["issues"].([]any) {
		m := i.(map[string]any)
		if s, _ := m["closed_at"].(string); s != "" {
			m["closed_at"] = "<ts>"
		}
	}
	return db
}

func envRun(t *testing.T, dir, stdin string, extra []string, name string, args ...string) run {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(append([]string{}, baseEnv...), extra...)
	cmd.Stdin = strings.NewReader(stdin)
	var so, se strings.Builder
	cmd.Stdout, cmd.Stderr = &so, &se
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("%s: %v", name, err)
		}
		code = ee.ExitCode()
	}
	return run{code: code, stdout: so.String(), stderr: se.String(), dir: dir}
}

// fixture builds the scenario's project with its origin remote.
func (s isc) fixture(t *testing.T) (base string, in info) {
	t.Helper()
	f := fx{config: issueCfg}
	if s.file {
		f = fx{}
	} else {
		f.noBacklog = true
	}
	if s.cfg != "" {
		f.config = s.cfg
	}
	base, in = f.build(t)
	remote := map[string]string{"": "https://github.com/example/repo.git", "gitlab": "https://gitlab.com/example/repo.git"}[s.remote]
	if remote != "" {
		git(t, base, "remote", "add", "origin", remote)
	}
	return base, in
}

func (s isc) exec(t *testing.T) {
	t.Parallel()
	base, in := s.fixture(t)
	dir := copyTree(t, base)
	dbPath := writeDB(t, seedDB())
	argv := subst(s.argv, in)
	env := frozenEnv(append([]string{"FAKE_TRACKER_DB=" + dbPath}, s.env...))
	r := envRun(t, dir, s.in, env, rotaBin, argv...)
	if r.code != s.want {
		t.Errorf("exit = %d, want %d\nargv: %v\nstdout: %s\nstderr: %s", r.code, s.want, argv, r.stdout, r.stderr)
	}
	db := readDB(t, dbPath)
	st := newStep(t, r, snapshot(t, base), dir, db)
	if s.textSame {
		st.text(envRun(t, dir, s.in, env, rotaBin, withoutJSON(argv)...).stdout)
	}
	e := parseEnv(t, "go", r)
	if s.changed != nil && s.want == 0 && at(e, "data.changed") != *s.changed {
		t.Errorf("data.changed = %v, want %v", at(e, "data.changed"), *s.changed)
	}
	if s.check != nil {
		e["__info"] = in
		s.check(t, e, db)
	}
	frozenCheck(t, frozenRec{Steps: []frozenStep{st}})
}

func TestIssueFakesFirst(t *testing.T) {
	for _, tool := range []string{"gh", "glab"} {
		p, err := exec.LookPath(tool)
		if err != nil || !strings.HasPrefix(p, filepath.Join(repoDir, "test", "fakes")+string(os.PathSeparator)) {
			t.Fatalf("%s resolves to %q (%v), not test/fakes", tool, p, err)
		}
	}
}

func suiteItemIssue(t *testing.T) {
	var all []isc
	add := func(s ...isc) { all = append(all, s...) }
	both := func(s isc) { // github and gitlab
		g := s
		g.name = s.name + "/gitlab"
		g.remote = "gitlab"
		s.name += "/github"
		add(s, g)
	}
	rate := []string{"FAKE_TRACKER_FAIL=issue view", "FAKE_TRACKER_FAIL_MSG=secondary rate limit"}

	// negatives every ref-taking verb shares: unknown, wrong letter, tracker issue,
	// rate limit, forge unavailable.
	type verb struct {
		name string
		argv func(id string) []string // argv including --json
		ok   string                   // a valid ID for the rate-limit and unavailable runs
	}
	verbs := []verb{
		{"complete", func(id string) []string { return j("item", "complete", id, "--commit", "abc1234", "--no-proof") }, "2"},
		{"reopen", func(id string) []string { return j("item", "reopen", id) }, "4"},
		{"statev", func(id string) []string { return j("item", "state", id, "--to", "in-progress") }, "2"},
		{"fieldget", func(id string) []string { return j("item", "field", "get", id, "--name", "title") }, "1"},
		{"fieldset", func(id string) []string { return j("item", "field", "set", id, "--name", "related", "--value", "B1") }, "2"},
		{"show", func(id string) []string { return j("item", "show", id) }, "5"},
		{"claim", func(id string) []string { return j("item", "claim", id, "--as", "me") }, "2"},
		{"release", func(id string) []string { return j("item", "release", id, "--as", "me") }, "7"},
		{"ready", func(id string) []string { return j("item", "ready", id) }, "1"},
		{"commentlist", func(id string) []string { return j("item", "comment", "list", id) }, "5"},
	}
	for _, v := range verbs {
		mk := func(name, id string, want int, env []string, remote string) isc {
			return isc{name: v.name + "/" + name, argv: v.argv(id), env: env, remote: remote, want: want}
		}
		add(mk("unknown", "99", 3, nil, ""), mk("wrong-letter", "F2", 3, nil, ""), mk("tracker-issue", "3", 3, nil, ""),
			mk("rate-limit", v.ok, 6, rate, ""), mk("forge-unavailable", v.ok, 5, nil, "none"))
	}
	// the same refusals when the ref names a bug as a feature letter and the
	// other spellings of a valid ref resolve.
	for _, ref := range []string{"2", "#2", "B2", "b2"} {
		add(isc{name: "show/ref-" + ref, argv: j("item", "show", ref), want: 0,
			check: func(t *testing.T, e envl, db map[string]any) {
				eq(t, e, "data.id", "2")
				eq(t, e, "data.type", "B")
			}})
	}

	// ---- create
	cr := func(name string, want int, args ...string) isc {
		return isc{name: "create/" + name, argv: j(append([]string{"item", "create"}, args...)...), want: want, changed: yes()}
	}
	add(
		cr("feature-full", 0, "--kind", "features", "--title", "New thing", "--tag", "Major", "--desc", "d", "--milestone", "M07", "--related", "B2", "--subsystem", "web", "--captured", "2026-10-01"),
		cr("bug-tag", 0, "--kind", "bugs", "--title", "Broken", "--tag", "P2"),
		cr("task-plain", 0, "--kind", "tasks", "--title", "Chore"),
		cr("milestone-missing", 3, "--kind", "bugs", "--title", "x", "--milestone", "M99"),
		cr("bad-tag", 2, "--kind", "bugs", "--title", "x", "--tag", "Major"),
		cr("no-title", 2, "--kind", "bugs"),
		isc{name: "create/milestone-bad-format", argv: j("item", "create", "--kind", "bugs", "--title", "x", "--milestone", "next"), want: 2},
		isc{name: "create/rate-limit", argv: j("item", "create", "--kind", "bugs", "--title", "x"), want: 6,
			env: []string{"FAKE_TRACKER_FAIL=issue create", "FAKE_TRACKER_FAIL_MSG=secondary rate limit"}},
		isc{name: "create/forge-unavailable", remote: "none", argv: j("item", "create", "--kind", "bugs", "--title", "x"), want: 5},
		isc{name: "create/id-placeholder-body", argv: j("item", "create", "--kind", "tasks", "--title", "Doc", "--body-file", "body.md"), want: 0, changed: yes(),
			check: func(t *testing.T, e envl, db map[string]any) {
				for _, i := range db["issues"].([]any) {
					if m := i.(map[string]any); m["number"] == float64(12) && !strings.Contains(fmt.Sprint(m["body"]), "# T12") {
						t.Errorf("{ID} not replaced: %v", m["body"])
					}
				}
				eq(t, e, "data.id", "12")
				eq(t, e, "data.type", "T")
			}},
		isc{name: "create/custom-labels", cfg: `{"backlog":{"backend":"issues"},"issues":{"retryWaitSeconds":0,"labels":{"types":{"bug":"kind/bug","feature":"kind/feature","task":"kind/task"},"priorityPrefix":"prio-","sizePrefix":"effort:"}}}`,
			argv: j("item", "create", "--kind", "bugs", "--title", "Custom", "--tag", "P1"), want: 0, changed: yes()},
	)
	both(cr("gitlab-feature", 0, "--kind", "features", "--title", "New thing", "--tag", "Minor", "--milestone", "M07"))

	// ---- field set / get / list
	fs := func(name, id, field, value string, want int, ch *bool) isc {
		return isc{name: "fieldset/" + name, argv: j("item", "field", "set", id, "--name", field, "--value", value), want: want, changed: ch}
	}
	add(
		fs("milestone", "2", "milestone", "M07", 0, yes()),
		fs("milestone-same", "1", "milestone", "M07", 0, no()),
		fs("milestone-clear", "1", "milestone", "", 0, yes()),
		fs("milestone-clear-none", "2", "milestone", "", 0, no()),
		fs("related", "2", "related", "B1", 0, yes()),
		fs("related-same", "1", "related", "B2", 0, no()),
		fs("related-clear", "1", "related", "", 0, yes()),
		fs("repos", "2", "repos", "web, api", 0, yes()),
		fs("milestone-missing", "2", "milestone", "M99", 3, nil),
		isc{name: "fieldset/detail-refused", argv: j("item", "field", "set", "2", "--name", "detail", "--value", "x"), want: 4},
		fs("bad-field", "2", "title", "x", 2, nil),
		isc{name: "fieldset/closed", argv: j("item", "field", "set", "4", "--name", "related", "--value", "B1"), want: 4},
		isc{name: "fieldset/milestone-bad-format", argv: j("item", "field", "set", "2", "--name", "milestone", "--value", "later"), want: 2},
		isc{name: "fieldget/title", argv: j("item", "field", "get", "1", "--name", "title"), want: 0},
		isc{name: "fieldget/milestone", argv: j("item", "field", "get", "1", "--name", "milestone"), want: 0},
		isc{name: "fieldget/related-bracketed", argv: j("item", "field", "get", "1", "--name", "related"), want: 0},
		isc{name: "fieldget/closed-reason", argv: j("item", "field", "get", "9", "--name", "reason"), want: 0},
		isc{name: "fieldlist/open", argv: j("item", "field", "list", "1"), want: 0},
		isc{name: "fieldlist/closed", argv: j("item", "field", "list", "4"), want: 0},
	)
	both(fs("gitlab-milestone", "2", "milestone", "M07", 0, yes()))

	// ---- complete
	co := func(name, id string, want int, ch *bool, args ...string) isc {
		return isc{name: "complete/" + name, argv: j(append([]string{"item", "complete", id, "--commit", "abc1234"}, args...)...), want: want, changed: ch}
	}
	add(
		co("done-with-proof", "5", 0, yes()),
		co("done-proof-missing", "2", 4, nil),
		co("done-no-proof", "2", 0, yes(), "--no-proof"),
		co("done-note", "2", 0, yes(), "--no-proof", "--note", "a\nb"),
		co("done-clears-state-labels", "7", 0, yes(), "--no-proof"),
		co("dropped", "2", 0, yes(), "--reason", "dropped", "--note", "n"),
		co("handed-off", "6", 0, yes(), "--reason", "handed-off"),
		co("blocked", "2", 0, yes(), "--reason", "blocked", "--note", "waiting"),
		co("blocked-already", "8", 0, no(), "--reason", "blocked"),
		co("already-closed", "4", 0, no()),
		co("bad-reason", "2", 2, nil, "--reason", "nope"),
	)
	both(co("gitlab-done-proof", "5", 0, yes()))
	both(co("gitlab-dropped", "6", 0, yes(), "--reason", "dropped"))

	// ---- reopen / state
	for _, c := range []struct {
		n, id string
		ch    *bool
	}{{"closed", "4", yes()}, {"dropped", "9", yes()}, {"unblock", "8", yes()}, {"open-noop", "2", no()}} {
		add(isc{name: "reopen/" + c.n, argv: j("item", "reopen", c.id), want: 0, changed: c.ch})
	}
	both(isc{name: "reopen/gitlab", argv: j("item", "reopen", "9"), want: 0, changed: yes()})
	st := func(name, id, to string, want int, ch *bool) isc {
		return isc{name: "state/" + name, argv: j("item", "state", id, "--to", to), want: want, changed: ch}
	}
	add(
		st("in-progress", "2", "in-progress", 0, yes()),
		st("swap", "6", "needs-review", 0, yes()),
		st("changes", "7", "changes-requested", 0, yes()),
		st("none", "7", "none", 0, yes()),
		st("noop", "7", "needs-review", 0, no()),
		st("none-noop", "2", "none", 0, no()),
		st("bad-to", "2", "bogus", 2, nil),
	)
	both(st("gitlab-in-progress", "2", "in-progress", 0, yes()))

	// ---- claim / release
	cl := func(name, id, as string, want int, check func(*testing.T, envl, map[string]any)) isc {
		return isc{name: "claim/" + name, argv: j("item", "claim", id, "--as", as), want: want, check: check}
	}
	won := func(t *testing.T, e envl, _ map[string]any) {
		eq(t, e, "data.changed", true)
		eq(t, e, "data.claimId", "me")
		eq(t, e, "data.type", "B")
	}
	add(
		cl("win", "2", "me", 0, won),
		cl("already-holder", "7", "me", 0, func(t *testing.T, e envl, _ map[string]any) { eq(t, e, "data.type", "T") }),
		cl("held-by-other", "6", "me", 4, func(t *testing.T, e envl, _ map[string]any) {
			// #106: failure data carries the posted claim and release
			eq(t, e, "data.blockedBy", "claimed")
			eq(t, e, "data.changed", true)
		}),
		cl("closed", "4", "me", 3, nil),
		cl("bad-as", "2", "a b", 2, nil),
		cl("empty-as", "2", "", 2, nil),
		cl("arrow-as", "2", "a-->b", 2, nil),
	)
	both(cl("gitlab-win", "2", "me", 0, won))
	rl := func(name, id, as string, want int, ch bool) isc {
		return isc{name: "release/" + name, argv: j("item", "release", id, "--as", as), want: want,
			check: func(t *testing.T, e envl, _ map[string]any) {
				if want == 0 {
					eq(t, e, "data.changed", ch)
				}
			}}
	}
	add(rl("match-last-claim", "6", "other", 0, true), rl("match-keeps-state", "7", "me", 0, true), rl("no-match", "6", "zzz", 0, false),
		rl("closed-item", "4", "me", 0, false), rl("bad-as", "6", "a b", 2, false))
	both(rl("gitlab-match", "6", "other", 0, true))

	// ---- show
	showCheck := func(want map[string]any) func(*testing.T, envl, map[string]any) {
		return func(t *testing.T, e envl, _ map[string]any) {
			for k, v := range want {
				eq(t, e, "data."+k, v)
			}
		}
	}
	add(
		isc{name: "show/noted", argv: j("item", "show", "5"), want: 0, textSame: true,
			check: showCheck(map[string]any{"id": "5", "type": "B", "status": "open", "claimedBy": nil, "state": nil})},
		isc{name: "show/claimed", argv: j("item", "show", "6"), want: 0, textSame: true,
			check: showCheck(map[string]any{"id": "6", "type": "F", "claimedBy": "other", "state": "in-progress"})},
		isc{name: "show/milestone", argv: j("item", "show", "1"), want: 0, textSame: true,
			check: showCheck(map[string]any{"milestone": "M07"})},
		isc{name: "show/closed", argv: j("item", "show", "9"), want: 0, textSame: true,
			check: showCheck(map[string]any{"status": "closed"})},
		isc{name: "show/file-backend", file: true, remote: "none", argv: j("item", "show", "B01"), want: 1},
	)
	both(isc{name: "show/noted", argv: j("item", "show", "5"), want: 0, textSame: true})

	// ---- ready
	rdy := func(name, id string, want int, ready bool) isc {
		return isc{name: "ready/" + name, argv: j("item", "ready", id), want: want,
			check: func(t *testing.T, e envl, _ map[string]any) {
				if want == 3 {
					return
				}
				eq(t, e, "data.ready", ready)
			}}
	}
	add(rdy("criteria", "1", 0, true), rdy("design-note", "5", 0, true), rdy("not-ready", "10", 1, false), rdy("not-ready-bug", "2", 1, false))
	both(rdy("criteria", "1", 0, true))

	// ---- comment add / list
	ca := func(name, id, kind, body string, want int) isc {
		return isc{name: "comment-add/" + name, argv: j("item", "comment", "add", id, "--kind", kind, "--body-file", "-"), in: body, want: want,
			check: func(t *testing.T, e envl, _ map[string]any) {
				if want == 0 {
					eq(t, e, "data.changed", true)
				}
			}}
	}
	add(
		ca("question", "2", "question", "Why?\nbecause\n", 0),
		ca("unicode", "5", "decision", "Café — ok ✓", 0),
		ca("empty", "2", "question", "  \n", 2),
		ca("bad-kind", "2", "bogus", "x", 2),
		ca("unknown", "99", "question", "x", 3),
		isc{name: "comment-add/milestone-tracker", argv: j("item", "comment", "add", "3", "--kind", "question", "--body-file", "-"), in: "x", want: 3},
	)
	both(ca("question", "2", "question", "Why?", 0))
	cll := func(name, id string, args []string, want int) isc {
		return isc{name: "comment-list/" + name, argv: j(append([]string{"item", "comment", "list", id}, args...)...), want: want, textSame: true}
	}
	add(cll("all", "5", nil, 0), cll("kind", "5", []string{"--kind", "question"}, 0),
		cll("kind-empty", "5", []string{"--kind", "answer"}, 0), cll("none", "2", nil, 0),
		cll("bad-kind", "5", []string{"--kind", "bogus"}, 2))

	// ---- note add / show / rm
	nadd := func(name, id, kind, text string, want int, ch bool, env ...string) isc {
		return isc{name: "note-add/" + name, argv: j("item", "note", "add", id, "--kind", kind, "--body-file", "-"), in: text, env: env, want: want,
			check: func(t *testing.T, e envl, _ map[string]any) {
				if want == 0 {
					eq(t, e, "data.changed", ch)
				}
			}}
	}
	long := strings.Repeat("a line of the plan that is fairly long\n", 12)
	add(
		nadd("new-proof", "2", "proof", "## Proof\n- x · PASS · y\n", 0, true),
		nadd("edit-design", "5", "design", "new design\n", 0, true),
		nadd("identical", "5", "design", "the design\n", 0, false),
		nadd("unicode", "2", "plan", "Café — ✓\n", 0, true),
		nadd("split", "2", "plan", long, 0, true, "ROTA_NOTE_LIMIT=82"),
		nadd("shrink-parts", "11", "plan", "short", 0, true, "ROTA_NOTE_LIMIT=82"),
		nadd("grow-parts", "5", "design", long, 0, true, "ROTA_NOTE_LIMIT=82"),
		nadd("long-line", "2", "design", strings.Repeat("x", 300), 0, true, "ROTA_NOTE_LIMIT=82"),
		nadd("bad-kind", "2", "bogus", "x", 2, false),
		nadd("unknown", "99", "proof", "x", 3, false),
		isc{name: "note-add/empty-body", argv: j("item", "note", "add", "2", "--kind", "proof", "--body-file", "-"), in: "  \n", want: 2},
		isc{name: "note-add/file-backend", file: true, remote: "none", argv: j("item", "note", "add", "B01", "--kind", "proof", "--body-file", "-"), in: "x", want: 4},
	)
	both(nadd("new-proof", "2", "proof", "## Proof\n- x · PASS · y\n", 0, true))
	nshow := func(name, id, kind string, want int, exists bool, env ...string) isc {
		return isc{name: "note-show/" + name, argv: j("item", "note", "show", id, "--kind", kind), want: want, env: env, textSame: true,
			check: func(t *testing.T, e envl, _ map[string]any) {
				if want != 0 {
					return
				}
				eq(t, e, "data.exists", exists)
			}}
	}
	add(nshow("proof", "5", "proof", 0, true), nshow("design", "5", "design", 0, true), nshow("multipart-plan", "11", "plan", 0, true),
		nshow("absent", "2", "plan", 0, false), nshow("bad-kind", "2", "bogus", 2, false), nshow("file-backend", "B01", "design", 1, false),
		isc{name: "note-show/wrong-letter", argv: j("item", "note", "show", "F2", "--kind", "proof"), want: 3}) // resolves the type letter (3)
	// file-backend scenarios need file:true; patch the one above. note show is
	// read-only, so the contract (#106) wants exit 1.
	for i := range all {
		if strings.HasPrefix(all[i].name, "note-show/file-backend") {
			all[i].file, all[i].remote = true, "none"
		}
	}
	both(nshow("proof", "5", "proof", 0, true))
	nrm := func(name, id, kind string, want int, ch bool) isc {
		return isc{name: "note-rm/" + name, argv: j("item", "note", "rm", id, "--kind", kind), want: want,
			check: func(t *testing.T, e envl, _ map[string]any) {
				if want == 0 {
					eq(t, e, "data.changed", ch)
				}
			}}
	}
	add(nrm("design", "5", "design", 0, true), nrm("multipart", "11", "plan", 0, true), nrm("absent", "2", "plan", 0, false),
		nrm("bad-kind", "2", "bogus", 2, false), nrm("unknown", "99", "plan", 3, false))
	both(nrm("design", "5", "design", 0, true))

	// ---- file backend: claim / release / state are no-ops
	fileOK := func(name string, argv []string, want int, key string, val any) isc {
		return isc{name: "file/" + name, file: true, remote: "none", argv: argv, want: want,
			check: func(t *testing.T, e envl, _ map[string]any) {
				if want == 0 {
					eq(t, e, key, val)
				}
			}}
	}
	add(
		fileOK("claim", j("item", "claim", "B01", "--as", "me"), 0, "data.changed", false),
		fileOK("release", j("item", "release", "B01", "--as", "me"), 0, "data.changed", false),
		isc{name: "file/state", file: true, remote: "none", argv: j("item", "state", "B01", "--to", "in-progress"), want: 0},
		isc{name: "file/state-unknown", file: true, remote: "none", argv: j("item", "state", "B99", "--to", "none"), want: 3},
		isc{name: "file/claim-unknown", file: true, remote: "none", argv: j("item", "claim", "B99", "--as", "me"),
			want: 3},
	)

	seen := map[string]bool{}
	for _, s := range all {
		if seen[s.name] {
			t.Fatalf("duplicate scenario %s", s.name)
		}
		seen[s.name] = true
	}
	t.Logf("%d scenarios", len(all))
	if len(all) < 150 {
		t.Fatalf("only %d scenarios", len(all))
	}
	for _, s := range all {
		s := s
		t.Run(s.name, s.exec)
	}
}
