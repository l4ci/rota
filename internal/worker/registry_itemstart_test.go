package worker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClearItemStartWithoutAnEntryWritesNothing(t *testing.T) {
	root := t.TempDir()
	if err := ClearItemStart(root, "12"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".rota", "workers.json")); !os.IsNotExist(err) {
		t.Fatalf("workers.json must not be created: %v", err)
	}
}
