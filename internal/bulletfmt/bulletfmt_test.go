package bulletfmt

import (
	"errors"
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

func TestEnd(t *testing.T) {
	lines := []string{"- **A** — x", "  more", "\tand more", "", "  after blank", "- **B**", "prose"}
	for i, want := range map[int]int{0: 3, 5: 6, 6: 7} {
		if got := End(lines, i); got != want {
			t.Errorf("End(%d) = %d, want %d", i, got, want)
		}
	}
}

func TestResolveTitle(t *testing.T) {
	titles := []string{"Alpha", "beta", "ALPHA", "", "Gamma"}
	cases := []struct {
		name, want string
		idx        int
		err        error
		named      []string
	}{
		{"exact case", "Gamma", 4, nil, nil},
		{"other case", "gamma", 4, nil, nil},
		{"padded", "  GAMMA ", 4, nil, nil},
		{"case-only twins refuse and name both", "alpha", -1, ErrMultiMatch, []string{"'Alpha'", "'ALPHA'"}},
		{"none", "delta", -1, ErrNotFound, nil},
		{"blank never matches an untitled entry", "", -1, ErrNotFound, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			idx, err := ResolveTitle(titles, c.want)
			if !errors.Is(err, c.err) || (c.err == nil) != (err == nil) {
				t.Fatalf("ResolveTitle(%q) err = %v, want %v", c.want, err, c.err)
			}
			if idx != c.idx {
				t.Fatalf("ResolveTitle(%q) = %d, want %d", c.want, idx, c.idx)
			}
			if err == nil {
				return
			}
			if !strings.Contains(err.Error(), "'"+strings.TrimSpace(c.want)+"'") {
				t.Errorf("error %q does not name the wanted title", err)
			}
			for _, n := range c.named {
				if !strings.Contains(err.Error(), n) {
					t.Errorf("error %q does not name hit %s", err, n)
				}
			}
			if errors.Is(err, ErrNotFound) && errors.Is(err, ErrMultiMatch) {
				t.Errorf("error is both classes")
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
			a := lineStart(lines, i)
			want := lineStart(lines, End(lines, i))
			if got := EndOffset(text, a); got != want {
				t.Errorf("EndOffset(%q, %d) = %d, want %d", text, a, got, want)
			}
		}
	}
}

func TestCheckLine(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		bad      bool
	}{
		{"plain", "one line", false},
		{"empty", "", false},
		{"newline", "a\nb", true},
		{"carriage return", "a\rb", true},
		{"stamp closer", "x -->", true},
	} {
		err := CheckLine("text", tc.in)
		if tc.bad && !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", tc.name, err)
		}
		if !tc.bad && err != nil {
			t.Errorf("%s: err = %v, want nil", tc.name, err)
		}
	}
}

func TestCheckMarker(t *testing.T) {
	if err := CheckMarker("- **T** — b"); err != nil {
		t.Errorf("marked line: %v", err)
	}
	for _, in := range []string{"**T** — b", "", "-**T**"} {
		if err := CheckMarker(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("CheckMarker(%q) = %v, want ErrInvalid", in, err)
		}
	}
}

func TestBullets(t *testing.T) {
	text := "intro\n- **One** a\n  - **Nested** not a bullet start\n- plain\n- **A*B** star\n- **Two** b\n"
	got := Bullets(text)
	want := []Bullet{{Start: strings.Index(text, "- **One**"), Title: "One"}, {Start: strings.Index(text, "- **Two**"), Title: "Two"}}
	if len(got) != len(want) {
		t.Fatalf("Bullets = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Bullets[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSpans(t *testing.T) {
	lines := []string{"# H", "", "- **A** — x", "  more", "", "- plain", "\tcont", "prose", "- **B**"}
	want := []Span{{Start: 2, End: 4, Title: "A"}, {Start: 5, End: 7}, {Start: 8, End: 9, Title: "B"}}
	got := Spans(lines)
	if len(got) != len(want) {
		t.Fatalf("Spans = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Spans[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}
