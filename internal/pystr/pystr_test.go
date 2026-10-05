package pystr

import (
	"regexp"
	"strconv"
	"testing"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/golden"
)

// ranges compresses a sorted code point list to [lo, hi] runs, which keeps the
// golden small (the Unicode \w class alone has over 100k members).
func ranges(cs []int) [][2]int {
	out := [][2]int{}
	for _, c := range cs {
		if n := len(out); n > 0 && out[n-1][1] == c-1 {
			out[n-1][1] = c
		} else {
			out = append(out, [2]int{c, c})
		}
	}
	return out
}

// Every code point up to U+2FFFF must classify as Python does, for \s, \w, \d
// and the regexp class Go uses in its place.
func TestClassesMatchPython(t *testing.T) {
	class := regexp.MustCompile(`\A[` + SpaceClass + `]\z`)
	var space, word, digit []int
	digitValue := map[string]int{}
	for c := 0; c < 0x30000; c++ {
		r := rune(c)
		if !utf8.ValidRune(r) {
			continue
		}
		if IsSpace(r) {
			space = append(space, c)
		}
		if IsWord(r) {
			word = append(word, c)
		}
		if IsDigit(r) {
			digit = append(digit, c)
			digitValue[itoa(c)] = DigitValue(r)
		}
		if class.MatchString(string(r)) != IsSpace(r) {
			t.Errorf("SpaceClass disagrees with IsSpace at U+%04X", c)
		}
	}
	golden.Check(t, map[string]any{"input": nil}, map[string]any{
		"Space": ranges(space), "Word": ranges(word), "Digit": ranges(digit), "DigitValue": digitValue})
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestStringHelpersMatchPython(t *testing.T) {
	cases := []string{"", "a", "a\nb", "a\n", "\n", "a\r\nb\rc\n\nd", "x\vy\fz", "a\x1cb\x1dc\x1ed\x1fe", "a\u0085b c d", " \t x  　", "\r", "\r\n", "\n\r", "café ", "a\x0b\x0c"}
	var got []any
	for _, s := range cases {
		lines := Splitlines(s)
		if lines == nil {
			lines = []string{}
		}
		got = append(got, map[string]any{"strip": Strip(s), "rstrip": Rstrip(s), "lines": lines})
	}
	golden.Check(t, map[string]any{"input": cases}, got)
}

func TestSplitCSV(t *testing.T) {
	got := SplitCSV(" a ,, b ,")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("got %q", got)
	}
	if got := SplitCSV(""); got == nil || len(got) != 0 {
		t.Errorf("empty = %#v, want non-nil empty", got)
	}
}
