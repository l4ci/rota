package usagelimit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSameConfigDirIn(t *testing.T) {
	home, _ := os.UserHomeDir()
	if !SameConfigDirIn("", filepath.Join(home, ".claude"), home) || !SameConfigDirIn("~/.x/", filepath.Join(home, ".x"), home) || SameConfigDirIn("/a", "/b", home) {
		t.Error("SameConfigDir")
	}
}
