package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestSupportLabelKeyRetired(t *testing.T) {
	for _, k := range Keys {
		if k.Name == "issues.labels.support" {
			t.Fatal("issues.labels.support is still a schema key")
		}
	}
	if !slices.Contains(RemovedKeys, "issues.labels.support") {
		t.Fatal("support label not reported removed")
	}
}
func TestFillKeepsSupportLabelKey(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".rota"), 0755)
	os.WriteFile(filepath.Join(root, ".rota/config.json"), []byte(`{"issues":{"labels":{"support":"remote"}}}`), 0644)
	if _, err := Fill(root); err != nil {
		t.Fatal(err)
	}
	v, ok := Lookup(Load(filepath.Join(root, ".rota/config.json")), "issues.labels.support")
	if !ok || v != "remote" {
		t.Fatalf("old label = %v, %v", v, ok)
	}
	if !slices.Contains(Removed(root), "issues.labels.support") {
		t.Fatal("removed report missing support label")
	}
}
