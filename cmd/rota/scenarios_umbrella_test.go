package main

// Go-only scenarios for the tracker verbs in an umbrella (#48, last slice). The
// issue-backend scenarios build an umbrella project whose sub-repos have github
// or gitlab origins, seed one stateful fake forge store per sub-repo
// (FAKE_TRACKER_DB_DIR, keyed by the sub-repo's git toplevel basename), run the
// Go binary and check its exit code, envelope, .rota/ tree and every sub-repo's
// final forge store against the frozen record. The file-backend scenarios reuse
// the shared harness (scn).
//
// Safety: the shared TestMain refuses to run unless gh resolves to test/fakes
// (TestUbFakesFirst checks glab too); every run has its own store directory
// under t.TempDir, and FAKE_TRACKER_DB_DIR wins over the harness-wide
// FAKE_TRACKER_DB, so no scenario can touch another's store.
//
// Behaviour worth knowing (contract rulings):
//  1. --repo narrows reads and bare references to the sub-repo and names the
//     capture target (scope S, orchestrator ruling on #119).
//  2. cwd inside a sub-repo narrows reads and bare refs the same way.
//  3. field set --name repos is a usage error (read-only field).
//  4. ids, summary recents and item show print the qualified ID of rule 11
//     ("web:12").
//  5. a stream's item hides only its own sub-repo's row (backlog list).
//  6. data.changed reports whether the tracker changed.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestUbFakesFirst(t *testing.T) { TestIssueFakesFirst(t) }

// ubRepo is one sub-repo of the fixture umbrella.
type ubRepo struct {
	name   string
	remote string // github | gitlab | none
	db     func() map[string]any
}

const (
	ubGH = "https://github.com/example/%s.git"
	ubGL = "https://gitlab.com/example/%s.git"
)

func ubClosed(is map[string]any, at, reason string) map[string]any {
	is["state"], is["state_reason"], is["closed_at"] = "closed", reason, at
	return is
}

func ubDB(milestones []any, issues ...map[string]any) map[string]any {
	next := 1
	var list []any
	for _, i := range issues {
		list = append(list, i)
		if n := i["number"].(int); n >= next {
			next = n + 1
		}
	}
	if list == nil {
		list = []any{}
	}
	if milestones == nil {
		milestones = []any{}
	}
	return map[string]any{"next_issue": next, "next_milestone": len(milestones) + 1, "next_comment": 50, "next_mr": 1,
		"labels": []any{}, "prs": []any{}, "milestones": milestones, "issues": list}
}

func ubMilestone(n int, title, state string) any {
	return map[string]any{"number": n, "title": title, "description": "", "state": state}
}

// ubWebDB is the github sub-repo and the home of the milestone tracking issues:
// F1 and B2 open, T3 closed, #4 the M07 tracking issue, T5 with a design note.
// Its numbers collide with api's on purpose.
func ubWebDB() map[string]any {
	f1 := iss(1, "Web feat", "Web.\n\n## Acceptance\n- [ ] works\n\n<!-- rota:fields\nRelated: B2\n-->",
		[]string{"type:feature", "size:Major"}, "open")
	f1["milestone"] = []any{"M07 — Title", 1}
	return ubDB([]any{ubMilestone(1, "M07 — Title", "open")},
		f1,
		iss(2, "Web bug", "boom", []string{"type:bug", "p1"}, "open"),
		iss(3, "Web done", "x", []string{"type:task"}, "closed"),
		iss(4, "M07 — Title", "", []string{"milestone-tracker"}, "open"),
		iss(5, "Web task", "t", []string{"type:task"}, "open", cm(1, "<!-- rota:design -->\nthe design")),
	)
}

// ubAPIDB is the gitlab sub-repo: F1 open, B2 closed, T3 open, B4 with a
// proof note and a question, F5 claimed by "other".
func ubAPIDB() map[string]any {
	f5 := iss(5, "Api held", "x", []string{"type:feature", "in-progress"}, "open", cm(7, "<!-- rota:claim other -->\nClaimed by other"))
	return ubDB(nil,
		iss(1, "Api feat", "Api.", []string{"type:feature"}, "open"),
		ubClosed(iss(2, "Api done", "x", []string{"type:bug"}, "closed"), "2026-09-03T10:00:00Z", "completed"),
		iss(3, "Api task", "", []string{"type:task"}, "open"),
		iss(4, "Api bug", "crash\n\n## Acceptance\n- [ ] fixed", []string{"type:bug", "p2"}, "open",
			cm(2, "<!-- rota:proof -->\n## Proof\n- build · PASS · ok"), cm(3, "<!-- rota:comment question -->\nWhy?")),
		f5,
	)
}

// ubLibDB is a third sub-repo (github), for three-way ambiguity.
func ubLibDB() map[string]any {
	return ubDB(nil,
		iss(1, "Lib feat", "L", []string{"type:feature"}, "open"),
		iss(5, "Lib task", "L", []string{"type:task"}, "open"),
		iss(6, "Lib bug", "L", []string{"type:bug"}, "open"),
	)
}

func ubDefault() []ubRepo {
	return []ubRepo{{"web", "github", ubWebDB}, {"api", "gitlab", ubAPIDB}}
}

func ubThree() []ubRepo {
	return append(ubDefault(), ubRepo{"lib", "github", ubLibDB})
}

// ubCfg names web the home repo (milestone tracking issues): the fixture
// registry is sorted by name, so the default home would be api.
const ubCfg = `{"backlog": {"backend": "issues"}, "issues": {"retryWaitSeconds": 0, "homeRepo": "web"}}`

// ubcase is one scenario against an issue-backend umbrella.
type ubcase struct {
	name     string
	repos    []ubRepo // default ubDefault
	cfg      string   // config.json, default ubCfg
	status   string   // .rota/status.json
	cwd      string   // working directory relative to the umbrella ("" root, "web", "web/src/deep")
	argv     []string // argv, --json included
	in       string
	env      []string
	want     int
	changed  *bool // Go's data.changed on success
	textSame bool  // also record the text-mode stdout (read-only verbs)
	check    func(t *testing.T, e envl, dbs map[string]map[string]any)
}

