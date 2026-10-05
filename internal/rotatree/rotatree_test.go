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
