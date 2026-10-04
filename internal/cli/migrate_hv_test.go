package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// legacyProject is a directory holding .hv/ and no .rota/, with a sub-directory.
func legacyProject(t *testing.T) (dir, sub string) {
	t.Helper()
	dir, _ = filepath.EvalSymlinks(t.TempDir())
	sub = filepath.Join(dir, "src", "deep")
	if err := os.MkdirAll(sub, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".hv"), 0o777); err != nil {
		t.Fatal(err)
	}
	return dir, sub
}

func TestUnmigratedProjectStopsEveryVerbButTheExemptOnes(t *testing.T) {
	_, sub := legacyProject(t)
	for _, args := range [][]string{
		{"backlog", "list"}, {"config", "show"}, {"init"}, {"knowledge", "query", "x"}, {"status", "show"}, {"round", "status"},
	} {
		code, _, stderr := rotaIn(t, sub, args...)
		if code != ExitResolution || !strings.Contains(stderr, "run: rota migrate hv") {
			t.Errorf("rota %v: exit %d, stderr %q", args, code, stderr)
		}
	}
	// --help, version and the skills group still answer.
	for _, args := range [][]string{{"backlog", "list", "--help"}, {"version"}, {"skills", "status", "--scope", "user"}} {
		if _, _, stderr := rotaIn(t, sub, args...); strings.Contains(stderr, "migrate hv") {
			t.Errorf("rota %v stopped: %q", args, stderr)
		}
	}
}

func TestAMigratedProjectIsNotStopped(t *testing.T) {
	dir, sub := legacyProject(t)
	os.Mkdir(filepath.Join(dir, ".rota"), 0o777)
	_, _, stderr := rotaIn(t, sub, "backlog", "list")
	if strings.Contains(stderr, "migrate hv") {
		t.Errorf("stopped: %q", stderr)
	}
}

func TestDoctorFailsOnAnHvLeftover(t *testing.T) {
	doctorFakes(t, map[string]string{"git": `case "$1" in remote) exit 2;; esac; exit 0`})
	_, sub := legacyProject(t)
	code, out, _ := rotaIn(t, sub, "doctor", "--json")
	if code != ExitFailed || !strings.Contains(out, `"name": "state"`) || !strings.Contains(out, "run: rota migrate hv") {
		t.Errorf("exit %d: %s", code, out)
	}
}

func TestMigrateHvRunsOnAnUnmigratedProject(t *testing.T) {
	doctorFakes(t, nil)
	dir := gitRepo(t)
	os.RemoveAll(filepath.Join(dir, ".rota"))
	os.MkdirAll(filepath.Join(dir, ".hv"), 0o777)
	os.WriteFile(filepath.Join(dir, ".hv", "config.json"), []byte(`{"hv":{"version":"4.9.0"}}`), 0o644)
	installedVersionFn = func() string { return "5.0.0" }
	t.Cleanup(func() { installedVersionFn = installedVersion })
	code, out, stderr := rotaIn(t, dir, "--json", "migrate", "hv")
	if code != 0 || !strings.Contains(out, `"move": true`) || !strings.Contains(stderr, "preview only") {
		t.Fatalf("dry-run: exit %d %s %s", code, out, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, ".hv")); err != nil {
		t.Fatal("dry-run moved .hv/")
	}
	code, out, stderr = rotaIn(t, dir, "--json", "migrate", "hv", "--apply", "--skip-skills")
	if code != 0 {
		t.Fatalf("apply: exit %d %s %s", code, out, stderr)
	}
	cfg, _ := os.ReadFile(filepath.Join(dir, ".rota", "config.json"))
	if !strings.Contains(string(cfg), `"5.0.0"`) {
		t.Errorf("config = %s", cfg)
	}
	code, out, _ = rotaIn(t, dir, "--json", "migrate", "hv", "--apply")
	if code != 0 || !strings.Contains(out, `"noop": true`) {
		t.Errorf("second apply: exit %d %s", code, out)
	}
}
