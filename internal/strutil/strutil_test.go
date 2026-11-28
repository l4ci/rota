package strutil

import "testing"

func TestFirstLine(t *testing.T) {
	for in, want := range map[string]string{
		"":                "",
		"one\ntwo":        "one",
		"\n  \n  two \nx": "two",
		"a\r\nb":          "a",
		" pad \nb":        "pad",
		"\n \t\n\r\n":     "",
		"\x1f":            "",
	} {
		if got := FirstLine(in); got != want {
			t.Errorf("FirstLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClip(t *testing.T) {
	for _, c := range []struct {
		in   string
		n    int
		want string
	}{
		{"abc", 5, "abc"},
		{"abcdef", 3, "abc"},
		{"ééé", 2, "éé"},
		{"", 0, ""},
	} {
		if got := Clip(c.in, c.n); got != c.want {
			t.Errorf("Clip(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestShortSHA(t *testing.T) {
	for in, want := range map[string]string{
		"":                 "",
		"abc":              "abc",
		"abcdefg":          "abcdefg",
		"0123456789abcdef": "0123456",
	} {
		if got := ShortSHA(in); got != want {
			t.Errorf("ShortSHA(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFirstNonEmpty(t *testing.T) {
	for name, tc := range map[string]struct {
		in   []string
		want string
	}{
		"no input":       {nil, ""},
		"single empty":   {[]string{""}, ""},
		"single value":   {[]string{"a"}, "a"},
		"first wins":     {[]string{"a", "b"}, "a"},
		"skips empty":    {[]string{"", "", "c", "d"}, "c"},
		"all empty":      {[]string{"", ""}, ""},
		"whitespace set": {[]string{" ", "b"}, " "},
	} {
		if got := FirstNonEmpty(tc.in...); got != tc.want {
			t.Errorf("%s: FirstNonEmpty(%q) = %q, want %q", name, tc.in, got, tc.want)
		}
	}
}

func TestOrDefault(t *testing.T) {
	for _, tc := range []struct{ v, d, want string }{
		{"", "d", "d"},
		{"v", "d", "v"},
		{" ", "d", " "},
		{"", "", ""},
	} {
		if got := OrDefault(tc.v, tc.d); got != tc.want {
			t.Errorf("OrDefault(%q, %q) = %q, want %q", tc.v, tc.d, got, tc.want)
		}
	}
}

const (
	esc  = "\x1b[31m"
	bidi = "‮⁦"
	zw   = "a​b­c"
)

func TestHasControl(t *testing.T) {
	for in, want := range map[string]bool{
		"":            false,
		"plain":       false,
		"é 日本":        false,
		"a\x00b":      true,
		"a\x7fb":      true,
		"a\u0085b":    true,
		"a\nb":        true,
		"a\tb":        true,
		"a" + bidi:    true,
		"a​b":         true,
		"a\U000e0001": true,
	} {
		if got := HasControl(in); got != want {
			t.Errorf("HasControl(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestStripControls(t *testing.T) {
	for _, c := range []struct {
		name, in string
		keep     []rune
		want     string
	}{
		{"control", "a\x00b\x7fc\u0085d", nil, "abcd"},
		{"escape", "x" + esc + "y", nil, "x[31my"},
		{"bidi", "a" + bidi + "b", nil, "ab"},
		{"zero width and soft hyphen", zw, nil, "abc"},
		{"newline tab dropped by default", "a\nb\tc", nil, "abc"},
		{"newline tab kept", "a\nb\tc\x00", []rune{'\n', '\t'}, "a\nb\tc"},
		{"keep only newline", "a\nb\tc", []rune{'\n'}, "a\nbc"},
		{"keep does not rescue other controls", "a\rb\n", []rune{'\n'}, "ab\n"},
		{"plain unchanged", "é 日本", nil, "é 日本"},
	} {
		if got := StripControls(c.in, c.keep...); got != c.want {
			t.Errorf("%s: StripControls(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestDisplay(t *testing.T) {
	for _, c := range []struct {
		name, in string
		limit    int
		want     string
	}{
		{"strips control and format", "a\x1bb" + bidi + "c​d", 10, "abcd"},
		{"newline stripped", "a\nb", 10, "ab"},
		{"fits exactly", "abcde", 5, "abcde"},
		{"cut ends in ellipsis", "abcdef", 5, "abcd…"},
		{"cut counts runes", "日本語日本語", 3, "日本…"},
		{"stripped text is what is bounded", "a​b​c", 3, "abc"},
	} {
		if got := Display(c.in, c.limit); got != c.want {
			t.Errorf("%s: Display(%q, %d) = %q, want %q", c.name, c.in, c.limit, got, c.want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	for n, want := range map[uint64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 2048: "2.0 KiB", 3 << 30: "3.0 GiB"} {
		if got := HumanBytes(n); got != want {
			t.Errorf("HumanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestSameSHA(t *testing.T) {
	full := "bbbbbbb0123456789012345678901234567890a"
	for _, c := range []struct {
		name, a, b string
		want       bool
	}{
		{"equal full", full, full, true},
		{"short vs full", "bbbbbbb", full, true},
		{"full vs short", full, "bbbbbbb", true},
		{"longer vs shorter abbreviation", "bbbbbbb0123", "bbbbbbb01", true},
		{"mismatch", "aaaaaaa", full, false},
		{"mismatch same length", "bbbbbbc", "bbbbbbb", false},
		{"empty a", "", full, false},
		{"empty b", full, "", false},
		{"both empty", "", "", false},
		{"short below minimum", "bbb", full, false},
		{"both short equal", "bbb", "bbb", false},
		{"six chars", "bbbbbb", full, false},
	} {
		if got := SameSHA(c.a, c.b); got != c.want {
			t.Errorf("%s: SameSHA(%q, %q) = %v, want %v", c.name, c.a, c.b, got, c.want)
		}
	}
}

func TestTailLines(t *testing.T) {
	for _, tc := range []struct {
		in   string
		n    int
		want string
	}{
		{"", 3, ""},
		{"a", 3, "a"},
		{"a\nb\nc\n\n", 2, "b\nc"},
		{"a\n\nc", 2, "\nc"},
	} {
		if got := TailLines(tc.in, tc.n); got != tc.want {
			t.Errorf("TailLines(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}
