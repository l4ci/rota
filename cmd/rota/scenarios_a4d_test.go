package main

// Scenarios for `rota issues label|imported|close|provider` and `rota
// migrate issues` (#48). Each scenario builds a git project whose origin
// resolves to github or gitlab, seeds the stateful fake forge
// (test/fakes/fake_tracker.py), runs the Go binary and compares the exit
// code, the --json envelope, the final .rota/ tree and the final fake-forge
// database with the scenario's frozen record. A scenario may run several
// commands in a row on the same project (resume after a failure).
//
// Safety: the shared TestMain refuses to run unless gh resolves to test/fakes,
// TestDFakesFirst checks glab too, and every run has its own FAKE_TRACKER_DB
// under t.TempDir.
//
// Where Go departed from the old helpers on purpose:
//  1. data.changed reports whether the forge changed; the old helpers said
//     true for every label and close that exited 0 (drun.changed).
//  2. A forge CLI that exits with a code other than 1, 3 or 4 is exit 5 (the
//     CLI failed).
//  3. issues.autoCreateLabel false means no label creation (the old helper
//     read it with jq's `// true` and created the label anyway).
//  4. A .rota/issue-map.json that is a JSON array is a corrupt state file, exit
//     70 (the old helper died with usage).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type drun struct {
	argv    []string
	env     []string
	want    int
	changed *bool  // Go's data.changed on success
	msgHas  string // the error message contains this
	check   func(t *testing.T, g envl, db map[string]any)
}

type dsc struct {
	name   string
	remote string // "" github, "gitlab", "none"
	fx     fx
	db     func() map[string]any
	runs   []drun
}

const dCfg = `{"issues": {"retryWaitSeconds": 0, "bulkPaceMs": 0}}`

func dseed() map[string]any {
	i3 := iss(3, "Gamma closed", "gamma", []string{"bug"}, "closed")
	i4 := iss(4, "Delta mine", "delta", []string{"bug", "existing"}, "open")
	i4["assignees"] = []any{"fake-user"}
	return map[string]any{
		"next_issue": 7, "next_milestone": 2, "next_comment": 3, "next_mr": 1,
		"labels": []any{"bug", "feature", "existing"}, "prs": []any{},
		"milestones": []any{map[string]any{"number": 1, "title": "M09 — Shipped", "description": "", "state": "closed"}},
		"issues": []any{
			iss(1, "Alpha", "alpha body", []string{"bug"}, "open"),
			iss(2, "Beta", "beta body", []string{"feature", "existing"}, "open"),
			i3, i4,
			iss(5, "Epsilon", "eps", []string{}, "open", cm(1, "a note")),
			iss(6, "Zeta", "zeta", []string{"existing"}, "open"),
		},
	}
}

func dempty() map[string]any { return nil }

// dseed78 is dseed with the issues #7 and #8 a pre-populated map points at.
func dseed78() map[string]any {
	db := dseed()
	db["issues"] = append(db["issues"].([]any), iss(7, "First bug", "x", []string{"type:bug"}, "open"), iss(8, "Feature", "y", []string{"type:feature"}, "open"))
	db["next_issue"] = 9
	return db
}

// fixture builds the scenario's project with its origin remote and the seed.
func (s dsc) fixture(t *testing.T) (base string, in info, seed map[string]any) {
	t.Helper()
	f := s.fx
	if f.config == "" {
		f.config = dCfg
	}
	base, in = f.build(t)
	remote := map[string]string{"": "https://github.com/example/repo.git", "gitlab": "https://gitlab.com/example/repo.git"}[s.remote]
	if remote != "" {
		git(t, base, "remote", "add", "origin", remote)
	}
	if s.db != nil {
		seed = s.db()
	}
	return base, in, seed
}

