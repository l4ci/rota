package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func editProject(t *testing.T, names ...string) string {
	t.Helper()
	root := trackerProject(t, "")
	root, _ = filepath.EvalSymlinks(root)
	for _, n := range names {
		if out, err := exec.Command("git", "init", "-q", filepath.Join(root, n)).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}
	}
	return root
}

func registryOf(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, ".rota", "repos.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRepoAddRm(t *testing.T) {
	root := editProject(t, "web", "api")

	code, env, stderr := rotaRun(t, "--json", "-C", root, "repo", "add", "web")
	if code != 0 || get(dataOf(env), "changed") != true {
		t.Fatalf("add: exit %d %v %s", code, env, stderr)
	}
	if code, _, stderr = rotaRun(t, "--json", "-C", root, "repo", "add", "api", "--name", "backend"); code != 0 {
		t.Fatalf("add --name: exit %d %s", code, stderr)
	}
	got := registryOf(t, root)
	if strings.Index(got, `"backend"`) > strings.Index(got, `"web"`) || !strings.Contains(got, `"path": "./api"`) {
		t.Fatalf("registry not sorted or wrong path:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(root, ".rota", "knowledge", "web")); err != nil {
		t.Fatalf("knowledge dir not seeded: %v", err)
	}

	// Same entry again is a no-op; the file does not change.
	code, env, _ = rotaRun(t, "--json", "-C", root, "repo", "add", "web")
	if code != 0 || get(dataOf(env), "changed") != false || registryOf(t, root) != got {
		t.Fatalf("re-add: exit %d %v", code, env)
	}
	// Name taken by another directory, and directory taken by another name.
	os.Mkdir(filepath.Join(root, "other"), 0o755)
	exec.Command("git", "init", "-q", filepath.Join(root, "other")).Run()
	if code, _, _ = rotaRun(t, "-C", root, "repo", "add", "other", "--name", "web"); code != ExitRefused {
		t.Fatalf("name clash: exit %d", code)
	}
	if code, _, _ = rotaRun(t, "-C", root, "repo", "add", "web", "--name", "front"); code != ExitRefused {
		t.Fatalf("path clash: exit %d", code)
	}
	// Not a git repo, missing, outside the umbrella.
	os.Mkdir(filepath.Join(root, "plain"), 0o755)
	for _, p := range []string{"plain", "nope", t.TempDir(), "."} {
		if code, _, _ = rotaRun(t, "-C", root, "repo", "add", p); code != ExitResolution {
			t.Fatalf("add %q: exit %d", p, code)
		}
	}
	if got2 := registryOf(t, root); got2 != got {
		t.Fatalf("failed adds changed the registry:\n%s", got2)
	}

	code, env, _ = rotaRun(t, "--json", "-C", root, "repo", "rm", "web")
	if code != 0 || strings.Contains(registryOf(t, root), `"web"`) {
		t.Fatalf("rm: exit %d %v", code, env)
	}
	if _, err := os.Stat(filepath.Join(root, "web", ".git")); err != nil {
		t.Fatalf("rm touched the repo: %v", err)
	}
	if code, _, _ = rotaRun(t, "-C", root, "repo", "rm", "web"); code != ExitResolution {
		t.Fatalf("rm unknown: exit %d", code)
	}
}

func TestRepoRmWarnsOnReferences(t *testing.T) {
	root := editProject(t, "web")
	os.WriteFile(filepath.Join(root, ".rota", "repos.json"), []byte(`{"repos": [{"name": "web", "path": "./web"}]}`), 0o644)
	os.WriteFile(filepath.Join(root, ".rota", "BACKLOG.md"), []byte("# TODO\n\n## Bugs\n- **[B01] [P1] First.** x Repos: web\n- **[B02] [P1] Other.** y\n\n## Completed\n"), 0o644)
	os.WriteFile(filepath.Join(root, ".rota", "status.json"), []byte(`{"active": [{"branch": "b", "repo": "web", "items": ["B01"], "startedAt": "2026-09-01T10:00:00Z"}]}`), 0o644)

	code, env, stderr := rotaRun(t, "--json", "-C", root, "repo", "rm", "web")
	if code != 0 {
		t.Fatalf("exit %d %s", code, stderr)
	}
	data := dataOf(env)
	if items, _ := get(data, "openItems").([]any); len(items) != 1 || items[0] != "B01" {
		t.Fatalf("openItems = %v", get(data, "openItems"))
	}
	if s, _ := get(data, "activeStreams").([]any); len(s) != 1 || s[0] != "b" {
		t.Fatalf("activeStreams = %v", get(data, "activeStreams"))
	}
	if !strings.Contains(stderr, "B01") || !strings.Contains(stderr, "active stream") {
		t.Fatalf("warnings missing: %s", stderr)
	}
}
