package pathglob

import "testing"

func TestMatch(t *testing.T) {
	for _, c := range []struct {
		g, f string
		want bool
	}{
		{"docs/**", "docs/a/b.md", true},
		{"docs/**", "docs", true},
		{"*.md", "docs/a.md", false},
		{"**/*.go", "a/b/c.go", true},
		{"a/?.go", "a/b.go", true},
		{"*.md", "README.md", true},
		{"docs/**", "docsx/a.md", false},
		{"**/*.png", "a.png", true},
		{"**/*.png", "x/y/a.png", true},
		{"**/*.png", "a.png.go", false},
		{"internal/worker/gate*.go", "internal/worker/gate_test.go", true},
	} {
		if got := Match(c.g, c.f); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.g, c.f, got, c.want)
		}
	}
}

func TestMatchEntry(t *testing.T) {
	const (
		R = Recursive
		S = SingleLevel
	)
	for _, c := range []struct {
		f           Flavour
		entry, file string
		want        bool
	}{
		// entry rules, identical in both flavours
		{R, "rota-release", "rota-release/SKILL.md", true},
		{S, "rota-release", "rota-release/SKILL.md", true},
		{R, "rota-release/", "rota-release/SKILL.md", true},
		{S, "rota-release/", "rota-release/SKILL.md", true},
		{R, "./docs", "docs/a/b.md", true},
		{S, "./docs", "docs/a/b.md", true},
		{R, "docs", "docsx/a.md", false},
		{S, "docs", "docsx/a.md", false},
		{R, "go.mod", "go.mod", true},
		{S, "go.mod", "go.mod", true},
		{R, "*.md", "README.md", true},
		{S, "*.md", "README.md", true},
		{R, "*.md", "docs/README.md", false},
		{S, "*.md", "docs/README.md", false},
		{R, "internal/*/gate.go", "internal/cli/gate.go", true},
		{S, "internal/*/gate.go", "internal/cli/gate.go", true},
		{R, "", "x", false},
		{S, "", "x", false},
		{R, ".", "x", false},
		{S, ".", "x", false},
		{S, "./", "x", false},
		{R, "[", "[", true}, // equal wins over a malformed glob
		{S, "[", "[", true},
		{R, "[", "a", false},
		{S, "[", "a", false},
		// the flavours differ on **
		{R, "docs/**", "docs/a/b.md", true}, // mergeApprovalPaths: recursive
		{S, "docs/**", "docs/a/b.md", true}, // directory prefix still covers it
		{R, "internal/**/x.go", "internal/a/b/x.go", true},
		{S, "internal/**/x.go", "internal/a/b/x.go", false}, // sharedPaths: one level
		{S, "internal/**/x.go", "internal/a/x.go", true},
		{R, "internal/*/golden/**", "internal/x/golden/a.txt", true},
		{S, "internal/*/golden/**", "internal/x/golden/a.txt", true},
		{R, "internal/*/golden/**", "internal/x/golden/a/b.txt", true},
		{S, "internal/*/golden/**", "internal/x/golden/a/b.txt", false},
		{R, "docs/**", "docs", true},
	} {
		if got := MatchEntry(c.f, c.entry, c.file); got != c.want {
			t.Errorf("MatchEntry(%v, %q, %q) = %v, want %v", c.f, c.entry, c.file, got, c.want)
		}
	}
}

func TestMatchUnder(t *testing.T) {
	for _, c := range []struct {
		glob, file string
		want       bool
	}{
		{"internal/*/testdata/golden/**", "internal/x/testdata/golden/a/b.txt", true},
		{"internal/*/testdata/golden/**", "internal/x/testdata/other/a.txt", false},
		{"docs/**", "docs/a.md", true},
		{"*.md", "README.md", true},
		{"*.md", "docs/a.md", false},
		{"test/sections/**", "test/sections/1_a.sh", true},
		{"docs", "docs/a.md", true},
		{"internal/*/testdata", "internal/x/testdata/a.go", true},
		{"internal/*/testdata", "internal/x/other/a.go", false},
	} {
		if got := MatchUnder(c.glob, c.file); got != c.want {
			t.Errorf("MatchUnder(%q, %q) = %v, want %v", c.glob, c.file, got, c.want)
		}
	}
}

func TestValidEntry(t *testing.T) {
	for g, want := range map[string]bool{
		"docs/**": true, "internal/*/x": true, "*.md": true,
		"**/x": false, "a/**/b": false, "[": false, "docs/**/**": false,
	} {
		if got := ValidEntry(g); got != want {
			t.Errorf("ValidEntry(%q) = %v, want %v", g, got, want)
		}
	}
}
