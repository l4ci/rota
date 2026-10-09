package cli

import "testing"

func TestFirstText(t *testing.T) {
	for _, c := range []struct {
		name    string
		streams []string
		want    string
	}{
		{"first stream wins", []string{"boom\nmore", "out"}, "boom"},
		{"blank stream falls through", []string{"\n \n", "out"}, "out"},
		{"python-only whitespace falls through", []string{"\x1f", "out"}, "out"},
		{"all blank takes fallback", []string{"\x1f", " \n"}, "fb"},
		{"no streams", nil, "fb"},
	} {
		if got := firstText("fb", c.streams...); got != c.want {
			t.Errorf("%s: firstText = %q, want %q", c.name, got, c.want)
		}
	}
}