func (s dsc) exec(t *testing.T) {
	t.Parallel()
	base, in, seed := s.fixture(t)
	dir := copyTree(t, base)
	dbPath := filepath.Join(t.TempDir(), "go.json")
	if seed != nil {
		raw, _ := json.Marshal(seed)
		if err := os.WriteFile(dbPath, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var rec frozenRec
	before := snapshot(t, base)
	for n, rn := range s.runs {
		argv := subst(rn.argv, in)
		tag := fmt.Sprintf("run %d %v", n+1, argv)
		r := envRun(t, dir, "", frozenEnv(append([]string{"FAKE_TRACKER_DB=" + dbPath}, rn.env...)), rotaBin, argv...)
		if r.code != rn.want {
			t.Errorf("%s: exit = %d, want %d\nstdout: %s\nstderr: %s", tag, r.code, rn.want, r.stdout, r.stderr)
		}
		db := readDB(t, dbPath)
		rec.Steps = append(rec.Steps, newStep(t, r, before, dir, db))
		e := parseEnv(t, "go", r)
		if rn.msgHas != "" {
			if msg, _ := at(e, "error.message").(string); !strings.Contains(msg, rn.msgHas) {
				t.Errorf("%s: error message %q does not contain %q", tag, msg, rn.msgHas)
			}
		}
		if rn.changed != nil && r.code == 0 && at(e, "data.changed") != *rn.changed {
			t.Errorf("%s: data.changed = %v, want %v", tag, at(e, "data.changed"), *rn.changed)
		}
		if rn.check != nil {
			rn.check(t, e, db)
		}
	}
	frozenCheck(t, rec)
}

func TestDFakesFirst(t *testing.T) { TestIssueFakesFirst(t) }

func dr(want int, argv ...string) drun {
	return drun{argv: append([]string{"--json"}, argv...), want: want}
}

func (r drun) env1(kv ...string) drun { r.env = append(r.env, kv...); return r }
func (r drun) ch(b bool) drun         { r.changed = &b; return r }
func (r drun) msg(s string) drun      { r.msgHas = s; return r }

// one is a single-run scenario on the standard project and the standard forge.
func one(name string, r drun) dsc { return dsc{name: name, db: dseed, runs: []drun{r}} }

func (s dsc) on(remote string) dsc { s.remote = remote; return s }

// both adds the github and the gitlab form of a scenario.
func dboth(all *[]dsc, s dsc) {
	g := s
	g.name = s.name + "/github"
	l := s
	l.name = s.name + "/gitlab"
	l.remote = "gitlab"
	*all = append(*all, g, l)
}

func umbrella(f fx) fx {
	f.subs = map[string][]string{"web": {"feat: web start"}, "api": {"feat: api start"}}
	prev := f.after
	f.after = func(t *testing.T, dir string, in *info) {
		git(t, filepath.Join(dir, "web"), "remote", "add", "origin", "https://github.com/example/web.git")
		git(t, filepath.Join(dir, "api"), "remote", "add", "origin", "https://gitlab.com/example/api.git")
		in.x["web"] = git(t, filepath.Join(dir, "web"), "rev-parse", "--short", "HEAD")
		in.x["api"] = git(t, filepath.Join(dir, "api"), "rev-parse", "--short", "HEAD")
		if prev != nil {
			prev(t, dir, in)
		}
	}
	return f
}

func suiteA4D(t *testing.T) {
	var all []dsc
	add := func(s ...dsc) { all = append(all, s...) }
	rate := []string{"FAKE_TRACKER_FAIL_MSG=secondary rate limit"}

	// ---- issues provider ----
	for _, c := range []struct{ name, remote string }{{"github", ""}, {"gitlab", "gitlab"}, {"none", "none"}} {
		add(one("provider/"+c.name, dr(0, "issues", "provider")).on(c.remote))
	}
	for name, url := range map[string]string{
		"ghe-host":    "https://github.corp.example/o/r.git",
		"ssh-short":   "git@gitlab.example.org:o/r.git",
		"ssh-url":     "ssh://git@github.com/o/r.git",
		"other":       "https://bitbucket.org/o/r.git",
		"upper-host":  "https://GitHub.com/o/r.git",
		"gitlab-self": "https://gitlab.internal/o/r.git",
	} {
		url := url
		f := fx{after: func(t *testing.T, dir string, in *info) { git(t, dir, "remote", "add", "origin", url) }}
		add(dsc{name: "provider/url-" + name, remote: "none", fx: f, db: dseed, runs: []drun{dr(0, "issues", "provider")}})
	}
	add(dsc{name: "provider/umbrella-web", remote: "none", fx: umbrella(fx{}), runs: []drun{dr(0, "issues", "provider", "--repo", "web")}},
		dsc{name: "provider/umbrella-api", remote: "none", fx: umbrella(fx{}), runs: []drun{dr(0, "issues", "provider", "--repo", "api")}},
		dsc{name: "provider/umbrella-root", remote: "none", fx: umbrella(fx{}), runs: []drun{dr(0, "issues", "provider")}},
		dsc{name: "provider/repo-unknown", remote: "none", fx: umbrella(fx{}), runs: []drun{dr(3, "issues", "provider", "--repo", "nope")}},
		dsc{name: "provider/repo-no-umbrella", runs: []drun{dr(3, "issues", "provider", "--repo", "web")}},
		dsc{name: "provider/no-hv", remote: "none", fx: fx{noHV: true}, runs: []drun{dr(3, "issues", "provider")}},
		dsc{name: "provider/extra-arg", runs: []drun{dr(2, "issues", "provider", "x")}},
	)

	// ---- issues label ----
	dboth(&all, one("label/add-existing", dr(0, "issues", "label", "1", "--add", "bug").ch(false)))
	dboth(&all, one("label/add-new-label", dr(0, "issues", "label", "1", "--add", "brand-new").ch(true)))
	dboth(&all, one("label/add-registered", dr(0, "issues", "label", "1", "--add", "feature").ch(true)))
	dboth(&all, one("label/remove-present", dr(0, "issues", "label", "2", "--remove", "existing").ch(true)))
	dboth(&all, one("label/remove-absent", dr(0, "issues", "label", "1", "--remove", "existing").ch(false)))
	dboth(&all, one("label/add-closed-issue", dr(0, "issues", "label", "3", "--add", "feature").ch(true)))
	dboth(&all, one("label/add-equals-form", dr(0, "issues", "label", "6", "--add=bug").ch(true)))
	dboth(&all, one("label/unicode-label", dr(0, "issues", "label", "5", "--add", "prioé").ch(true)))
	dboth(&all, one("label/spaced-label", dr(0, "issues", "label", "5", "--add", "needs review").ch(true)))
	dboth(&all, one("label/missing-issue", dr(3, "issues", "label", "99", "--add", "bug")))
	dboth(&all, one("label/missing-issue-remove", dr(3, "issues", "label", "99", "--remove", "bug")))
	dboth(&all, one("label/both-flags", dr(2, "issues", "label", "1", "--add", "a", "--remove", "b")))
	dboth(&all, one("label/neither-flag", dr(2, "issues", "label", "1")))
	dboth(&all, one("label/empty-label", dr(2, "issues", "label", "1", "--add", "")))
	dboth(&all, one("label/no-number", dr(2, "issues", "label", "--add", "bug")))
	dboth(&all, one("label/bad-number", dr(2, "issues", "label", "abc", "--add", "bug")))
	dboth(&all, one("label/two-numbers", dr(2, "issues", "label", "1", "2", "--add", "bug")))
	dboth(&all, one("label/forge-fails", dr(5, "issues", "label", "1", "--add", "bug").env1("FAKE_TRACKER_FAIL=issue")))
	add(one("label/rate-limited/github", dr(6, "issues", "label", "1", "--add", "bug").env1(append([]string{"FAKE_TRACKER_FAIL=issue edit"}, rate...)...)),
		one("label/rate-limited-remove/github", dr(6, "issues", "label", "1", "--remove", "bug").env1(append([]string{"FAKE_TRACKER_FAIL=issue edit"}, rate...)...)),
		one("label/rate-limited/gitlab", dr(6, "issues", "label", "1", "--add", "bug").env1(append([]string{"FAKE_TRACKER_FAIL=issue update"}, rate...)...)).on("gitlab"),
		one("label/rate-limited-remove/gitlab", dr(6, "issues", "label", "1", "--remove", "bug").env1(append([]string{"FAKE_TRACKER_FAIL=issue update"}, rate...)...)).on("gitlab"))
	add(one("label/unknown-provider", dr(3, "issues", "label", "1", "--add", "bug").msg("issues.provider")).on("none"))
	add(dsc{name: "label/autocreate-off/github", db: dseed, fx: fx{config: `{"issues": {"retryWaitSeconds": 0, "autoCreateLabel": false}}`},
		runs: []drun{dr(3, "issues", "label", "1", "--add", "brand-new")}}, // divergence 3
		dsc{name: "label/autocreate-off/gitlab", remote: "gitlab", db: dseed, fx: fx{config: `{"issues": {"retryWaitSeconds": 0, "autoCreateLabel": false}}`},
			runs: []drun{dr(0, "issues", "label", "1", "--add", "brand-new").ch(true)}})
	dboth(&all, dsc{name: "label/autocreate-off-existing", db: dseed, fx: fx{config: `{"issues": {"retryWaitSeconds": 0, "autoCreateLabel": false}}`},
		runs: []drun{dr(0, "issues", "label", "1", "--add", "existing").ch(true)}})
	dboth(&all, dsc{name: "label/autocreate-explicit-on", db: dseed, fx: fx{config: `{"issues": {"retryWaitSeconds": 0, "autoCreateLabel": true}}`},
		runs: []drun{dr(0, "issues", "label", "2", "--add", "newer").ch(true)}})
	add(dsc{name: "label/umbrella-web", remote: "none", fx: umbrella(fx{}), db: dseed, runs: []drun{dr(0, "issues", "label", "1", "--add", "bug", "--repo", "web").ch(false)}},
		dsc{name: "label/umbrella-api", remote: "none", fx: umbrella(fx{}), db: dseed, runs: []drun{dr(0, "issues", "label", "1", "--add", "fresh", "--repo", "api").ch(true)}},
		dsc{name: "label/repo-unknown", remote: "none", fx: umbrella(fx{}), db: dseed, runs: []drun{dr(3, "issues", "label", "1", "--add", "x", "--repo", "nope")}},
		dsc{name: "label/add-then-remove", db: dseed, runs: []drun{
			dr(0, "issues", "label", "1", "--add", "tmp").ch(true), dr(0, "issues", "label", "1", "--remove", "tmp").ch(true), dr(0, "issues", "label", "1", "--remove", "tmp").ch(false)}},
		dsc{name: "label/add-twice-gitlab", remote: "gitlab", db: dseed, runs: []drun{
			dr(0, "issues", "label", "1", "--add", "tmp").ch(true), dr(0, "issues", "label", "1", "--add", "tmp").ch(false)}},
		dsc{name: "label/add-twice-github", db: dseed, runs: []drun{
			dr(0, "issues", "label", "1", "--add", "tmp").ch(true), dr(0, "issues", "label", "1", "--add", "tmp").ch(false)}},
	)

	// ---- issues close ----
	dboth(&all, one("close/plain", dr(0, "issues", "close", "1", "--commit", "{head}").ch(true)))
	dboth(&all, one("close/with-item", dr(0, "issues", "close", "2", "--commit", "{head}", "--item", "F07").ch(true)))
	dboth(&all, one("close/short-sha", dr(0, "issues", "close", "6", "--commit", "{h1}", "--item", "B03").ch(true)))
	dboth(&all, one("close/ref-name", dr(0, "issues", "close", "1", "--commit", "HEAD").ch(true)))
	dboth(&all, one("close/empty-item", dr(0, "issues", "close", "1", "--commit", "{head}", "--item", "").ch(true)))
	dboth(&all, one("close/item-with-space", dr(0, "issues", "close", "1", "--commit", "{head}", "--item", "F 7").ch(true)))
	dboth(&all, one("close/commit-unknown", dr(3, "issues", "close", "1", "--commit", "deadbeef")))
	dboth(&all, one("close/missing-commit", dr(2, "issues", "close", "1")))
	dboth(&all, one("close/no-number", dr(2, "issues", "close", "--commit", "{head}")))
	dboth(&all, one("close/bad-number", dr(2, "issues", "close", "x1", "--commit", "{head}")))
	dboth(&all, one("close/missing-issue", dr(3, "issues", "close", "99", "--commit", "{head}")))
	dboth(&all, one("close/forge-fails", dr(5, "issues", "close", "1", "--commit", "{head}").env1("FAKE_TRACKER_FAIL=issue close")))
	dboth(&all, one("close/rate-limited", dr(6, "issues", "close", "1", "--commit", "{head}").env1(append([]string{"FAKE_TRACKER_FAIL=issue close"}, rate...)...)))
	dboth(&all, one("close/auth-fails", dr(5, "issues", "close", "1", "--commit", "{head}").env1("FAKE_TRACKER_FAIL=auth status")))
	add(one("close/unknown-provider", dr(3, "issues", "close", "1", "--commit", "{head}").msg("issues.provider")).on("none"))
	add(one("close/already-closed/gitlab", dr(0, "issues", "close", "3", "--commit", "{head}").ch(false)).on("gitlab"))
	add(one("close/already-closed/github", dr(0, "issues", "close", "3", "--commit", "{head}").ch(false)))
	add(dsc{name: "close/umbrella-web", remote: "none", fx: umbrella(fx{}), db: dseed, runs: []drun{
		{argv: []string{"--json", "issues", "close", "1", "--commit", "{x:web}", "--repo", "web"}, want: 0, changed: yes()}}})
	add(dsc{name: "close/umbrella-api", remote: "none", fx: umbrella(fx{}), db: dseed, runs: []drun{
		{argv: []string{"--json", "issues", "close", "2", "--commit", "{x:api}", "--repo", "api", "--item", "T1"}, want: 0, changed: yes()}}})
	add(dsc{name: "close/repo-unknown", remote: "none", fx: umbrella(fx{}), db: dseed, runs: []drun{dr(3, "issues", "close", "1", "--commit", "HEAD", "--repo", "nope")}})
	add(dsc{name: "close/twice/gitlab", remote: "gitlab", db: dseed, runs: []drun{
		dr(0, "issues", "close", "1", "--commit", "{head}").ch(true), dr(0, "issues", "close", "1", "--commit", "{head}").ch(false)}})

	// ---- issues imported ----
	importedFx := fx{
		backlog: importedBacklog,
		archive: "sectioned",
		files: map[string]string{
			".rota/bugs/B01.md":     "# B01\n\nUpstream GH: #12 and GH: #13.\n",
			".rota/features/F01.md": "# F01\n\nGL: #8 Repos: web\n",
			".rota/tasks/T09.md":    "# T09 detail\n\nGH: #70\n",
		},
		after: func(t *testing.T, dir string, in *info) {
			write(t, dir, ".rota/ARCHIVE.md", "# Archive\n\n## Completed\n- ~~**[B05] [P2] Archived bug.** old GH: #50 Repos: web~~ Done 2026-05-15 [`e4abdbe`]\n- ~~**[F05] [Minor] Old.** old GL: #51~~ Done 2026-05-15 [`e4abdbe`]\n")
		},
	}
	imp := func(name string, argv ...string) {
		add(dsc{name: "imported/" + name, remote: "none", fx: importedFx, db: dseed, runs: []drun{dr(0, append([]string{"issues", "imported"}, argv...)...)}})
	}
	imp("all")
	imp("for-repo-web", "--for-repo", "web")
	imp("for-repo-api", "--for-repo", "api")
	imp("for-repo-none", "--for-repo", "nosuch")
	imp("open-only")
	imp("open-only-web", "--open-only", "--for-repo", "web")
	imp("open-only-api", "--for-repo", "api", "--open-only")
	add(dsc{name: "imported/open-only-umbrella", remote: "none", fx: umbrella(importedFx), db: importedDB, runs: []drun{
		{argv: []string{"--json", "issues", "imported", "--open-only"}, want: 0}}})
	add(dsc{name: "imported/open-only-umbrella-web", remote: "none", fx: umbrella(importedFx), db: importedDB, runs: []drun{
		{argv: []string{"--json", "issues", "imported", "--open-only", "--for-repo", "web"}, want: 0}}})
	add(dsc{name: "imported/open-only-forge-fails", remote: "none", fx: importedFx, db: importedDB, runs: []drun{
		{argv: []string{"--json", "issues", "imported", "--open-only"}, want: 0, env: []string{"FAKE_TRACKER_FAIL=issue view"}}}})
	add(dsc{name: "imported/empty-backlog", remote: "none", fx: fx{backlog: "# TODO\n\n## Bugs\n\n## Completed\n"}, runs: []drun{dr(0, "issues", "imported")}})
	add(dsc{name: "imported/no-archive-no-details", remote: "none", fx: fx{backlog: importedBacklog, files: map[string]string{".rota/bugs/B01.md": "", ".rota/features/F01.md": "", ".rota/plans/M01-B02.md": "", ".rota/plans/M02-F01.md": ""}}, runs: []drun{
		{argv: []string{"--json", "issues", "imported"}, want: 0}}})
	add(dsc{name: "imported/no-hv", remote: "none", fx: fx{noHV: true}, runs: []drun{dr(3, "issues", "imported")}})
	add(dsc{name: "imported/repo-flag-rejected", remote: "none", fx: umbrella(importedFx), runs: []drun{dr(2, "issues", "imported", "--repo", "web")}})
	add(dsc{name: "imported/positional", remote: "none", fx: importedFx, runs: []drun{dr(2, "issues", "imported", "web")}})
	add(dsc{name: "imported/text-vs-open", remote: "none", fx: fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] One.** GH:#5 GL:  #6 Repos: web , api Related: [F01]\n- **[B02] [P1] Two.** GL: #6\n\n## Completed\n- ~~**[B03] [P1] Done.** GH: #1~~ Done 2026-01-01 [`a`]\n"}, runs: []drun{
		{argv: []string{"--json", "issues", "imported"}, want: 0}}})
	add(dsc{name: "imported/archive-and-open-same-key", remote: "none", fx: fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] One.** GH: #5\n\n## Completed\n",
		after: func(t *testing.T, dir string, in *info) {
			write(t, dir, ".rota/ARCHIVE.md", "## Old\n- ~~**[B01] [P1] One.** GH: #5~~ Done 2026-01-01 [`a`]\n")
		}}, runs: []drun{{argv: []string{"--json", "issues", "imported"}, want: 0}}})

	// ---- migrate issues ----
	addMigrate(&all)

	for _, s := range all {
		s := s
		t.Run(s.name, s.exec)
	}
	t.Logf("%d scenarios", len(all))
}

