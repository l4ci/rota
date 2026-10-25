package cli

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/hook"
)

func TestReviewScaffoldingNoBaseDoesNotAssumeMain(t *testing.T) {
	d := t.TempDir()
	repo := filepath.Join(d, "r")
	gittest.Init(t, repo, "work")
	gitT(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	gitT(t, repo, "checkout", "-q", "-b", "feat")
	o := trRun(t, repo, "", "review", "scaffolding", "feat")
	if o.code != 3 || !strings.Contains(o.stderr+o.stdout, "could not determine base branch") {
		t.Fatalf("code=%d out=%q err=%q; want exit 3 naming the unresolved base", o.code, o.stdout, o.stderr)
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

// A set git.baseBranch that does not resolve must not leave the round env on a
// literal "main": the env carries an error naming the key instead.
func TestDefaultRoundEnvConfiguredBaseMissing(t *testing.T) {
	dir := t.TempDir()
	gittest.Init(t, dir, "master")
	gitT(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	os.MkdirAll(filepath.Join(dir, ".rota"), 0o755)
	os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{"git":{"baseBranch":"gone"}}`), 0o644)
	e := defaultRoundEnv(context.Background(), dir, defaultDeps(), false)
	if e.Base == "main" {
		t.Errorf("Base = %q; want no silent main fallback", e.Base)
	}
	if !strings.Contains(e.BaseErr, "git.baseBranch") || !strings.Contains(e.BaseErr, "gone") {
		t.Errorf("BaseErr = %q; want it to name git.baseBranch and the value", e.BaseErr)
	}
}

func TestStopHandoffSkipsWithoutPath(t *testing.T) {
	cd := t.TempDir()
	path, err := hook.StatePath(cd, "s1")
	if err != nil {
		t.Fatal(err)
	}
	pct := 95.0
	if err := hook.UpdateState(path, func(st hook.State, _ bool) hook.State {
		st.SessionID, st.ContextPct = "s1", &pct
		return st
	}); err != nil {
		t.Fatal(err)
	}
	hc := hookContext{commonDir: cd, handoff: filepath.Join(cd, "h.md"), now: time.Now}
	if _, ok := stopHandoff(hc, "s1", false); !ok {
		t.Fatal("control: a state with a handoff path should reach a decision")
	}
	hc.handoff = ""
	if _, ok := stopHandoff(hc, "s1", false); ok {
		t.Error("no handoff path: want no decision, not a note under a guessed branch")
	}
}

func noBaseRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gittest.Init(t, dir, "work")
	gitT(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	return dir
}

func TestReviewScaffoldingBaseFlagBypassesUnresolvedBase(t *testing.T) {
	repo := noBaseRepo(t)
	gitT(t, repo, "checkout", "-q", "-b", "feat")
	o := trRun(t, repo, "", "review", "scaffolding", "--base", "work", "feat")
	if o.code != 0 {
		t.Fatalf("code=%d out=%q err=%q; --base should need no resolved base", o.code, o.stdout, o.stderr)
	}
	o = trRun(t, repo, "", "review", "scaffolding", "feat")
	if o.code != 3 || !strings.Contains(o.stderr+o.stdout, "pass --base") {
		t.Errorf("code=%d out=%q err=%q; want exit 3 with the --base hint", o.code, o.stdout, o.stderr)
	}
}

func TestRoundStartWarnsWhenNoBaseResolves(t *testing.T) {
	dir := orchProject(t, false)
	os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{"git":{"baseBranch":"gone"}}`), 0o644)
	os.WriteFile(filepath.Join(dir, ".rota", "BACKLOG.md"), []byte("# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n"), 0o644)
	code, out, errOut := rotaStdin(t, dir, "", "round", "start", "--holder-pid", strconv.Itoa(os.Getpid()), "--base", "feat/x")
	if code != 0 || !strings.Contains(errOut, "could not determine base branch") {
		t.Errorf("code=%d out=%q err=%q; want the shared no-base warning", code, out, errOut)
	}
}
