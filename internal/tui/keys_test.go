package tui

import (
	"os"
	"reflect"
	"testing"
)

func TestDecodeKeys(t *testing.T) {
	k := func(kind KeyKind) Key { return Key{Kind: kind} }
	cases := []struct {
		name string
		in   string
		want []Key
	}{
		{"up CSI", "\x1b[A", []Key{k(KeyUp)}},
		{"down CSI", "\x1b[B", []Key{k(KeyDown)}},
		{"right CSI", "\x1b[C", []Key{k(KeyRight)}},
		{"left CSI", "\x1b[D", []Key{k(KeyLeft)}},
		{"up SS3", "\x1bOA", []Key{k(KeyUp)}},
		{"down SS3", "\x1bOB", []Key{k(KeyDown)}},
		{"right SS3", "\x1bOC", []Key{k(KeyRight)}},
		{"left SS3", "\x1bOD", []Key{k(KeyLeft)}},
		{"page up", "\x1b[5~", []Key{k(KeyPgUp)}},
		{"page down", "\x1b[6~", []Key{k(KeyPgDn)}},
		{"enter CR", "\r", []Key{k(KeyEnter)}},
		{"enter LF", "\n", []Key{k(KeyEnter)}},
		{"backspace DEL", "\x7f", []Key{k(KeyBackspace)}},
		{"backspace BS", "\x08", []Key{k(KeyBackspace)}},
		{"ctrl-c", "\x03", []Key{k(KeyCtrlC)}},
		{"lone esc", "\x1b", []Key{k(KeyEsc)}},
		{"esc at chunk end", "a\x1b", []Key{Rune('a'), k(KeyEsc)}},
		{"esc before a plain key", "\x1bx", []Key{k(KeyEsc), Rune('x')}},
		{"delete dropped", "\x1b[3~", nil},
		{"F5 dropped, no digits leak", "\x1b[15~", nil},
		{"F1 SS3 dropped, no P leaks", "\x1bOP", nil},
		{"modified arrow dropped", "\x1b[1;5A", nil},
		{"truncated CSI dropped", "\x1b[", nil},
		{"rune", "q", []Key{Rune('q')}},
		{"utf8 rune", "é日", []Key{Rune('é'), Rune('日')}},
		{"control bytes dropped", "\x00\x01\x02\x1f", nil},
		{"invalid utf8 dropped", "\xff", nil},
		{"mixed", "a\x1b[Ab\r", []Key{Rune('a'), k(KeyUp), Rune('b'), k(KeyEnter)}},
		{"empty", "", nil},
	}
	for _, tc := range cases {
		if got := DecodeKeys([]byte(tc.in)); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: DecodeKeys(%q) = %v, want %v", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestKeyIs(t *testing.T) {
	if !Rune('x').Is('x') || Rune('x').Is('y') || (Key{Kind: KeyEnter}).Is('x') {
		t.Error("Key.Is")
	}
}

func TestStyleStripRemovesWhatStyleAdds(t *testing.T) {
	on, off := Style{Color: true}, Style{}
	fns := map[string]func(Style, string) string{
		"Bold":   Style.Bold,
		"Dim":    Style.Dim,
		"Red":    Style.Red,
		"Green":  Style.Green,
		"Yellow": Style.Yellow,
		"Cyan":   Style.Cyan,
	}
	for name, f := range fns {
		if got := f(on, "hi"); got == "hi" || Strip(got) != "hi" {
			t.Errorf("%s on: %q strips to %q", name, got, Strip(got))
		}
		if got := f(off, "hi"); got != "hi" {
			t.Errorf("%s off: %q", name, got)
		}
		if got := f(on, ""); got != "" {
			t.Errorf("%s of empty text: %q", name, got)
		}
	}
	nested := on.Bold("a" + on.Red("b") + "c")
	if got := Strip(nested); got != "abc" {
		t.Errorf("nested: %q", got)
	}
	if got := Strip("plain"); got != "plain" {
		t.Errorf("plain: %q", got)
	}
}

func TestWidthIgnoresEscapes(t *testing.T) {
	st := Style{Color: true}
	for _, tc := range []struct {
		in   string
		want int
	}{{"", 0}, {"abc", 3}, {st.Red("héllo"), 5}, {st.Bold(st.Cyan("日本")), 2}} {
		if got := Width(tc.in); got != tc.want {
			t.Errorf("Width(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestFitAndPad(t *testing.T) {
	for _, tc := range []struct {
		in   string
		w    int
		want string
	}{
		{"hello", 10, "hello"}, {"hello", 5, "hello"}, {"hello", 4, "hel…"},
		{"hello", 1, "h"}, {"hello", 0, "hello"}, {"hello", -3, "hello"},
		{"héllo wörld", 6, "héllo…"},
	} {
		if got := Fit(tc.in, tc.w); got != tc.want {
			t.Errorf("Fit(%q, %d) = %q, want %q", tc.in, tc.w, got, tc.want)
		}
	}
	st := Style{Color: true}
	for _, tc := range []struct {
		in   string
		w    int
		want int
	}{{"ab", 5, 5}, {st.Red("ab"), 5, 5}, {"abcdef", 3, 6}, {"", 2, 2}} {
		if got := Width(Pad(tc.in, tc.w)); got != tc.want {
			t.Errorf("Width(Pad(%q, %d)) = %d, want %d", tc.in, tc.w, got, tc.want)
		}
	}
	if got := Pad("ab", 4); got != "ab  " {
		t.Errorf("Pad: %q", got)
	}
	if got := Pad("abcdef", 3); got != "abcdef" {
		t.Errorf("Pad wider: %q", got)
	}
}

func TestEnvStyle(t *testing.T) {
	for _, tc := range []struct {
		name, term, noColor string
		want                bool
	}{
		{"colour", "xterm-256color", "", true},
		{"NO_COLOR", "xterm-256color", "1", false},
		{"dumb", "dumb", "", false},
		{"unset TERM", "", "", false},
	} {
		t.Setenv("TERM", tc.term)
		t.Setenv("NO_COLOR", tc.noColor)
		if got := EnvStyle().Color; got != tc.want {
			t.Errorf("%s: Color = %v, want %v", tc.name, got, tc.want)
		}
		if got := Dumb(); got != (tc.term == "" || tc.term == "dumb") {
			t.Errorf("%s: Dumb = %v", tc.name, got)
		}
	}
}

func TestTerminalNewTerminal(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	// Dumb TERM: not ok, even for a file. Nothing here runs stty.
	t.Setenv("TERM", "dumb")
	if tm, ok := NewTerminal(os.Stdin, os.Stderr); ok || tm.MakeRaw != nil || tm.Style.Color {
		t.Errorf("dumb: ok %v raw %v color %v", ok, tm.MakeRaw != nil, tm.Style.Color)
	}
	t.Setenv("TERM", "xterm")
	// A reader that is not a terminal file.
	if tm, ok := NewTerminal(&fakeTerm{}, os.Stderr); ok || tm.MakeRaw != nil || tm.In == nil || tm.Out == nil {
		t.Errorf("non-file: ok %v raw %v", ok, tm.MakeRaw != nil)
	}
	// A file under a smart TERM is ok; the closures are not called.
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if tm, ok := NewTerminal(f, os.Stderr); !ok || tm.MakeRaw == nil || tm.Size == nil || !tm.Style.Color {
		t.Errorf("file: ok %v raw %v size %v", ok, tm.MakeRaw != nil, tm.Size != nil)
	}
}

func TestStripHandlesNonSGRCSI(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		width    int
	}{
		{"a\x1b[2Kb text\x1b[31mred", "ab textred", 10},
		{"\x1b[1;5Hx\x1b[?25ly", "xy", 2},
		// Text after a non-SGR CSI holds an 'm': the old first-m rule would eat up to it.
		{"a\x1b[2Kmore", "amore", 5},
		{"\x1b[Hmmm\x1b[Jm", "mmm" + "m", 4},
		// Unterminated: the rest is dropped from Strip and from Width's count (as stripDim did).
		{"a\x1b[31", "a", 1},
		{"a\x1b[31 12", "a", 1},
		// Width counts runes, not bytes.
		{"é☃日\x1b[31m本", "é☃日本", 4},
		// ESC not starting a CSI passes through: bare, OSC, charset select.
		{"a\x1bb", "a\x1bb", 3},
		{"a\x1b", "a\x1b", 2},
		{"\x1b]0;t\x07x", "\x1b]0;t\x07x", 7},
		{"\x1b(Bx", "\x1b(Bx", 4},
	} {
		if got := Strip(tc.in); got != tc.want {
			t.Errorf("Strip(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if got := Width(tc.in); got != tc.width {
			t.Errorf("Width(%q) = %d, want %d", tc.in, got, tc.width)
		}
	}
}

func TestSanitize(t *testing.T) {
	for _, tc := range []struct {
		in     string
		keepNL bool
		want   string
	}{
		{"a\nb\tc", true, "a\nb c"},
		{"a\nb\tc", false, "a b c"},
		{"a\x1b[2Kb\x1b[31mc\x07\x7f\u0085d", true, "abcd"},
		{"ok é ☃", false, "ok é ☃"},
	} {
		if got := Sanitize(tc.in, tc.keepNL); got != tc.want {
			t.Errorf("Sanitize(%q, %v) = %q, want %q", tc.in, tc.keepNL, got, tc.want)
		}
	}
}
