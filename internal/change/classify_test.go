package change

import (
	"slices"
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	docs := Policy{DocsPaths: DefaultDocsPaths}
	cases := []struct {
		name   string
		in     Input
		p      Policy
		tier   Tier
		files  []string // the deciding files
		reason string   // a substring of the reason
	}{
		{"root README", Input{Files: []string{"README.md"}, Lines: 4}, docs, TierDocs, []string{"README.md"}, "docs-only"},
		{"README and docs page", Input{Files: []string{"README.md", "docs/a.md"}, Lines: 9}, docs, TierDocs, []string{"README.md", "docs/a.md"}, "docs-only"},
		{"images anywhere", Input{Files: []string{"a.png", "docs/images/b.png", "skills/x/c.svg"}}, docs, TierDocs, []string{"a.png", "docs/images/b.png", "skills/x/c.svg"}, "docs-only"},
		{"skill markdown is not root markdown", Input{Files: []string{"skills/x/SKILL.md"}}, docs, TierFull, []string{"skills/x/SKILL.md"}, "not documentation"},
		{"code beside docs", Input{Files: []string{"a.md", "x.go"}, Lines: 3}, docs, TierFull, []string{"x.go"}, "not documentation"},
		{"risky docs path", Input{Files: []string{"docs/a.md", "docs/secret/b.md"}}, Policy{DocsPaths: DefaultDocsPaths, RiskyPaths: []string{"docs/secret/**"}}, TierFull, []string{"docs/secret/b.md"}, "change.riskyPaths"},
		{"one-way door", Input{Files: []string{"README.md"}, Door: true}, docs, TierFull, nil, "Door: one-way"},
		{"no files", Input{}, docs, TierFull, nil, "no changed files"},
		{"small off", Input{Files: []string{"x.go"}, Lines: 5}, docs, TierFull, []string{"x.go"}, "not documentation"},
		{"small under", Input{Files: []string{"x.go"}, Lines: 99}, Policy{DocsPaths: DefaultDocsPaths, SmallBelow: 100, SmallPaths: []string{"**"}}, TierSmall, []string{"x.go"}, "99 changed lines, under change.smallBelow 100"},
		{"small at threshold", Input{Files: []string{"x.go"}, Lines: 100}, Policy{DocsPaths: DefaultDocsPaths, SmallBelow: 100, SmallPaths: []string{"**"}}, TierFull, []string{"x.go"}, "100 changed lines"},
		{"unknown line count", Input{Files: []string{"x.go"}, Lines: -1}, Policy{DocsPaths: DefaultDocsPaths, SmallBelow: 100, SmallPaths: []string{"**"}}, TierFull, []string{"x.go"}, "not documentation"},
		{"rename out of code", Input{Files: []string{"internal/x.go", "moved/out.md"}}, Policy{DocsPaths: []string{"**/*.md"}}, TierFull, []string{"internal/x.go"}, "not documentation"},
		{"risky beats small", Input{Files: []string{"go.mod"}, Lines: 1}, Policy{DocsPaths: DefaultDocsPaths, RiskyPaths: []string{"go.mod"}, SmallBelow: 100, SmallPaths: []string{"**"}}, TierFull, []string{"go.mod"}, "change.riskyPaths"},
		{"no docs paths", Input{Files: []string{"README.md"}}, Policy{}, TierFull, []string{"README.md"}, "not documentation"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Classify(c.in, c.p)
			if got.Tier != c.tier {
				t.Fatalf("%v: got %s (%s), want %s", c.in.Files, got.Tier, got.Reason, c.tier)
			}
			if !slices.Equal(got.Files, c.files) {
				t.Errorf("deciding files = %v, want %v", got.Files, c.files)
			}
			if !strings.Contains(got.Reason, c.reason) {
				t.Errorf("reason %q lacks %q", got.Reason, c.reason)
			}
		})
	}
}

// A docs glob alone never makes a file documentation: only Markdown and image
// files are, wherever they sit (#2348 review).
func TestDocsTierAdmitsOnlyDocFileTypes(t *testing.T) {
	p := Policy{DocsPaths: DefaultDocsPaths, SmallBelow: 100, SmallPaths: []string{"**"}}
	for _, f := range []string{"docs/x.go", "docs/build.sh", "docs/Makefile", "docs/a.md.sh", "docs/data.json"} {
		got := Classify(Input{Files: []string{f}, Lines: 500}, p)
		if got.Tier != TierFull {
			t.Errorf("%s: tier %s (%s), want full", f, got.Tier, got.Reason)
		}
	}
	if got := Classify(Input{Files: []string{"docs/a.MD", "docs/b/c.PNG"}}, p); got.Tier != TierDocs {
		t.Errorf("upper-case doc extensions: tier %s (%s), want docs", got.Tier, got.Reason)
	}
}

