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
	} {
		if got := Match(c.g, c.f); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.g, c.f, got, c.want)
		}
	}
}
