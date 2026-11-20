package bulletfmt

import (
	"strings"
	"testing"
)

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

func TestSelectTitle(t *testing.T) {
	titles := []string{"Alpha", "beta", "ALPHA", "", "Gamma"}
	cases := []struct {
		name, want string
		hits       []int
	}{
		{"exact case", "Gamma", []int{4}},
		{"other case", "gamma", []int{4}},
		{"padded", "  GAMMA ", []int{4}},
		{"case-only twins both hit", "alpha", []int{0, 2}},
		{"none", "delta", nil},
		{"blank never matches an untitled entry", "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SelectTitle(titles, c.want)
			if len(got) != len(c.hits) {
				t.Fatalf("SelectTitle(%q) = %v, want %v", c.want, got, c.hits)
			}
			for i := range got {
				if got[i] != c.hits[i] {
					t.Fatalf("SelectTitle(%q) = %v, want %v", c.want, got, c.hits)
				}
			}
		})
	}
}

func TestSelectFragment(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		frag  string
		want  []int
	}{
		{"title and body", []string{"- **A** one <!-- 2026-01-01 -->", "- **B** two"}, "two", []int{1}},
		{"case-sensitive", []string{"- **A** one"}, "ONE", nil},
		{"stamp is not searched", []string{"- **A** one <!-- 2026-01-01 -->"}, "2026", nil},
		{"only the last comment is the stamp", []string{"- **A** one <!-- x --> <!-- 2026-01-01 -->"}, "x", []int{0}},
		{"continuation text belongs to its bullet", []string{"- **A** one", "  more", "- **B** two"}, "more", []int{0}},
		{"indented dash line is not a bullet", []string{"- **A** one", "  - nested", "- **B** two"}, "nested", []int{0}},
		{"unindented prose is not a bullet", []string{"- **A** one", "", "prose here"}, "prose", nil},
		{"several", []string{"- **A** x", "- **B** x"}, "x", []int{0, 1}},
	}
	for _, c := range cases {
		got := SelectFragment(c.lines, c.frag)
		if len(got) != len(c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: got %v want %v", c.name, got, c.want)
			}
		}
	}
}

func TestReplaceInKeepsTheStamp(t *testing.T) {
	got := ReplaceIn([]string{"- **A** one 2026 <!-- 2026-01-01 -->", "  one 2026"}, "2026", "Y")
	if got[0] != "- **A** one Y <!-- 2026-01-01 -->" || got[1] != "  one Y" {
		t.Errorf("got %q", got)
	}
}

func TestDeleteBullet(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		i     int
		want  []string
	}{
		{"blank both sides collapses", []string{"a", "", "- x", "", "b"}, 2, []string{"a", "", "b"}},
		{"wrapped bullet goes whole", []string{"a", "", "- x", "  wrap", "", "b"}, 2, []string{"a", "", "b"}},
		{"blank only before stays", []string{"a", "", "- x", "b"}, 2, []string{"a", "", "b"}},
		{"blank only after stays", []string{"a", "- x", "", "b"}, 1, []string{"a", "", "b"}},
		{"no final newline", []string{"- a", "- x"}, 1, []string{"- a", ""}},
		{"first line", []string{"- x", "- a"}, 0, []string{"- a"}},
	}
	for _, c := range cases {
		got := DeleteBullet(append([]string(nil), c.lines...), c.i)
		if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		name, line string
		want       Parts
	}{
		{"dated", "- **T** — body <!-- 2026-01-02 sha:abc1234 -->",
			Parts{"T", "body", " <!-- 2026-01-02 sha:abc1234 -->", true, true}},
		{"padded title", "- ** T ** — body <!-- 2026-01-02 -->",
			Parts{"T", "body", " <!-- 2026-01-02 -->", true, true}},
		{"loose dash", "- **T** -- body",
			Parts{"T", "-- body", "", true, false}},
		{"no dash, no stamp", "- **T** body",
			Parts{"T", "body", "", true, false}},
		{"untitled", "- plain text <!-- 2026-01-02 -->",
			Parts{"", "plain text", " <!-- 2026-01-02 -->", false, false}},
		{"empty body, one space before stamp", "- **T** — <!-- 2026-01-02 -->",
			Parts{"T", "", " <!-- 2026-01-02 -->", true, true}},
		{"empty body, double space before stamp", "- **T** —  <!-- 2026-01-02 -->",
			Parts{"T", "", "  <!-- 2026-01-02 -->", true, true}},
		{"earlier comment stays in the body", "- **T** — a <!-- x --> b <!-- 2026-01-02 -->",
			Parts{"T", "a <!-- x --> b", " <!-- 2026-01-02 -->", true, true}},
	}
	for _, c := range cases {
		if got := Parse(c.line); got != c.want {
			t.Errorf("%s: Parse(%q) = %+v, want %+v", c.name, c.line, got, c.want)
		}
	}
}

func TestStampFieldsAndIsDate(t *testing.T) {
	got := StampFields(" <!-- 2026-01-02 source:x seen:go@1.22 -->")
	if len(got) != 3 || got[0] != "2026-01-02" || got[2] != "seen:go@1.22" {
		t.Fatalf("StampFields = %q", got)
	}
	if len(StampFields("")) != 0 {
		t.Fatal("an empty stamp has no fields")
	}
	if !IsDate("2026-01-02") || IsDate("2026-1-2") || IsDate("x2026-01-02") {
		t.Fatal("IsDate must accept only YYYY-MM-DD")
	}
}

func TestEndOffsetAgreesWithEnd(t *testing.T) {
	for _, text := range []string{
		"- **A** — x\n  more\n\tand more\n\n  after blank\n- **B**\nprose\n",
		"- **A** — x\n  more",
		"- **A** — x\n",
		"- **A** — x",
	} {
		lines := strings.Split(text, "\n")
		for i := range lines {
			a := LineStart(lines, i)
			want := LineStart(lines, End(lines, i))
			if got := EndOffset(text, a); got != want {
				t.Errorf("EndOffset(%q, %d) = %d, want %d", text, a, got, want)
			}
		}
	}
}
