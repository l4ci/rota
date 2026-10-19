package config

import (
	"slices"
	"strings"
	"testing"
)

func TestReviewPolicyResolve(t *testing.T) {
	obj := `{"ship":{"review":{"default":"full","lightBelow":50,"labels":{"risk:high":"full","best-of:2":"light","partial-slice":"none"}}}}`
	for _, c := range []struct {
		name    string
		cfg     string
		changed int
		labels  []string
		want    ReviewDepth
		whyHas  string
	}{
		{"unset is full", `{}`, 10, nil, DepthFull, "default"},
		{"legacy true", `{"ship":{"review":true}}`, 1, nil, DepthFull, "true"},
		{"legacy false", `{"ship":{"review":false}}`, 1000, nil, DepthNone, "false"},
		{"string light", `{"ship":{"review":"light"}}`, 1000, nil, DepthLight, "default"},
		{"string none", `{"ship":{"review":"none"}}`, 1, nil, DepthNone, "default"},
		{"small diff goes light", obj, 49, nil, DepthLight, "49 changed lines"},
		{"threshold is exclusive", obj, 50, nil, DepthFull, "default"},
		{"unknown size skips size rule", obj, -1, nil, DepthFull, "default"},
		{"best-of label", obj, 500, []string{"best-of:2"}, DepthLight, "best-of:2"},
		{"partial-slice label", obj, 500, []string{"partial-slice"}, DepthNone, "partial-slice"},
		{"label beats size", obj, 3, []string{"partial-slice"}, DepthNone, "partial-slice"},
		{"risk:high forces full over size", obj, 3, []string{"risk:high"}, DepthFull, "risk:high"},
		{"strictest label wins", obj, 3, []string{"partial-slice", "risk:high"}, DepthFull, "risk:high"},
		{"unrelated label ignored", obj, 500, []string{"p1"}, DepthFull, "default"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, err := ReviewPolicyOf(decode(t, c.cfg))
			if err != nil {
				t.Fatal(err)
			}
			got, why := p.Resolve(c.changed, c.labels)
			if got != c.want || !strings.Contains(why, c.whyHas) {
				t.Errorf("Resolve(%d, %v) = %s %q, want %s with %q in why", c.changed, c.labels, got, why, c.want, c.whyHas)
			}
		})
	}
}

func TestReviewPolicyShape(t *testing.T) {
	for in, ok := range map[string]bool{
		`true`: true, `false`: true, `"full"`: true, `"light"`: true, `"none"`: true,
		`{}`: true, `{"default":"light"}`: true, `{"lightBelow":20,"labels":{"x":"none"}}`: true,
		`"heavy"`: false, `3`: false, `[]`: false,
		`{"default":"heavy"}`: false, `{"default":true}`: false,
		`{"lightBelow":-1}`: false, `{"lightBelow":"5"}`: false, `{"lightBelow":1.5}`: false,
		`{"labels":["x"]}`: false, `{"labels":{"x":"deep"}}`: false, `{"labels":{"":"full"}}`: false,
		`{"unknown":1}`: false,
	} {
		_, err := ParseReviewPolicy(decode(t, in))
		if (err == nil) != ok {
			t.Errorf("ParseReviewPolicy(%s) err = %v, want ok=%v", in, err, ok)
		}
		if verr := Validate("ship.review", in); (verr == nil) != ok {
			t.Errorf("Validate(ship.review, %s) = %v, want ok=%v", in, verr, ok)
		}
	}
}