func (s dsc) withDB(f func() map[string]any) dsc { s.db = f; return s }

const importedBacklog = `# TODO

## Bugs
- **[B01] [P1] First bug.** x GH: #12 Related: [F01] Since: {h1}
- **[B02] [P2] Second bug.** y GL: #7 Repos: web
- **[B03] [P3] Third bug.** z GH: #5 Repos: web, api Milestone: M01
- **[B04] [P2] No refs.** nothing

## Features
- **[F01] [Major] Feature.** GH: #20 GL: #8 Detail: ` + "`.rota/features/F01.md`" + `

## Tasks
- **[T01] Task.** GL: #3 GH: #4 Repos: api

## Completed
- ~~**[B08] [P2] Done bug.** GH: #99~~ Done 2026-09-30 [` + "`abc1234`" + `]
`

// importedDB has the issues the GL: references point at, open and closed.
func importedDB() map[string]any {
	db := dseed()
	var rows []any
	for _, n := range []int{3, 7, 8, 12, 51} {
		st := "open"
		if n == 3 {
			st = "closed"
		}
		rows = append(rows, iss(n, fmt.Sprintf("Issue %d", n), "b", []string{}, st))
	}
	db["issues"] = rows
	db["next_issue"] = 100
	return db
}

var migMilestones = map[string]string{
	".rota/milestones/M00.md": "---\nid: M00\ntitle: Old\nstatus: shipped\ndepends: []\ncreated: 2026-01-01\n---\n\n# M00 — Old\n\n## Goal\n\nDone already.\n",
	".rota/milestones/M01.md": "---\nid: M01\ntitle: Core engine\nstatus: planned\ndepends: []\ncreated: 2026-01-02\n---\n\n# M01 — Core engine\n\n## Goal\n\nBuild the core\nin two lines.\n\n## Acceptance criteria\n\n- ship [B01] and [F01]; T1 is a label\n",
	".rota/milestones/M02.md": "---\nid: M02\ntitle: Second\nstatus: active\ndepends: [M01]\ncreated: 2026-01-03\n---\n\n# M02 — Second\n\n## Goal\n\nThe second thing.\n\n## Notes\n\nSee B03 and F01.\n",
	".rota/plans/M01-S01.md":  "# slice 1\n\n- [B01] first\n- T1 bare\n",
	".rota/plans/M01-S02.md":  "   \n",
	".rota/plans/M02-S01.md":  "# slice\n\n[F01] and [B03]\n",
}

