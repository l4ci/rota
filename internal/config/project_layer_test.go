package config

import (
	"os"
	"testing"

	"github.com/l4ci/rota/internal/rotatree"
)

// Project is the tracked layer alone; Load adds the local overlay, both found
// from the project root.
func TestProjectIsTrackedLayerOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(rotatree.Dir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, doc string) {
		if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(rotatree.Config(root), `{"docs":{"path":"site"}}`)
	write(rotatree.ConfigLocal(root), `{"docs":{"path":"mine"}}`)
	if v, _ := Lookup(Project(root), "docs.path"); v != "site" {
		t.Errorf("Project docs.path = %v, want site (no local overlay)", v)
	}
	if v, _ := Lookup(Load(dirEnv(t, t.TempDir()), root), "docs.path"); v != "mine" {
		t.Errorf("Load docs.path = %v, want mine (local overlay)", v)
	}
	if got := Project(t.TempDir()); got == nil {
		t.Error("Project of a missing config is nil, want {}")
	}
}
