package repos

import (
	"errors"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/rotatree"
	"os"
	"path/filepath"
	"testing"
)

func umbrella(t *testing.T, reg string, subs ...string) string {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	os.MkdirAll(filepath.Join(root, ".rota"), 0o755)
	os.WriteFile(filepath.Join(root, ".rota", "repos.json"), []byte(reg), 0o644)
	for _, s := range subs {
		os.MkdirAll(filepath.Join(root, s), 0o755)
		gittest.Init(t, filepath.Join(root, s), "main")
		gittest.Run(t, filepath.Join(root, s), "commit", "-q", "--allow-empty", "-m", "init")
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

func TestWhichUnregistered(t *testing.T) {
	root := umbrella(t, `{"repos": [{"name": "web", "path": "web"}]}`, "web")
	// the umbrella root itself is no git repo here
	if _, err := Which(git.Exec, root); !errors.Is(err, ErrNotGit) {
		t.Errorf("umbrella root: %v", err)
	}
	gittest.Init(t, root, "main")
	if _, err := Which(git.Exec, root); !errors.Is(err, ErrNotRegistered) {
		t.Errorf("umbrella root as repo: %v", err)
	}
}

func TestWhichMaskedByStrayHV(t *testing.T) {
	root := umbrella(t, `{"repos": [{"name": "web", "path": "web"}]}`, "web")
	os.MkdirAll(filepath.Join(root, "web", ".rota"), 0o755)
	_, err := Which(git.Exec, filepath.Join(root, "web"))
	var m *rotatree.MaskedError
	if !errors.As(err, &m) || m.Name != "web" || m.Stray != filepath.Join(root, "web", ".rota") {
		t.Errorf("masked: %v", err)
	}
	// a stray .rota/ in a directory the registry does not list is no mask
	os.MkdirAll(filepath.Join(root, "other", ".rota"), 0o755)
	if got, err := FindRoot(filepath.Join(root, "other")); err != nil || got != filepath.Join(root, "other") {
		t.Errorf("unregistered stray: %q %v", got, err)
	}
}

// A stray .rota/ deep inside a registered sub-repo's source tree masks the
// umbrella just like one at the sub-repo root.
func TestFindRootMaskedByDeepStrayHV(t *testing.T) {
	root := umbrella(t, `{"repos": [{"name": "web", "path": "./web"}]}`)
	deep := filepath.Join(root, "web", "src")
	os.MkdirAll(filepath.Join(deep, ".rota"), 0o755)
	_, err := FindRoot(deep)
	var m *rotatree.MaskedError
	if !errors.As(err, &m) || m.Name != "web" || m.Stray != filepath.Join(deep, ".rota") {
		t.Errorf("deep stray: %v", err)
	}
}

func TestFindRootNone(t *testing.T) {
	if _, err := FindRoot(t.TempDir()); !errors.Is(err, rotatree.ErrNoRoot) {
		t.Errorf("%v", err)
	}
}

func TestFindRootMaskAlsoWithoutNameAndAbsolutePath(t *testing.T) {
	root := umbrella(t, "", "web")
	os.WriteFile(filepath.Join(root, ".rota", "repos.json"), []byte(`{"repos": [{"path": "`+filepath.Join(root, "web")+`"}]}`), 0o644)
	os.MkdirAll(filepath.Join(root, "web", ".rota"), 0o755)
	var m *rotatree.MaskedError
	if _, err := FindRoot(filepath.Join(root, "web")); !errors.As(err, &m) {
		t.Errorf("%v", err)
	}
}

func TestVerdictRepo(t *testing.T) {
	um := umbrella(t, `{"repos": [{"name": "web", "path": "web"}]}`, "web")
	web := filepath.Join(um, "web")
	plain, _ := filepath.EvalSymlinks(t.TempDir())
	os.MkdirAll(filepath.Join(plain, ".rota"), 0o755)
	gittest.Init(t, plain, "main")
	gittest.Run(t, plain, "commit", "-q", "--allow-empty", "-m", "init")
	cases := []struct{ name, root, dir, flag, want string }{
		{"flag overrides umbrella", um, web, "api", "api"},
		{"umbrella dir outside every sub-repo", um, um, "", ""},
		{"plain project", plain, plain, "", ""},
		{"plain project with flag", plain, plain, "api", "api"},
	}
	for _, c := range cases {
		if got := VerdictRepo(git.Exec, c.root, c.dir, c.flag); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