// mfx is a migration project: the standard backlog (open B01-B04, F01, F02,
// T01, T02 with detail, proof, design and plan files) plus the extras.
func mfx(extra map[string]string) fx {
	files := map[string]string{}
	for k, v := range extra {
		files[k] = v
	}
	return fx{files: files}
}

func withMS(extra map[string]string) fx {
	files := map[string]string{}
	for k, v := range migMilestones {
		files[k] = v
	}
	for k, v := range extra {
		files[k] = v
	}
	return fx{files: files}
}

// mig is a run of rota migrate issues.
func mig(want int, args ...string) drun {
	return dr(want, append([]string{"migrate", "issues"}, args...)...)
}

func addMigrate(all *[]dsc) {
	add := func(s ...dsc) { *all = append(*all, s...) }
	rate := "FAKE_TRACKER_FAIL_MSG=secondary rate limit"
	m := func(name string, f fx, db func() map[string]any, runs ...drun) dsc {
		return dsc{name: "migrate/" + name, fx: f, db: db, runs: runs}
	}

	// preview
	for _, f := range []struct {
		name string
		fx   fx
		db   func() map[string]any
		args []string
	}{
		{"preview/items", mfx(nil), dseed, nil},
		{"preview/items-empty-forge", mfx(nil), dempty, nil},
		{"preview/with-milestones", withMS(nil), dseed, nil},
		{"preview/limit-3", withMS(nil), dseed, []string{"--limit", "3"}},
		{"preview/limit-0", withMS(nil), dseed, []string{"--limit", "0"}},
		{"preview/limit-99", mfx(nil), dseed, []string{"--limit=99"}},
	} {
		dboth(all, m(f.name, f.fx, f.db, mig(0, f.args...).ch(false)))
	}
	add(m("preview/unknown-provider-ok", withMS(nil), dseed, mig(0)).on("none"))
	// apply
	dboth(all, m("apply/items", mfx(nil), dseed, mig(0, "--apply")))
	dboth(all, m("apply/items-empty-forge", mfx(nil), dempty, mig(0, "--apply")))
	dboth(all, m("apply/with-milestones", withMS(nil), dseed, mig(0, "--apply")))
	dboth(all, m("apply/with-milestones-empty-forge", withMS(nil), dempty, mig(0, "--apply")))
	dboth(all, m("apply/twice", withMS(nil), dseed, mig(0, "--apply"), mig(0, "--apply")))
	dboth(all, m("apply/then-preview", withMS(nil), dseed, mig(0, "--apply"), mig(0)))
	dboth(all, m("apply/limit-resume", withMS(nil), dseed, mig(0, "--apply", "--limit", "2"), mig(0, "--apply", "--limit", "3"), mig(0, "--apply")))
	dboth(all, m("apply/limit-then-preview", withMS(nil), dseed, mig(0, "--apply", "--limit", "2"), mig(0)))
	dboth(all, m("apply/limit-0", withMS(nil), dseed, mig(0, "--apply", "--limit", "0")))
	dboth(all, m("apply/limit-0-items-only", mfx(nil), dseed, mig(0, "--apply", "--limit", "0"), mig(0, "--apply")))
	dboth(all, m("apply/pace-2ms", func() fx {
		f := withMS(nil)
		f.config = `{"issues": {"retryWaitSeconds": 0, "bulkPaceMs": 2}}`
		return f
	}(), dseed, mig(0, "--apply")))
	dboth(all, m("apply/pace-default-key-missing-zero", func() fx { f := mfx(nil); f.config = `{"issues": {"retryWaitSeconds": 0, "bulkPaceMs": 0}}`; return f }(), dseed, mig(0, "--apply")))
	// usage and refusals
	dboth(all, m("usage/limit-abc", mfx(nil), dseed, mig(2, "--limit", "abc")))
	dboth(all, m("usage/limit-neg", mfx(nil), dseed, mig(2, "--limit", "-1")))
	dboth(all, m("usage/limit-empty", mfx(nil), dseed, mig(2, "--limit", "")))
	dboth(all, m("usage/positional", mfx(nil), dseed, mig(2, "x")))
	dboth(all, m("usage/unknown-flag", mfx(nil), dseed, mig(2, "--dry-run")))
	dboth(all, m("refuse/no-backlog", fx{noBacklog: true}, dseed, mig(3)))
	dboth(all, m("refuse/no-backlog-apply", fx{noBacklog: true}, dseed, mig(3, "--apply")))
	add(dsc{name: "refuse/umbrella/preview", remote: "none", fx: umbrella(fx{}), db: dseed, runs: []drun{mig(4)}},
		dsc{name: "refuse/umbrella/apply", remote: "none", fx: umbrella(fx{}), db: dseed, runs: []drun{mig(4, "--apply")}},
		dsc{name: "refuse/no-hv", remote: "none", fx: fx{noHV: true}, runs: []drun{mig(3)}},
		dsc{name: "refuse/no-provider-apply", remote: "none", fx: mfx(nil), db: dseed, runs: []drun{mig(5, "--apply").msg("0 of 8 migrated")}},
		dsc{name: "refuse/no-provider-apply-empty-backlog", remote: "none", fx: fx{backlog: "# TODO\n\n## Bugs\n\n## Completed\n"}, db: dseed, runs: []drun{mig(0, "--apply")}},
	)
	// tracker failures and resume
	dboth(all, m("fail/tracking-issue", withMS(nil), dseed, mig(5, "--apply").env1("FAKE_TRACKER_FAIL=issue create").msg("0 of 8 migrated")))
	dboth(all, m("fail/milestone-create", withMS(nil), dseed, mig(5, "--apply").env1("FAKE_TRACKER_FAIL=milestones").msg("0 of 8 migrated")))
	dboth(all, m("fail/item-create", mfx(nil), dseed, mig(5, "--apply").env1("FAKE_TRACKER_FAIL=issue create").msg("0 of 8 migrated")))
	dboth(all, m("fail/rate-limit-item", mfx(nil), dseed, mig(6, "--apply").env1("FAKE_TRACKER_FAIL=issue create", rate).msg("0 of 8 migrated")))
	for _, p := range []struct{ remote, notes, edit string }{{"", "comments", "issue edit"}, {"gitlab", "notes", "issue update"}} {
		name := map[string]string{"": "github", "gitlab": "gitlab"}[p.remote]
		add(m("fail/notes-then-resume/"+name, mfx(nil), dseed,
			mig(5, "--apply").env1("FAKE_TRACKER_FAIL="+p.notes).msg("8 of 8 migrated"), mig(0, "--apply"), mig(0, "--apply")).on(p.remote),
			m("fail/notes-rate-limit-then-resume/"+name, mfx(nil), dseed,
				mig(6, "--apply").env1("FAKE_TRACKER_FAIL="+p.notes, rate).msg("8 of 8 migrated"), mig(0, "--apply")).on(p.remote),
			m("fail/related-edit-then-resume/"+name, mfx(nil), dseed,
				mig(5, "--apply").env1("FAKE_TRACKER_FAIL="+p.edit).msg("8 of 8 migrated"), mig(0, "--apply")).on(p.remote))
	}
	dboth(all, m("ok/auth-status-not-called", mfx(nil), dseed, mig(0, "--apply").env1("FAKE_TRACKER_FAIL=auth status")))
	dboth(all, m("fail/get-url", mfx(nil), dseed, mig(5, "--apply").env1("FAKE_TRACKER_FAIL=issue view").msg("0 of 8 migrated")))
	// map state
	preMap := `{
  "B01": {"id": "B7", "number": 7, "url": "https://example.test/7", "done": ["proof"]},
  "F01": {"id": "F8", "number": 8, "url": "https://example.test/8", "done": []}
}
`
	dboth(all, m("map/prepopulated-preview", mfx(map[string]string{".rota/issue-map.json": preMap}), dseed, mig(0)))
	dboth(all, m("map/prepopulated-apply", mfx(map[string]string{".rota/issue-map.json": preMap}), dseed78, mig(0, "--apply")))
	dboth(all, m("map/array-is-corrupt", mfx(map[string]string{".rota/issue-map.json": "[]\n"}), dseed,
		mig(70, "--apply"))) // divergence 4
	dboth(all, m("map/unparseable-restarts", mfx(map[string]string{".rota/issue-map.json": "{not json"}), dseed, mig(0, "--apply")))
	dboth(all, m("map/empty-object", mfx(map[string]string{".rota/issue-map.json": "{}\n"}), dseed, mig(0)))
	dboth(all, m("map/foreign-keys-kept", mfx(map[string]string{".rota/issue-map.json": `{"X9": {"id": "X9", "number": 1, "url": "u", "done": []}}`}), dseed, mig(0, "--apply")))
	fullMap := `{
  "B01": {"id": "B7", "number": 7, "url": "u", "done": ["proof", "related"]}, "B02": {"id": "B8", "number": 8, "url": "u", "done": ["plan", "related"]},
  "B03": {"id": "B9", "number": 9, "url": "u", "done": []}, "B04": {"id": "B10", "number": 10, "url": "u", "done": []},
  "F01": {"id": "F11", "number": 11, "url": "u", "done": ["plan", "related"]}, "F02": {"id": "F12", "number": 12, "url": "u", "done": ["design", "related"]},
  "T01": {"id": "T13", "number": 13, "url": "u", "done": []}, "T02": {"id": "T14", "number": 14, "url": "u", "done": ["related"]}
}`
	dboth(all, m("map/all-migrated-freezes", mfx(map[string]string{".rota/issue-map.json": fullMap}), dseed, mig(0, "--apply").ch(true)))
	frozen := "> Frozen: this backlog moved to the issue tracker on 2026-01-01 (see .rota/issue-map.json). Edit issues, not this file.\n\n"
	dboth(all, m("map/all-migrated-frozen-noop", fx{backlog: frozen + stdBacklog, files: map[string]string{".rota/issue-map.json": fullMap}}, dseed, mig(0, "--apply").ch(false)))
	dboth(all, m("freeze/already-frozen", fx{backlog: frozen + stdBacklog}, dseed, mig(0, "--apply")))
	dboth(all, m("freeze/leading-blank-lines", fx{backlog: "\n\n" + frozen + stdBacklog}, dseed, mig(0, "--apply")))
	dboth(all, m("freeze/empty-backlog", fx{backlog: "# TODO\n\n## Bugs\n\n## Completed\n"}, dseed, mig(0, "--apply"), mig(0, "--apply")))
	// content cases
	dboth(all, m("content/unmapped-related", fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] One.** a Related: [B08], [F01] Since: {h1}\n- **[B02] [P2] Two.** b Related: [B08], [B09]\n\n## Features\n- **[F01] [Major] Feat.** c\n\n## Completed\n- ~~**[B08] [P2] Done.** x~~ Done 2026-09-30 [`abc`]\n"}, dseed, mig(0), mig(0, "--apply")))
	dboth(all, m("content/bad-tag-dropped", fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P9] Bad tag.** a\n- **[B02] [Major] Wrong kind tag.** b\n\n## Features\n- **[F01] [P1] Wrong too.** c\n\n## Tasks\n- **[T01] [Major] Task tag.** d\n"}, dseed, mig(0), mig(0, "--apply")))
	dboth(all, m("content/milestone-not-on-tracker", fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] One.** a Milestone: M07\n- **[B02] [P1] Two.** b Milestone: M09\n"}, dseed, mig(0), mig(0, "--apply")))
	dboth(all, m("content/milestone-in-map-and-files", withMS(map[string]string{}), dempty, mig(0), mig(0, "--apply")))
	dboth(all, m("content/id-placeholder", fx{files: map[string]string{".rota/tasks/T01.md": "# {ID}\n\nSee [{ID}] here.\n"}}, dseed, mig(0, "--apply")))
	dboth(all, m("content/unicode-and-quotes", fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] Café \"quoted\" title.** résumé ☃ text Since: {h1}\n\n## Tasks\n- **[T01] Dotted v1.2.3 name.** body\n"}, dseed, mig(0), mig(0, "--apply")))
	dboth(all, m("content/duplicate-id", fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] One.** a\n- **[B01] [P1] One again.** b\n"}, dseed, mig(0), mig(0, "--apply")))
	dboth(all, m("content/title-only-dots", fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] Fine.** a\n- **[B02] [P1] ..** b\n- **[B03] [P1] After.** c\n"}, dseed, mig(2, "--apply")))
	dboth(all, m("content/note-split", fx{files: map[string]string{".rota/designs/F02.md": strings.Repeat("a design line\n", 40), ".rota/plans/M01-B02.md": strings.Repeat("plan line [B01]\n", 30)}}, dseed,
		mig(0, "--apply").env1("ROTA_NOTE_LIMIT=200")))
	dboth(all, m("content/proof-only-detail", fx{files: map[string]string{".rota/bugs/B01.md": "## Proof\n- build · PASS · ok\n"}}, dseed, mig(0, "--apply")))
	dboth(all, m("content/detail-blank", fx{files: map[string]string{".rota/bugs/B01.md": "\n\n   \n"}}, dseed, mig(0, "--apply")))
	dboth(all, m("content/related-bare-and-bracketed", fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] One.** a Related: B02, [F01]\n- **[B02] [P1] Two.** b Related: [B01]\n\n## Features\n- **[F01] [Major] F.** c Related: [B01], [B02], T01\n\n## Tasks\n- **[T01] T.** d\n", files: map[string]string{".rota/designs/F01.md": "see B01, [B02], B01.md and /path/B02 and xB01 and B011\n", ".rota/plans/M01-F01.md": "Relates to F01 and [T01]\n"}}, dseed, mig(0), mig(0, "--apply")))
	dboth(all, m("content/custom-labels", fx{config: `{"issues": {"retryWaitSeconds": 0, "bulkPaceMs": 0, "labels": {"types": {"bug": "kind/bug", "feature": "kind/feature", "task": "kind/task"}, "priorityPrefix": "prio-", "sizePrefix": "effort:", "milestoneTracker": "ms-tracker"}}}`, files: migMilestones}, dseed, mig(0), mig(0, "--apply")))
	dboth(all, m("content/config-provider-gitlab", fx{config: `{"issues": {"retryWaitSeconds": 0, "bulkPaceMs": 0, "provider": "gitlab"}}`}, dseed, mig(0, "--apply")))
	dboth(all, m("content/config-provider-github", fx{config: `{"issues": {"retryWaitSeconds": 0, "bulkPaceMs": 0, "provider": "github"}}`}, dseed, mig(0, "--apply")))
	add(m("content/config-provider-without-remote", fx{config: `{"issues": {"retryWaitSeconds": 0, "bulkPaceMs": 0, "provider": "github"}}`}, dseed, mig(0, "--apply")).on("none"))
	dboth(all, m("content/ms-id-mismatch", withMS(map[string]string{".rota/milestones/M02.md": "---\nid: M05\ntitle: Odd\nstatus: planned\ndepends: [M01, M09]\n---\n\n# M05\n\n## Goal\n\nGoal text.\n"}), dseed, mig(0), mig(0, "--apply")))
	dboth(all, m("content/ms-no-frontmatter-id", withMS(map[string]string{".rota/milestones/M02.md": "---\ntitle: Noid\nstatus: active\n---\n\n# M02 — Heading title\n\n## Goal\n\nText.\n"}), dseed, mig(0), mig(0, "--apply")))
	dboth(all, m("content/ms-archived-and-shipped-skipped", withMS(map[string]string{".rota/milestones/M03.md": "---\nid: M03\ntitle: Arch\nstatus: archived\n---\n\n# M03\n", ".rota/milestones/M04.md": "no frontmatter\n"}), dseed, mig(0, "--apply")))
	dboth(all, m("content/ms-only", fx{backlog: "# TODO\n\n## Bugs\n\n## Completed\n", files: migMilestones}, dempty, mig(0), mig(0, "--apply")))
	dboth(all, m("content/ms-depends-multiple", withMS(map[string]string{".rota/milestones/M02.md": "---\nid: M02\ntitle: Deps\nstatus: active\ndepends: [M01, M00]\n---\n\n# M02 — Deps\n\n## Goal\n\nDepends on two.\n"}), dempty, mig(0, "--apply")))
	dboth(all, m("content/fields-all", fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] Full.** desc text Detail: `.rota/bugs/B01.md` Related: [F01]. Milestone: M09 Repos: web, api Subsystem: capture. Captured: 2026-01-02 Since: {h1}\n\n## Features\n- **[F01] [Cosmetic] Pretty.** x\n"}, dseed, mig(0), mig(0, "--apply")))
	dboth(all, m("content/crlf-backlog", fx{backlog: "# TODO\r\n\r\n## Bugs\r\n- **[B01] [P1] Crlf.** a Since: {h1}\r\n"}, dseed, mig(0, "--apply")))
	dboth(all, m("content/plan-glob-ambiguity", fx{files: map[string]string{".rota/plans/M01-B01.md": "p1\n", ".rota/plans/M02-B01.md": "p2\n", ".rota/plans/M01-B011.md": "wrong\n"}}, dseed, mig(0, "--apply")))
}
