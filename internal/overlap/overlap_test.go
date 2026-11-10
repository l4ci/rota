package overlap

import (
	"reflect"
	"testing"
)

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
		{"dot-slash prefix is stripped", []string{"./a/b.go"}, []string{"a/b.go"}, []string{"a/b.go"}, nil},
		{"dot-slash dir is stripped", []string{"./a/"}, []string{"a/b.go"}, []string{"a/b.go"}, []string{"a/"}},
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

// TestBothVersusPaths pins the intended split: Both (gate, changed files)
// is exact equality, Paths (readiness, declared footprints) honours globs and
// directories. The same inputs must give different answers.
func TestBothVersusPaths(t *testing.T) {
	for _, c := range []struct {
		name       string
		a, b       []string
		both, path []string
	}{
		{"glob vs file", []string{"docs/*.md"}, []string{"docs/a.md"}, nil, []string{"docs/a.md"}},
		{"dir vs file", []string{"internal/x/"}, []string{"internal/x/a.go"}, nil, []string{"internal/x/a.go"}},
		{"identical", []string{"a.go"}, []string{"a.go"}, []string{"a.go"}, []string{"a.go"}},
	} {
		if got := Both(c.a, c.b, nil); !reflect.DeepEqual(got, c.both) {
			t.Errorf("%s: Both = %v, want %v", c.name, got, c.both)
		}
		if got := Paths(c.a, c.b); !reflect.DeepEqual(got, c.path) {
			t.Errorf("%s: Paths = %v, want %v", c.name, got, c.path)
		}
	}
}
