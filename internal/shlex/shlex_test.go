package shlex

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplit(t *testing.T) {
	cases := []struct {
		in   string
		want []string
		bad  bool
	}{
		{"claude --model sonnet", []string{"claude", "--model", "sonnet"}, false},
		{`sh -c "claude -c"`, []string{"sh", "-c", "claude -c"}, false},
		{`a 'b c' d\ e`, []string{"a", "b c", "d e"}, false},
		{`a "" b`, []string{"a", "", "b"}, false},
		{`a "b \" c"`, []string{"a", `b " c`}, false},
		{`a "b \n c"`, []string{"a", `b \n c`}, false},
		{`claude "oops`, nil, true},
		{`a \`, nil, true},
		{"", nil, false},
	}
	for _, c := range cases {
		got, err := Split(c.in)
		if (err != nil) != c.bad {
			t.Errorf("Split(%q) err = %v, want bad=%v", c.in, err, c.bad)
			continue
		}
		if !c.bad && !reflect.DeepEqual(got, c.want) && !(len(got) == 0 && len(c.want) == 0) {
			t.Errorf("Split(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestQuote(t *testing.T) {
	for in, want := range map[string]string{"abc/d-e.f": "abc/d-e.f", "": "''", "a b": "'a b'", "it's": `'it'\''s'`, "$x": "'$x'"} {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestQuoteAlways(t *testing.T) {
	for in, want := range map[string]string{"abc": "'abc'", "": "''", "it's": `'it'\''s'`} {
		if got := QuoteAlways(in); got != want {
			t.Errorf("QuoteAlways(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestScannerWord(t *testing.T) {
	isBreak := func(c byte) bool { return c == ' ' || c == ';' }
	for _, tc := range []struct {
		in, word string
		rest     string
		nested   []string
	}{
		{`a'b c'"d\"e"f g`, `ab cd"ef`, " g", nil},
		{`x\;y;z`, `x;y`, ";z", nil},
		{`"$(echo "$(inner)")" tail`, ``, " tail", []string{`echo "$(inner)"`}},
		{"`cmd`;", ``, ";", []string{"cmd"}},
		{`$((1+2)) z`, ``, " z", nil},
	} {
		sc := &Scanner{S: tc.in}
		got, err := sc.Word(isBreak)
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if tc.word != "" && got != tc.word {
			t.Errorf("%q: word %q, want %q", tc.in, got, tc.word)
		}
		if sc.S[sc.I:] != tc.rest {
			t.Errorf("%q: rest %q, want %q", tc.in, sc.S[sc.I:], tc.rest)
		}
		if strings.Join(sc.Nested, "|") != strings.Join(tc.nested, "|") {
			t.Errorf("%q: nested %q, want %q", tc.in, sc.Nested, tc.nested)
		}
	}
	for _, bad := range []string{`'open`, `"open`, "`open", `$(open`} {
		if _, err := (&Scanner{S: bad}).Word(isBreak); err != ErrUnbalanced {
			t.Errorf("%q: err %v", bad, err)
		}
	}
}