func TestReviewPolicyDecide(t *testing.T) {
	const cfg = `{"ship":{"review":{"default":"full","lightBelow":50,"labels":{"partial-slice":"none","best-of:2":"light"},"paths":{"migrations/**":"full","docs/*":"light"}}}}`
	issue := "## Files\n\n- `internal/a/x.go`\n- docs/*\n\n## Touches\n\n- `internal/b/`\n- `ship.review`\n"
	oneWay := "summary\n\nDoor: one-way. Blast radius: the schema.\n"
	for _, c := range []struct {
		name   string
		cfg    string
		in     ReviewInput
		want   ReviewDepth
		signal string
		whyHas string
	}{
		{"size light", cfg, ReviewInput{Changed: 3}, DepthLight, SignalSize, "3 changed"},
		{"one-way door lifts a small diff", cfg, ReviewInput{Changed: 3, PRBody: oneWay}, DepthFull, SignalDoor, "one-way"},
		{"two-way door does not", cfg, ReviewInput{Changed: 3, PRBody: "Door: two-way."}, DepthLight, SignalSize, ""},
		{"door in prose does not", cfg, ReviewInput{Changed: 3, PRBody: "this is not a Door: one-way door"}, DepthLight, SignalSize, ""},
		{"label none beats the door", cfg, ReviewInput{Changed: 3, PRBody: oneWay, Labels: []string{"partial-slice"}}, DepthNone, SignalLabel, "partial-slice"},
		{"label light does not beat the door", cfg, ReviewInput{Changed: 3, PRBody: oneWay, Labels: []string{"best-of:2"}}, DepthFull, SignalDoor, ""},
		{"scope drift lifts and lists files", cfg, ReviewInput{Changed: 3, IssueBody: issue, Files: []string{"internal/a/x.go", "internal/c/y.go", "internal/b/z.go"}}, DepthFull, SignalScopeDrift, "internal/c/y.go"},
		{"declared files do not drift", cfg, ReviewInput{Changed: 3, IssueBody: issue, Files: []string{"internal/a/x.go", "internal/b/z.go", "docs/a.md"}}, DepthLight, SignalSize, ""},
		{"issue without a file set cannot drift", cfg, ReviewInput{Changed: 3, IssueBody: "## Summary\n\nx", Files: []string{"anything.go"}}, DepthLight, SignalSize, ""},
		{"path lifts", cfg, ReviewInput{Changed: 3, Files: []string{"migrations/001.sql"}}, DepthFull, SignalPath, "migrations/001.sql"},
		{"trailing /** matches nested files", cfg, ReviewInput{Changed: 3, Files: []string{"migrations/2026/001.sql"}}, DepthFull, SignalPath, "migrations/2026"},
		{"path never lowers", cfg, ReviewInput{Changed: 500, Files: []string{"docs/a.md"}}, DepthFull, SignalDefault, ""},
		{"label none is final over a path", cfg, ReviewInput{Changed: 3, Labels: []string{"partial-slice"}, Files: []string{"migrations/001.sql"}}, DepthNone, SignalLabel, ""},
		{"door lifts a default of none", `{"ship":{"review":"none"}}`, ReviewInput{PRBody: oneWay}, DepthFull, SignalDoor, ""},
		{"drift lifts a default of none", `{"ship":{"review":"none"}}`, ReviewInput{IssueBody: issue, Files: []string{"z.go"}}, DepthFull, SignalScopeDrift, "z.go"},
		{"pointers count as declared files", cfg, ReviewInput{Changed: 3, IssueBody: "## Pointers\n\n- internal/config/reviewdepth.go (Resolve, ParseReviewPolicy)\n- docs/usage/a.md, docs/reference/b.md\n", Files: []string{"internal/config/reviewdepth.go", "docs/reference/b.md"}}, DepthLight, SignalSize, ""},
		{"pointers line mixing a backticked non-path and a bare path", cfg, ReviewInput{Changed: 3, IssueBody: "## Pointers\n\n- skills/references/worker-contract.md (the `Door:` line)\n- `ship.review` and internal/config/reviewdepth.go\n- docs/usage/a.md\n", Files: []string{"skills/references/worker-contract.md", "internal/config/reviewdepth.go"}}, DepthLight, SignalSize, ""},
		{"pointers declare a set to drift from", cfg, ReviewInput{Changed: 3, IssueBody: "## Pointers\n\n- internal/config/reviewdepth.go\n", Files: []string{"internal/cli/review.go"}}, DepthFull, SignalScopeDrift, "internal/cli/review.go"},
		{"declared dir/** covers nested files", cfg, ReviewInput{Changed: 3, IssueBody: "## Files\n\n- internal/cli/**\n", Files: []string{"internal/cli/a/b.go"}}, DepthLight, SignalSize, ""},
		{"scopeDrift false turns the rule off", `{"ship":{"review":{"default":"full","lightBelow":50,"scopeDrift":false}}}`, ReviewInput{Changed: 3, IssueBody: issue, Files: []string{"z.go"}}, DepthLight, SignalSize, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, err := ReviewPolicyOf(decode(t, c.cfg))
			if err != nil {
				t.Fatal(err)
			}
			got := p.Decide(c.in)
			if got.Depth != c.want || got.Signal != c.signal || !strings.Contains(got.Why, c.whyHas) {
				t.Errorf("Decide = %+v, want %s via %s with %q in why", got, c.want, c.signal, c.whyHas)
			}
		})
	}
}

func TestReviewPolicyPathsAndDriftShape(t *testing.T) {
	for _, cfg := range []string{
		`{"ship":{"review":{"paths":"x"}}}`,
		`{"ship":{"review":{"paths":{"a/**":"deep"}}}}`,
		`{"ship":{"review":{"scopeDrift":"no"}}}`,
		`{"ship":{"review":{"paths":{"a/**/b":"full"}}}}`,
		`{"ship":{"review":{"paths":{"a/[":"full"}}}}`,
	} {
		if _, err := ReviewPolicyOf(decode(t, cfg)); err == nil {
			t.Errorf("%s: want an error", cfg)
		}
	}
}

