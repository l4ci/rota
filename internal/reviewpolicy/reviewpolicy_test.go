package reviewpolicy

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/jsonx"
)

func decode(t *testing.T, s string) any {
	t.Helper()
	v, err := jsonx.Decode([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestReviewPolicyResolve(t *testing.T) {
	obj := `{"ship":{"review":{"default":"full","lightBelow":50,"labels":{"risk:high":"full","best-of:2":"light","partial-slice":"none"}}}}`
	for _, c := range []struct {
		name    string
		cfg     string
		changed int
		labels  []string
		want    config.ReviewDepth
		whyHas  string
	}{
		{"unset is full", `{}`, 10, nil, config.DepthFull, "default"},
		{"legacy true", `{"ship":{"review":true}}`, 1, nil, config.DepthFull, "true"},
		{"legacy false", `{"ship":{"review":false}}`, 1000, nil, config.DepthNone, "false"},
		{"string light", `{"ship":{"review":"light"}}`, 1000, nil, config.DepthLight, "default"},
		{"string none", `{"ship":{"review":"none"}}`, 1, nil, config.DepthNone, "default"},
		{"small diff goes light", obj, 49, nil, config.DepthLight, "49 changed lines"},
		{"threshold is exclusive", obj, 50, nil, config.DepthFull, "default"},
		{"unknown size skips size rule", obj, -1, nil, config.DepthFull, "default"},
		{"best-of label", obj, 500, []string{"best-of:2"}, config.DepthLight, "best-of:2"},
		{"partial-slice label", obj, 500, []string{"partial-slice"}, config.DepthNone, "partial-slice"},
		{"label beats size", obj, 3, []string{"partial-slice"}, config.DepthNone, "partial-slice"},
		{"risk:high forces full over size", obj, 3, []string{"risk:high"}, config.DepthFull, "risk:high"},
		{"strictest label wins", obj, 3, []string{"partial-slice", "risk:high"}, config.DepthFull, "risk:high"},
		{"unrelated label ignored", obj, 500, []string{"p1"}, config.DepthFull, "default"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, err := config.ReviewPolicyOf(decode(t, c.cfg))
			if err != nil {
				t.Fatal(err)
			}
			d := Decide(p, Input{Changed: c.changed, Labels: c.labels})
			got, why := d.Depth, d.Why
			if got != c.want || !strings.Contains(why, c.whyHas) {
				t.Errorf("Resolve(%d, %v) = %s %q, want %s with %q in why", c.changed, c.labels, got, why, c.want, c.whyHas)
			}
		})
	}
}

func TestReviewPolicyDecide(t *testing.T) {
	const cfg = `{"ship":{"review":{"default":"full","lightBelow":50,"labels":{"partial-slice":"none","best-of:2":"light"},"paths":{"migrations/**":"full","docs/*":"light"}}}}`
	issue := "## Files\n\n- `internal/a/x.go`\n- docs/*\n\n## Touches\n\n- `internal/b/`\n- `ship.review`\n"
	oneWay := "summary\n\nDoor: one-way. Blast radius: the schema.\n"
	for _, c := range []struct {
		name   string
		cfg    string
		in     Input
		want   config.ReviewDepth
		signal string
		whyHas string
	}{
		{"size light", cfg, Input{Changed: 3}, config.DepthLight, SignalSize, "3 changed"},
		{"one-way door lifts a small diff", cfg, Input{Changed: 3, PRBody: oneWay}, config.DepthFull, SignalDoor, "one-way"},
		{"two-way door does not", cfg, Input{Changed: 3, PRBody: "Door: two-way."}, config.DepthLight, SignalSize, ""},
		{"door in prose does not", cfg, Input{Changed: 3, PRBody: "this is not a Door: one-way door"}, config.DepthLight, SignalSize, ""},
		{"label none beats the door", cfg, Input{Changed: 3, PRBody: oneWay, Labels: []string{"partial-slice"}}, config.DepthNone, SignalLabel, "partial-slice"},
		{"label light does not beat the door", cfg, Input{Changed: 3, PRBody: oneWay, Labels: []string{"best-of:2"}}, config.DepthFull, SignalDoor, ""},
		{"scope drift lifts and lists files", cfg, Input{Changed: 3, IssueBody: issue, Files: []string{"internal/a/x.go", "internal/c/y.go", "internal/b/z.go"}}, config.DepthFull, SignalScopeDrift, "internal/c/y.go"},
		{"declared files do not drift", cfg, Input{Changed: 3, IssueBody: issue, Files: []string{"internal/a/x.go", "internal/b/z.go", "docs/a.md"}}, config.DepthLight, SignalSize, ""},
		{"issue without a file set cannot drift", cfg, Input{Changed: 3, IssueBody: "## Summary\n\nx", Files: []string{"anything.go"}}, config.DepthLight, SignalSize, ""},
		{"path lifts", cfg, Input{Changed: 3, Files: []string{"migrations/001.sql"}}, config.DepthFull, SignalPath, "migrations/001.sql"},
		{"trailing /** matches nested files", cfg, Input{Changed: 3, Files: []string{"migrations/2026/001.sql"}}, config.DepthFull, SignalPath, "migrations/2026"},
		{"path never lowers", cfg, Input{Changed: 500, Files: []string{"docs/a.md"}}, config.DepthFull, SignalDefault, ""},
		{"label none is final over a path", cfg, Input{Changed: 3, Labels: []string{"partial-slice"}, Files: []string{"migrations/001.sql"}}, config.DepthNone, SignalLabel, ""},
		{"door lifts a default of none", `{"ship":{"review":"none"}}`, Input{PRBody: oneWay}, config.DepthFull, SignalDoor, ""},
		{"drift lifts a default of none", `{"ship":{"review":"none"}}`, Input{IssueBody: issue, Files: []string{"z.go"}}, config.DepthFull, SignalScopeDrift, "z.go"},
		{"pointers count as declared files", cfg, Input{Changed: 3, IssueBody: "## Pointers\n\n- internal/config/reviewdepth.go (Resolve, ParseReviewPolicy)\n- docs/usage/a.md, docs/reference/b.md\n", Files: []string{"internal/config/reviewdepth.go", "docs/reference/b.md"}}, config.DepthLight, SignalSize, ""},
		{"pointers line mixing a backticked non-path and a bare path", cfg, Input{Changed: 3, IssueBody: "## Pointers\n\n- skills/references/worker-contract.md (the `Door:` line)\n- `ship.review` and internal/config/reviewdepth.go\n- docs/usage/a.md\n", Files: []string{"skills/references/worker-contract.md", "internal/config/reviewdepth.go"}}, config.DepthLight, SignalSize, ""},
		{"pointers declare a set to drift from", cfg, Input{Changed: 3, IssueBody: "## Pointers\n\n- internal/config/reviewdepth.go\n", Files: []string{"internal/cli/review.go"}}, config.DepthFull, SignalScopeDrift, "internal/cli/review.go"},
		{"declared dir/** covers nested files", cfg, Input{Changed: 3, IssueBody: "## Files\n\n- internal/cli/**\n", Files: []string{"internal/cli/a/b.go"}}, config.DepthLight, SignalSize, ""},
		{"scopeDrift false turns the rule off", `{"ship":{"review":{"default":"full","lightBelow":50,"scopeDrift":false}}}`, Input{Changed: 3, IssueBody: issue, Files: []string{"z.go"}}, config.DepthLight, SignalSize, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, err := config.ReviewPolicyOf(decode(t, c.cfg))
			if err != nil {
				t.Fatal(err)
			}
			got := Decide(p, c.in)
			if got.Depth != c.want || got.Signal != c.signal || !strings.Contains(got.Why, c.whyHas) {
				t.Errorf("Decide = %+v, want %s via %s with %q in why", got, c.want, c.signal, c.whyHas)
			}
		})
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
			p, err := config.ReviewPolicyOf(decode(t, c.cfg))
			if err != nil {
				t.Fatal(err)
			}
			got := Decide(p, Input{Changed: 3, IssueBody: issue, Files: c.files})
			if c.drift == "" && got.Signal != SignalSize || c.drift != "" && (got.Signal != SignalScopeDrift || got.Why != "diff touches files the issue never named: "+c.drift) {
				t.Errorf("Decide = %+v, want drift %q", got, c.drift)
			}
		})
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
	p, err := config.ReviewPolicyOf(decode(t, `{"ship":{"review":{"default":"full","lightBelow":50}}}`))
	if err != nil {
		t.Fatal(err)
	}
	d := Decide(p, Input{Changed: 3, IssueBody: issue, Files: files})
	wantWhy := "diff touches files the issue never named: " + strings.Join(want, ", ")
	if d.Signal != SignalScopeDrift || d.Why != wantWhy {
		t.Errorf("Decide = %+v, want drift %s", d, wantWhy)
	}
}

