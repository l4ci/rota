package main

// Scenarios for the A4 item verbs (#48): id next, item create, complete,
// field get|list|set, reopen, rm, shipped, ready and comment add|list, on the
// shared harness (harness_test.go) and checked against their frozen records.

import (
	"strings"
	"testing"
)

func suiteA4(t *testing.T) {
	bodyFx := fx{files: map[string]string{"body.md": "# {ID}\n\nSee [{ID}] in the backlog.\n"}}
	relFx := fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] a.** x\n- **[B02] [P2] b.** y Related: [B01], [F01], [T01] Milestone: M01 Since: {h1}\n\n## Features\n- **[F01] [Major] f.** z Related: [B01], [B02]\n\n## Tasks\n- **[T01] t.** w Related: [B01]\n"}
	var all []scn
	add := func(s ...scn) { all = append(all, s...) }

	// ---- id next
	for _, c := range []struct{ kind, id string }{{"bugs", "B10"}, {"features", "F09"}, {"tasks", "T09"}, {"milestones", "M02"}} {
		c := c
		add(variants(scn{name: "idnext/" + c.kind, argv: j("id", "next", "--kind", c.kind), want: 0,
			check: func(t *testing.T, e envl) { eq(t, e, "data.id", c.id); eq(t, e, "data.changed", true) }})...)
	}
	add(
		scn{name: "idnext/no-counters-file", fx: fx{counters: "-"}, argv: j("id", "next", "--kind", "tasks"), want: 0},
		scn{name: "idnext/counter-ahead", fx: fx{counters: "{\n  \"bugs\": 50\n}\n"}, argv: j("id", "next", "--kind", "bugs"), want: 0,
			check: func(t *testing.T, e envl) { eq(t, e, "data.id", "B51") }},
		scn{name: "idnext/missing-kind", argv: j("id", "next"), want: 2},
		scn{name: "idnext/bad-kind", argv: j("id", "next", "--kind", "stories"), want: 2},
		scn{name: "idnext/positional", argv: j("id", "next", "--kind", "bugs", "extra"), want: 2},
		scn{name: "idnext/unknown-flag", argv: j("id", "next", "--kind", "bugs", "--bogus"), want: 2},
		scn{name: "idnext/no-hv", fx: fx{noHV: true}, argv: j("id", "next", "--kind", "bugs"), want: 3},
		scn{name: "idnext/repo-outside-umbrella", argv: j("id", "next", "--kind", "bugs", "--repo", "web"), want: 3},
		scn{name: "idnext/issues-backend", fx: fx{config: issuesConfig}, argv: j("id", "next", "--kind", "bugs"), want: 4,
			check: func(t *testing.T, e envl) {
				eq(t, e, "data.blockedBy", "backend")
				eq(t, e, "data.changed", false)
			}},
	)

	// ---- item create
	add(variants(scn{name: "create/bug-all-fields", want: 0,
		argv: j("item", "create", "--kind", "bugs", "--title", "Crash  on   save", "--tag", "P1", "--desc", "It crashes.",
			"--related", "[B01]", "--milestone", "M01", "--repos", "web", "--subsystem", "io", "--captured", "2026-10-02"),
		check: func(t *testing.T, e envl) { eq(t, e, "data.id", "B10"); eq(t, e, "data.type", "B") }})...)
	add(variants(scn{name: "create/feature", argv: j("item", "create", "--kind", "features", "--title", "New thing", "--tag", "Major"), want: 0})...)
	add(
		scn{name: "create/task", argv: j("item", "create", "--kind", "tasks", "--title", "Chore!"), want: 0},
		scn{name: "create/title-ends-question", argv: j("item", "create", "--kind", "bugs", "--title", "Why does it fail?", "--tag", "P3"), want: 0},
		scn{name: "create/body-file", fx: bodyFx, argv: j("item", "create", "--kind", "features", "--title", "With body", "--body-file", "body.md"), want: 0,
			check: func(t *testing.T, e envl) { eq(t, e, "data.detail", ".rota/features/F09.md") }},
		scn{name: "create/body-stdin", argv: j("item", "create", "--kind", "bugs", "--title", "Stdin body", "--body-file", "-"), in: "# {ID}\nbody from stdin\n", want: 0},
		scn{name: "create/empty-flag-values", argv: j("item", "create", "--kind", "bugs", "--title", "Skips", "--desc", "", "--related", "", "--tag", ""), want: 2},
		scn{name: "create/empty-desc-and-tag-ok", argv: j("item", "create", "--kind", "bugs", "--title", "Skips", "--desc", "", "--tag", ""), want: 0},
		scn{name: "create/unreadable-raw-file",
			argv: j("item", "create", "--kind", "bugs", "--raw-file", "missing.md"), want: 3},
		scn{name: "create/invalid-backend", fx: fx{config: `{"backlog": {"backend": "jira"}}`}, goOnly: true, want: 70,
			argv: j("item", "create", "--kind", "bugs", "--title", "x")},
		scn{name: "create/bad-tag-bug", argv: j("item", "create", "--kind", "bugs", "--title", "x", "--tag", "Major"), want: 2},
		scn{name: "create/tag-on-task", argv: j("item", "create", "--kind", "tasks", "--title", "x", "--tag", "P1"), want: 2},
		scn{name: "create/missing-title", argv: j("item", "create", "--kind", "bugs"), want: 2},
		scn{name: "create/bad-kind", argv: j("item", "create", "--kind", "epics", "--title", "x"), want: 2},
		scn{name: "create/no-backlog", fx: fx{noBacklog: true}, argv: j("item", "create", "--kind", "bugs", "--title", "x"), want: 3},
		scn{name: "create/missing-section-burns-id", fx: fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] Only.** x\n"},
			argv: j("item", "create", "--kind", "tasks", "--title", "x"), want: 3},
		scn{name: "create/unreadable-body", argv: j("item", "create", "--kind", "bugs", "--title", "x", "--body-file", "missing.md"), want: 3},
		scn{name: "create/section-in-middle", fx: fx{backlog: "# TODO\n\n## Bugs\n\n## Features\n- **[F01] [Major] f.** x\n\n## Completed\n"},
			argv: j("item", "create", "--kind", "bugs", "--title", "First bug"), want: 0},
		scn{name: "create/raw-file", fx: fx{files: map[string]string{"raw.md": "- **[B77] [P2] Raw bullet.** Verbatim body.\n\n"}},
			argv: j("item", "create", "--kind", "bugs", "--raw-file", "raw.md"), want: 0,
			check: func(t *testing.T, e envl) { eq(t, e, "data.id", "B77") }},
		scn{name: "create/raw-file-stdin", argv: j("item", "create", "--kind", "features", "--raw-file", "-"), in: "- **[F77] [Minor] From stdin.** x Since: abc\n", want: 0},
		scn{name: "create/raw-file-no-id", fx: fx{files: map[string]string{"raw.md": "- no id here\n"}},
			argv: j("item", "create", "--kind", "bugs", "--raw-file", "raw.md"), want: 2},
		scn{name: "create/raw-file-with-title", fx: fx{files: map[string]string{"raw.md": "- **[B77] [P2] x.** y\n"}},
			argv: j("item", "create", "--kind", "bugs", "--raw-file", "raw.md", "--title", "t"), want: 2},
		scn{name: "create/raw-file-missing-section", fx: fx{backlog: "# TODO\n\n## Bugs\n", files: map[string]string{"raw.md": "- **[T77] x.** y\n"}},
			argv: j("item", "create", "--kind", "tasks", "--raw-file", "raw.md"), want: 3},
		scn{name: "create/whitespace-title",
			argv: j("item", "create", "--kind", "bugs", "--title", "   "), want: 2},
		scn{name: "create/whitespace-field",
			argv: j("item", "create", "--kind", "bugs", "--title", "x", "--related", "  "), want: 2},
		scn{name: "create/issues-backend", fx: fx{config: issuesConfig}, goOnly: true, want: 5,
			argv: j("item", "create", "--kind", "bugs", "--title", "x")},
		scn{name: "create/raw-file-issues-refused", fx: fx{config: issuesConfig, files: map[string]string{"raw.md": "- **[B77] x.** y\n"}}, goOnly: true, want: 4,
			argv:  j("item", "create", "--kind", "bugs", "--raw-file", "raw.md"),
			check: func(t *testing.T, e envl) { eq(t, e, "data.blockedBy", "backend") }},
	)

	// ---- item complete
	add(variants(scn{name: "complete/done-with-proof", argv: j("item", "complete", "B01", "--commit", "{h1}"), want: 0,
		check: func(t *testing.T, e envl) { eq(t, e, "data.changed", true); eq(t, e, "data.commit", "{h1}") }})...)
	add(
		scn{name: "complete/default-commit", argv: j("item", "complete", "B01"), want: 0},
		scn{name: "complete/no-proof-refused", argv: j("item", "complete", "B02", "--commit", "{h1}"), want: 4,
			check: func(t *testing.T, e envl) {
				eq(t, e, "data.blockedBy", "proof missing")
				eq(t, e, "data.changed", false)
			}},
		scn{name: "complete/no-proof-flag", argv: j("item", "complete", "B02", "--commit", "{h1}", "--no-proof"), want: 0},
		scn{name: "complete/no-proof-false-still-gated", argv: j("item", "complete", "B02", "--commit", "{h1}", "--no-proof=false"), want: 4},
		scn{name: "complete/feature-bumps-features", argv: j("item", "complete", "F02", "--commit", "{h1}", "--no-proof"), want: 0},
		scn{name: "complete/task-no-bump", argv: j("item", "complete", "T01", "--commit", "{h1}", "--no-proof"), want: 0},
		scn{name: "complete/refactor-commit-no-bump", argv: j("item", "complete", "B03", "--commit", "{refactor}", "--no-proof"), want: 0},
		scn{name: "complete/unresolvable-hash-bumps", argv: j("item", "complete", "B03", "--commit", "deadbee", "--no-proof"), want: 0},
		scn{name: "complete/dropped-with-note", argv: j("item", "complete", "F02", "--commit", "{h1}", "--reason", "dropped", "--note", "line one\nline two"), want: 0},
		scn{name: "complete/handed-off-no-proof-needed", argv: j("item", "complete", "B03", "--commit", "{h1}", "--reason", "handed-off"), want: 0},
		scn{name: "complete/blocked-note", argv: j("item", "complete", "B03", "--commit", "{h1}", "--reason", "blocked", "--note", "waits on X. Related: [B01]"), want: 0},
		scn{name: "complete/done-ignores-note", argv: j("item", "complete", "B01", "--commit", "{h1}", "--note", "ignored"), want: 0},
		scn{name: "complete/already-completed", argv: j("item", "complete", "B08", "--commit", "{h1}"), want: 0,
			check: func(t *testing.T, e envl) { eq(t, e, "data.changed", false) }},
		scn{name: "complete/unknown-id", argv: j("item", "complete", "B99", "--commit", "{h1}"), want: 3},
		scn{name: "complete/archived-id-not-found", fx: fx{archive: "plain"}, argv: j("item", "complete", "B05", "--commit", "{h1}"), want: 3},
		scn{name: "complete/bad-reason", argv: j("item", "complete", "B01", "--reason", "wontfix"), want: 2},
		scn{name: "complete/missing-id", argv: j("item", "complete"), want: 2},
		scn{name: "complete/no-head", fx: fx{noCommit: true}, argv: j("item", "complete", "B01", "--no-proof"), want: 5},
		scn{name: "complete/no-head-explicit-commit", fx: fx{noCommit: true}, argv: j("item", "complete", "B03", "--commit", "abc1234", "--no-proof"), want: 0},
		scn{name: "complete/no-counters-file", fx: fx{counters: "-"}, argv: j("item", "complete", "B03", "--commit", "{h1}", "--no-proof"), want: 0},
		scn{name: "complete/no-completed-section", fx: fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] Only.** x\n\n## Features\n- **[F01] [Major] f.** y\n"},
			argv: j("item", "complete", "B01", "--commit", "{h1}", "--no-proof"), want: 0},
		scn{name: "complete/section-after-completed", fx: fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] Only.** x\n\n## Completed\n- ~~**[B08] x.**~~ Done 2026-09-30 [`abc`]\n\n## Notes\nhello\n"},
			argv: j("item", "complete", "B01", "--commit", "{h1}", "--no-proof"), want: 0},
		scn{name: "complete/no-backlog", fx: fx{noBacklog: true}, argv: j("item", "complete", "B01", "--commit", "{h1}", "--no-proof"), want: 3},
		scn{name: "complete/last-line-no-newline", fx: fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] Last.** x"},
			argv: j("item", "complete", "B01", "--commit", "{h1}", "--no-proof"), want: 0},
		scn{name: "complete/issues-backend", fx: fx{config: issuesConfig}, goOnly: true, want: 5,
			argv: j("item", "complete", "12", "--commit", "abc1234")},
	)

	// ---- item field get / list
	for _, f := range []struct{ id, name, want string }{
		{"B01", "title", "First bug"}, {"B01", "detail", "`.rota/bugs/B01.md`"}, {"B01", "related", "[F01]"},
		{"B01", "milestone", "M01"}, {"B01", "since", "{h1}"}, {"B01", "reason", ""}, {"B03", "repos", "web"},
		{"B03", "subsystem", "capture"}, {"B04", "title", "Fix v1"}, {"B08", "reason", "done"},
		{"B09", "reason", "dropped"}, {"B09", "note", "no longer needed"}, {"F08", "title", "Done refactor feature"},
		{"T01", "title", "First task"},
	} {
		f := f
		add(variants(scn{name: "fieldget/" + f.id + "-" + f.name, argv: j("item", "field", "get", f.id, "--name", f.name), want: 0,
			check: func(t *testing.T, e envl) { eq(t, e, "data.value", f.want) }})...)
	}
	add(
		scn{name: "fieldget/archived-title", fx: fx{archive: "plain"}, argv: j("item", "field", "get", "B05", "--name", "title"), want: 0},
		scn{name: "fieldget/archived-title-sectioned", fx: fx{archive: "sectioned"}, argv: j("item", "field", "get", "B05", "--name", "title"), want: 0},
		scn{name: "fieldget/archived-title-no-archive", argv: j("item", "field", "get", "B05", "--name", "title"), want: 3},
		scn{name: "fieldget/archived-reason", fx: fx{archive: "plain"}, argv: j("item", "field", "get", "F05", "--name", "reason"), want: 0},
		scn{name: "fieldget/unknown-id", argv: j("item", "field", "get", "B99", "--name", "title"), want: 3},
		scn{name: "fieldget/short-id-not-padded", argv: j("item", "field", "get", "B1", "--name", "title"), want: 3},
		scn{name: "fieldget/bad-name", argv: j("item", "field", "get", "B01", "--name", "color"), want: 2},
		scn{name: "fieldget/missing-name", argv: j("item", "field", "get", "B01"), want: 2},
		scn{name: "fieldget/no-backlog", fx: fx{noBacklog: true}, argv: j("item", "field", "get", "B01", "--name", "title"), want: 3},
		scn{name: "fieldget/title-without-period", fx: fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] No period here** body\n"},
			// the contract types value as a string; the old helper printed None
			argv: j("item", "field", "get", "B01", "--name", "title"), want: 0,
			check: func(t *testing.T, e envl) { eq(t, e, "data.value", "") }},
	)
	add(variants(scn{name: "fieldlist/open", argv: j("item", "field", "list", "B01"), want: 0})...)
	add(
		scn{name: "fieldlist/done", argv: j("item", "field", "list", "B09"), want: 0},
		scn{name: "fieldlist/unknown", argv: j("item", "field", "list", "T99"), want: 3},
		scn{name: "fieldlist/task", argv: j("item", "field", "list", "T02"), want: 0},
	)

	// ---- item field set
	set := func(name, id, field, value string, want int, extra ...string) scn {
		return scn{name: "fieldset/" + name, argv: j(append([]string{"item", "field", "set", id, "--name", field, "--value", value}, extra...)...), want: want}
	}
	add(variants(set("milestone-set", "B03", "milestone", "M09", 0))...)
	add(
		set("milestone-idempotent", "B03", "milestone", "M02", 0),
		set("milestone-clear", "B03", "milestone", "", 0),
		set("milestone-add-new", "B04", "milestone", "M05", 0),
		set("related-replace", "B02", "related", "[F02]", 0),
		set("related-clear", "B02", "related", "", 0),
		set("repos", "B01", "repos", "web,api", 0),
		set("subsystem-add", "B01", "subsystem", "io", 0),
		set("clear-absent-field", "B04", "subsystem", "", 0),
		set("dash-value", "B04", "related", "--weird", 0),
		set("detail-existing-file", "B04", "detail", ".rota/features/F01.md", 0),
		set("detail-backticked", "B04", "detail", "`.rota/features/F01.md`", 0),
		set("detail-clear", "B01", "detail", "", 0),
		set("detail-missing-file", "B04", "detail", ".rota/features/nope.md", 3),
		set("detail-only-backticks", "B04", "detail", "``", 2),
		set("closed-item", "B08", "milestone", "M01", 4),
		set("unknown-id", "B99", "milestone", "M01", 3),
		set("read-only-field", "B01", "since", "abc", 2),
		set("unknown-field", "B01", "colour", "red", 2),
		scn{name: "fieldset/missing-value", argv: j("item", "field", "set", "B01", "--name", "milestone"), want: 2},
		scn{name: "fieldset/missing-name", argv: j("item", "field", "set", "B01", "--value", "x"), want: 2},
		scn{name: "fieldset/no-backlog", fx: fx{noBacklog: true}, argv: j("item", "field", "set", "B01", "--name", "milestone", "--value", "x"), want: 3},
		scn{name: "fieldset/issues-detail-refused", fx: fx{config: issuesConfig}, goOnly: true, want: 4,
			argv:  j("item", "field", "set", "12", "--name", "detail", "--value", "x"),
			check: func(t *testing.T, e envl) { eq(t, e, "data.blockedBy", "backend") }},
	)
	add(scn{name: "fieldset/archived-item-plain", fx: fx{archive: "plain"}, argv: j("item", "field", "set", "B05", "--name", "milestone", "--value", "M01"), want: 4},
		scn{name: "fieldset/archived-item-sectioned", fx: fx{archive: "sectioned"}, argv: j("item", "field", "set", "B05", "--name", "milestone", "--value", "M01"), want: 4},
		set("archived-item-not-in-archive", "B05", "milestone", "M01", 3))
	for i := range all {
		if all[i].name == "fieldset/closed-item" {
			all[i].check = func(t *testing.T, e envl) {
				eq(t, e, "data.blockedBy", "closed item")
				eq(t, e, "data.changed", false)
			}
		}
	}

	// ---- item reopen
	reopen := func(name, id string, f fx, want int, chk func(*testing.T, envl)) scn {
		return scn{name: "reopen/" + name, fx: f, argv: j("item", "reopen", id), want: want, check: chk}
	}
	changed := func(v bool) func(*testing.T, envl) {
		return func(t *testing.T, e envl) { eq(t, e, "data.changed", v) }
	}
	add(variants(reopen("done-bug", "B08", fx{}, 0, changed(true)))...)
	add(
		reopen("refactor-commit-no-decrement", "F08", fx{}, 0, changed(true)),
		reopen("task-no-decrement", "T08", fx{}, 0, changed(true)),
		reopen("unresolvable-hash", "B09", fx{}, 0, changed(true)),
		reopen("already-active", "B01", fx{}, 0, changed(false)),
		reopen("unknown", "B99", fx{}, 3, nil),
		reopen("no-backlog", "B08", fx{noBacklog: true}, 3, nil),
		reopen("no-counters-file", "B08", fx{counters: "-"}, 0, changed(true)),
		reopen("target-section-missing", "T08", fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] x.** y\n\n## Completed\n- ~~**[T08] Done task.** body~~ Done 2026-09-30 [`{h1}`]\n"}, 0, changed(true)),
		reopen("target-section-empty", "T08", fx{backlog: "# TODO\n\n## Tasks\n\n## Completed\n- ~~**[T08] Done task.** body~~ Done 2026-09-30 [`{h1}`]\n"}, 0, changed(true)),
		reopen("no-completed-heading-active", "B01", fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] x.** y\n"}, 0, changed(false)),
		reopen("from-plain-archive", "B05", fx{archive: "plain"}, 0, changed(true)),
		reopen("from-sectioned-archive", "F05", fx{archive: "sectioned"}, 0, changed(true)),
		reopen("archive-missing", "B05", fx{}, 3, nil),
		scn{name: "reopen/issues-refused-5", fx: fx{config: issuesConfig}, goOnly: true, want: 5, argv: j("item", "reopen", "12")},
	)

	// ---- item rm
	rmPlan := func(name string, f fx, ids string, apply bool, scrub bool, want int, chk func(*testing.T, envl)) scn {
		argv := []string{"item", "rm"}
		if apply {
			argv = append(argv, "--apply")
		}
		if scrub {
			argv = append(argv, "--scrub-archive")
		}
		argv = append(argv, strings.Split(ids, ",")...)
		return scn{name: "rm/" + name, fx: f, argv: j(argv...), want: want, check: chk}
	}
	add(variants(rmPlan("preview-b01", fx{}, "B01", false, false, 0, func(t *testing.T, e envl) {
		eq(t, e, "data.applied", false)
		eq(t, e, "data.changed", false)
		eq(t, e, "data.items.0.crossRefs", float64(4)) // B02, F01, F02 (strict Related lines) and the done B09
		eq(t, e, "data.items.0.detailFile", ".rota/bugs/B01.md")
		eq(t, e, "data.items.0.todoEntry", true)
		eq(t, e, "warnings.0", "preview only; pass --apply")
	}))...)
	add(variants(rmPlan("apply-b01", fx{}, "B01", true, false, 0, func(t *testing.T, e envl) {
		eq(t, e, "data.applied", true)
		eq(t, e, "data.changed", true)
	}))...)
	add(variants(rmPlan("apply-b01-scrub", fx{}, "B01", true, true, 0, nil))...)
	add(
		rmPlan("apply-two-sharing-related", fx{}, "B01,F01", true, false, 0, nil),
		rmPlan("apply-three-with-plans", fx{}, "B02,F01,T01", true, false, 0, func(t *testing.T, e envl) {
			eq(t, e, "data.items.1.planFiles", []any{".rota/plans/M02-F01.md"})
			eq(t, e, "data.items.0.planFiles", []any{".rota/plans/M01-B02.md"})
		}),
		rmPlan("apply-task", fx{}, "T02", true, false, 0, nil),
		rmPlan("apply-completed-item", fx{}, "B09", true, false, 0, nil),
		rmPlan("apply-related-keeps-others", fx{}, "F01", true, false, 0, nil),
		rmPlan("preview-two", fx{}, "B03,T01", false, false, 0, nil),
		rmPlan("apply-no-newline-at-eof", fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] x.** a\n\n## Completed\n- ~~**[B08] y.** z Related: [B01]~~ Done 2026-09-30 [`abc`]"},
			"B08", true, false, 0, nil),
		rmPlan("related-keeps-the-others", relFx, "B01", true, false, 0, nil),
		rmPlan("related-keeps-the-others-two-removed", relFx, "B01,F01", true, false, 0, nil),
		rmPlan("related-no-space-after-comma", fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] a.** x\n- **[B02] [P2] b.** y Related: [B01],[F01],[T01] Since: {h1}\n\n## Features\n- **[F01] [Major] f.** z\n"},
			"B01", true, false, 0, nil),
		rmPlan("related-middle-field", fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] a.** x\n- **[B02] [P2] b.** y Related: [B01] Milestone: M01 Since: {h1}\n"},
			"B01", true, false, 0, nil),
		rmPlan("related-no-space-after-colon", fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] a.** x\n- **[B02] [P2] b.** y Related:[B01] Milestone: M01\n"},
			"B01", true, false, 0, nil),
		rmPlan("related-only-self", fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] a.** x Related: [B01]\n\n- **[B02] [P2] b.** y\n"},
			"B01", true, false, 0, nil),
		rmPlan("blank-line-after-bullet-collapses", fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] a.** x\n\n## Features\n- **[F01] [Major] f.** z\n"},
			"B01", true, false, 0, nil),
		rmPlan("unknown", fx{}, "B99", true, false, 3, nil),
		rmPlan("one-unknown-among-known", fx{}, "B01,B99", true, false, 3, nil),
		rmPlan("no-backlog", fx{noBacklog: true}, "B01", true, false, 3, nil),
		rmPlan("plain-archive-entry-not-found", fx{archive: "plain"}, "B05", true, true, 3, nil),
		rmPlan("sectioned-archive-without-scrub", fx{archive: "sectioned"}, "B05", true, false, 0, func(t *testing.T, e envl) {
			eq(t, e, "data.items.0.archive", true)
			eq(t, e, "data.items.0.todoEntry", false)
		}),
		rmPlan("sectioned-archive-with-scrub", fx{archive: "sectioned"}, "B05", true, true, 0, nil),
		rmPlan("sectioned-archive-scrub-other-item", fx{archive: "sectioned"}, "B01", true, true, 0, func(t *testing.T, e envl) {
			eq(t, e, "data.items.0.crossRefs", float64(5))
		}),
		rmPlan("active-branch-apply-refused", fx{status: "{\n  \"active\": [\n    {\n      \"branch\": \"feat/x\",\n      \"items\": [\"B01\"]\n    }\n  ]\n}\n"},
			"B01", true, false, 4, func(t *testing.T, e envl) {
				eq(t, e, "data.blockedBy", "active")
				eq(t, e, "data.id", "B01")
				eq(t, e, "data.activeBranch", "feat/x")
				eq(t, e, "data.changed", false)
			}),
		rmPlan("active-csv-items-apply-refused", fx{status: "{\n  \"active\": [\n    {\n      \"branch\": \"feat/y\",\n      \"items\": \"B03, T01\"\n    }\n  ]\n}\n"},
			"T01", true, false, 4, nil),
		rmPlan("status-other-item-not-blocking", fx{status: "{\n  \"active\": [\n    {\n      \"branch\": \"feat/z\",\n      \"items\": [\"F02\"]\n    }\n  ]\n}\n"},
			"B03", true, false, 0, nil),
		scn{name: "rm/preview-active-shows-branch", fx: fx{status: "{\n  \"active\": [\n    {\n      \"branch\": \"feat/x\",\n      \"items\": [\"B01\"]\n    }\n  ]\n}\n"},
			argv: j("item", "rm", "B01"), want: 0, // the contract previews an active ID; only --apply refuses it
			check: func(t *testing.T, e envl) {
				eq(t, e, "data.items.0.activeBranch", "feat/x")
				eq(t, e, "data.applied", false)
			}},
		scn{name: "rm/issues-backend-refused", fx: fx{config: issuesConfig}, argv: j("item", "rm", "--apply", "B01"), want: 4,
			check: func(t *testing.T, e envl) { eq(t, e, "data.blockedBy", "backend") }},
		scn{name: "rm/no-ids", argv: j("item", "rm"), goOnly: true, want: 2},
		scn{name: "rm/blank-ids", argv: j("item", "rm", " , "), goOnly: true, want: 2},
	)

	// ---- item shipped: exit 0 when a title has evidence, 1 when none has
	shipped := func(name string, want int, titles ...string) scn {
		return scn{name: "shipped/" + name, fx: fx{commits: map[bool]int{true: 5}[strings.HasPrefix(name, "many-commits")]}, argv: j(append([]string{"item", "shipped"}, titles...)...),
			want: want, check: func(t *testing.T, e envl) { eq(t, e, "data.found", want == 0) }}
	}
	add(
		shipped("strong-distinct", 0, "Add `parser-core` and lexer-v2 support"),
		shipped("path-hit", 0, "Update `src/main.go` handling"),
		shipped("path-and-commit", 0, "Rework `src/main.go` for `parser-core` lexer-v2"),
		shipped("none", 1, "Reticulate the quantum splines"),
		shipped("two-titles-one-hit", 0, "Reticulate splines", "Add `parser-core` and lexer-v2 support"),
		shipped("medium-common-tokens", 0, "Refactor tidy lexer"),
		shipped("stopwords-only", 1, "add the new fix"),
		shipped("blank-title-ignored", 0, "   ", "Add `parser-core` support lexer-v2"),
		shipped("path-with-space-ignored", 1, "Check `src/some file.go` thing"),
		shipped("dotted-token", 0, "Handle `README.md` in lexer"),
		shipped("many-commits-caps-at-three", 0, "Add `parser-core` and lexer-v2 support"),
		shipped("many-commits-medium", 0, "Add parser lexer support"),
		shipped("many-titles", 0, "Add `parser-core` lexer-v2", "Update `src/main.go`", "Nothing relevant zzz"),
		scn{name: "shipped/no-titles", argv: j("item", "shipped"), want: 2},
		scn{name: "shipped/only-blank", argv: j("item", "shipped", "  "), want: 2},
	)

	// ---- item ready
	ready := func(name, id string, f fx, want int) scn {
		return scn{name: "ready/" + name, fx: f, argv: j("item", "ready", id), want: want,
			check: func(t *testing.T, e envl) {
				if want != 3 {
					eq(t, e, "data.ready", want == 0)
				}
			}}
	}
	add(variants(ready("criteria-in-detail", "B01", fx{}, 0))...)
	add(variants(ready("not-ready", "B03", fx{}, 1))...)
	add(
		ready("design-note", "F02", fx{}, 0),
		ready("plan-note", "B02", fx{}, 0),
		ready("detail-without-criteria", "F01", fx{files: map[string]string{".rota/plans/M02-F01.md": ""}}, 1),
		ready("checkbox-line", "T01", fx{files: map[string]string{".rota/tasks/T01.md": "# T01\n\n  * [x] done thing\n"}}, 0),
		ready("heading-acceptance-lowercase", "T02", fx{files: map[string]string{".rota/tasks/T02.md": "# T02\n\n### acceptance criteria\ntext\n"}}, 0),
		ready("heading-needs-level", "T02", fx{files: map[string]string{".rota/tasks/T02.md": "# T02\n\nacceptance but no heading\n"}}, 1),
		ready("completed-item", "B08", fx{}, 1),
		ready("unknown", "B99", fx{}, 3),
		ready("archived", "B05", fx{archive: "plain"}, 1),
		ready("archived-missing", "B05", fx{}, 3),
	)

	// ---- item comment add
	cAdd := func(name, id, kind, body string, f fx, want int, extra ...string) scn {
		argv := append([]string{"item", "comment", "add", id, "--kind", kind, "--body-file", "-"}, extra...)
		return scn{name: "commentadd/" + name, fx: f, in: body, argv: j(argv...), want: want}
	}
	add(variants(cAdd("existing-log", "B01", "decision", "Ship it.", fx{}, 0))...)
	add(
		cAdd("multi-line", "B01", "question", "First line\nsecond line\n\nfourth after blank\n", fx{}, 0),
		cAdd("crlf-body", "B01", "feedback", "one\r\ntwo\r\n", fx{}, 0),
		cAdd("detail-without-log", "F01", "answer", "Because.", fx{}, 0),
		cAdd("creates-detail-file", "B03", "question", "Is this needed?", fx{}, 0),
		cAdd("creates-task-detail", "T01", "decision", "Do it.", fx{}, 0),
		cAdd("log-not-last-section", "B03", "answer", "x", fx{files: map[string]string{".rota/bugs/B03.md": "# B03\n\n## Log\n- 2026-01-01 · question · q\n\n## Notes\nn\n"}}, 0),
		cAdd("done-item", "B08", "feedback", "late note", fx{}, 0),
		cAdd("archived-item", "B05", "feedback", "late note", fx{archive: "plain"}, 0),
		cAdd("empty-body", "B01", "question", "  \n\n", fx{}, 2),
		cAdd("bad-kind", "B01", "remark", "x", fx{}, 2),
		cAdd("unknown-id", "B99", "question", "x", fx{}, 3),
		cAdd("no-backlog", "B01", "question", "x", fx{noBacklog: true}, 3),
		scn{name: "commentadd/missing-body-file", argv: j("item", "comment", "add", "B01", "--kind", "question"), want: 2},
		scn{name: "commentadd/issues-5", fx: fx{config: issuesConfig}, goOnly: true, want: 5, in: "x",
			argv: j("item", "comment", "add", "12", "--kind", "question", "--body-file", "-")},
	)

	// ---- item comment list
	cList := func(name, id, kind string, f fx, want int) scn {
		argv := []string{"item", "comment", "list", id}
		if kind != "" {
			argv = append(argv, "--kind", kind)
		}
		return scn{name: "commentlist/" + name, fx: f, argv: j(argv...), want: want}
	}
	add(variants(cList("all", "B01", "", fx{}, 0))...)
	add(
		cList("filter-question", "B01", "question", fx{}, 0),
		cList("filter-none", "B01", "feedback", fx{}, 0),
		cList("no-log", "F01", "", fx{}, 0),
		cList("no-detail", "B03", "", fx{}, 0),
		cList("bad-kind", "B01", "remark", fx{}, 2),
		cList("unknown", "B99", "", fx{}, 3),
		cList("done-item-no-detail", "B08", "", fx{}, 0),
		cList("task", "T01", "", fx{files: map[string]string{".rota/tasks/T01.md": "# T01\n\n## Log\n- 2026-02-02 · decision · a\n  b\n\n  c\n- 2026-02-03 · answer · d\n"}}, 0),
	)

	seen := map[string]bool{}
	for _, s := range all {
		if seen[s.name] {
			t.Fatalf("duplicate scenario name %q", s.name)
		}
		seen[s.name] = true
	}
	if len(all) < 60 {
		t.Fatalf("only %d scenarios", len(all))
	}
	t.Logf("%d scenarios", len(all))
	for _, s := range all {
		s := s
		t.Run(s.name, s.exec)
	}
}
