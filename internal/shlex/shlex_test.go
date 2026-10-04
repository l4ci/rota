package shlex

import (
	"reflect"
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
