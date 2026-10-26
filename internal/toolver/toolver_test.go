package toolver

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGodot(t *testing.T) {
	root := t.TempDir()
	write(t, root, "project.godot", "; Engine configuration file.\nconfig_version=5\n\n[application]\nconfig/name=\"G\"\nconfig/features=PackedStringArray(\"4.6\", \"Forward Plus\")\n")
	if got := Detect(root, root)["godot"]; got != "4.6" {
		t.Errorf("godot = %q", got)
	}

	// Walks up from a subdirectory to the root.
	sub := filepath.Join(root, "scripts", "ai")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Detect(sub, root)["godot"]; got != "4.6" {
		t.Errorf("from sub: godot = %q", got)
	}

	// No manifest.
	if got := Detect(t.TempDir(), ""); len(got) != 0 {
		t.Errorf("empty dir = %v", got)
	}

	// Garbled lines yield no entry and no panic.
	bad := t.TempDir()
	write(t, bad, "project.godot", "config/features=PackedStringArray(\nconfig/features=oops\n")
	if got := Detect(bad, bad); len(got) != 0 {
		t.Errorf("garbled = %v", got)
	}
	// A missing dir is an empty map.
	if got := Detect(filepath.Join(bad, "nope"), bad); len(got) != 0 {
		t.Errorf("missing dir = %v", got)
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		seen, project string
		want          bool
	}{
		{"4.6", "4.8", true},
		{"4.6", "4.6.2", false},
		{"4.8", "4.6", false},
		{"4", "4.2", false},
		{"4", "5", true},
		{"1.35.3", "1.35.9", false},
		{"1.35.3", "1.36", true},
		{"r186", "r190", true},
		{"junk", "4.8", false},
		{"4.6", "", false},
	} {
		if got := Newer(c.seen, c.project); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.seen, c.project, got, c.want)
		}
	}
}