// The small tier runs test.small, which only covers what change.smallPaths
// names; any other file in the diff makes it full.
func TestSmallTierNeedsEveryFileMapped(t *testing.T) {
	p := Policy{DocsPaths: DefaultDocsPaths, SmallBelow: 100, SmallPaths: []string{"internal/**/*.go", "test/sections/*.sh"}}
	cases := []struct {
		name  string
		files []string
		p     Policy
		tier  Tier
	}{
		{"all mapped", []string{"internal/a/b.go", "test/sections/1_x.sh"}, p, TierSmall},
		{"mapped beside unmapped", []string{"internal/a/b.go", "hooks/x.sh"}, p, TierFull},
		{"only unmapped", []string{"scripts/site-sync.py"}, p, TierFull},
		{"no smallPaths", []string{"internal/a/b.go"}, Policy{DocsPaths: DefaultDocsPaths, SmallBelow: 100}, TierFull},
	}
	for _, c := range cases {
		if got := Classify(Input{Files: c.files, Lines: 5}, c.p); got.Tier != c.tier {
			t.Errorf("%s: tier %s (%s), want %s", c.name, got.Tier, got.Reason, c.tier)
		}
	}
	got := Classify(Input{Files: []string{"internal/a/b.go", "hooks/x.sh"}, Lines: 5}, p)
	if !strings.Contains(got.Reason, "change.smallPaths") || !strings.Contains(got.Reason, "hooks/x.sh") {
		t.Errorf("reason %q should name change.smallPaths and the unmapped file", got.Reason)
	}
}

func TestRiskyResultIsFlagged(t *testing.T) {
	p := Policy{DocsPaths: DefaultDocsPaths, RiskyPaths: []string{"internal/**"}}
	if got := Classify(Input{Files: []string{"internal/x.go"}}, p); !got.Risky {
		t.Errorf("a risky match must set Result.Risky")
	}
	if got := Classify(Input{Files: []string{"x.go"}}, p); got.Risky {
		t.Errorf("no risky match must leave Result.Risky false")
	}
}

func TestMatch(t *testing.T) {
	cases := []struct {
		g, f string
		want bool
	}{
		{"*.md", "README.md", true},
		{"*.md", "docs/a.md", false},
		{"docs/**", "docs/a/b/c.md", true},
		{"docs/**", "docs", true},
		{"docs/**", "docsx/a.md", false},
		{"**/*.png", "a.png", true},
		{"**/*.png", "x/y/a.png", true},
		{"**/*.png", "a.png.go", false},
		{"internal/worker/gate*.go", "internal/worker/gate_test.go", true},
	}
	for _, c := range cases {
		if got := Match(c.g, c.f); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.g, c.f, got, c.want)
		}
	}
}

func TestValidGlob(t *testing.T) {
	for _, g := range []string{"*.md", "docs/**", "**/*.png", "a/b?.go", "[ab].md"} {
		if err := ValidGlob(g); err != nil {
			t.Errorf("ValidGlob(%q) = %v, want nil", g, err)
		}
	}
	for _, g := range []string{"", " ", "/abs.md", "docs//a", "docs/**x", "{a,b}.md", "[a.md"} {
		if err := ValidGlob(g); err == nil {
			t.Errorf("ValidGlob(%q) = nil, want an error", g)
		}
	}
}

func TestHigher(t *testing.T) {
	for _, c := range []struct{ a, b, want Tier }{
		{TierDocs, TierDocs, TierDocs}, {TierDocs, TierSmall, TierSmall}, {TierSmall, TierDocs, TierSmall},
		{TierSmall, TierFull, TierFull}, {TierDocs, "", TierFull}, {"", TierDocs, TierFull}, {"bogus", TierSmall, TierFull},
	} {
		if got := Higher(c.a, c.b); got != c.want {
			t.Errorf("Higher(%q, %q) = %q, want %q", c.a, c.b, got, c.want)
		}
	}
}
