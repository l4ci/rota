package bulletfmt

import "testing"

func TestTitle(t *testing.T) {
	for _, c := range []struct {
		line, want string
		ok         bool
	}{
		{"- **A title** — body", "A title", true},
		{"  - **Indented** — body", "Indented", true},
		{"- **No body**", "No body", true},
		{"- plain bullet", "", false},
		{"**not a bullet**", "", false},
		{"- ****", "", false},
	} {
		got, ok := Title(c.line)
		if got != c.want || ok != c.ok {
			t.Errorf("Title(%q) = %q, %v; want %q, %v", c.line, got, ok, c.want, c.ok)
		}
	}
}

func TestRender(t *testing.T) {
	if got, want := Render("T", "body", "2026-01-02 sha:abc1234"), "- **T** — body <!-- 2026-01-02 sha:abc1234 -->"; got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

func TestHasTitle(t *testing.T) {
	lines := []string{"prose", "- **Alpha** — x <!-- 2026-01-02 -->", "- plain"}
	if !HasTitle(lines, "alpha") || HasTitle(lines, "beta") || HasTitle(lines, "plain") {
		t.Error("HasTitle must match a bold title, ignoring case, and nothing else")
	}
}

func TestEnd(t *testing.T) {
	lines := []string{"- **A** — x", "  more", "\tand more", "", "  after blank", "- **B**", "prose"}
	for i, want := range map[int]int{0: 3, 5: 6, 6: 7} {
		if got := End(lines, i); got != want {
			t.Errorf("End(%d) = %d, want %d", i, got, want)
		}
	}
}
