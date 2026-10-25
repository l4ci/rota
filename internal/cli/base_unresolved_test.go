package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/gittest"
)

func TestReviewScaffoldingNoBaseDoesNotAssumeMain(t *testing.T) {
	d := t.TempDir()
	repo := filepath.Join(d, "r")
	gittest.Init(t, repo, "work")
	gitT(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	gitT(t, repo, "checkout", "-q", "-b", "feat")
	o := trRun(t, repo, "", "review", "scaffolding", "feat")
	if o.code != 3 || !strings.Contains(o.stderr+o.stdout, "base") {
		t.Fatalf("code=%d out=%q err=%q; want exit 3 naming the base", o.code, o.stdout, o.stderr)
	}
}

func TestHandoffFileUnresolvedBaseHasNoPath(t *testing.T) {
	for name, cfg := range map[string]string{
		"configured missing": `{"git":{"baseBranch":"gone"}}`,
		"no base":            `{}`,
	} {
		dir := t.TempDir()
		gittest.Init(t, dir, "work")
		gitT(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
		os.MkdirAll(filepath.Join(dir, ".rota"), 0o755)
		cfgPath := filepath.Join(dir, ".rota", "config.json")
		os.WriteFile(cfgPath, []byte(cfg), 0o644)
		if got := handoffFile(defaultDeps(), dir, config.Load(cfgPath)); got != "" {
			t.Errorf("%s: handoffFile = %q; want empty, not a main.md fallback", name, got)
		}
	}
}
