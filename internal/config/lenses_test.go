package config

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestParseLenses(t *testing.T) {
	for _, c := range []struct {
		name, cfg, errHas string
	}{
		{"unset is empty", `{}`, ""},
		{"empty object", `{"review":{"lenses":{}}}`, ""},
		{"paths and labels", `{"review":{"lenses":{"security":{"paths":["internal/git/**","cmd/*.go"],"labels":["risk:security"]}}}}`, ""},
		{"unknown lens", `{"review":{"lenses":{"style":{"paths":["a"]}}}}`, `review.lenses: unknown lens "style"`},
		{"unknown field", `{"review":{"lenses":{"ux":{"globs":["a"]}}}}`, `review.lenses.ux: unknown field "globs"`},
		{"not an object", `{"review":{"lenses":["security"]}}`, "review.lenses must be an object"},
		{"entry not an object", `{"review":{"lenses":{"perf":"x"}}}`, "review.lenses.perf must be an object"},
		{"paths not a list", `{"review":{"lenses":{"perf":{"paths":"a"}}}}`, "review.lenses.perf.paths must be a list of globs"},
		{"labels not strings", `{"review":{"lenses":{"perf":{"labels":[1]}}}}`, "review.lenses.perf.labels must be a list of strings"},
		{"malformed glob", `{"review":{"lenses":{"ux":{"paths":["internal/[cli"]}}}}`, `review.lenses.ux.paths: glob "internal/[cli" is not supported`},
		{"double star mid glob", `{"review":{"lenses":{"ux":{"paths":["a/**/b"]}}}}`, `review.lenses.ux.paths: glob "a/**/b" is not supported`},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := LensesOf(decode(t, c.cfg))
			switch {
			case c.errHas == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case c.errHas != "" && (err == nil || !strings.Contains(err.Error(), c.errHas)):
				t.Fatalf("error = %v, want it to contain %q", err, c.errHas)
			}
		})
	}
}

func TestLensesMatch(t *testing.T) {
	l, err := LensesOf(decode(t, `{"review":{"lenses":{
		"ux":{"paths":["internal/cli/**"]},
		"security":{"paths":["internal/git/**"],"labels":["risk:security"]}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	got := l.Match([]string{"README.md", "internal/cli/review.go", "internal/git/run.go"}, []string{"risk:security", "p1"})
	want := []LensHit{
		{"security", "path internal/git/run.go"},
		{"ux", "path internal/cli/review.go"},
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
	got = l.Match([]string{"README.md"}, []string{"risk:security"})
	if len(got) != 1 || got[0] != (LensHit{"security", "label risk:security"}) {
		t.Errorf("label-only hits = %v", got)
	}
	if got = l.Match([]string{"README.md"}, nil); len(got) != 0 {
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
	l, err := LensesOf(decode(t, string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{
		"internal/cli/browser.go", "internal/cli/hook.go", "internal/cli/keepalive.go", "internal/cli/testlock.go",
		"internal/tui/term.go", "internal/git/git.go", "internal/round/retarget.go", "internal/hvmigrate/x.go", "internal/worker/registry.go",
	} {
		if hits := l.Match([]string{f}, nil); !slices.ContainsFunc(hits, func(h LensHit) bool { return h.Name == "security" }) {
			t.Errorf("%s does not trigger the security lens: %v", f, hits)
		}
	}
	if hits := l.Match([]string{"docs/usage/x.md"}, nil); len(hits) != 0 {
		t.Errorf("docs trigger %v", hits)
	}
	if hits := l.Match([]string{"README.md"}, []string{"risk:security"}); len(hits) != 1 || hits[0].Name != "security" {
		t.Errorf("label risk:security: %v", hits)
	}
}
