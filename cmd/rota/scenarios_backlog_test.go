package main

// Go-only scenarios for the backlog views, summary, status and refactor verbs
// (#48), on the harness of harness_test.go. Each scenario runs the Go binary and
// checks the exit code and its own assertions; frozen_test.go then compares the
// run with the frozen record, which pins the envelope and the .rota/ changes.

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// ---- shared pieces -----------------------------------------------------------

func strs(v any) []string {
	out := []string{}
	l, _ := v.([]any)
	for _, x := range l {
		out = append(out, fmt.Sprint(x))
	}
	return out
}

func lines(s string) []string {
	out := []string{}
	for _, l := range strings.Split(s, "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func both(fs ...func(t *testing.T, e envl)) func(t *testing.T, e envl) {
	return func(t *testing.T, e envl) {
		t.Helper()
		for _, f := range fs {
			f(t, e)
		}
	}
}

func eqCheck(path string, want any) func(t *testing.T, e envl) {
	return func(t *testing.T, e envl) { t.Helper(); eq(t, e, path, want) }
}

func blocked(t *testing.T, e envl) {
	t.Helper()
	eq(t, e, "data.blockedBy", "backend")
	eq(t, e, "data.changed", false)
}

// umbFx is a file-backend umbrella: the standard backlog at the root and two
// registered sub-repos, each a git repo.
var umbFx = fx{subs: map[string][]string{
	"web": {"chore: web init", "fix: [B01] web side"},
	"api": {"chore: api init"},
}}

func withFx(f fx, mod func(*fx)) fx { mod(&f); return f }

const stFile = `{
  "active": [
    {
      "branch": "feat/x",
      "repo": null,
      "items": ["B02"],
      "worktree": null,
      "startedAt": "2026-09-01T10:00:00Z"
    }
  ]
}
`

const stTwo = `{
  "active": [
    {"branch": "feat/x", "repo": null, "items": ["B01", "F01"], "worktree": "../wt-x", "startedAt": "2026-09-01T10:00:00Z"},
    {"branch": "feat/y", "repo": null, "items": ["T01"], "worktree": null, "startedAt": "2026-09-02T11:30:00Z"}
  ],
  "loopStartedAt": "2026-09-03T08:00:00Z"
}
`

const stUmb = `{
  "active": [
    {"branch": "feat/x", "repo": "web", "items": ["B01"], "worktree": "web-wt", "startedAt": "2026-09-01T10:00:00Z"},
    {"branch": "feat/x", "repo": "api", "items": ["B01"], "worktree": null, "startedAt": "2026-09-01T10:00:00Z"},
    {"branch": "feat/z", "repo": null, "items": ["T02"], "worktree": null, "startedAt": "2026-09-04T10:00:00Z"}
  ]
}
`

// ---- the scenarios ---------------------------------------------------------------

func suiteBacklog(t *testing.T) {
	var all []scn
	add := func(s ...scn) { all = append(all, s...) }

	// ---- backlog list
	const bl = "# TODO\n\n## Bugs\n"
	add(
		scn{name: "list/std", argv: j("backlog", "list"), want: 0, text: true,
			check: func(t *testing.T, e envl) {
				eq(t, e, "data.bugs.0.id", "B01")
				eq(t, e, "data.bugs.0.priority", "P1")
				eq(t, e, "data.features.0.size", "Minor")
				eq(t, e, "data.clusters.0.0", "B01")
			}},
		scn{name: "list/no-backlog", fx: fx{noBacklog: true}, argv: j("backlog", "list"), want: 0, text: true},
		scn{name: "list/empty-sections", fx: fx{backlog: "# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n"},
			argv: j("backlog", "list"), want: 0, text: true},
		scn{name: "list/no-sections", fx: fx{backlog: "# TODO\n"}, argv: j("backlog", "list"), want: 0, text: true},
		scn{name: "list/grep-title", argv: j("backlog", "list", "--grep", "first"), want: 0, text: true},
		scn{name: "list/grep-upper", argv: j("backlog", "list", "--grep", "SECOND"), want: 0, text: true},
		scn{name: "list/grep-related", argv: j("backlog", "list", "--grep", "[F01]"), want: 0, text: true},
		scn{name: "list/grep-field-text", argv: j("backlog", "list", "--grep", "capture"), want: 0, text: true},
		scn{name: "list/grep-none", argv: j("backlog", "list", "--grep", "zzzz"), want: 0, text: true},
		scn{name: "list/grep-empty", argv: j("backlog", "list", "--grep", ""), want: 0, text: true},
		scn{name: "list/grep-equals-form", argv: j("backlog", "list", "--grep=task"), want: 0, text: true},
		scn{name: "list/grep-keeps-cluster", argv: j("backlog", "list", "--grep", "Second bug"), want: 0, text: true},
		scn{name: "list/grep-spaces-and-dot", argv: j("backlog", "list", "--grep", "parser. dotted"), want: 0, text: true},
		scn{name: "list/active-one", fx: fx{status: stFile}, argv: j("backlog", "list"), want: 0, text: true,
			check: func(t *testing.T, e envl) {
				eq(t, e, "data.inProgress.0.id", "B02")
				eq(t, e, "data.inProgress.0.type", "B")
				eq(t, e, "data.inProgress.0.title", "[P2] Second bug")
				eq(t, e, "data.inProgress.0.startedAt", "2026-09-01T10:00:00Z")
			}},
		scn{name: "list/active-two-branches", fx: fx{status: stTwo}, argv: j("backlog", "list"), want: 0, text: true},
		scn{name: "list/active-not-filtered-by-grep", fx: fx{status: stFile}, argv: j("backlog", "list", "--grep", "task"), want: 0, text: true},
		scn{name: "list/active-no-match-message", fx: fx{status: stFile}, argv: j("backlog", "list", "--grep", "zzz"), want: 0, text: true},
		scn{name: "list/active-csv-items", fx: fx{status: `{"active": [{"branch": "b", "repo": null, "items": "B01, F01", "worktree": null, "startedAt": "2026-09-01T10:00:00Z"}]}`},
			argv: j("backlog", "list"), want: 0, text: true},
		scn{name: "list/active-no-items", fx: fx{status: `{"active": [{"branch": "b", "repo": null, "items": [], "worktree": null, "startedAt": "2026-09-01T10:00:00Z"}]}`},
			argv: j("backlog", "list"), want: 0, text: true},
		scn{name: "list/active-unknown-id", fx: fx{status: `{"active": [{"branch": "b", "repo": null, "items": ["B99", "T01"], "worktree": null, "startedAt": "2026-09-01T10:00:00Z"}]}`},
			argv: j("backlog", "list"), want: 0, text: true},
		scn{name: "list/active-no-started", fx: fx{status: `{"active": [{"branch": "b", "repo": null, "items": ["T01"]}]}`},
			argv: j("backlog", "list"), want: 0, text: true},
		scn{name: "list/active-corrupt-status", fx: fx{status: "{not json"}, argv: j("backlog", "list"), want: 0, text: true},
		scn{name: "list/active-repo-column", fx: withFx(umbFx, func(f *fx) { f.status = stUmb }), argv: j("backlog", "list"), want: 0, text: true,
			check: func(t *testing.T, e envl) { eq(t, e, "data.inProgress.0.repo", "web") }},
		scn{name: "list/sort-bug-prio", fx: fx{backlog: bl + "- **[B01] [P2] two.** a\n- **[B02] [P0] zero.** b\n- **[B03] none.** c\n- **[B04] [P1] one.** d\n- **[B05] [P9] odd.** e\n"},
			argv: j("backlog", "list"), want: 0, text: true},
		scn{name: "list/sort-feature-size", fx: fx{backlog: "# TODO\n\n## Features\n- **[F01] [Major] big.** a\n- **[F02] [Cosmetic] tiny.** b\n- **[F03] none.** c\n- **[F04] [Minor] mid.** d\n"},
			argv: j("backlog", "list"), want: 0, text: true},
		scn{name: "list/milestone-column", fx: fx{backlog: bl + "- **[B01] [P1] a.** x Milestone: M01\n- **[B02] [P1] b.** y\n- **[B03] [P1] c.** z Milestone: M01, M03\n"},
			argv: j("backlog", "list"), want: 0, text: true},
		scn{name: "list/milestone-only-features", fx: fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] a.** x\n\n## Features\n- **[F01] [Major] f.** y Milestone: M02\n"},
			argv: j("backlog", "list"), want: 0, text: true},
		scn{name: "list/clusters-chain-and-pair", fx: fx{backlog: bl + "- **[B01] [P1] a.** x Related: [B02]\n- **[B02] [P1] b.** y\n- **[B03] [P1] c.** z Related: [B04], [F01]\n- **[B04] [P1] d.** w\n- **[B05] [P1] lonely.** v Related: [B77]\n\n## Features\n- **[F01] [Major] f.** u\n"},
			argv: j("backlog", "list"), want: 0, text: true},
		scn{name: "list/clusters-grep-one-member", fx: fx{backlog: bl + "- **[B01] [P1] alpha.** x Related: [B02]\n- **[B02] [P1] beta.** y\n- **[B03] [P1] gamma.** z Related: [B04]\n- **[B04] [P1] delta.** w\n"},
			argv: j("backlog", "list", "--grep", "gamma"), want: 0, text: true},
		scn{name: "list/clusters-with-active-member", fx: fx{status: stFile}, argv: j("backlog", "list"), want: 0, text: true},
		scn{name: "list/related-trailing-dot", fx: fx{backlog: bl + "- **[B01] [P1] a.** x Related: [B02]. Milestone: M01\n- **[B02] [P1] b.** y\n"},
			argv: j("backlog", "list"), want: 0, text: true},
		scn{name: "list/indented-bullet", fx: fx{backlog: bl + "- **[B01] [P1] a.** x\n  - **[B02] [P1] nested.** y\n"},
			argv: j("backlog", "list"), want: 0, text: true},
		scn{name: "list/duplicate-ids", fx: fx{backlog: bl + "- **[B01] [P1] a.** x\n- **[B01] [P2] again.** y\n"},
			argv: j("backlog", "list"), want: 0, text: true},
		scn{name: "list/no-period-title", fx: fx{backlog: "# TODO\n\n## Tasks\n- **[T01] No period**\n- **[T02] With. period** rest\n"},
			argv: j("backlog", "list"), want: 0, text: true},
		scn{name: "list/unicode-title", fx: fx{backlog: bl + "- **[B01] [P1] Größe — naïve.** Zeile Related: [B02]\n- **[B02] [P1] 日本語.** y\n"},
			argv: j("backlog", "list", "--grep", "GRÖSSE"), want: 0, text: true},
		scn{name: "list/completed-only", fx: fx{backlog: "# TODO\n\n## Completed\n- ~~**[B01] [P1] Done.** x~~ Done 2026-09-30 [`abc1234`]\n"},
			argv: j("backlog", "list"), want: 0, text: true},
		scn{name: "list/umbrella-no-repo", fx: umbFx, argv: j("backlog", "list"), want: 0, text: true},
		scn{name: "list/umbrella-repo-registered", fx: umbFx, argv: j("backlog", "list", "--repo", "web"), want: 0, text: true},
		scn{name: "list/umbrella-repo-unregistered", fx: umbFx, argv: j("backlog", "list", "--repo", "nope"), want: 3},
		scn{name: "list/repo-outside-umbrella", argv: j("backlog", "list", "--repo", "web"), want: 3},
		scn{name: "list/positional", argv: j("backlog", "list", "extra"), want: 2},
		scn{name: "list/unknown-flag", argv: j("backlog", "list", "--bogus"), want: 2},
		scn{name: "list/grep-needs-value", argv: j("backlog", "list", "--grep"), want: 2},
		scn{name: "list/no-hv", fx: fx{noHV: true}, argv: j("backlog", "list"), want: 3},
		scn{name: "list/issues-backend", fx: fx{config: issuesConfig}, goOnly: true, want: 5, argv: j("backlog", "list")},
	)
	_ = bl

	// ---- backlog ids
	idsOf := func(name string, f fx, mid string, extra ...string) scn {
		return scn{name: "ids/" + name, fx: f, argv: j(append([]string{"backlog", "ids", "--milestone", mid}, extra...)...),
			want: 0, check: eqCheck("data.milestone", mid)}
	}
	msFx := fx{backlog: bl + "- **[B01] [P1] a.** x Milestone: M01\n- **[B02] [P1] b.** y Milestone: M01, M03\n- **[B03] [P1] c.** z Milestone: M03\n  - **[B04] [P1] indented.** w Milestone: M01\n\n## Features\n- **[F01] [Major] f.** u Milestone: M03\n\n## Tasks\n- **[T01] t.** v Milestone: M01\n\n## Completed\n- ~~**[B09] [P1] done.** q Milestone: M01~~ Done 2026-09-30 [`abc`]\n"}
	add(
		idsOf("std-m01", fx{}, "M01"),
		idsOf("std-m02", fx{}, "M02"),
		idsOf("unknown", fx{}, "M99"),
		idsOf("multi-valued-m01", msFx, "M01"),
		idsOf("multi-valued-m03", msFx, "M03"),
		idsOf("completed-skipped", msFx, "M09"),
		idsOf("no-backlog", fx{noBacklog: true}, "M01"),
		idsOf("duplicate-ids", fx{backlog: bl + "- **[B01] [P1] a.** x Milestone: M01\n- **[B01] [P1] again.** y Milestone: M01\n"}, "M01"),
		idsOf("umbrella", umbFx, "M01", "--repo", "web"),
		idsOf("lowercase-not-matched", fx{backlog: bl + "- **[B01] [P1] a.** x Milestone: m01\n"}, "M01"),
		scn{name: "ids/empty-value", goOnly: true, argv: j("backlog", "ids", "--milestone", ""), want: 2},
		scn{name: "ids/missing-flag", goOnly: true, argv: j("backlog", "ids"), want: 2},
		scn{name: "ids/positional", goOnly: true, argv: j("backlog", "ids", "--milestone", "M01", "M01"), want: 2},
		scn{name: "ids/no-hv", goOnly: true, fx: fx{noHV: true}, argv: j("backlog", "ids", "--milestone", "M01"), want: 3},
		scn{name: "ids/repo-unregistered", goOnly: true, fx: umbFx, argv: j("backlog", "ids", "--milestone", "M01", "--repo", "x"), want: 3},
		scn{name: "ids/issues-backend", fx: fx{config: issuesConfig}, goOnly: true, want: 5, argv: j("backlog", "ids", "--milestone", "M01")},
	)

	// ---- backlog milestones
	mil := func(name string, f fx, items ...string) scn {
		return scn{name: "milestones/" + name, fx: f, argv: j(append([]string{"backlog", "milestones"}, items...)...),
			want: 0}
	}
	numFx := fx{backlog: bl + "- **[B01] [P1] a.** x Milestone: M10, M2\n- **[B02] [P1] b.** y Milestone: M2, M03\n- **[B03] [P1] c.** z\n\n## Completed\n- ~~**[B09] [P1] done.** q Milestone: M07~~ Done 2026-09-30 [`abc`]\n"}
	add(
		mil("one", fx{}, "B01"),
		mil("two-sorted-unique", fx{}, "B01", "B03", "B02"),
		mil("numeric-order", numFx, "B01", "B02"),
		mil("untagged", fx{}, "T01"),
		mil("unknown-silent", fx{}, "B99", "B01"),
		mil("completed-not-surfaced", numFx, "B09"),
		mil("short-form-not-matched", fx{}, "B1"),
		mil("no-backlog", fx{noBacklog: true}, "B01"),
		mil("umbrella", umbFx, "B01", "B03"),
		scn{name: "milestones/no-ids", goOnly: true, argv: j("backlog", "milestones"), want: 2},
		scn{name: "milestones/no-hv", goOnly: true, fx: fx{noHV: true}, argv: j("backlog", "milestones", "B01"), want: 3},
		scn{name: "milestones/repo-unregistered", goOnly: true, argv: j("backlog", "milestones", "B01", "--repo", "x"), want: 3},
		scn{name: "milestones/issues-backend", fx: fx{config: issuesConfig}, goOnly: true, want: 5, argv: j("backlog", "milestones", "12")},
	)

	// ---- summary
	know := func(n int) string {
		var b strings.Builder
		b.WriteString("# Knowledge\n\npreamble\n")
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "\n## Topic%d\n- entry\n", i+1)
		}
		return b.String()
	}
	ms := func(id, title, status string) string {
		s := "---\n"
		if id != "" {
			s += "id: " + id + "\n"
		}
		if title != "" {
			s += "title: " + title + "\n"
		}
		if status != "" {
			s += "status: " + status + "\n"
		}
		return s + "---\n\nbody\n"
	}
	sum := func(name string, f fx) scn {
		return scn{name: "summary/" + name, fx: f, argv: j("summary"), want: 0, text: true}
	}
	add(variants(sum("std", fx{}))...)
	add(
		sum("no-sections", fx{backlog: "# TODO\n"}),
		sum("empty-sections", fx{backlog: "# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n"}),
		sum("singular-counts", fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] a.** x\n\n## Features\n- **[F01] [Major] f.** x\n\n## Tasks\n- **[T01] t.** x\n"}),
		sum("counts-any-bold-bracket-line", fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] a.** x\n  - **[B02] [P1] indented.** y\n- **[X9] unknown letter.** z\n- plain\n"}),
		sum("recent-reasons", fx{backlog: "# TODO\n\n## Completed\n- ~~**[B01] [P1] a.** x~~ Done 2026-09-30 [`abc1234`] (handed-off: elsewhere)\n- ~~**[F02] [Major] b.** y~~ Done 2026-09-29 [`abc1234`] (blocked)\n- ~~**[T03] c.** z~~ Done 2026-09-28 [`abc1234`] (dropped: no)\n- ~~**[B04] d.** w~~ Done 2026-09-27 [`abc1234`]\n"}),
		sum("recent-fewer-than-three", fx{backlog: "# TODO\n\n## Completed\n- ~~**[B01] [P1] a.** x~~ Done 2026-09-30 [`abc1234`]\n"}),
		sum("recent-without-id", fx{backlog: "# TODO\n\n## Completed\n- ~~no id here~~ Done 2026-09-30 [`abc1234`]\n- ~~**[B02] [P1] b.** y~~ Done 2026-09-29 [`abc1234`]\n"}),
		sum("recent-ignores-indented-and-other-lines", fx{backlog: "# TODO\n\n## Completed\nnotes\n  - ~~**[B01] [P1] a.** x~~ Done 2026-09-30 [`abc1234`]\n- ~~**[B02] [P1] b.** y~~ Done 2026-09-29 [`abc1234`]\n"}),
		sum("active-one", fx{status: stFile}),
		sum("active-two-with-worktree", fx{status: stTwo}),
		sum("active-umbrella-repos", withFx(umbFx, func(f *fx) { f.status = stUmb })),
		sum("active-no-items", fx{status: `{"active": [{"branch": "b", "repo": null, "items": [], "worktree": null, "startedAt": "2026-09-01T10:00:00Z"}]}`}),
		sum("active-csv-items", fx{status: `{"active": [{"branch": "b", "repo": null, "items": "B01, T01", "worktree": null, "startedAt": "2026-09-01T10:00:00Z"}]}`}),
		sum("active-no-started", fx{status: `{"active": [{"branch": "b", "items": ["B01"]}]}`}),
		sum("active-corrupt-status", fx{status: "{nope"}),
		sum("knowledge-one-topic", fx{files: map[string]string{".rota/KNOWLEDGE.md": know(1)}}),
		sum("knowledge-four-topics", fx{files: map[string]string{".rota/KNOWLEDGE.md": know(4)}}),
		sum("knowledge-five-topics", fx{files: map[string]string{".rota/KNOWLEDGE.md": know(5)}}),
		sum("knowledge-no-topics", fx{files: map[string]string{".rota/KNOWLEDGE.md": "# Knowledge\n\nnothing\n"}}),
		sum("decisions-and-knowledge", fx{files: map[string]string{".rota/KNOWLEDGE.md": know(2), ".rota/DECISIONS.md": know(6)}}),
		sum("archive-one-item", fx{files: map[string]string{".rota/ARCHIVE.md": "# Archive\n- ~~**[B05] [P2] a.** old~~ Done 2026-05-15 [`e4abdbe`]\n"}}),
		sum("archive-no-done-lines", fx{files: map[string]string{".rota/ARCHIVE.md": "# Archive\n\nnothing\n"}}),
		sum("milestones-active", fx{files: map[string]string{".rota/milestones/M01.md": ms("M01", "First milestone", "active"), ".rota/milestones/M02.md": ms("M02", "Second", "planned"),
			".rota/milestones/M03.md": ms("M03", "Third one", "active")}}),
		sum("milestones-id-from-filename", fx{files: map[string]string{".rota/milestones/M07.md": ms("", "Seventh", "active")}}),
		sum("milestones-default-status-planned", fx{files: map[string]string{".rota/milestones/M04.md": ms("M04", "Fourth", "")}}),
		sum("milestones-no-title", fx{files: map[string]string{".rota/milestones/M05.md": ms("M05", "", "active")}}),
		sum("milestones-no-frontmatter-skipped", fx{files: map[string]string{".rota/milestones/M06.md": "# M06\nno frontmatter\n", ".rota/milestones/M08.md": ms("M08", "Eighth", "active")}}),
		sum("everything", withFx(umbFx, func(f *fx) {
			f.status = stTwo
			f.archive = "plain"
			f.files = map[string]string{".rota/KNOWLEDGE.md": know(5), ".rota/DECISIONS.md": know(1), ".rota/milestones/M01.md": ms("M01", "First milestone", "active")}
		})),
		scn{name: "summary/no-backlog", fx: fx{noBacklog: true}, argv: j("summary"), want: 3},
		scn{name: "summary/no-hv", fx: fx{noHV: true}, argv: j("summary"), want: 3},
		scn{name: "summary/positional", argv: j("summary", "x"), want: 2, goOnly: true},
		scn{name: "summary/repo-unregistered", fx: umbFx, argv: j("summary", "--repo", "nope"), want: 3},
		scn{name: "summary/repo-registered", fx: umbFx, argv: j("summary", "--repo", "web"), want: 0, text: true},
		scn{name: "summary/issues-backend", fx: fx{config: issuesConfig}, goOnly: true, want: 5, argv: j("summary")},
		scn{name: "summary/topic-names-with-commas-kept-whole", goOnly: true, want: 0,
			fx:   fx{files: map[string]string{".rota/KNOWLEDGE.md": "# K\n\n## One, two\nx\n\n## Three\ny\n"}},
			argv: j("summary"), check: func(t *testing.T, e envl) {
				eq(t, e, "data.knowledge.count", float64(2))
				if got := strs(at(e, "data.knowledge.topics")); !reflect.DeepEqual(got, []string{"One, two", "Three"}) {
					t.Errorf("topics = %q", got)
				}
			}},
	)

	// ---- backlog archive
	arch := "# TODO\n\n## Bugs\n- **[B01] [P1] a.** x\n\n## Completed\n" +
		"- ~~**[B02] [P1] old.** x~~ Done {d30} [`abc1234`]\n" +
		"- ~~**[B03] [P1] mid.** x~~ Done {d6} [`abc1234`]\n" +
		"- ~~**[B04] [P1] edge.** x~~ Done {d5} [`abc1234`]\n" +
		"- ~~**[B05] [P1] fresh.** x~~ Done {d2} [`abc1234`] (dropped: gone)\n" +
		"- ~~**[B06] [P1] today.** x~~ Done {d0} [`abc1234`]\n"
	archFx := fx{backlog: arch}
	ar := func(name string, f fx, want int, args ...string) scn {
		return scn{name: "archive/" + name, fx: f, argv: j(append([]string{"backlog", "archive"}, args...)...), want: want}
	}
	add(variants(ar("default-days", archFx, 0))...)
	add(variants(ar("days-0", archFx, 0, "--days", "0"))...)
	add(
		ar("days-1", archFx, 0, "--days", "1"),
		ar("days-6", archFx, 0, "--days", "6"),
		ar("days-100-nothing-old-enough", archFx, 0, "--days", "100"),
		ar("days-equals-form", archFx, 0, "--days=3"),
		ar("std-fixture-old-dates", fx{}, 0),
		ar("std-fixture-days-9999", fx{}, 0, "--days", "9999"),
		ar("existing-plain-archive", withFx(archFx, func(f *fx) { f.archive = "plain" }), 0),
		ar("existing-sectioned-archive", withFx(archFx, func(f *fx) { f.archive = "sectioned" }), 0),
		ar("archive-without-trailing-newline", withFx(archFx, func(f *fx) {
			f.files = map[string]string{".rota/ARCHIVE.md": "# Archive\n\n- ~~**[B00] x.** y~~ Done 2026-01-01 [`abc`]   \n\n\n"}
		}), 0),
		ar("completed-last-no-trailing-newline", fx{backlog: strings.TrimRight(arch, "\n")}, 0),
		ar("completed-before-other-section", fx{backlog: arch + "\n## Notes\nhello\n"}, 0),
		ar("completed-before-other-section-no-blank", fx{backlog: arch + "## Notes\nhello\n"}, 0),
		ar("completed-empty", fx{backlog: "# TODO\n\n## Completed\n"}, 0),
		ar("non-done-lines-kept", fx{backlog: "# TODO\n\n## Completed\nnote line\n- ~~**[B02] [P1] old.** x~~ Done {d30} [`abc1234`]\n  - ~~**[B03] [P1] indented.** x~~ Done {d30} [`abc1234`]\n\ntail text\n"}, 0),
		ar("no-completed-section", fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] a.** x\n"}, 0),
		ar("no-backlog", fx{noBacklog: true}, 0),
		ar("crlf-backlog", fx{backlog: strings.ReplaceAll(arch, "\n", "\r\n")}, 0),
		ar("handed-off-and-blocked-moved", fx{backlog: "# TODO\n\n## Completed\n- ~~**[B02] [P1] a.** x~~ Done {d30} [`abc`] (handed-off: to F01. Related: [F01])\n- ~~**[B03] [P1] b.** x~~ Done {d30} [`abc`] (blocked)\n"}, 0),
		ar("days-abc", archFx, 2, "--days", "abc"),
		ar("days-negative", archFx, 2, "--days", "-1"),
		ar("days-fraction", archFx, 2, "--days", "3.5"),
		ar("days-empty", archFx, 2, "--days", ""),
		ar("positional", archFx, 2, "7"),
		ar("no-hv", fx{noHV: true}, 3),
		scn{name: "archive/issues-backend", fx: fx{config: issuesConfig}, argv: j("backlog", "archive"), want: 4, check: blocked},
		scn{name: "archive/invalid-date-line", fx: fx{backlog: "# TODO\n\n## Completed\n- ~~**[B02] [P1] a.** x~~ Done 2026-13-45 [`abc`]\n"},
			argv: j("backlog", "archive"), want: 70}, // a calendar-invalid date: rota exits 70 and writes nothing
		scn{name: "archive/repo-registered", fx: withFx(umbFx, func(f *fx) { f.backlog = arch }), argv: j("backlog", "archive", "--repo", "web"), want: 0},
		scn{name: "archive/repo-unregistered", fx: umbFx, argv: j("backlog", "archive", "--repo", "x"), want: 3},
	)

	// ---- backlog stale
	today := "2026-10-02"
	stale := func(name string, f fx, kind, todayV string, days int, want int) scn {
		argv := []string{"backlog", "stale", "--kind", kind}
		if days >= 0 {
			argv = append(argv, "--days", strconv.Itoa(days))
		}
		return scn{name: "stale/" + name, fx: f, argv: j(argv...), want: want, env: []string{"ROTA_TEST_TODAY=" + todayV},
			check: eqCheck("data.kind", kind)}
	}
	todoFx := fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] old.** x Captured: 2026-01-01\n- **[B02] [P1] fresh.** y Captured: 2026-09-15\n- **[B03] [P1] undated.** z\n- **[B04] [P1] junk.** w Captured: not-a-date\n\n## Features\n- **[F01] [Major] compact.** v Captured: 20260101\n- **[F02] [Minor] padded.** u Captured:  2026-02-02 \n\n## Tasks\n- **[T01] t.** s Captured: 2026-07-04\n\n## Completed\n- ~~**[B09] [P1] done.** q Captured: 2020-01-01~~ Done 2026-09-30 [`abc`]\n"}
	knowFx := fx{files: map[string]string{".rota/KNOWLEDGE.md": "# K\n\n## Alpha\nx\n\n## Beta, gamma\ny\n\n## Delta  \nz\n"}}
	mapFile := func(sub, touched string) string {
		s := "---\nsubsystem: " + sub + "\n"
		if touched != "" {
			s += "touched: " + touched + "\n"
		}
		return s + "---\nbody\n"
	}
	mapFx := fx{files: map[string]string{
		".rota/map/auth.md": mapFile("auth", "2026-01-01"), ".rota/map/cache.md": mapFile("cache", "2026-09-30"),
		".rota/map/billing.md": mapFile("billing", ""), ".rota/map/nosub.md": "---\ntitle: x\n---\nbody\n",
		".rota/map/broken.md": "no frontmatter\n", ".rota/map/zeta.md": mapFile("aaa-first", "2025-12-31"),
		".rota/map/bad-date.md": mapFile("baddate", "yesterday"),
	}}
	add(
		stale("todo-90", todoFx, "todo", today, 90, 0),
		stale("todo-default-days", todoFx, "todo", today, -1, 0),
		stale("todo-0-days", todoFx, "todo", today, 0, 0),
		stale("todo-1000-days", todoFx, "todo", today, 1000, 0),
		stale("todo-earlier-today", todoFx, "todo", "2026-01-31", 20, 0),
		stale("todo-future-captured-not-stale", todoFx, "todo", "2025-01-01", 0, 0),
		stale("todo-no-backlog", fx{noBacklog: true}, "todo", today, 90, 0),
		stale("todo-std-fixture", fx{}, "todo", today, 90, 0),
		stale("knowledge-stale", knowFx, "knowledge", "2027-06-01", 90, 0),
		stale("knowledge-fresh", knowFx, "knowledge", today, 90, 0),
		stale("knowledge-boundary-0-days", knowFx, "knowledge", today, 0, 0),
		stale("knowledge-missing-file", fx{}, "knowledge", "2027-06-01", 90, 0),
		stale("knowledge-no-headings", fx{files: map[string]string{".rota/KNOWLEDGE.md": "# K\nnothing\n"}}, "knowledge", "2027-06-01", 90, 0),
		stale("map-touched", mapFx, "map", today, 90, 0),
		stale("map-git-date-fallback", mapFx, "map", "2027-06-01", 90, 0),
		stale("map-none-stale", mapFx, "map", "2025-01-01", 90, 0),
		stale("map-missing-dir", fx{}, "map", today, 90, 0),
		stale("map-umbrella", withFx(umbFx, func(f *fx) { f.files = mapFx.files }), "map", today, 90, 0),
		scn{name: "stale/bad-kind", argv: j("backlog", "stale", "--kind", "plans"), want: 2},
		scn{name: "stale/missing-kind", goOnly: true, argv: j("backlog", "stale"), want: 2},
		scn{name: "stale/positional", goOnly: true, argv: j("backlog", "stale", "--kind", "todo", "x"), want: 2},
		scn{name: "stale/days-not-a-number", goOnly: true, argv: j("backlog", "stale", "--kind", "todo", "--days", "soon"), want: 2},
		scn{name: "stale/bad-today-env", goOnly: true, argv: j("backlog", "stale", "--kind", "todo"), env: []string{"ROTA_TEST_TODAY=tomorrow"}, want: 2},
		scn{name: "stale/no-hv", goOnly: true, fx: fx{noHV: true}, argv: j("backlog", "stale", "--kind", "todo"), want: 3},
		scn{name: "stale/repo-unregistered", goOnly: true, fx: umbFx, argv: j("backlog", "stale", "--kind", "todo", "--repo", "x"), want: 3},
		scn{name: "stale/todo-reads-file-under-issues-config", fx: withFx(todoFx, func(f *fx) { f.config = issuesConfig }), argv: j("backlog", "stale", "--kind", "todo", "--days", "90"),
			env: []string{"ROTA_TEST_TODAY=" + today}, want: 0},
		scn{name: "stale/default-today-is-real-today", goOnly: true, argv: j("backlog", "stale", "--kind", "todo", "--days", "0"), want: 0, fx: todoFx,
			check: func(t *testing.T, e envl) {
				if n := len(strs(at(e, "data.entries"))); n == 0 {
					t.Errorf("no entries with --days 0 and the real date")
				}
			}},
	)

	// ---- backlog drift
	dr := func(name string, f fx, want int, args ...string) scn {
		return scn{name: "drift/" + name, fx: f, argv: j(append([]string{"backlog", "drift"}, args...)...), want: want}
	}
	// ids pins what the scenario must find, so an empty answer cannot pass.
	ids := func(s scn, drift, sym []string) scn {
		s.check = func(t *testing.T, e envl) {
			t.Helper()
			var gd, gs []string
			for _, it := range at(e, "data.drift").([]any) {
				gd = append(gd, it.(map[string]any)["id"].(string))
			}
			for _, it := range at(e, "data.symbolDrift").([]any) {
				gs = append(gs, it.(map[string]any)["id"].(string))
			}
			if !reflect.DeepEqual(gd, drift) || !reflect.DeepEqual(gs, sym) {
				t.Errorf("drift ids = %v, symbol ids = %v; want %v and %v", gd, gs, drift, sym)
			}
		}
		return s
	}
	// A project whose history mentions open items before and after their anchors.
	driftHist := func(backlog string, more func(t *testing.T, dir string, in *info)) fx {
		return fx{after: func(t *testing.T, dir string, in *info) {
			in.x["c1"] = commitFile(t, dir, "fix: handle [B01] edge case", "c1.txt", "x\n")
			in.x["c2"] = commitFile(t, dir, "feat: [B02] and [F01] landed", "c2.txt", "x\n")
			in.x["c3"] = commitFile(t, dir, "chore: bump", "c3.txt", "x\n")
			in.x["c4"] = commitFile(t, dir, "docs: mention [B02] again; also [T01] [X99] [B5] [B08]", "c4.txt", "x\n")
			if more != nil {
				more(t, dir, in)
			}
			write(t, dir, ".rota/BACKLOG.md", expand(backlog, *in))
		}}
	}
	const driftBacklog = "# TODO\n\n## Bugs\n" +
		"- **[B01] [P1] Edge.** x Since: {x:c2}\n" + // c1 predates the anchor
		"- **[B02] [P1] Landed.** y Since: {h1}\n" + // c2 and c4 are after it
		"- **[B03] [P2] Unresolvable anchor.** z Since: deadbeef\n" +
		"- **[B04] [P2] Option-like anchor.** z Since: --exec=x\n" +
		"\n## Features\n- **[F01] [Major] No anchor.** w\n\n## Tasks\n- **[T01] Tidy.** v\n  - **[B07] [P1] Indented.** u\n\n" +
		"## Completed\n- ~~**[B08] [P1] Done.** q~~ Done 2026-09-30 [`abc1234`]\n"
	symFiles := func(t *testing.T, dir string, in *info) {
		in.x["c5"] = commitFile(t, dir, "refactor: lexer", "src/lexer.go",
			"func ParserCore() {}\nvar snake_case_thing = 1\n// rota-new-helper\nsee config/loader.yaml and lib.py\nnaive_parser\n")
		commitFile(t, dir, "docs: reference", "docs/ref.txt", "see src/gr\u00f6\u00dfe/modul.go here and Gr\u00f6\u00dfeWert\n")
	}
	symBacklog := "# TODO\n\n## Bugs\n" +
		"- **[B01] [P1] Add `ParserCore` and snake_case_thing.** Detail: `.rota/bugs/B01.md` Since: {h1}\n" +
		"- **[B02] [P1] Needs rota-new-helper and config/loader.yaml plus lib.py.** y Since: {h1}\n" +
		"- **[B03] [P1] Already there: ParserCore.** z Since: {x:c5}\n" +
		"- **[B04] [P1] Only in the backlog: `OnlyInBacklogText`.** w Since: {h1}\n" +
		"- **[B05] [P2] Rework src/gr\u00f6\u00dfe/modul.go and na\u00efve_parser here.** v Since: {h1}\n" +
		"- **[B06] [P2] Short: `abc` and `ab_cd`.** u Since: {h1}\n" +
		"- **[B07] [P2] Unanchored `ParserCore`.** t\n"
	manySyms := func(t *testing.T, dir string, in *info) {
		var body, title []string
		for i := 1; i <= 10; i++ {
			body = append(body, fmt.Sprintf("sym_number_%d", i))
			title = append(title, fmt.Sprintf("`sym_number_%d`", i))
		}
		for i := 1; i <= 7; i++ {
			commitFile(t, dir, "feat: many "+strconv.Itoa(i), fmt.Sprintf("many/f%d.txt", i), strings.Join(body, "\n")+"\n")
		}
		in.x["title"] = strings.Join(title, " ")
	}
	add(
		dr("std-no-mentions", fx{}, 0),
		ids(dr("anchors-and-fallbacks", driftHist(driftBacklog, nil), 0), []string{"B02", "F01", "T01"}, nil),
		dr("closed-and-unknown-ids-not-reported", driftHist(driftBacklog, nil), 0),
		dr("many-commits-per-id", withFx(driftHist(driftBacklog, nil), func(f *fx) { f.commits = 3 }), 0),
		ids(dr("symbol-drift", driftHist(symBacklog, symFiles), 0), []string{"B01", "B02"}, []string{"B01", "B02", "B05"}),
		ids(dr("symbol-cap-8-and-5-files", driftHist("# TODO\n\n## Bugs\n- **[B01] [P1] Many {x:title}.** x Since: {h1}\n", manySyms), 0), []string{"B01"}, []string{"B01"}),
		dr("no-open-items", fx{backlog: "# TODO\n\n## Completed\n- ~~**[B08] [P1] Done.** q~~ Done 2026-09-30 [`abc1234`]\n"}, 0),
		dr("no-backlog", fx{noBacklog: true}, 0),
		dr("empty-since-field", driftHist("# TODO\n\n## Bugs\n- **[B01] [P1] a.** x Since:\n- **[B02] [P1] b.** y Since: \n", nil), 0),
		ids(dr("umbrella-no-since", withFx(umbFx, func(f *fx) { f.backlog = "# TODO\n\n## Bugs\n- **[B01] [P1] web side.** x\n" }), 0), []string{"B01"}, nil),
		dr("umbrella-root-anchor-unknown-in-subrepos", umbFx, 0),
		dr("umbrella-repo-web", withFx(umbFx, func(f *fx) { f.backlog = "# TODO\n\n## Bugs\n- **[B01] [P1] web side.** x\n" }), 0, "--repo", "web"),
		scn{name: "drift/umbrella-repo-api-filters-out-web", goOnly: true, want: 0, argv: j("backlog", "drift", "--repo", "api"),
			fx: withFx(umbFx, func(f *fx) { f.backlog = "# TODO\n\n## Bugs\n- **[B01] [P1] web side.** x\n" }),
			check: func(t *testing.T, e envl) {
				eq(t, e, "data.drift", []any{})
				eq(t, e, "data.symbolDrift", []any{})
			}},
		scn{name: "drift/issues-backend", fx: fx{config: issuesConfig}, goOnly: true, argv: j("backlog", "drift"), want: 1, check: blocked},
		scn{name: "drift/no-hv", goOnly: true, fx: fx{noHV: true}, argv: j("backlog", "drift"), want: 3},
		scn{name: "drift/repo-outside-umbrella", goOnly: true, argv: j("backlog", "drift", "--repo", "web"), want: 3},
		scn{name: "drift/positional", goOnly: true, argv: j("backlog", "drift", "x"), want: 2},
	)

	// ---- backlog backfill
	bf := func(name string, f fx, want int, args ...string) scn {
		return scn{name: "backfill/" + name, fx: f, argv: j(append([]string{"backlog", "backfill"}, args...)...), want: want}
	}
	add(variants(bf("std", fx{}, 0))...)
	add(
		bf("all-stamped", fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] a.** x Since: abc1234\n\n## Tasks\n- **[T01] t.** y Since: abc1234\n"}, 0),
		bf("sections-out-of-order", fx{backlog: "# TODO\n\n## Tasks\n- **[T01] t.** y\n- **[T02] u.** y\n\n## Features\n- **[F01] [Major] f.** z\n\n## Bugs\n- **[B01] [P1] a.** x\n- **[B02] [P1] b.** x Since: abc\n"}, 0),
		bf("trailing-spaces-and-fields", fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] a.** x Related: [B02]   \n- **[B02] [P1] b.** y Milestone: M01 Detail: `.rota/bugs/B02.md`\n"}, 0),
		bf("completed-and-indented-untouched", fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] a.** x\n  - **[B02] [P1] nested.** y\n\n## Completed\n- ~~**[B09] [P1] done.** q~~ Done 2026-09-30 [`abc1234`]\n"}, 0),
		bf("empty-since-value", fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] a.** x Since:\n- **[B02] [P1] b.** y Since: \n"}, 0),
		bf("since-in-description-text", fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] a.** since last week Since: abc\n- **[B02] [P1] b.** the word Since: appears\n"}, 0),
		bf("crlf-backlog", fx{backlog: strings.ReplaceAll(stdBacklog, "\n", "\r\n")}, 0),
		bf("last-line-no-newline", fx{backlog: "# TODO\n\n## Bugs\n- **[B01] [P1] a.** x"}, 0),
		bf("empty-sections", fx{backlog: "# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n"}, 0),
		bf("no-backlog", fx{noBacklog: true}, 0),
		bf("umbrella-root-head", umbFx, 0),
		scn{name: "backfill/no-head", fx: fx{noCommit: true}, argv: j("backlog", "backfill"), want: 5},
		scn{name: "backfill/no-head-no-backlog", fx: fx{noCommit: true, noBacklog: true}, argv: j("backlog", "backfill"), want: 5},
		scn{name: "backfill/issues-backend", fx: fx{config: issuesConfig}, argv: j("backlog", "backfill"), want: 4, check: blocked},
		scn{name: "backfill/no-hv", goOnly: true, fx: fx{noHV: true}, argv: j("backlog", "backfill"), want: 3},
		scn{name: "backfill/positional", goOnly: true, argv: j("backlog", "backfill", "x"), want: 2},
		scn{name: "backfill/repo-unregistered", goOnly: true, fx: umbFx, argv: j("backlog", "backfill", "--repo", "x"), want: 3},
	)

	// ---- status add
	sa := func(name string, f fx, want int, args ...string) scn {
		return scn{name: "status-add/" + name, fx: f, argv: j(append([]string{"status", "add"}, args...)...), want: want}
	}
	st := func(s string) fx { return fx{status: s} }
	stWeb := `{"active": [{"branch": "feat/x", "repo": "web", "items": ["B01"], "worktree": null, "startedAt": "2026-09-01T10:00:00Z"}]}`
	add(
		sa("single-new", fx{}, 0, "feat/new", "--items", "B01,F01"),
		sa("single-with-worktree", fx{}, 0, "feat/new", "--items", "B01", "--worktree", "../wt-new"),
		sa("single-if-absent-existing", st(stFile), 0, "feat/x", "--items", "B01", "--if-absent"),
		sa("single-if-absent-new", st(stFile), 0, "feat/new", "--items", "B01", "--if-absent"),
		sa("single-replaces-existing", st(stFile), 0, "feat/x", "--items", "T01"),
		sa("single-replace-moves-to-end", st(stTwo), 0, "feat/x", "--items", "T02", "--worktree", "w"),
		sa("single-preserves-other-keys", st(stTwo), 0, "feat/z", "--items", "T02"),
		sa("items-with-spaces-and-empties", fx{}, 0, "feat/new", "--items", "B01, F01 ,, T01"),
		sa("items-blank-list", fx{}, 0, "feat/new", "--items", " , "),
		sa("items-equals-form", fx{}, 0, "feat/new", "--items=B01"),
		sa("branch-with-dots-and-slashes", fx{}, 0, "release/1.2.x", "--items", "B01"),
		sa("no-status-file", fx{}, 0, "feat/new", "--items", "B01"),
		sa("corrupt-status-file", st("{bad json"), 0, "feat/new", "--items", "B01"),
		sa("status-without-active-key", st(`{"loopStartedAt": "2026-09-03T08:00:00Z"}`), 0, "feat/new", "--items", "B01"),
		sa("existing-csv-items-entry-if-absent", st(`{"active": [{"branch": "feat/x", "repo": null, "items": "B01, T01", "worktree": null, "startedAt": "2026-09-01T10:00:00Z"}]}`), 0, "feat/x", "--items", "B01", "--if-absent"),
		sa("existing-extra-keys-entry-kept", st(`{"active": [{"branch": "other", "repo": null, "items": ["B01"], "worktree": null, "startedAt": "2026-09-01T10:00:00Z", "note": "k\u00fc"}]}`), 0, "feat/new", "--items", "B02"),
		sa("umbrella-repo", umbFx, 0, "feat/x", "--items", "B01", "--repo", "web"),
		sa("umbrella-repo-worktree", umbFx, 0, "feat/x", "--items", "B01", "--repo", "web", "--worktree", "web-wt"),
		sa("umbrella-repo-if-absent-existing", withFx(umbFx, func(f *fx) { f.status = stUmb }), 0, "feat/x", "--items", "B01", "--repo", "web", "--if-absent"),
		sa("umbrella-repo-replaces", withFx(umbFx, func(f *fx) { f.status = stUmb }), 0, "feat/x", "--items", "B02", "--repo", "web"),
		sa("umbrella-no-repo-is-its-own-pair", withFx(umbFx, func(f *fx) { f.status = stUmb }), 0, "feat/x", "--items", "B02"),
		sa("repos-multi", umbFx, 0, "feat/m", "--items", "B01,T01", "--repos", "web,api", "--worktrees", "w1,w2"),
		sa("repos-multi-no-worktrees", umbFx, 0, "feat/m", "--items", "B01", "--repos", "web,api"),
		sa("repos-spaces-after-comma", umbFx, 0, "feat/m", "--items", "B01", "--repos", "web, api"),
		sa("repos-blank-worktree-entry", umbFx, 0, "feat/m", "--items", "B01", "--repos", "web,api", "--worktrees", ",w2"),
		sa("repos-duplicate-name", umbFx, 0, "feat/m", "--items", "B01", "--repos", "web,web"),
		sa("repos-if-absent-both-exist", withFx(umbFx, func(f *fx) { f.status = stUmb }), 0, "feat/x", "--items", "B01", "--repos", "web,api", "--if-absent"),
		sa("repos-if-absent-partial", withFx(umbFx, func(f *fx) { f.status = stWeb }), 0, "feat/x", "--items", "B01", "--repos", "web,api", "--if-absent"),
		sa("repos-replaces-existing", withFx(umbFx, func(f *fx) { f.status = stUmb }), 0, "feat/x", "--items", "T01", "--repos", "web,api"),
		sa("repos-unregistered-name", umbFx, 3, "feat/m", "--items", "B01", "--repos", "web,nope"),
		sa("repos-outside-umbrella", fx{}, 3, "feat/m", "--items", "B01", "--repos", "web"),
		sa("repos-empty-value", umbFx, 2, "feat/m", "--items", "B01", "--repos", ""),
		sa("repos-worktrees-count-mismatch", umbFx, 2, "feat/m", "--items", "B01", "--repos", "web,api", "--worktrees", "w1"),
		sa("repos-and-repo", umbFx, 2, "feat/m", "--items", "B01", "--repos", "api", "--repo", "web"),
		sa("repos-with-worktree", umbFx, 2, "feat/m", "--items", "B01", "--repos", "web", "--worktree", "w"),
		sa("worktrees-without-repos", umbFx, 2, "feat/m", "--items", "B01", "--worktrees", "w"),
		sa("missing-items", fx{}, 2, "feat/new"),
		sa("empty-items", fx{}, 2, "feat/new", "--items", ""),
		sa("no-branch", fx{}, 2, "--items", "B01"),
		sa("two-branches", fx{}, 2, "a", "b", "--items", "B01"),
		sa("unknown-flag", fx{}, 2, "feat/new", "--items", "B01", "--bogus"),
		sa("repo-unregistered", umbFx, 3, "feat/x", "--items", "B01", "--repo", "nope"),
		sa("repo-outside-umbrella", fx{}, 3, "feat/x", "--items", "B01", "--repo", "web"),
		sa("no-hv", fx{noHV: true}, 3, "feat/x", "--items", "B01"),
		scn{name: "status-add/repos-only-commas", fx: umbFx, argv: j("status", "add", "feat/m", "--items", "B01", "--repos", ","), want: 2}, // --repos "," names nothing: a usage error,
	)

	// ---- status rm
	sr := func(name string, f fx, want int, args ...string) scn {
		return scn{name: "status-rm/" + name, fx: f, argv: j(append([]string{"status", "rm"}, args...)...), want: want}
	}
	hand := func(f fx, files map[string]string) fx {
		f.files = files
		return f
	}
	add(
		sr("existing", st(stFile), 0, "feat/x"),
		sr("no-entry", st(stFile), 0, "feat/nope"),
		sr("no-status-file", fx{}, 0, "feat/x"),
		sr("corrupt-status-file", st("{nope"), 0, "feat/x"),
		sr("keeps-other-entries-and-keys", st(stTwo), 0, "feat/y"),
		sr("removes-every-entry-of-the-pair", st(`{"active": [{"branch": "b", "repo": null, "items": ["B01"]}, {"branch": "b", "repo": null, "items": ["B02"]}, {"branch": "c", "repo": null, "items": []}]}`), 0, "b"),
		sr("with-handoff", hand(st(stFile), map[string]string{".rota/handoff/feat/x.md": "# handoff\n"}), 0, "feat/x"),
		sr("handoff-only", hand(fx{}, map[string]string{".rota/handoff/feat/x.md": "# handoff\n"}), 0, "feat/x"),
		sr("handoff-flat-branch", hand(st(stFile), map[string]string{".rota/handoff/solo.md": "# handoff\n"}), 0, "solo"),
		sr("repo-keyed", withFx(umbFx, func(f *fx) {
			f.status = stUmb
			f.files = map[string]string{".rota/handoff/feat/x@web.md": "h\n", ".rota/handoff/feat/x.md": "flat\n"}
		}), 0, "feat/x", "--repo", "web"),
		sr("repo-keyed-no-entry", withFx(umbFx, func(f *fx) { f.status = stUmb }), 0, "feat/x", "--repo", "api"),
		sr("no-repo-keeps-umbrella-entries", withFx(umbFx, func(f *fx) {
			f.status = stUmb
			f.files = map[string]string{".rota/handoff/feat/x.md": "flat\n", ".rota/handoff/feat/x@web.md": "h\n"}
		}), 0, "feat/x"),
		sr("no-repo-removes-legacy-entry", withFx(umbFx, func(f *fx) { f.status = stUmb }), 0, "feat/z"),
		sr("no-hv", fx{noHV: true}, 3, "feat/x"),
		sr("repo-unregistered", umbFx, 3, "feat/x", "--repo", "nope"),
		sr("repo-outside-umbrella", fx{}, 3, "feat/x", "--repo", "web"),
		sr("no-branch", fx{}, 2),
		sr("two-branches", fx{}, 2, "a", "b"),
		scn{name: "status-rm/branch-escaping-handoff-dir", goOnly: true, want: 2, argv: j("status", "rm", "../x"), fx: fx{files: map[string]string{".rota/x.md": "keep\n"}},
			check: func(t *testing.T, e envl) {
				if _, err := os.Stat(filepath.Join(e["__godir"].(string), ".rota", "x.md")); err != nil {
					t.Errorf("a file outside .rota/handoff was removed: %v", err)
				}
			}},
	)

	// ---- status show
	show := func(name string, f fx, branch string, active bool, items []string, repo, worktree any, extra ...string) scn {
		return scn{name: "status-show/" + name, fx: f, argv: j(append([]string{"status", "show", branch}, extra...)...), want: 0,
			check: func(t *testing.T, e envl) {
				t.Helper()
				eq(t, e, "data.branch", branch)
				eq(t, e, "data.active", active)
				if got := strs(at(e, "data.items")); !reflect.DeepEqual(got, items) {
					t.Errorf("items = %q, want %q", got, items)
				}
				eq(t, e, "data.repo", repo)
				eq(t, e, "data.worktree", worktree)
				if active && at(e, "data.startedAt") == nil {
					t.Errorf("no startedAt on an active entry")
				}
				if !active && at(e, "data.startedAt") != nil {
					t.Errorf("startedAt on an inactive branch")
				}
			}}
	}
	add(
		show("single-repo", st(stFile), "feat/x", true, []string{"B02"}, nil, nil),
		show("worktree-and-items", st(stTwo), "feat/x", true, []string{"B01", "F01"}, nil, "../wt-x"),
		show("inactive", st(stFile), "feat/nope", false, []string{}, nil, nil),
		show("no-status-file", fx{}, "feat/x", false, []string{}, nil, nil),
		show("corrupt-status-file", st("{x"), "feat/x", false, []string{}, nil, nil),
		show("umbrella-first-entry-by-branch", withFx(umbFx, func(f *fx) { f.status = stUmb }), "feat/x", true, []string{"B01"}, "web", "web-wt"),
		show("csv-items-listed", st(`{"active": [{"branch": "b", "repo": null, "items": "B01, T01", "worktree": null, "startedAt": "2026-09-01T10:00:00Z"}]}`), "b", true, []string{"B01", "T01"}, nil, nil),
		show("legacy-entry-no-repo-key", st(`{"active": [{"branch": "b", "items": ["B01"], "startedAt": "2026-09-01T10:00:00Z"}]}`), "b", true, []string{"B01"}, nil, nil),
		scn{name: "status-show/repo-narrows-to-the-pair", goOnly: true, want: 0, fx: withFx(umbFx, func(f *fx) { f.status = stUmb }), argv: j("status", "show", "feat/x", "--repo", "api"),
			check: both(eqCheck("data.repo", "api"), eqCheck("data.worktree", nil), eqCheck("data.active", true))},
		scn{name: "status-show/repo-no-match-is-inactive", goOnly: true, want: 0, fx: withFx(umbFx, func(f *fx) { f.status = stUmb }), argv: j("status", "show", "feat/z", "--repo", "web"),
			check: eqCheck("data.active", false)},
		scn{name: "status-show/repo-unregistered", goOnly: true, want: 3, fx: umbFx, argv: j("status", "show", "feat/x", "--repo", "nope")},
		scn{name: "status-show/no-hv", goOnly: true, want: 3, fx: fx{noHV: true}, argv: j("status", "show", "b")},
		scn{name: "status-show/no-branch", goOnly: true, want: 2, argv: j("status", "show")},
	)

	// ---- status handoff
	ho := func(name string, f fx, branch string, repo string, canonical bool, wantPath any, wantExists bool) scn {
		argv := []string{"status", "handoff", branch}
		if repo != "" {
			argv = append(argv, "--repo", repo)
		}
		if canonical {
			argv = append(argv, "--canonical")
		}
		return scn{name: "status-handoff/" + name, fx: f, argv: j(argv...), want: 0,
			check: func(t *testing.T, e envl) {
				t.Helper()
				eq(t, e, "data.path", wantPath)
				eq(t, e, "data.exists", wantExists)
				eq(t, e, "data.branch", branch)
			}}
	}
	hf := map[string]string{".rota/handoff/feat/x.md": "h\n", ".rota/handoff/solo.md": "h\n", ".rota/handoff/feat/x@web.md": "w\n", ".rota/handoff/only-flat@web.md": "x\n"}
	hfx := withFx(umbFx, func(f *fx) { f.files = hf })
	add(
		ho("read-flat", hfx, "solo", "", false, ".rota/handoff/solo.md", true),
		ho("read-flat-slash-branch", hfx, "feat/x", "", false, ".rota/handoff/feat/x.md", true),
		ho("read-missing", hfx, "nothing", "", false, nil, false),
		ho("read-repo-keyed", hfx, "feat/x", "web", false, ".rota/handoff/feat/x@web.md", true),
		ho("read-repo-falls-back-to-flat", hfx, "feat/x", "api", false, ".rota/handoff/feat/x.md", true),
		ho("read-repo-keyed-only", hfx, "only-flat", "web", false, ".rota/handoff/only-flat@web.md", true),
		ho("read-no-repo-ignores-keyed-note", hfx, "only-flat", "", false, nil, false),
		ho("read-repo-missing-both", hfx, "nothing", "web", false, nil, false),
		ho("canonical-flat-existing", hfx, "solo", "", true, ".rota/handoff/solo.md", true),
		ho("canonical-flat-missing", hfx, "nothing", "", true, ".rota/handoff/nothing.md", false),
		ho("canonical-repo-existing", hfx, "feat/x", "web", true, ".rota/handoff/feat/x@web.md", true),
		ho("canonical-repo-missing-does-not-fall-back", hfx, "feat/x", "api", true, ".rota/handoff/feat/x@api.md", false),
		ho("directory-is-not-a-handoff", withFx(umbFx, func(f *fx) { f.files = map[string]string{".rota/handoff/dir.md/inner.txt": "x\n"} }), "dir", "", false, nil, false),
		scn{name: "status-handoff/no-hv", goOnly: true, want: 3, fx: fx{noHV: true}, argv: j("status", "handoff", "b")},
		scn{name: "status-handoff/repo-unregistered", goOnly: true, want: 3, fx: umbFx, argv: j("status", "handoff", "b", "--repo", "nope")},
		scn{name: "status-handoff/no-branch", goOnly: true, want: 2, argv: j("status", "handoff")},
		scn{name: "status-handoff/branch-escaping-the-dir", goOnly: true, want: 2, argv: j("status", "handoff", "../../x")},
		scn{name: "status-handoff/unknown-flag", goOnly: true, want: 2, argv: j("status", "handoff", "b", "--write")},
	)

	// ---- refactor age / reset / targets
	ageFx := func(counters string) fx { return fx{counters: counters} }
	age := func(name string, f fx, feats, bugs float64, args ...string) scn {
		return scn{name: "refactor-age/" + name, fx: f, argv: j(append([]string{"refactor", "age"}, args...)...), want: 0,
			check: func(t *testing.T, e envl) {
				t.Helper()
				eq(t, e, "data.features", feats)
				eq(t, e, "data.bugs", bugs)
			}}
	}
	add(
		age("std", fx{}, 1, 2),
		age("no-counters-file", ageFx("-"), 0, 0),
		age("no-since-refactor-key", ageFx("{\n  \"bugs\": 4\n}\n"), 0, 0),
		age("partial-keys", ageFx("{\"since_refactor\": {\"bugs\": 7}}"), 0, 7),
		age("large-numbers", ageFx("{\"since_refactor\": {\"features\": 12345678, \"bugs\": 0}}"), 12345678, 0),
		age("corrupt-counters", ageFx("{oops"), 0, 0),
		age("umbrella-repo-registered", withFx(umbFx, func(f *fx) { f.counters = stdCounters }), 1, 2, "--repo", "web"),
		scn{name: "refactor-age/repo-unregistered", goOnly: true, want: 3, fx: umbFx, argv: j("refactor", "age", "--repo", "x")},
		scn{name: "refactor-age/no-hv", goOnly: true, want: 3, fx: fx{noHV: true}, argv: j("refactor", "age")},
		scn{name: "refactor-age/positional", goOnly: true, want: 2, argv: j("refactor", "age", "x")},
		scn{name: "refactor-age/works-under-issues-config", fx: fx{config: issuesConfig}, argv: j("refactor", "age"), want: 0, check: eqCheck("data.bugs", float64(2))},
	)
	rst := func(name string, f fx, changed bool, args ...string) scn {
		return scn{name: "refactor-reset/" + name, fx: f, argv: j(append([]string{"refactor", "reset"}, args...)...), want: 0, check: eqCheck("data.changed", changed)}
	}
	add(
		rst("std", fx{}, true),
		rst("already-zero", ageFx("{\"bugs\": 4, \"since_refactor\": {\"features\": 0, \"bugs\": 0}}"), false),
		rst("only-bugs-nonzero", ageFx("{\"since_refactor\": {\"features\": 0, \"bugs\": 3}}"), true),
		rst("only-features-nonzero", ageFx("{\"since_refactor\": {\"features\": 3, \"bugs\": 0}}"), true),
		rst("no-counters-file-is-created", ageFx("-"), false),
		rst("no-since-refactor-key-appended-last", ageFx("{\n  \"bugs\": 4,\n  \"tasks\": 1\n}\n"), false),
		rst("key-order-kept-and-extra-inner-keys-dropped", ageFx("{\"since_refactor\": {\"tasks\": 9, \"features\": 1, \"bugs\": 1}, \"bugs\": 4}"), true),
		rst("corrupt-counters-replaced", ageFx("{oops"), false),
		rst("unicode-kept-escaped", ageFx("{\"note\": \"k\u00fc\", \"since_refactor\": {\"features\": 1, \"bugs\": 0}}"), true),
		rst("umbrella-repo-registered", withFx(umbFx, func(f *fx) { f.counters = stdCounters }), true, "--repo", "api"),
		scn{name: "refactor-reset/repo-unregistered", goOnly: true, want: 3, fx: umbFx, argv: j("refactor", "reset", "--repo", "x")},
		scn{name: "refactor-reset/no-hv", goOnly: true, want: 3, fx: fx{noHV: true}, argv: j("refactor", "reset")},
		scn{name: "refactor-reset/positional", goOnly: true, want: 2, argv: j("refactor", "reset", "x")},
	)
	clearCode := func(t *testing.T, dir string, in *info) {
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			switch e.Name() {
			case ".git", ".rota", "web", "api":
			default:
				os.RemoveAll(filepath.Join(dir, e.Name()))
			}
		}
	}
	tgt := func(name string, f fx) scn {
		return scn{name: "refactor-targets/" + name, fx: f, argv: j("refactor", "targets"), want: 0}
	}
	add(
		tgt("single-repo", fx{}),
		tgt("no-rota-at-all", fx{noHV: true}),
		tgt("umbrella-with-code", umbFx),
		tgt("umbrella-no-code", withFx(umbFx, func(f *fx) { f.after = clearCode })),
		tgt("umbrella-extra-ignored-entries-only", withFx(umbFx, func(f *fx) {
			f.after = func(t *testing.T, dir string, in *info) {
				clearCode(t, dir, in)
				for _, n := range []string{".claude", ".claude-plugin", ".gitignore", ".docsignore", ".stow-local-ignore", ".DS_Store"} {
					write(t, dir, n+"/x", "x\n")
				}
			}
		})),
		tgt("umbrella-sorted-by-name", withFx(fx{}, func(f *fx) {
			f.subs = map[string][]string{"zed": {"c"}, "alpha": {"c"}, "mid": {"c"}}
		})),
		tgt("registered-path-missing", withFx(umbFx, func(f *fx) {
			f.after = func(t *testing.T, dir string, in *info) {
				write(t, dir, ".rota/repos.json", `{"repos": [{"name": "web", "path": "web"}, {"name": "ghost", "path": "not/there/yet"}]}`+"\n")
			}
		})),
		tgt("registered-via-symlink", withFx(umbFx, func(f *fx) {
			f.after = func(t *testing.T, dir string, in *info) {
				if err := os.Symlink("web", filepath.Join(dir, "link")); err != nil {
					t.Fatal(err)
				}
				write(t, dir, ".rota/repos.json", `{"repos": [{"name": "alias", "path": "link"}]}`+"\n")
			}
		})),
		tgt("registered-nested-path", withFx(umbFx, func(f *fx) {
			f.after = func(t *testing.T, dir string, in *info) {
				write(t, dir, ".rota/repos.json", `{"repos": [{"name": "deep", "path": "apps/deep"}, {"name": "up", "path": "../elsewhere"}]}`+"\n")
				if err := os.MkdirAll(filepath.Join(dir, "apps", "deep"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
		})),
		tgt("entries-without-name-or-path-ignored", withFx(umbFx, func(f *fx) {
			f.after = func(t *testing.T, dir string, in *info) {
				write(t, dir, ".rota/repos.json", `{"repos": [{"name": "web"}, {"path": "api"}, {"name": "", "path": "api"}, {"name": "ok", "path": "api"}]}`+"\n")
			}
		})),
		tgt("duplicate-name-last-path-wins", withFx(umbFx, func(f *fx) {
			f.after = func(t *testing.T, dir string, in *info) {
				write(t, dir, ".rota/repos.json", `{"repos": [{"name": "x", "path": "web"}, {"name": "y", "path": "api"}, {"name": "x", "path": "api"}]}`+"\n")
			}
		})),
		tgt("empty-registry", withFx(fx{}, func(f *fx) {
			f.after = func(t *testing.T, dir string, in *info) { write(t, dir, ".rota/repos.json", `{"repos": []}`+"\n") }
		})),
		tgt("corrupt-registry", withFx(fx{}, func(f *fx) {
			f.after = func(t *testing.T, dir string, in *info) { write(t, dir, ".rota/repos.json", "{not json") }
		})),
		scn{name: "refactor-targets/from-a-subdirectory-uses-its-own-cwd", goOnly: true, want: 0, fx: umbFx, argv: j("-C", "web", "refactor", "targets"),
			check: both(eqCheck("data.umbrella", nil), eqCheck("data.subRepos", []any{}))},
		scn{name: "refactor-targets/repo-flag-rejected", goOnly: true, want: 2, fx: umbFx, argv: j("refactor", "targets", "--repo", "web")},
		scn{name: "refactor-targets/positional", goOnly: true, want: 2, argv: j("refactor", "targets", "x")},
	)

	finish(t, all)
}

// finish checks the scenario table and runs it.
func finish(t *testing.T, all []scn) {
	t.Helper()
	seen := map[string]bool{}
	for _, s := range all {
		if seen[s.name] {
			t.Fatalf("duplicate scenario name %q", s.name)
		}
		seen[s.name] = true
	}
	t.Logf("%d scenarios", len(all))
	for _, s := range all {
		s := s
		t.Run(s.name, s.exec)
	}
}
