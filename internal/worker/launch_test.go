package worker

import (
	"os"
	"path/filepath"
	"testing"
)

func launchRoot(t *testing.T, cfg string) string {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".rota"), 0o755)
	if cfg != "" {
		os.WriteFile(filepath.Join(root, ".rota", "config.json"), []byte(cfg), 0o644)
	}
	return root
}
