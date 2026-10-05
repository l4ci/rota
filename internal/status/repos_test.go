package status

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/l4ci/rota/internal/repos"
)

func TestParseReposCSVAndMissing(t *testing.T) {
	if got := ParseReposCSV(" web, api ,,web , "); !reflect.DeepEqual(got, []string{"web", "api", "web"}) {
		t.Errorf("ParseReposCSV = %q", got)
	}
	if got := ParseReposCSV(" , "); got != nil {
		t.Errorf("ParseReposCSV blank = %q", got)
	}
	registry := []repos.Repo{{Name: "web", Path: "/w"}}
	if got := Missing(registry, []string{"web", "nope", "x"}); !reflect.DeepEqual(got, []string{"nope", "x"}) {
		t.Errorf("Missing = %q", got)
	}
}

func TestHasCode(t *testing.T) {
	root := t.TempDir()
	real, _ := filepath.EvalSymlinks(root)
	for _, d := range []string{".git", ".rota", ".claude", "web"} {
		os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	os.WriteFile(filepath.Join(root, ".gitignore"), nil, 0o644)
	registry := []repos.Repo{{Name: "web", Path: filepath.Join(real, "web")}, {Name: "outside", Path: "/somewhere/else"}}
	if HasCode(root, registry) {
		t.Error("only ignorable entries and a sub-repo, yet hasCode")
	}
	os.WriteFile(filepath.Join(root, "main.go"), nil, 0o644)
	if !HasCode(root, registry) {
		t.Error("a top-level file is code")
	}
	if HasCode(filepath.Join(root, "missing"), registry) {
		t.Error("an unreadable directory has code")
	}
}
