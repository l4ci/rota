package skills

import "testing"

func TestFlatSibling(t *testing.T) {
	for target, want := range map[string]bool{
		"a.md": true, "x-1.2_y.md": true, "references/r.md": false,
		"../x.md": false, "sub/a.md": false, "a.txt": false, "a.md.bak": false, "": false,
	} {
		if got := FlatSibling(target); got != want {
			t.Errorf("FlatSibling(%q) = %v, want %v", target, got, want)
		}
	}
}