func TestLensesMatch(t *testing.T) {
	l, err := config.LensesOf(decode(t, `{"review":{"lenses":{
		"ux":{"paths":["internal/cli/**"]},
		"security":{"paths":["internal/git/**"],"labels":["risk:security"]}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	got := MatchLenses(l, []string{"README.md", "internal/cli/review.go", "internal/git/run.go"}, []string{"risk:security", "p1"})
	want := []config.LensHit{
		{Name: "security", Why: "path internal/git/run.go"},
		{Name: "ux", Why: "path internal/cli/review.go"},
	}
	if len(got) != len(want) {
		t.Fatalf("hits = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("hit %d = %v, want %v", i, got[i], want[i])
		}
	}
	// a label alone triggers when no path does
	got = MatchLenses(l, []string{"README.md"}, []string{"risk:security"})
	if len(got) != 1 || got[0] != (config.LensHit{Name: "security", Why: "label risk:security"}) {
		t.Errorf("label-only hits = %v", got)
	}
	if got = MatchLenses(l, []string{"README.md"}, nil); len(got) != 0 {
		t.Errorf("no match hits = %v", got)
	}
}

// rota's own .rota/config.json enables the security lens for the shell-out
// and file-write code, so a diff touching those files triggers it (#2064).
func TestOwnConfigTriggersSecurityLens(t *testing.T) {
	raw, err := os.ReadFile("../../.rota/config.json")
	if err != nil {
		t.Fatal(err)
	}
	l, err := config.LensesOf(decode(t, string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{
		"internal/cli/browser.go", "internal/cli/hook.go", "internal/cli/keepalive.go", "internal/cli/testlock.go",
		"internal/tui/term.go", "internal/git/git.go", "internal/worker/registry.go",
	} {
		if hits := MatchLenses(l, []string{f}, nil); !slices.ContainsFunc(hits, func(h config.LensHit) bool { return h.Name == "security" }) {
			t.Errorf("%s does not trigger the security lens: %v", f, hits)
		}
	}
	// file-write packages get the risk:security label instead of a glob
	for _, f := range []string{"internal/round/retarget.go", "internal/hvmigrate/x.go", "internal/cli/round_assign.go"} {
		if hits := MatchLenses(l, []string{f}, nil); slices.ContainsFunc(hits, func(h config.LensHit) bool { return h.Name == "security" }) {
			t.Errorf("%s triggers the security lens: %v", f, hits)
		}
	}
	has := func(f, lens string) bool {
		return slices.ContainsFunc(MatchLenses(l, []string{f}, nil), func(h config.LensHit) bool { return h.Name == lens })
	}
	if !has("internal/cli/exit.go", "ux") || !has("docs/usage/x.md", "ux") || has("internal/cli/round_assign.go", "ux") {
		t.Error("ux lens: exit.go and docs/usage must trigger it, round_assign.go must not")
	}
	if hits := MatchLenses(l, []string{"README.md"}, []string{"risk:ux"}); len(hits) != 1 || hits[0].Name != "ux" {
		t.Errorf("label risk:ux: %v", hits)
	}
	if hits := MatchLenses(l, []string{"README.md"}, []string{"risk:security"}); len(hits) != 1 || hits[0].Name != "security" {
		t.Errorf("label risk:security: %v", hits)
	}
}

// TestPathEntrySemantics pins how configured paths, declared files and support
// globs match a changed file.
func TestPathEntrySemantics(t *testing.T) {
	const cfg = `{"ship":{"review":{"default":"full","lightBelow":50,"paths":{"internal/*/gate.go":"full","docs/*":"full","skills":"full","internal/*/golden/**":"full"}}}}`
	p, err := config.ReviewPolicyOf(decode(t, cfg))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		file string
		want bool
	}{
		{"internal/cli/gate.go", true},
		{"docs/a.md", true},
		{"docs/a/b.md", false},
		{"skills/x/SKILL.md", true},
		{"internal/cli/golden", true},
		{"other.go", false},
	} {
		got := Decide(p, Input{Changed: 3, Files: []string{c.file}})
		if hit := got.Signal == SignalPath; hit != c.want {
			t.Errorf("paths match %q = %v, want %v (%+v)", c.file, hit, c.want, got)
		}
	}
	// declared entries: bare dir, ./ prefix, glob; support matches any ancestor
	p, err = config.ReviewPolicyOf(decode(t, `{"ship":{"review":{"default":"full","lightBelow":50,"supportPaths":["internal/*/testdata/golden/**"]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	issue := "## Files\n\n- ./internal/a\n- docs/*.md\n"
	for _, c := range []struct {
		file  string
		drift bool
	}{
		{"internal/a/x/y.go", false},
		{"docs/a.md", false},
		{"docs/sub/a.md", true},
		{"internal/b/x.go", true},
		{"internal/q/testdata/golden/a/b.txt", false},
	} {
		got := Decide(p, Input{Changed: 3, IssueBody: issue, Files: []string{c.file}})
		if drift := got.Signal == SignalScopeDrift; drift != c.drift {
			t.Errorf("drift %q = %v, want %v (%+v)", c.file, drift, c.drift, got)
		}
	}
}
