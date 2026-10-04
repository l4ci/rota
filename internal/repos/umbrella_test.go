package repos

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func umbrella(t *testing.T, reg string, subs ...string) string {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	os.MkdirAll(filepath.Join(root, ".rota"), 0o755)
	os.WriteFile(filepath.Join(root, ".rota", "repos.json"), []byte(reg), 0o644)
	for _, s := range subs {
		os.MkdirAll(filepath.Join(root, s), 0o755)
		gitIn(t, filepath.Join(root, s), "init", "-q", "-b", "main")
		gitIn(t, filepath.Join(root, s), "commit", "-q", "--allow-empty", "-m", "init")
	}
	return root
}

func TestUmbrellaOn(t *testing.T) {
	root := umbrella(t, `{"repos": []}`)
	if Umbrella(root) {
		t.Error("empty registry is an umbrella")
	}
	os.WriteFile(filepath.Join(root, ".rota", "repos.json"), []byte(`{"repos": [{"name": "a", "path": "a"}]}`), 0o644)
	if !Umbrella(root) {
		t.Error("one entry is not an umbrella")
	}
	if Umbrella(t.TempDir()) {
		t.Error("no .rota is an umbrella")
	}
}

func TestWhichInsideAndOutside(t *testing.T) {
	root := umbrella(t, `{"repos": [{"name": "web", "path": "web"}, {"name": "api", "path": "api"}]}`, "web", "api")
	os.MkdirAll(filepath.Join(root, "web", "src", "deep"), 0o755)
	for _, dir := range []string{"web", "web/src/deep"} {
		r, err := Which(filepath.Join(root, dir))
		if err != nil || r.Name != "web" || r.Path != filepath.Join(root, "web") {
			t.Errorf("%s: %+v %v", dir, r, err)
		}
	}
	// the umbrella root itself is no git repo here
	if _, err := Which(root); !errors.Is(err, ErrNotGit) {
		t.Errorf("umbrella root: %v", err)
	}
	gitIn(t, root, "init", "-q", "-b", "main")
	if _, err := Which(root); !errors.Is(err, ErrNotRegistered) {
		t.Errorf("umbrella root as repo: %v", err)
	}
}

func TestWhichLayoutBWorktree(t *testing.T) {
	root := umbrella(t, `{"repos": [{"name": "web", "path": "web"}]}`, "web")
	wt := filepath.Join(root, "web-wt")
	gitIn(t, filepath.Join(root, "web"), "worktree", "add", "-q", "-b", "feat/x", wt)
	r, err := Which(wt)
	if err != nil || r.Name != "web" || r.Path != filepath.Join(root, "web") {
		t.Errorf("worktree: %+v %v", r, err)
	}
}

func TestWhichMaskedByStrayHV(t *testing.T) {
	root := umbrella(t, `{"repos": [{"name": "web", "path": "web"}]}`, "web")
	os.MkdirAll(filepath.Join(root, "web", ".rota"), 0o755)
	_, err := Which(filepath.Join(root, "web"))
	var m *MaskedError
	if !errors.As(err, &m) || m.Name != "web" || m.Stray != filepath.Join(root, "web", ".rota") {
		t.Errorf("masked: %v", err)
	}
	// a stray .rota/ in a directory the registry does not list is no mask
	os.MkdirAll(filepath.Join(root, "other", ".rota"), 0o755)
	if got, err := FindUmbrella(filepath.Join(root, "other")); err != nil || got != filepath.Join(root, "other") {
		t.Errorf("unregistered stray: %q %v", got, err)
	}
}

// A stray .rota/ deep inside a registered sub-repo's source tree masks the
// umbrella just like one at the sub-repo root.
func TestFindUmbrellaMaskedByDeepStrayHV(t *testing.T) {
	root := umbrella(t, `{"repos": [{"name": "web", "path": "./web"}]}`)
	deep := filepath.Join(root, "web", "src")
	os.MkdirAll(filepath.Join(deep, ".rota"), 0o755)
	_, err := FindUmbrella(deep)
	var m *MaskedError
	if !errors.As(err, &m) || m.Name != "web" || m.Stray != filepath.Join(deep, ".rota") {
		t.Errorf("deep stray: %v", err)
	}
}

func TestFindUmbrellaNone(t *testing.T) {
	if _, err := FindUmbrella(t.TempDir()); !errors.Is(err, ErrNoUmbrella) {
		t.Errorf("%v", err)
	}
}

func TestFindUmbrellaMaskAlsoWithoutNameAndAbsolutePath(t *testing.T) {
	root := umbrella(t, "", "web")
	os.WriteFile(filepath.Join(root, ".rota", "repos.json"), []byte(`{"repos": [{"path": "`+filepath.Join(root, "web")+`"}]}`), 0o644)
	os.MkdirAll(filepath.Join(root, "web", ".rota"), 0o755)
	var m *MaskedError
	if _, err := FindUmbrella(filepath.Join(root, "web")); !errors.As(err, &m) {
		t.Errorf("%v", err)
	}
}

func TestWithin(t *testing.T) {
	for _, c := range []struct {
		path, dir string
		want      bool
	}{{"/a/b", "/a/b", true}, {"/a/b/c", "/a/b", true}, {"/a/bc", "/a/b", false}, {"/a", "/a/b", false}, {"/x", "/", true}} {
		if got := within(c.path, c.dir); got != c.want {
			t.Errorf("within(%s,%s) = %v", c.path, c.dir, got)
		}
	}
}
