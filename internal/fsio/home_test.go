package fsio

import "testing"

func TestExpandHome(t *testing.T) {
	for _, c := range []struct{ name, p, home, want string }{
		{"tilde slash", "~/work", "/h", "/h/work"},
		{"bare tilde", "~", "/h", "/h"},
		{"nested", "~/.a/b", "/h", "/h/.a/b"},
		{"trailing slash cleaned", "~/x/", "/h", "/h/x"},
		{"empty home leaves tilde slash", "~/work", "", "~/work"},
		{"empty home leaves bare tilde", "~", "", "~"},
		{"absolute untouched", "/abs", "/h", "/abs"},
		{"relative untouched", "rel", "/h", "rel"},
		{"other user untouched", "~bob/x", "/h", "~bob/x"},
		{"empty path", "", "/h", ""},
	} {
		if got := ExpandHome(c.p, c.home); got != c.want {
			t.Errorf("%s: ExpandHome(%q, %q) = %q, want %q", c.name, c.p, c.home, got, c.want)
		}
	}
}
