package status

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadRepos(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".rota"), 0o755)
	os.MkdirAll(filepath.Join(root, "web"), 0o755)
	os.Symlink("web", filepath.Join(root, "link"))
	os.WriteFile(filepath.Join(root, ".rota", "repos.json"), []byte(`{"repos": [
		{"name": "web", "path": "web"},
		{"name": "alias", "path": "link"},
		{"name": "ghost", "path": "not/there/yet"},
		{"name": "x", "path": "web"},
		{"name": "x", "path": "other"},
		{"name": "", "path": "web"}, {"name": "nopath"}, {"path": "web"}, 7]}`), 0o644)
	real, _ := filepath.EvalSymlinks(root)
	got := LoadRepos(root)
	want := []Repo{
		{"web", filepath.Join(real, "web")},
		{"alias", filepath.Join(real, "web")},
		{"ghost", filepath.Join(real, "not/there/yet")},
		{"x", filepath.Join(real, "other")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LoadRepos:\n%v\nwant\n%v", got, want)
	}
	if LoadRepos(t.TempDir()) != nil {
		t.Error("a project without repos.json has repos")
	}
	os.WriteFile(filepath.Join(root, ".rota", "repos.json"), []byte("{bad"), 0o644)
	if LoadRepos(root) != nil {
		t.Error("a corrupt repos.json has repos")
	}
}

func TestRealpathAbsoluteEntry(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".rota"), 0o755)
	abs := t.TempDir()
	os.WriteFile(filepath.Join(root, ".rota", "repos.json"), []byte(`{"repos": [{"name": "a", "path": "`+abs+`"}]}`), 0o644)
	real, _ := filepath.EvalSymlinks(abs)
	if got := LoadRepos(root); len(got) != 1 || got[0].Path != real {
		t.Errorf("absolute path: %v, want %s", got, real)
	}
}

func TestParseReposCSVAndMissing(t *testing.T) {
	if got := ParseReposCSV(" web, api ,,web , "); !reflect.DeepEqual(got, []string{"web", "api", "web"}) {
		t.Errorf("ParseReposCSV = %q", got)
	}
	if got := ParseReposCSV(" , "); got != nil {
		t.Errorf("ParseReposCSV blank = %q", got)
	}
	repos := []Repo{{"web", "/w"}}
	if got := Missing(repos, []string{"web", "nope", "x"}); !reflect.DeepEqual(got, []string{"nope", "x"}) {
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
	repos := []Repo{{"web", filepath.Join(real, "web")}, {"outside", "/somewhere/else"}}
	if HasCode(root, repos) {
		t.Error("only ignorable entries and a sub-repo, yet hasCode")
	}
	os.WriteFile(filepath.Join(root, "main.go"), nil, 0o644)
	if !HasCode(root, repos) {
		t.Error("a top-level file is code")
	}
	if HasCode(filepath.Join(root, "missing"), repos) {
		t.Error("an unreadable directory has code")
	}
}