func ubInit(t *testing.T, c ubcase) (dir string, repos []ubRepo) {
	t.Helper()
	repos = c.repos
	if repos == nil {
		repos = ubDefault()
	}
	cfg := c.cfg
	if cfg == "" {
		cfg = ubCfg
	}
	subs := map[string][]string{}
	for _, r := range repos {
		subs[r.name] = nil
	}
	f := fx{config: cfg, status: c.status, noBacklog: true, subs: subs, after: func(t *testing.T, dir string, _ *info) {
		for _, r := range repos {
			if r.remote != "none" {
				git(t, filepath.Join(dir, r.name), "remote", "add", "origin", fmt.Sprintf(map[string]string{"github": ubGH, "gitlab": ubGL}[r.remote], r.name))
			}
			if err := os.MkdirAll(filepath.Join(dir, r.name, "src", "deep"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.MkdirAll(filepath.Join(dir, "docs", "deep"), 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, dir, "body.md", "# {ID}\n\nSee [{ID}] in the backlog.\n")
		write(t, dir, "raw.md", "- **[B77] [P1] Raw.** x\n")
	}}
	dir, _ = f.build(t)
	return dir, repos
}

func ubSeed(t *testing.T, repos []ubRepo) string {
	t.Helper()
	d := t.TempDir()
	for _, r := range repos {
		raw, _ := json.Marshal(r.db())
		if err := os.WriteFile(filepath.Join(d, r.name+".json"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func ubReadAll(t *testing.T, dbDir string, repos []ubRepo) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, r := range repos {
		out[r.name] = readDB(t, filepath.Join(dbDir, r.name+".json"))
	}
	return out
}

func (c ubcase) exec(t *testing.T) {
	t.Parallel()
	base, repos := ubInit(t, c)
	dir := copyTree(t, base)
	dbDir := ubSeed(t, repos)
	cwd := filepath.Join(dir, c.cwd)
	env := frozenEnv(append([]string{"FAKE_TRACKER_DB_DIR=" + dbDir}, c.env...))
	r := envRun(t, cwd, c.in, env, rotaBin, c.argv...)
	if r.code != c.want {
		t.Errorf("exit = %d, want %d\nargv: %v\nstdout: %s\nstderr: %s", r.code, c.want, c.argv, r.stdout, r.stderr)
	}
	dbs := ubReadAll(t, dbDir, repos)
	st := newStep(t, r, snapshot(t, base), dir, dbs)
	if c.textSame {
		st.text(envRun(t, cwd, c.in, env, rotaBin, withoutJSON(c.argv)...).stdout)
	}
	e := parseEnv(t, "go", r)
	if c.changed != nil && c.want == 0 && at(e, "data.changed") != *c.changed {
		t.Errorf("data.changed = %v, want %v", at(e, "data.changed"), *c.changed)
	}
	if c.check != nil {
		e["__info"] = info{}
		c.check(t, e, dbs)
	}
	frozenCheck(t, frozenRec{Steps: []frozenStep{st}})
}

// ubIssue is issue n of a repo's final store, nil when absent.
func ubIssue(dbs map[string]map[string]any, repo string, n int) map[string]any {
	if dbs[repo] == nil {
		return nil
	}
	for _, i := range dbs[repo]["issues"].([]any) {
		if m := i.(map[string]any); fmt.Sprint(m["number"]) == fmt.Sprint(n) {
			return m
		}
	}
	return nil
}

func ubMilestoneTitles(dbs map[string]map[string]any, repo string) string {
	var out []string
	for _, m := range dbs[repo]["milestones"].([]any) {
		mm := m.(map[string]any)
		out = append(out, mm["title"].(string)+":"+mm["state"].(string))
	}
	return strings.Join(out, ",")
}

func ubHas(labels any, want string) bool {
	for _, l := range labels.([]any) {
		if l == want {
			return true
		}
	}
	return false
}

var ubQualRe = regexp.MustCompile(`^[a-z]+:`)

func suiteUmbrella(t *testing.T) {
	var all []ubcase
	add := func(s ...ubcase) { all = append(all, s...) }
	cwds := []string{"", "web", "web/src/deep", "api", "docs/deep"}
	withCwds := func(base ubcase, cws ...string) {
		for _, cw := range cws {
			s := base
			s.cwd = cw
			s.name = fmt.Sprintf("%s/cwd=%s", base.name, map[bool]string{true: "root", false: strings.ReplaceAll(cw, "/", "_")}[cw == ""])
			add(s)
		}
	}
	rate := []string{"FAKE_TRACKER_FAIL=issue view", "FAKE_TRACKER_FAIL_MSG=secondary rate limit"}

	// ---- field get / list: every ref spelling and cwd
	fget := func(name, ref, field string, want int) ubcase {
		return ubcase{name: "fieldget/" + name, argv: j("item", "field", "get", ref, "--name", field), want: want}
	}
	for _, c := range []struct {
		n, ref, field string
		want          int
		title         string
	}{
		{"qual-colon-letter", "web:F1", "title", 0, "Web feat"}, {"qual-colon-number", "web:1", "title", 0, "Web feat"},
		{"qual-colon-hash", "web:#1", "title", 0, "Web feat"}, {"qual-hash", "web#1", "title", 0, "Web feat"},
		{"qual-api", "api:F1", "title", 0, "Api feat"}, {"qual-lower-letter", "api:f1", "title", 0, "Api feat"},
		{"bare-unique-T5", "T5", "title", 0, "Web task"}, {"bare-unique-B4", "B4", "title", 0, "Api bug"},
		{"bare-unique-F5", "F5", "title", 0, "Api held"}, {"bare-hash-unique", "#4", "title", 0, "Api bug"},
		{"bare-number-unique", "6", "title", 3, ""},
		{"ambiguous-F1", "F1", "title", 2, ""}, {"ambiguous-hash", "#1", "title", 2, ""}, {"ambiguous-number", "1", "title", 2, ""},
		{"ambiguous-closed-B2", "B2", "title", 2, ""}, {"ambiguous-T3", "T3", "title", 2, ""}, {"ambiguous-hash5", "#5", "title", 2, ""},
		{"unknown-bare", "F99", "title", 3, ""}, {"unknown-qualified", "web:99", "title", 3, ""}, {"unknown-repo", "nope:F1", "title", 3, ""},
		{"unknown-repo-hash", "nope#1", "title", 3, ""}, {"wrong-letter", "web:B1", "title", 3, ""}, {"tracker-issue", "web:4", "title", 3, ""},
		{"malformed", "web:", "title", 3, ""}, {"junk", "zzz", "title", 3, ""},
		{"milestone", "web:F1", "milestone", 0, "M07"}, {"related", "web:F1", "related", 0, "[B2]"}, {"repos", "api:F1", "repos", 0, "api"},
		{"closed-reason", "api:B2", "reason", 0, "done"}, {"closed-title", "web:T3", "title", 0, "Web done"},
		{"detail-url", "web:F1", "detail", 0, ""}, {"empty-field", "api:T3", "milestone", 0, ""},
		{"bad-field", "web:F1", "nope", 2, ""},
	} {
		c := c
		s := fget(c.n, c.ref, c.field, c.want)
		if c.want == 0 && c.field == "title" {
			s.check = func(t *testing.T, e envl, _ map[string]map[string]any) {
				eq(t, e, "data.value", c.title)
				if id, _ := at(e, "data.id").(string); !ubQualRe.MatchString(id) {
					t.Errorf("data.id %q is not qualified", id)
				}
			}
		}
		add(s)
	}
	// Scope S (orchestrator ruling on #119): a sub-repo cwd narrows reads and
	// bare refs like --repo does, so F1 resolves to the cwd's own F1 and B4
	// (api only) is unknown from web. Qualified refs resolve anywhere.
	for _, cw := range cwds {
		in := ubSubRepoOf(cw)
		amb, uniq := 2, 0
		if in != "" {
			amb = 0
		}
		if in == "web" {
			uniq = 3
		}
		withCwds(fget("cwd-ambiguous", "F1", "title", amb), cw)
		withCwds(fget("cwd-qualified", "api:F1", "title", 0), cw)
		withCwds(fget("cwd-bare-unique", "B4", "title", uniq), cw)
	}
	add(
		ubcase{name: "fieldlist/qualified", argv: j("item", "field", "list", "web:F1"), want: 0},
		ubcase{name: "fieldlist/bare-unique", argv: j("item", "field", "list", "T5"), want: 0},
		ubcase{name: "fieldlist/closed", argv: j("item", "field", "list", "api:B2"), want: 0},
		ubcase{name: "fieldlist/ambiguous", argv: j("item", "field", "list", "F1"), want: 2},
		ubcase{name: "fieldlist/unknown", argv: j("item", "field", "list", "api:F99"), want: 3},
		fget("rate-limit", "web:F1", "title", 6),
		fget("forge-down-one-repo", "web:F1", "title", 5),
	)
	all[len(all)-2].env = rate
	all[len(all)-1].repos = []ubRepo{{"web", "none", ubWebDB}, {"api", "gitlab", ubAPIDB}}
	// the other sub-repo's forge may be down without hurting a qualified ref
	add(ubcase{name: "fieldget/other-forge-down", repos: []ubRepo{{"web", "none", ubWebDB}, {"api", "gitlab", ubAPIDB}},
		argv: j("item", "field", "get", "api:F1", "--name", "title"), want: 0})
	// a bare ref has to probe every sub-repo, so a down forge fails it
	add(ubcase{name: "fieldget/bare-needs-every-forge", repos: []ubRepo{{"web", "none", ubWebDB}, {"api", "gitlab", ubAPIDB}},
		argv: j("item", "field", "get", "B4", "--name", "title"), want: 5})

	// ---- three sub-repos: the candidates are all listed
	add(
		ubcase{name: "three/ambiguous-lists-all", repos: ubThree(), argv: j("item", "field", "get", "F1", "--name", "title"), want: 2,
			check: func(t *testing.T, e envl, _ map[string]map[string]any) {
				msg, _ := at(e, "error.message").(string)
				for _, c := range []string{"web:1", "api:1", "lib:1"} {
					if !strings.Contains(msg, c) {
						t.Errorf("message %q lacks %s", msg, c)
					}
				}
			}},
		ubcase{name: "three/unique-after-third", repos: ubThree(), argv: j("item", "field", "get", "B6", "--name", "title"), want: 0},
		ubcase{name: "three/two-of-three", repos: ubThree(), argv: j("item", "field", "get", "T5", "--name", "title"), want: 2},
		ubcase{name: "three/qualified-third", repos: ubThree(), argv: j("item", "field", "get", "lib:F1", "--name", "title"), want: 0},
	)

	// ---- --repo: validated, then narrows (scope S)
	for _, c := range []struct {
		n    string
		argv []string
		want int
	}{
		{"unregistered", j("item", "field", "get", "web:F1", "--name", "title", "--repo", "nope"), 3},
		{"unregistered-create", j("item", "create", "--kind", "tasks", "--title", "x", "--repo", "nope"), 3},
		{"unregistered-complete", j("item", "complete", "web:F1", "--commit", "abc1234", "--no-proof", "--repo", "nope"), 3},
		{"unregistered-list", j("backlog", "list", "--repo", "nope"), 3},
		{"unregistered-summary", j("summary", "--repo", "nope"), 3},
		{"unregistered-show", j("item", "show", "web:F1", "--repo", "nope"), 3},
	} {
		add(ubcase{name: "repoflag/" + c.n, argv: c.argv, want: c.want})
	}
	add(
		ubcase{name: "repoflag/qualified-unaffected", argv: j("item", "field", "get", "api:F1", "--name", "title", "--repo", "web"), want: 0},
		ubcase{name: "repoflag/bare-narrowed", argv: j("item", "field", "get", "F1", "--name", "title", "--repo", "web"), want: 0,
			check: func(t *testing.T, e envl, _ map[string]map[string]any) {
				eq(t, e, "data.id", "web:1")
				eq(t, e, "data.value", "Web feat")
			}},
		ubcase{name: "repoflag/bare-narrowed-api", argv: j("item", "field", "get", "F1", "--name", "title", "--repo", "api"), want: 0,
			check: func(t *testing.T, e envl, _ map[string]map[string]any) { eq(t, e, "data.id", "api:1") }},
		ubcase{name: "repoflag/bare-outside-scope", argv: j("item", "field", "get", "B4", "--name", "title", "--repo", "web"), want: 3},
		ubcase{name: "repoflag/complete-narrowed", argv: j("item", "complete", "F1", "--commit", "abc1234", "--no-proof", "--repo", "api"), want: 0,
			check: func(t *testing.T, e envl, dbs map[string]map[string]any) {
				eq(t, e, "data.id", "api:1")
			}},
	)

	// ---- field set
	fset := func(name, ref, field, value string, want int, ch *bool) ubcase {
		return ubcase{name: "fieldset/" + name, argv: j("item", "field", "set", ref, "--name", field, "--value", value), want: want, changed: ch}
	}
	add(
		fset("milestone-native-exists", "web:B2", "milestone", "M07", 0, yes()),
		fset("milestone-same", "web:F1", "milestone", "M07", 0, no()),
		fset("milestone-clear", "web:F1", "milestone", "", 0, yes()),
		fset("milestone-clear-none", "api:F1", "milestone", "", 0, no()),
		fset("milestone-created-on-first-use", "api:F1", "milestone", "M07", 0, yes()),
		fset("milestone-created-bare", "B4", "milestone", "M07", 0, yes()),
		fset("milestone-unknown", "api:F1", "milestone", "M99", 3, nil),
		fset("milestone-unknown-home", "web:B2", "milestone", "M99", 3, nil),
		fset("related", "api:F1", "related", "B4", 0, yes()),
		fset("related-same", "web:F1", "related", "B2", 0, no()),
		fset("related-clear", "web:F1", "related", "", 0, yes()),
		fset("subsystem", "api:T3", "subsystem", "capture", 0, yes()),
		fset("closed", "web:T3", "related", "B1", 4, nil),
		fset("ambiguous", "F1", "related", "B1", 2, nil),
		fset("unknown", "api:F99", "related", "B1", 3, nil),
		fset("unknown-repo", "nope:F1", "related", "B1", 3, nil),
		fset("bad-name", "api:F1", "title", "x", 2, nil),
		fset("detail-refused", "api:F1", "detail", "x", 4, nil),
		fset("bare-unique", "T5", "related", "B2", 0, yes()),
		ubcase{name: "fieldset/repos-readonly", argv: j("item", "field", "set", "web:F1", "--name", "repos", "--value", "api"), want: 2},
		ubcase{name: "fieldset/repos-readonly-unknown-item", argv: j("item", "field", "set", "nope:F1", "--name", "repos", "--value", "api"), want: 3},
		ubcase{name: "fieldset/milestone-bad-format", argv: j("item", "field", "set", "api:F1", "--name", "milestone", "--value", "later"), want: 2},
		ubcase{name: "fieldset/rate-limit", argv: j("item", "field", "set", "web:F1", "--name", "related", "--value", "B1"), want: 6,
			env: []string{"FAKE_TRACKER_FAIL=issue edit", "FAKE_TRACKER_FAIL_MSG=secondary rate limit"}},
	)
	for _, cw := range []string{"web", "api", "docs/deep"} {
		withCwds(fset("milestone-created", "api:F1", "milestone", "M07", 0, yes()), cw)
	}
	// milestone assignment checks the native milestone lands on the owner only
	add(ubcase{name: "fieldset/milestone-created-native-only-on-owner", argv: j("item", "field", "set", "api:F1", "--name", "milestone", "--value", "M07"), want: 0, changed: yes(),
		check: func(t *testing.T, e envl, dbs map[string]map[string]any) {
			if got := ubMilestoneTitles(dbs, "api"); got != "M07 — Title:open" {
				t.Errorf("api milestones %q", got)
			}
			if got := ubMilestoneTitles(dbs, "web"); got != "M07 — Title:open" {
				t.Errorf("web milestones %q", got)
			}
		}})
	add(ubcase{name: "fieldset/home-repo-config", cfg: `{"backlog": {"backend": "issues"}, "issues": {"retryWaitSeconds": 0, "homeRepo": "web"}}`,
		argv: j("item", "field", "set", "api:F1", "--name", "milestone", "--value", "M07"), want: 0, changed: yes()})
	add(ubcase{name: "fieldset/home-repo-unregistered", cfg: `{"backlog": {"backend": "issues"}, "issues": {"retryWaitSeconds": 0, "homeRepo": "ghost"}}`,
		argv: j("item", "field", "set", "api:F1", "--name", "milestone", "--value", "M07"), want: 3})

	// ---- create
	cr := func(name string, want int, args ...string) ubcase {
		return ubcase{name: "create/" + name, argv: j(append([]string{"item", "create"}, args...)...), want: want, changed: yes()}
	}
	add(
		cr("repos-web-task", 0, "--kind", "tasks", "--title", "New task", "--repos", "web"),
		cr("repos-api-feature", 0, "--kind", "features", "--title", "New feat", "--tag", "Major", "--repos", "api", "--desc", "d", "--related", "B4", "--subsystem", "cli", "--captured", "2026-10-01"),
		cr("repos-api-bug-p1", 0, "--kind", "bugs", "--title", "Broken", "--tag", "P1", "--repos", "api"),
		cr("repos-spaces", 0, "--kind", "tasks", "--title", "Spaced", "--repos", " web "),
		cr("repos-milestone-native", 0, "--kind", "tasks", "--title", "With ms", "--repos", "web", "--milestone", "M07"),
		cr("repos-milestone-created", 0, "--kind", "tasks", "--title", "With ms", "--repos", "api", "--milestone", "M07"),
		cr("milestone-missing", 3, "--kind", "tasks", "--title", "x", "--repos", "api", "--milestone", "M99"),
		cr("no-target", 3, "--kind", "tasks", "--title", "Nowhere"),
		cr("several-repos", 3, "--kind", "tasks", "--title", "Both", "--repos", "web,api"),
		cr("unknown-repo", 3, "--kind", "tasks", "--title", "Bad", "--repos", "nope"),
		cr("bad-tag", 2, "--kind", "bugs", "--title", "x", "--tag", "Major", "--repos", "web"),
		cr("no-title", 2, "--kind", "tasks", "--repos", "web"),
		cr("empty-repos", 2, "--kind", "tasks", "--title", "x", "--repos", ""),
		cr("body-id-placeholder", 0, "--kind", "tasks", "--title", "Doc", "--repos", "web", "--body-file", "body.md"),
		ubcase{name: "create/rate-limit", argv: j("item", "create", "--kind", "tasks", "--title", "x", "--repos", "web"), want: 6,
			env: []string{"FAKE_TRACKER_FAIL=issue create", "FAKE_TRACKER_FAIL_MSG=secondary rate limit"}},
		ubcase{name: "create/forge-unavailable", repos: []ubRepo{{"web", "none", ubWebDB}, {"api", "gitlab", ubAPIDB}},
			argv: j("item", "create", "--kind", "tasks", "--title", "x", "--repos", "web"), want: 5},
		ubcase{name: "create/other-forge-down", repos: []ubRepo{{"web", "none", ubWebDB}, {"api", "gitlab", ubAPIDB}},
			argv: j("item", "create", "--kind", "tasks", "--title", "x", "--repos", "api"), want: 0, changed: yes()},
	)
	// cwd inside a sub-repo is the capture target, a deep directory too
	for _, c := range []struct {
		cw, repo string
		want     int
	}{{"web", "web", 0}, {"web/src/deep", "web", 0}, {"api", "api", 0}, {"api/src/deep", "api", 0}, {"", "", 3}, {"docs/deep", "", 3}} {
		c := c
		s := cr("cwd", c.want, "--kind", "tasks", "--title", "From cwd")
		s.cwd = c.cw
		s.name = "create/cwd=" + map[bool]string{true: "root", false: strings.ReplaceAll(c.cw, "/", "_")}[c.cw == ""]
		if c.want == 0 {
			s.check = func(t *testing.T, e envl, dbs map[string]map[string]any) {
				if id, _ := at(e, "data.id").(string); !strings.HasPrefix(id, c.repo+":") {
					t.Errorf("data.id %q, want %s:N", id, c.repo)
				}
			}
		}
		add(s)
	}
	add(
		withCwdCase(cr("cwd-field-wins", 0, "--kind", "tasks", "--title", "Field", "--repos", "api"), "web"),
		withCwdCase(cr("cwd-gitlab-field", 0, "--kind", "features", "--title", "Gl", "--repos", "api", "--milestone", "M07"), "web/src/deep"),
	)
	// --repo names the capture target (scope S)
	add(
		ubcase{name: "create/repoflag-target", argv: j("item", "create", "--kind", "tasks", "--title", "Via flag", "--repo", "api"), want: 0,
			check: func(t *testing.T, e envl, _ map[string]map[string]any) {
				eq(t, e, "data.id", "api:6")
				eq(t, e, "data.type", "T")
			}},
		ubcase{name: "create/repoflag-from-cwd-wins", cwd: "web", argv: j("item", "create", "--kind", "tasks", "--title", "Via flag", "--repo", "api"), want: 0,
			check: func(t *testing.T, e envl, _ map[string]map[string]any) { eq(t, e, "data.id", "api:6") }},
		cr("repoflag-and-repos-same", 0, "--kind", "tasks", "--title", "Same", "--repos", "web", "--repo", "web"),
		ubcase{name: "create/repoflag-and-repos-differ", argv: j("item", "create", "--kind", "tasks", "--title", "Differ", "--repos", "web", "--repo", "api"), want: 2},
	)

	// ---- complete
	co := func(name, ref string, want int, ch *bool, args ...string) ubcase {
		return ubcase{name: "complete/" + name, argv: j(append([]string{"item", "complete", ref, "--commit", "abc1234"}, args...)...), want: want, changed: ch}
	}
	add(
		co("done-with-proof", "api:B4", 0, yes()),
		co("done-proof-missing", "web:F1", 4, nil),
		co("done-no-proof", "web:F1", 0, yes(), "--no-proof"),
		co("done-bare-unique", "T5", 0, yes(), "--no-proof"),
		co("done-note", "web:B2", 0, yes(), "--no-proof", "--note", "a\nb"),
		co("dropped", "web:B2", 0, yes(), "--reason", "dropped", "--note", "n"),
		co("handed-off", "api:F5", 0, yes(), "--reason", "handed-off"),
		co("blocked", "api:T3", 0, yes(), "--reason", "blocked", "--note", "waiting"),
		co("already-closed", "web:T3", 0, no()),
		co("closed-in-other-repo-only", "api:B2", 0, no()),
		co("ambiguous", "F1", 2, nil, "--no-proof"),
		co("ambiguous-hash", "#1", 2, nil, "--no-proof"),
		co("unknown", "api:F99", 3, nil, "--no-proof"),
		co("unknown-repo", "nope:F1", 3, nil, "--no-proof"),
		co("bad-reason", "web:F1", 2, nil, "--reason", "nope"),
		co("hash-form", "api#1", 0, yes(), "--no-proof"),
	)
	add(ubcase{name: "complete/rate-limit", argv: j("item", "complete", "web:F1", "--commit", "abc1234", "--no-proof"), want: 6,
		env: []string{"FAKE_TRACKER_FAIL=issue close", "FAKE_TRACKER_FAIL_MSG=secondary rate limit"}})
	withCwds(co("cwd", "api:T3", 0, yes(), "--no-proof"), "web", "api/src/deep", "docs/deep")
	// the other repo stays untouched
	add(ubcase{name: "complete/other-repo-untouched", argv: j("item", "complete", "api:F1", "--commit", "abc1234", "--no-proof"), want: 0, changed: yes(),
		check: func(t *testing.T, e envl, dbs map[string]map[string]any) {
			if ubIssue(dbs, "api", 1)["state"] != "closed" || ubIssue(dbs, "web", 1)["state"] != "open" {
				t.Errorf("api #1 %v, web #1 %v", ubIssue(dbs, "api", 1)["state"], ubIssue(dbs, "web", 1)["state"])
			}
		}})

	// ---- reopen / state
	for _, c := range []struct {
		n, ref string
		ch     *bool
	}{{"closed-web", "web:T3", yes()}, {"closed-api", "api:B2", yes()}, {"open-noop", "web:B2", no()}, {"bare-unique-closed", "B2", nil}} {
		s := ubcase{name: "reopen/" + c.n, argv: j("item", "reopen", c.ref), want: 0, changed: c.ch}
		if c.ref == "B2" {
			s.want = 2 // closed API B2 and open web B2 collide
		}
		add(s)
	}
	add(
		ubcase{name: "reopen/unknown", argv: j("item", "reopen", "web:F99"), want: 3},
		ubcase{name: "reopen/ambiguous", argv: j("item", "reopen", "T3"), want: 2},
	)
	st := func(name, ref, to string, want int, ch *bool) ubcase {
		return ubcase{name: "state/" + name, argv: j("item", "state", ref, "--to", to), want: want, changed: ch}
	}
	add(
		st("in-progress", "web:B2", "in-progress", 0, yes()),
		st("needs-review-gitlab", "api:T3", "needs-review", 0, yes()),
		st("swap", "api:F5", "needs-review", 0, yes()),
		st("none", "api:F5", "none", 0, yes()),
		st("none-noop", "web:B2", "none", 0, no()),
		st("bare-unique", "T5", "changes-requested", 0, yes()),
		st("ambiguous", "F1", "in-progress", 2, nil),
		st("unknown", "web:F99", "in-progress", 3, nil),
		st("bad-to", "web:B2", "bogus", 2, nil),
	)

	// ---- claim / release / show / ready / comment / note
	cl := func(name, ref, as string, want int, check func(*testing.T, envl, map[string]map[string]any)) ubcase {
		return ubcase{name: "claim/" + name, argv: j("item", "claim", ref, "--as", as), want: want, check: check}
	}
	won := func(id, typ string) func(*testing.T, envl, map[string]map[string]any) {
		return func(t *testing.T, e envl, _ map[string]map[string]any) {
			eq(t, e, "data.id", id)
			eq(t, e, "data.type", typ)
			eq(t, e, "data.changed", true)
		}
	}
	add(
		cl("qualified-gh", "web:B2", "w1", 0, won("web:2", "B")),
		cl("qualified-gl", "api:T3", "w1", 0, won("api:3", "T")),
		cl("bare-unique", "B4", "w2", 0, won("api:4", "B")),
		cl("hash-form", "web#5", "w2", 0, won("web:5", "T")),
		cl("held-by-other", "api:F5", "me", 4, func(t *testing.T, e envl, _ map[string]map[string]any) {
			eq(t, e, "data.blockedBy", "claimed")
			eq(t, e, "data.changed", true)
		}),
		cl("ambiguous", "F1", "me", 2, nil),
		cl("ambiguous-hash", "#5", "me", 2, nil),
		cl("unknown", "web:99", "me", 3, nil),
		cl("unknown-repo", "nope:F1", "me", 3, nil),
		cl("closed", "web:T3", "me", 3, nil),
		cl("bad-as", "web:B2", "a b", 2, nil),
		cl("only-owner-repo", "api:T3", "w1", 0, func(t *testing.T, e envl, dbs map[string]map[string]any) {
			if !ubHas(ubIssue(dbs, "api", 3)["labels"], "in-progress") || ubHas(ubIssue(dbs, "web", 3)["labels"], "in-progress") {
				t.Errorf("claim label landed on the wrong tracker")
			}
		}),
	)
	rl := func(name, ref, as string, want int, ch bool) ubcase {
		return ubcase{name: "release/" + name, argv: j("item", "release", ref, "--as", as), want: want,
			check: func(t *testing.T, e envl, _ map[string]map[string]any) {
				if want == 0 {
					eq(t, e, "data.changed", ch)
				}
			}}
	}
	add(
		rl("match", "api:F5", "other", 0, true), rl("no-match", "api:F5", "zzz", 0, false), rl("never-claimed", "web:B2", "me", 0, false),
		rl("ambiguous", "F1", "me", 2, false), rl("unknown", "web:99", "me", 3, false), rl("closed", "web:T3", "me", 0, false),
	)
	show := func(name, ref string, want int, _ bool, check func(*testing.T, envl, map[string]map[string]any)) ubcase {
		return ubcase{name: "show/" + name, argv: j("item", "show", ref), want: want, check: check}
	}
	add(
		show("qualified-gh", "web:F1", 0, true, func(t *testing.T, e envl, _ map[string]map[string]any) {
			eq(t, e, "data.id", "web:1")
			eq(t, e, "data.type", "F")
			eq(t, e, "data.milestone", "M07")
		}),
		show("qualified-gl-noted", "api:B4", 0, true, func(t *testing.T, e envl, _ map[string]map[string]any) {
			eq(t, e, "data.id", "api:4")
			eq(t, e, "data.type", "B")
		}),
		show("claimed", "api:F5", 0, true, func(t *testing.T, e envl, _ map[string]map[string]any) { eq(t, e, "data.claimedBy", "other") }),
		show("closed", "api:B2", 0, true, func(t *testing.T, e envl, _ map[string]map[string]any) { eq(t, e, "data.status", "closed") }),
		show("hash-form", "web#5", 0, true, func(t *testing.T, e envl, _ map[string]map[string]any) { eq(t, e, "data.id", "web:5") }),
		show("bare-unique", "B4", 0, false, func(t *testing.T, e envl, _ map[string]map[string]any) {
			// item show always prints the qualified ID (rule 11)
			eq(t, e, "data.id", "api:4")
		}),
		show("ambiguous", "F1", 2, false, nil),
		show("unknown", "web:F99", 3, false, nil),
		show("tracker-issue", "web:4", 3, false, nil),
		show("rate-limit", "web:F1", 6, false, nil),
	)
	all[len(all)-1].env = rate
	for _, cw := range []string{"web", "api/src/deep"} {
		withCwds(show("cwd", "api:F1", 0, true, nil), cw)
	}
	rdy := func(name, ref string, want int, ready bool) ubcase {
		return ubcase{name: "ready/" + name, argv: j("item", "ready", ref), want: want,
			check: func(t *testing.T, e envl, _ map[string]map[string]any) {
				if want == 3 || want == 2 {
					return
				}
				eq(t, e, "data.ready", ready)
			}}
	}
	add(
		rdy("criteria", "web:F1", 0, true), rdy("design-note", "web:T5", 0, true), rdy("criteria-gl", "api:B4", 0, true),
		rdy("not-ready", "api:T3", 1, false), rdy("not-ready-bare", "F5", 1, false), rdy("ambiguous", "F1", 2, false), rdy("unknown", "web:99", 3, false),
	)
	ca := func(name, ref, kind, body string, want int) ubcase {
		return ubcase{name: "comment-add/" + name, argv: j("item", "comment", "add", ref, "--kind", kind, "--body-file", "-"), in: body, want: want}
	}
	add(
		ca("gh", "web:B2", "question", "Why?", 0), ca("gl", "api:T3", "decision", "Café ✓", 0), ca("bare-unique", "B4", "answer", "Because", 0),
		ca("ambiguous", "F1", "question", "x", 2), ca("unknown", "web:99", "question", "x", 3), ca("bad-kind", "web:B2", "bogus", "x", 2),
		ca("empty", "web:B2", "question", " \n", 2),
	)
	cll := func(name, ref string, args []string, want int) ubcase {
		return ubcase{name: "comment-list/" + name, argv: j(append([]string{"item", "comment", "list", ref}, args...)...), want: want, textSame: true}
	}
	add(cll("gl", "api:B4", nil, 0), cll("kind", "api:B4", []string{"--kind", "question"}, 0),
		cll("none", "web:B2", nil, 0), cll("ambiguous", "B2", nil, 2), cll("unknown", "api:99", nil, 3), cll("bare-unique", "B4", nil, 0))
	nadd := func(name, ref, kind, text string, want int, ch bool) ubcase {
		return ubcase{name: "note-add/" + name, argv: j("item", "note", "add", ref, "--kind", kind, "--body-file", "-"), in: text, want: want,
			check: func(t *testing.T, e envl, _ map[string]map[string]any) {
				if want == 0 {
					eq(t, e, "data.changed", ch)
				}
			}}
	}
	add(
		nadd("new-gh", "web:B2", "proof", "## Proof\n- x · PASS · y\n", 0, true), nadd("edit-gh", "web:T5", "design", "new\n", 0, true),
		nadd("identical", "web:T5", "design", "the design\n", 0, false), nadd("new-gl", "api:T3", "plan", "plan it\n", 0, true),
		nadd("bare-unique", "B4", "design", "d\n", 0, true), nadd("ambiguous", "F1", "plan", "p", 2, false),
		nadd("unknown", "web:99", "proof", "x", 3, false), nadd("bad-kind", "web:B2", "bogus", "x", 2, false),
	)
	nshow := func(name, ref, kind string, want int, exists bool) ubcase {
		return ubcase{name: "note-show/" + name, argv: j("item", "note", "show", ref, "--kind", kind), want: want, textSame: true,
			check: func(t *testing.T, e envl, _ map[string]map[string]any) {
				if want == 0 {
					eq(t, e, "data.exists", exists)
				}
			}}
	}
	add(nshow("design-gh", "web:T5", "design", 0, true), nshow("proof-gl", "api:B4", "proof", 0, true), nshow("absent", "web:B2", "plan", 0, false),
		nshow("bare-unique", "B4", "proof", 0, true), nshow("ambiguous", "F1", "plan", 2, false), nshow("unknown", "api:99", "plan", 3, false))
	nrm := func(name, ref, kind string, want int, ch bool) ubcase {
		return ubcase{name: "note-rm/" + name, argv: j("item", "note", "rm", ref, "--kind", kind), want: want,
			check: func(t *testing.T, e envl, _ map[string]map[string]any) {
				if want == 0 {
					eq(t, e, "data.changed", ch)
				}
			}}
	}
	add(nrm("design", "web:T5", "design", 0, true), nrm("proof-gl", "api:B4", "proof", 0, true), nrm("absent", "web:B2", "plan", 0, false),
		nrm("ambiguous", "F1", "plan", 2, false), nrm("unknown", "web:99", "plan", 3, false))

	// ---- backlog list / ids / milestones / summary
	add(
		ubcase{name: "backlog-list/merged", argv: j("backlog", "list"), want: 0,
			check: func(t *testing.T, e envl, _ map[string]map[string]any) {
				var got []string
				for _, k := range []string{"bugs", "features", "tasks"} {
					for _, r := range at(e, "data."+k).([]any) {
						got = append(got, r.(map[string]any)["id"].(string))
					}
				}
				want := []string{"web:2", "api:4", "web:1", "api:1", "api:5", "api:3", "web:5"}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("ids %v, want %v", got, want)
				}
			}},
		ubcase{name: "backlog-list/grep", argv: j("backlog", "list", "--grep", "api"), want: 0,
			check: func(t *testing.T, e envl, _ map[string]map[string]any) {
				if n := len(at(e, "data.features").([]any)); n != 2 {
					t.Errorf("features %d", n)
				}
			}},
		ubcase{name: "backlog-list/repo-narrowed", argv: j("backlog", "list", "--repo", "web"), want: 0,
			check: func(t *testing.T, e envl, _ map[string]map[string]any) {
				for _, k := range []string{"bugs", "features", "tasks"} {
					for _, r := range at(e, "data."+k).([]any) {
						if id := r.(map[string]any)["id"].(string); !strings.HasPrefix(id, "web:") {
							t.Errorf("--repo web lists %s", id)
						}
					}
				}
			}},
		ubcase{name: "backlog-list/active-stream-hides-its-repos-item", argv: j("backlog", "list"), want: 0,
			status: `{"active": [{"branch": "feat/x", "repo": "api", "items": ["F1"], "startedAt": "2026-10-01T10:00:00Z"}]}`,
			check: func(t *testing.T, e envl, _ map[string]map[string]any) {
				var got []string
				for _, r := range at(e, "data.features").([]any) {
					got = append(got, r.(map[string]any)["id"].(string))
				}
				if !reflect.DeepEqual(got, []string{"web:1", "api:5"}) {
					t.Errorf("features %v", got)
				}
				eq(t, e, "data.inProgress.0.id", "F1")
				eq(t, e, "data.inProgress.0.repo", "api")
			}},
		ubcase{name: "backlog-list/active-stream-qualified-item", argv: j("backlog", "list"), want: 0,
			status: `{"active": [{"branch": "feat/x", "repo": null, "items": ["web:F1"], "startedAt": "2026-10-01T10:00:00Z"}]}`,
			check: func(t *testing.T, e envl, _ map[string]map[string]any) {
				var got []string
				for _, r := range at(e, "data.features").([]any) {
					got = append(got, r.(map[string]any)["id"].(string))
				}
				if !reflect.DeepEqual(got, []string{"api:1", "api:5"}) {
					t.Errorf("features %v", got)
				}
			}},
		ubcase{name: "backlog-list/clusters", argv: j("backlog", "list"), want: 0,
			check: func(t *testing.T, e envl, _ map[string]map[string]any) {
				cl, _ := at(e, "data.clusters").([]any)
				if len(cl) != 1 {
					t.Fatalf("clusters %v", cl)
				}
				if got := fmt.Sprint(cl[0]); got != "[web:1 web:2]" && got != "[web:2 web:1]" {
					t.Errorf("cluster %s", got)
				}
			}},
	)
	for _, cw := range []string{"web", "api/src/deep", "docs/deep"} {
		want := map[string]int{"web": 1, "api": 2, "": 3}[ubSubRepoOf(cw)]
		withCwds(ubcase{name: "backlog-list/cwd", argv: j("backlog", "list"), want: 0,
			check: func(t *testing.T, e envl, _ map[string]map[string]any) {
				if n := len(at(e, "data.features").([]any)); n != want {
					t.Errorf("features %d, want %d for cwd %q (scope S)", n, want, cw)
				}
			}}, cw)
	}
	add(
		ubcase{name: "backlog-ids/native-milestone", argv: j("backlog", "ids", "--milestone", "M07"), want: 0,
			check: func(t *testing.T, e envl, _ map[string]map[string]any) { eq(t, e, "data.ids.0", "web:1") }},
		ubcase{name: "backlog-ids/none", argv: j("backlog", "ids", "--milestone", "M99"), want: 0},
		ubcase{name: "backlog-ids/no-flag", argv: j("backlog", "ids"), want: 2},
		ubcase{name: "backlog-milestones/qualified", argv: j("backlog", "milestones", "web:F1"), want: 0,
			check: func(t *testing.T, e envl, _ map[string]map[string]any) { eq(t, e, "data.milestones.0", "M07") }},
		ubcase{name: "backlog-milestones/bare", argv: j("backlog", "milestones", "F1"), want: 0,
			check: func(t *testing.T, e envl, _ map[string]map[string]any) { eq(t, e, "data.milestones.0", "M07") }},
		ubcase{name: "backlog-milestones/untagged", argv: j("backlog", "milestones", "api:F1", "api:T3"), want: 0,
			check: func(t *testing.T, e envl, _ map[string]map[string]any) {
				if n := len(at(e, "data.milestones").([]any)); n != 0 {
					t.Errorf("milestones %v", at(e, "data.milestones"))
				}
			}},
		ubcase{name: "summary/merged", argv: j("summary"), want: 0, changed: nil,
			check: func(t *testing.T, e envl, _ map[string]map[string]any) {
				eq(t, e, "data.backlog.bugs", float64(2))
				eq(t, e, "data.backlog.features", float64(3))
				eq(t, e, "data.backlog.tasks", float64(2))
				eq(t, e, "data.recent.0.id", "api:2")
				eq(t, e, "data.recent.0.type", "B")
				eq(t, e, "data.recent.1.id", "web:3")
			}},
		ubcase{name: "summary/rate-limit", argv: j("summary"), want: 6, env: []string{"FAKE_TRACKER_FAIL=issue list", "FAKE_TRACKER_FAIL_MSG=secondary rate limit"}},
	)
	for _, cw := range []string{"web", "docs/deep"} {
		want := map[string]float64{"web": 1, "": 3}[ubSubRepoOf(cw)]
		withCwds(ubcase{name: "summary/cwd", argv: j("summary"), want: 0,
			check: func(t *testing.T, e envl, _ map[string]map[string]any) {
				eq(t, e, "data.backlog.features", want)
			}}, cw)
	}

	// ---- file-only verbs under an issue umbrella are refused
	for _, c := range []struct {
		n    string
		argv []string
		want int
	}{
		{"id-next", j("id", "next", "--kind", "bugs"), 4},
		{"item-rm", j("item", "rm", "web:F1"), 4},
		{"backlog-archive", j("backlog", "archive"), 4},
		{"backlog-backfill", j("backlog", "backfill"), 4},
		{"backlog-drift", j("backlog", "drift"), 1},
		{"create-raw-file", j("item", "create", "--kind", "bugs", "--raw-file", "raw.md"), 4},
	} {
		add(ubcase{name: "file-only/" + c.n, argv: c.argv, want: c.want})
	}

	seen := map[string]bool{}
	for _, s := range all {
		if seen[s.name] {
			t.Fatalf("duplicate scenario %s", s.name)
		}
		seen[s.name] = true
	}
	t.Logf("%d issue-umbrella scenarios", len(all))
	for _, s := range all {
		s := s
		t.Run(s.name, s.exec)
	}
}

// ubSubRepoOf is the registered sub-repo a fixture cwd is in, "" for the
// umbrella root and the unregistered docs/ tree.
func ubSubRepoOf(cw string) string {
	for _, r := range []string{"web", "api"} {
		if cw == r || strings.HasPrefix(cw, r+"/") {
			return r
		}
	}
	return ""
}

func withCwdCase(c ubcase, cw string) ubcase {
	c.cwd = cw
	c.name += "/in-" + strings.ReplaceAll(cw, "/", "_")
	return c
}

// suiteUmbrellaFile runs the item verbs in a file-backend umbrella: one
// BACKLOG.md at the umbrella root, sub-repos registered in .rota/repos.json, the
// verbs run from the root, a sub-repo or a deep directory, with --repo valid
// and unregistered. Field verbs run below the root are marked goOnly.
func suiteUmbrellaFile(t *testing.T) {
	var all []scn
	add := func(s ...scn) { all = append(all, s...) }
	umb := umbFx
	raw := "- **[B77] [P2] Raw bullet.** Verbatim body.\n"
	umb.files = map[string]string{"raw.md": raw, "web/raw.md": raw, "web/src/raw.md": raw, "api/raw.md": raw}
	umb.after = func(t *testing.T, dir string, _ *info) {
		if err := os.MkdirAll(filepath.Join(dir, "web", "src"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, cw := range []string{"", "web", "web/src", "api"} {
		cw := cw
		tag := map[bool]string{true: "root", false: strings.ReplaceAll(cw, "/", "_")}[cw == ""]
		mk := func(name string, s scn) scn {
			s.name, s.cwd, s.fx = name+"/"+tag, cw, umb
			return s
		}
		// the verbs whose helpers self-locate behave the same from anywhere
		add(
			mk("complete", scn{argv: j("item", "complete", "B01", "--commit", "{h1}"), want: 0}),
			mk("complete-no-proof-refused", scn{argv: j("item", "complete", "B02", "--commit", "{h1}"), want: 4}),
			mk("complete-unknown", scn{argv: j("item", "complete", "B99", "--commit", "{h1}"), want: 3}),
			mk("reopen", scn{argv: j("item", "reopen", "B08"), want: 0}),
			mk("create-repos", scn{argv: j("item", "create", "--kind", "tasks", "--title", "Umb task", "--repos", "web"), want: 0}),
			mk("create-multi-repos", scn{argv: j("item", "create", "--kind", "bugs", "--title", "Both", "--tag", "P2", "--repos", "web, api"), want: 0}),
			mk("create-no-repos", scn{argv: j("item", "create", "--kind", "features", "--title", "None", "--tag", "Minor"), want: 0}),
			mk("raw-file", scn{argv: j("item", "create", "--kind", "bugs", "--raw-file", "raw.md"), want: 0}),
			mk("idnext", scn{argv: j("id", "next", "--kind", "bugs"), want: 0}),
			mk("ready", scn{argv: j("item", "ready", "B01"), want: 0}),
			mk("comment-add", scn{argv: j("item", "comment", "add", "B01", "--kind", "question", "--body-file", "-"), in: "Why?", want: 0}),
			mk("rm-preview", scn{argv: j("item", "rm", "B03"), want: 0}),
			mk("rm-apply", scn{argv: j("item", "rm", "B03", "--apply"), want: 0}),
			mk("shipped", scn{argv: j("item", "shipped", "parser-core lexer-v2"), want: map[bool]int{true: 0, false: 1}[cw == ""]}),
		)
		// the field verbs run below the root are goOnly
		fget := mk("fieldget-repos", scn{argv: j("item", "field", "get", "B03", "--name", "repos"), want: 0,
			check: func(t *testing.T, e envl) { eq(t, e, "data.value", "web") }})
		fset := mk("fieldset-repos", scn{argv: j("item", "field", "set", "B03", "--name", "repos", "--value", "web, api"), want: 0})
		flst := mk("fieldlist", scn{argv: j("item", "field", "list", "B03"), want: 0})
		if cw != "" {
			// Go finds the project by walking up, so these work below the root
			for _, f := range []*scn{&fget, &fset, &flst} {
				f.goOnly = true
				f.name += "/go-only"
			}
			fset.check = func(t *testing.T, e envl) {
				eq(t, e, "data.changed", true)
				eq(t, e, "data.value", "web, api")
			}
			flst.check = func(t *testing.T, e envl) { eq(t, e, "data.fields.repos", "web") }
		}
		add(fget, fset, flst)
	}
	// --repo: validated before the verb's own checks (3), a registered name changes nothing in file mode
	for _, c := range []struct {
		n    string
		argv []string
		want int
	}{
		{"complete", j("item", "complete", "B01", "--commit", "{h1}"), 0},
		{"reopen", j("item", "reopen", "B08"), 0},
		{"create", j("item", "create", "--kind", "tasks", "--title", "x", "--repos", "web"), 0},
		{"fieldget", j("item", "field", "get", "B03", "--name", "repos"), 0},
		{"fieldset", j("item", "field", "set", "B03", "--name", "milestone", "--value", "M02"), 0},
		{"idnext", j("id", "next", "--kind", "tasks"), 0},
		{"ready", j("item", "ready", "B01"), 0},
		{"rm-apply", j("item", "rm", "B03", "--apply"), 0},
	} {
		c := c
		reg := append(append([]string{}, c.argv...), "--repo", "web")
		add(scn{name: "repoflag-registered/" + c.n, fx: umb, argv: reg, want: c.want})
		bad := append(append([]string{}, c.argv...), "--repo", "nope")
		add(scn{name: "repoflag-unregistered/" + c.n, fx: umb, argv: bad, want: 3})
	}
	// an unknown ID under --repo is the verb's own 3, a bad flag still 2 after a valid --repo
	add(
		scn{name: "repoflag-then-verb-error/unknown-id", fx: umb, argv: j("item", "complete", "B99", "--commit", "{h1}", "--repo", "api"), want: 3},
		scn{name: "repoflag-then-verb-error/bad-reason", fx: umb, argv: j("item", "complete", "B01", "--reason", "wontfix", "--repo", "api"), want: 2},
		scn{name: "repoflag-then-verb-error/missing-title", fx: umb, argv: j("item", "create", "--kind", "bugs", "--repo", "api"), want: 2},
		scn{name: "repoflag-unregistered-first/bad-flag-value", fx: umb, argv: j("item", "complete", "B01", "--reason", "wontfix", "--repo", "nope"), want: 3},
		scn{name: "create/repos-names-are-not-validated", fx: umb, argv: j("item", "create", "--kind", "tasks", "--title", "Free text", "--repos", "elsewhere"), want: 0},
	)
	seen := map[string]bool{}
	for _, s := range all {
		if seen[s.name] {
			t.Fatalf("duplicate scenario %s", s.name)
		}
		seen[s.name] = true
	}
	t.Logf("%d file-umbrella scenarios", len(all))
	for _, s := range all {
		s := s
		t.Run(s.name, s.exec)
	}
}
