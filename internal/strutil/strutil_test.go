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
