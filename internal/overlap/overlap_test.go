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
