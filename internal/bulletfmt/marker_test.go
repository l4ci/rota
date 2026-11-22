package bulletfmt

import (
	"os"
	"testing"
)

func TestCutMarker(t *testing.T) {
	cases := []struct {
		name, line, markers, rest string
		ok                        bool
	}{
		{"dash", "- a", DashStar, "a", true},
		{"star", "* a", DashStar, "a", true},
		{"plus refused by dash-star", "+ a", DashStar, "", false},
		{"plus accepted by any", "+ a", AnyMarker, "a", true},
		{"dash only refuses star", "* a", DashOnly, "", false},
		{"no space", "-a", DashStar, "", false},
		{"bare marker", "-", DashStar, "", false},
		{"indented is not trimmed", "  - a", DashStar, "", false},
		{"prose", "text", AnyMarker, "", false},
		{"empty", "", AnyMarker, "", false},
		{"empty item", "- ", DashStar, "", true},
	}
	for _, c := range cases {
		rest, ok := CutMarker(c.line, c.markers)
		if rest != c.rest || ok != c.ok {
			t.Errorf("%s: CutMarker(%q, %q) = %q, %v; want %q, %v", c.name, c.line, c.markers, rest, ok, c.rest, c.ok)
		}
	}
}

func TestCount(t *testing.T) {
	text := "intro\n- **A** — one <!-- 2026-01-01 -->\n  - nested\n- **B** — wrapped\n  continued\n  - also nested\n\n- **C** — three\n"
	if got := Count(text); got != 3 {
		t.Errorf("Count = %d, want 3", got)
	}
	if got := Count(""); got != 0 {
		t.Errorf("Count(\"\") = %d, want 0", got)
	}
}

func TestCountSharedFixture(t *testing.T) {
	b, err := os.ReadFile("testdata/count.md")
	if err != nil {
		t.Fatal(err)
	}
	if got := Count(string(b)); got != 3 {
		t.Errorf("Count(fixture) = %d, want 3", got)
	}
}
