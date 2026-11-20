package skills

import (
	"os"
	"path/filepath"
	"testing"
)

// corruptRoot installs mode into a fresh root and overwrites its manifest with
// body, returning the root and the bytes of every file under it.
func corruptRoot(t *testing.T, mode, body string) (Root, *Set, map[string]string) {
	t.Helper()
	s := loadFake(t, map[string]string{"rota-a/SKILL.md": "---\nname: rota-a\n---\n# a\n", "rota-b/SKILL.md": "---\nname: rota-b\n---\n# b\n"})
	r := oneRoot(t)[0]
	if _, err := s.Install([]Root{r}, Options{Mode: mode}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Path, ManifestName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return r, s, snapshot(t, r.Path)
}

func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() && filepath.Ext(p) != ".lock" {
			b, _ := os.ReadFile(p)
			out[p] = string(b)
		}
		return nil
	})
	return out
}

func sameTree(t *testing.T, root string, before map[string]string) {
	t.Helper()
	after := snapshot(t, root)
	if len(after) != len(before) {
		t.Fatalf("file set changed: %d -> %d", len(before), len(after))
	}
	for p, b := range before {
		if after[p] != b {
			t.Errorf("%s changed", p)
		}
	}
}

func TestCorruptManifestRefusedByUninstallUpdateStatus(t *testing.T) {
	for _, body := range []string{"{", "", `{"schema":99,"files":{}}`} {
		r, s, before := corruptRoot(t, ModeStub, body)
		for _, o := range []Options{{}, {Mode: ModeFull}} {
			if _, err := s.Update([]Root{r}, o); err == nil {
				t.Errorf("update %q mode %q: no error", body, o.Mode)
			}
			sameTree(t, r.Path, before)
		}
		if _, err := Uninstall([]Root{r}, Options{}); err == nil {
			t.Errorf("uninstall %q: no error", body)
		}
		sameTree(t, r.Path, before)
		if _, err := s.Status([]Root{r}, "1"); err == nil {
			t.Errorf("status %q: no error", body)
		}
		sameTree(t, r.Path, before)
	}
}

func TestMissingManifestStillSkipped(t *testing.T) {
	s := loadFake(t, nil)
	r := oneRoot(t)[0]
	if res, err := Uninstall([]Root{r}, Options{}); err != nil || res[0].Installed {
		t.Fatalf("%+v %v", res, err)
	}
	if res, err := s.Update([]Root{r}, Options{}); err != nil || len(res) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if rep, err := s.Status([]Root{r}, "1"); err != nil || rep.Roots[0].Installed {
		t.Fatalf("%+v %v", rep, err)
	}
}
