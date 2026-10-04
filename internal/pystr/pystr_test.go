package pystr

import (
	"reflect"
	"regexp"
	"strconv"
	"testing"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/pytest"
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
	var want struct {
		Space, Word, Digit [][2]int
		DigitValue         map[string]int
	}
	pytest.GoldenJSON(t, nil, &want)

	class := regexp.MustCompile(`\A[` + SpaceClass + `]\z`)
	var space, word, digit []int
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
			if got := DigitValue(r); got != want.DigitValue[itoa(c)] {
				t.Errorf("DigitValue(U+%04X) = %d, Python int() = %d", c, got, want.DigitValue[itoa(c)])
			}
		}
		if class.MatchString(string(r)) != IsSpace(r) {
			t.Errorf("SpaceClass disagrees with IsSpace at U+%04X", c)
		}
	}
	for name, p := range map[string]struct{ got, want [][2]int }{"space": {ranges(space), want.Space}, "word": {ranges(word), want.Word}, "digit": {ranges(digit), want.Digit}} {
		if !reflect.DeepEqual(p.got, p.want) {
			t.Errorf("%s: Go has %d code point runs, Python %d", name, len(p.got), len(p.want))
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestStringHelpersMatchPython(t *testing.T) {
	cases := []string{"", "a", "a\nb", "a\n", "\n", "a\r\nb\rc\n\nd", "x\vy\fz", "a\x1cb\x1dc\x1ed\x1fe", "a\u0085b c d", " \t x  　", "\r", "\r\n", "\n\r", "café ", "a\x0b\x0c"}
	var want []map[string]any
	pytest.GoldenJSON(t, cases, &want)
	var got, w, in []any
	for i, s := range cases {
		lines := Splitlines(s)
		if lines == nil {
			lines = []string{}
		}
		got = append(got, map[string]any{"strip": Strip(s), "rstrip": Rstrip(s), "lines": lines})
		w, in = append(w, want[i]), append(in, s)
	}
	pytest.Compare(t, "pystr", in, got, w)
}
