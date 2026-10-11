package rotatree

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPaths(t *testing.T) {
	r := "/p"
	cases := map[string]string{
		Dir(r):                       "/p/.rota",
		Config(r):                    "/p/.rota/config.json",
		ConfigLocal(r):               "/p/.rota/config.local.json",
		Workers(r):                   "/p/.rota/workers.json",
		Repos(r):                     "/p/.rota/repos.json",
		Status(r):                    "/p/.rota/status.json",
		Verdicts(r):                  "/p/.rota/verdicts.json",
		Backlog(r):                   "/p/.rota/BACKLOG.md",
		Decisions(r):                 "/p/.rota/DECISIONS.md",
		Milestones(r):                "/p/.rota/MILESTONES.md",
		IssueMap(r):                  "/p/.rota/issue-map.json",
		Doc(r, PlansDir, "B07"):      "/p/.rota/plans/B07.md",
		File(r, KnowledgeDir, "api"): "/p/.rota/knowledge/api",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	}
}

func TestExists(t *testing.T) {
	d := t.TempDir()
	if Exists(d) {
		t.Fatal("empty dir has no .rota")
	}
	if err := os.Mkdir(filepath.Join(d, DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if !Exists(d) {
		t.Fatal(".rota dir not seen")
	}
}

func TestAscend(t *testing.T) {
	d := t.TempDir()
	deep := filepath.Join(d, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	var seen []string
	if !Ascend(deep, func(dir string) bool {
		seen = append(seen, dir)
		return dir == d
	}) {
		t.Fatal("walk should stop at the marker")
	}
	want := []string{deep, filepath.Dir(deep), d}
	if len(seen) != len(want) {
		t.Fatalf("visited %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("visited %v, want %v", seen, want)
		}
	}
}

func TestAscendReachesFilesystemRoot(t *testing.T) {
	root := string(filepath.Separator)
	var last string
	if Ascend(t.TempDir(), func(dir string) bool { last = dir; return false }) {
		t.Fatal("a predicate that never matches must not stop the walk")
	}
	if last != root {
		t.Fatalf("last visited %q, want the filesystem root %q", last, root)
	}
}

func TestFind(t *testing.T) {
	d := t.TempDir()
	deep := filepath.Join(d, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := Find(deep); ok && got == d {
		t.Fatal("found a root before .rota/ exists")
	}
	if err := os.Mkdir(filepath.Join(d, DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := Find(deep); !ok || got != d {
		t.Fatalf("Find = %q, %v; want %q, true", got, ok, d)
	}
	if got, ok := Find(d); !ok || got != d {
		t.Fatalf("Find at the root itself = %q, %v", got, ok)
	}
}