func TestScopeDriftSupportFiles(t *testing.T) {
	const cfg = `{"ship":{"review":{"default":"full","lightBelow":50}}}`
	issue := "## Files\n\n- `internal/config/reviewdepth.go`\n- `internal/gate`\n- `skills`\n"
	for _, c := range []struct {
		name  string
		cfg   string
		files []string
		drift string // "" means no drift
	}{
		{"test beside a declared file", cfg, []string{"internal/config/reviewdepth.go", "internal/config/other_test.go"}, ""},
		{"test in another package drifts", cfg, []string{"internal/config/reviewdepth.go", "internal/cli/x_test.go"}, "internal/cli/x_test.go"},
		{"non-test file beside a declared file drifts", cfg, []string{"internal/config/other.go"}, "internal/config/other.go"},
		{"test under a declared directory", cfg, []string{"internal/gate/sub/x_test.go"}, ""},
		{"declared bare directory covers its files", cfg, []string{"internal/gate/gate.go"}, ""},
		{"bare declared directory excuses no sibling test", cfg, []string{"internal/foo_test.go"}, "internal/foo_test.go"},
		{"top-level declared directory excuses no root test", "{\"ship\":{\"review\":{\"lightBelow\":50}}}", []string{"plugin_test.go"}, "plugin_test.go"},
		{"test sections are support", cfg, []string{"test/sections/146_gate_review_missing.sh"}, ""},
		{"docs are support", cfg, []string{"docs/usage/a.md", "docs/reference/config-options.md"}, ""},
		{"frozen records are support", cfg, []string{"cmd/rota/testdata/frozen/a.json"}, ""},
		{"golden files are support", cfg, []string{"internal/cli/testdata/golden/a.txt"}, ""},
		{"other testdata drifts", cfg, []string{"internal/cli/testdata/other/a.txt"}, "internal/cli/testdata/other/a.txt"},
		{"supportPaths replaces the list", `{"ship":{"review":{"lightBelow":50,"supportPaths":["fixtures/**"]}}}`, []string{"docs/a.md", "fixtures/a.json"}, "docs/a.md"},
		{"empty supportPaths counts everything", `{"ship":{"review":{"lightBelow":50,"supportPaths":[]}}}`, []string{"docs/a.md"}, "docs/a.md"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, err := ReviewPolicyOf(decode(t, c.cfg))
			if err != nil {
				t.Fatal(err)
			}
			got := p.Decide(ReviewInput{Changed: 3, IssueBody: issue, Files: c.files})
			if c.drift == "" && got.Signal != SignalSize || c.drift != "" && (got.Signal != SignalScopeDrift || got.Why != "diff touches files the issue never named: "+c.drift) {
				t.Errorf("Decide = %+v, want drift %q", got, c.drift)
			}
		})
	}
}

func TestSupportPathsShape(t *testing.T) {
	for cfg, ok := range map[string]bool{
		`{"ship":{"review":{"supportPaths":["docs/**","a/*/b/**"]}}}`: true,
		`{"ship":{"review":{"supportPaths":[]}}}`:                     true,
		`{"ship":{"review":{"supportPaths":"docs"}}}`:                 false,
		`{"ship":{"review":{"supportPaths":[1]}}}`:                    false,
		`{"ship":{"review":{"supportPaths":[""]}}}`:                   false,
		`{"ship":{"review":{"supportPaths":["a/**/b"]}}}`:             false,
		`{"ship":{"review":{"supportPaths":["a/["]}}}`:                false,
	} {
		if _, err := ReviewPolicyOf(decode(t, cfg)); (err == nil) != ok {
			t.Errorf("%s: err = %v, want ok=%v", cfg, err, ok)
		}
	}
}

// TestScopeDriftIssue898 applies the rule to the final diff of #916 (issue
// #898, git diff 230f07cd...f94ad675) and pins what drifts.
func TestScopeDriftIssue898(t *testing.T) {
	issue := "## Touches\n\n- `ship.review`\n- `rota worker gate`\n- `/rota-review` Step 7\n\n## Pointers\n\n- skills/rota-review/SKILL.md (Step 7, Step 8)\n- internal/gate (review-missing)\n- internal/verdict (combined)\n"
	files := []string{
		"cmd/rota/testdata/frozen/config.jsonl", "docs/contributing/contract/verdicts.md",
		"docs/reference/config-options.md", "docs/usage/configuration.md", "docs/usage/parallel-rounds.md",
		"docs/usage/review-and-ship.md", "internal/cli/gate_test.go", "internal/cli/verdict_test.go",
		"internal/config/keys.go", "internal/config/reviewdepth.go", "internal/config/schema.go",
		"internal/verdict/verdict.go", "internal/verdict/verdict_test.go", "internal/worker/gate.go",
		"internal/worker/gate_test.go", "skills/rota-orchestrate/SKILL.md", "skills/rota-review/SKILL.md",
		"skills/rota-ship/SKILL.md", "skills/rota-ship/review-gate.md", "test/sections/146_gate_review_missing.sh",
	}
	want := []string{
		"internal/cli/gate_test.go", "internal/cli/verdict_test.go", "internal/config/keys.go",
		"internal/config/reviewdepth.go", "internal/config/schema.go", "internal/worker/gate.go",
		"internal/worker/gate_test.go", "skills/rota-orchestrate/SKILL.md", "skills/rota-ship/SKILL.md",
		"skills/rota-ship/review-gate.md",
	}
	got := scopeDrift(issue, files, DefaultSupportPaths)
	if !slices.Equal(got, want) {
		t.Errorf("drift = %v, want %v", got, want)
	}
}
