package pystr

import "testing"

func TestUniversalNewlines(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"empty", "", ""},
		{"unix untouched", "a\nb\n", "a\nb\n"},
		{"crlf", "a\r\nb\r\n", "a\nb\n"},
		{"lone cr", "a\rb\r", "a\nb\n"},
		{"mixed", "a\r\nb\rc\nd", "a\nb\nc\nd"},
		{"cr cr lf is two breaks", "a\r\r\nb", "a\n\nb"},
		{"lf cr stays two breaks", "a\n\rb", "a\n\nb"},
		{"other separators untouched", "a\u2028b\vc", "a\u2028b\vc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := UniversalNewlines(tc.in); got != tc.want {
				t.Errorf("UniversalNewlines(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
