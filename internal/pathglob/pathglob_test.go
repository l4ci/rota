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
	for _, c := range []struct {
		entry, file string
		want        bool
	}{
		{"rota-release", "rota-release/SKILL.md", true},
		{"rota-release/", "rota-release/SKILL.md", true},
		{"./docs", "docs/a/b.md", true},
		{"docs", "docsx/a.md", false},
		{"go.mod", "go.mod", true},
		{"*.md", "README.md", true},
		{"*.md", "docs/README.md", false},
		{"internal/*/gate.go", "internal/cli/gate.go", true},
		{"docs/**", "docs/a/b.md", true},
		{"docs/**", "docs", true},
		{"internal/*/golden/**", "internal/x/golden/a.txt", true},
		{"", "x", false},
		{".", "x", false},
		{"[", "[", true}, // equal wins over a malformed glob
		{"[", "a", false},
	} {
		if got := MatchEntry(c.entry, c.file); got != c.want {
			t.Errorf("MatchEntry(%q, %q) = %v, want %v", c.entry, c.file, got, c.want)
		}
	}
}

func TestMatchEntryFlat(t *testing.T) {
	for _, c := range []struct {
		entry, file string
		want        bool
	}{
		{"rota-release/", "rota-release/SKILL.md", true},
		{"*.md", "docs/README.md", false},
		{"docs/**", "docs/a.md", true},
		{"docs/**", "docs/a/b.md", false}, // ** is one level here, not recursive
		{"[", "[", true},
		{"[", "a", false},
		{"", "x", false},
	} {
		if got := MatchEntryFlat(c.entry, c.file); got != c.want {
			t.Errorf("MatchEntryFlat(%q, %q) = %v, want %v", c.entry, c.file, got, c.want)
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
