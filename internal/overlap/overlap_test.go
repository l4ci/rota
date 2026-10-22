package overlap

import (
	"reflect"
	"testing"
)

func TestMatchPath(t *testing.T) {
	for _, c := range []struct {
		entry, file string
		want        bool
	}{
		{"rota-release", "rota-release/SKILL.md", true},
		{"rota-release/", "rota-release/SKILL.md", true},
		{"./docs", "docs/a/b.md", true},
		{"docs", "docsx/a.md", false},
		{"go.mod", "go.mod", true},
		{"*.md", "README.md", true},
		{"*.md", "docs/README.md", false},
		{"internal/*/gate.go", "internal/cli/gate.go", true},
		{"", "x", false},
		{"[", "[", true}, // equal wins over a malformed glob
		{"[", "a", false},
	} {
		if got := MatchPath(c.entry, c.file); got != c.want {
			t.Errorf("MatchPath(%q, %q) = %v", c.entry, c.file, got)
		}
	}
}

func TestFilter(t *testing.T) {
	got := Filter([]string{"b.go", "CHANGELOG.md", "docs/x.md", "a.go"}, []string{"CHANGELOG.md", "docs"})
	if want := []string{"b.go", "a.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Filter = %v, want %v", got, want)
	}
	if got := Filter([]string{"a"}, nil); !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("no shared: %v", got)
	}
}

func TestBoth(t *testing.T) {
	a := []string{"z.go", "CHANGELOG.md", "m.go", "only-a.go"}
	b := []string{"m.go", "z.go", "CHANGELOG.md", "only-b.go", "m.go"}
	if got, want := Both(a, b, []string{"CHANGELOG.md"}), []string{"m.go", "z.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Both = %v, want %v", got, want)
	}
	if got := Both(a, b, []string{"*.go", "CHANGELOG.md"}); len(got) != 0 {
		t.Errorf("all shared: %v", got)
	}
	if got := Both(nil, b, nil); len(got) != 0 {
		t.Errorf("empty side: %v", got)
	}
}

// TestFootprintClash pins both footprint checks in both argument orders.
// Paths is symmetric and reports the clashing path of the other side; Dirs
// lists only directory entries that matched a different path. Footprints are
// expected normalised: `./` is not stripped here (MatchPath strips it for
// configured entries).
func TestFootprintClash(t *testing.T) {
	for _, c := range []struct {
		name  string
		a, b  []string
		paths []string
		dirs  []string
	}{
		{"same file", []string{"a/b.go"}, []string{"a/b.go"}, []string{"a/b.go"}, nil},
		{"different files", []string{"a/b.go"}, []string{"a/c.go"}, nil, nil},
		{"dir over file", []string{"a/"}, []string{"a/b.go"}, []string{"a/b.go"}, []string{"a/"}},
		{"dir over nested file", []string{"a/"}, []string{"a/b/c.go"}, []string{"a/b/c.go"}, []string{"a/"}},
		{"same dir", []string{"a/"}, []string{"a/"}, []string{"a/"}, nil},
		{"dir prefix is not a path prefix", []string{"a/"}, []string{"ab/c.go"}, nil, nil},
		{"file without slash is not a dir", []string{"a"}, []string{"a/b.go"}, nil, nil},
		{"glob over file", []string{"internal/new/*.go"}, []string{"internal/new/x.go"}, []string{"internal/new/x.go"}, nil},
		{"glob does not cross slash", []string{"internal/*.go"}, []string{"internal/new/x.go"}, nil, nil},
		{"dot-slash prefix is not stripped", []string{"./a/b.go"}, []string{"a/b.go"}, nil, nil},
		{"dot-slash dir is not stripped", []string{"./a/"}, []string{"a/b.go"}, nil, nil},
		{"malformed glob only matches itself", []string{"["}, []string{"["}, []string{"["}, nil},
		{"empty side", nil, []string{"a.go"}, nil, nil},
	} {
		for _, swap := range []bool{false, true} {
			a, b := c.a, c.b
			if swap {
				a, b = b, a
			}
			if got := Paths(a, b); !reflect.DeepEqual(got, c.paths) {
				t.Errorf("%s: Paths(%v, %v) = %v, want %v", c.name, a, b, got, c.paths)
			}
			if got := Dirs(a, b); !reflect.DeepEqual(got, c.dirs) {
				t.Errorf("%s: Dirs(%v, %v) = %v, want %v", c.name, a, b, got, c.dirs)
			}
		}
	}
}
