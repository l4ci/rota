package repos

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/l4ci/rota/internal/rotatree"
)

func TestLoad(t *testing.T) {
	root := t.TempDir()
	real, _ := filepath.EvalSymlinks(root)
	os.MkdirAll(filepath.Join(root, ".rota"), 0o755)
	os.MkdirAll(filepath.Join(root, "svc"), 0o755)
	os.Symlink(filepath.Join(root, "svc"), filepath.Join(root, "link"))
	reg := `{"repos":[
		{"name":"svc","path":"svc"},
		{"name":"","path":"x"},
		{"name":"nopath"},
		{"name":7,"path":"x"},
		"junk",
		{"name":"link","path":"link"},
		{"name":"abs","path":"/srv/abs/../web"},
		{"name":"ghost","path":"ghost"},
		{"name":"svc","path":"other"}
	]}`
	os.WriteFile(filepath.Join(root, ".rota", "repos.json"), []byte(reg), 0o644)
	want := []Repo{
		{"svc", "svc", filepath.Join(real, "svc")},
		{"link", "link", filepath.Join(real, "svc")},
		{"abs", "/srv/abs/../web", "/srv/web"},
		{"ghost", "ghost", filepath.Join(real, "ghost")},
	}
	// A repeated name keeps its first position and takes the last entry.
	want[0] = Repo{"svc", "other", filepath.Join(real, "other")}
	if got := Load(root); !reflect.DeepEqual(got, want) {
		t.Fatalf("Load:\n got %+v\nwant %+v", got, want)
	}
	if p := Paths(root); len(p) != 4 || p["svc"] != filepath.Join(real, "other") {
		t.Fatalf("Paths keeps the last of a repeated name: %v", p)
	}
	for _, body := range []string{"", "not json", `{"repos":null}`, `[]`, `{"repos":{}}`} {
		os.WriteFile(filepath.Join(root, ".rota", "repos.json"), []byte(body), 0o644)
		if got := Load(root); len(got) != 0 {
			t.Errorf("%q: %v", body, got)
		}
	}
	if got := Load(t.TempDir()); got != nil {
		t.Errorf("no registry: %v", got)
	}
}

func TestSetTopLevel(t *testing.T) {
	root := t.TempDir()
	reg := `{"repos":[{"name":"gone","path":"./gone"},{"name":"web","path":"./apps/web","x":1},{"name":"keep","path":"./keep"}]}`
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rotatree.Repos(root), []byte(reg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetTopLevel(root, []string{"web", "api", "keep"}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range Load(root) {
		got = append(got, r.Name+"="+r.Rel)
	}
	want := []string{"api=./api", "keep=./keep", "web=./apps/web"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
