package strutil

import "testing"

func TestFirstLine(t *testing.T) {
	for in, want := range map[string]string{
		"":                "",
		"one\ntwo":        "one",
		"\n  \n  two \nx": "two",
		"a\r\nb":          "a",
		" pad \nb":        "pad",
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
