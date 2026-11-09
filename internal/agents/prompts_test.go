package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/cfgdir"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/prompts"
	"github.com/l4ci/rota/internal/rotatree"
)

func TestPromptsExplicitActivation(t *testing.T) {
	root := project(t, "")
	cfg := config.Load(cfgdir.OSEnv(), rotatree.Config(root))
	original, err := Write(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	desired, err := CompareWithPrompts(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range desired {
		if e.Content != original[i].Content || e.Status != StatusCurrent {
			t.Fatalf("default rendering changed: %s", e.Path)
		}
	}
	s := prompts.Open(root, prompts.Catalog())
	if _, err = s.Save("agents.explorer.report", "TEAM {{status}} {{closing}}", 0); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".claude", "agents", "rota-explorer.md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(before), "TEAM") {
		t.Fatal("save activated override")
	}
	if _, err = WriteWithPrompts(root, cfg, nil); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"TEAM `Not found:`", "Search and read; never edit or write files.", "Quote code, paths and errors verbatim;", "`done.`", "`too-big.`", "`ambiguous.`"} {
		if !strings.Contains(string(after), want) {
			t.Fatalf("activated explorer missing %q: %s", want, after)
		}
	}
	for _, e := range original {
		if !strings.Contains(e.Path, "explorer") {
			b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(e.Path)))
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != e.Content {
				t.Fatalf("unrelated role changed: %s", e.Path)
			}
		}
	}
}
