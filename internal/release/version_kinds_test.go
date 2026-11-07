package release

import (
	"os"
	"path/filepath"
	"testing"
)

// seed is a minimal version file per registered kind, at version 1.2.3.
var seed = map[string]struct{ rel, text string }{
	"plugin-json":  {".claude-plugin/plugin.json", "{\"name\": \"x\", \"version\": \"1.2.3\"}\n"},
	"package-json": {"package.json", "{\"name\": \"x\", \"version\": \"1.2.3\"}\n"},
	"pyproject":    {"pyproject.toml", "[project]\nname = \"x\"\nversion = \"1.2.3\"\n"},
	"cargo":        {"Cargo.toml", "[package]\nname = \"x\"\nversion = \"1.2.3\"\n"},
	"plain":        {"VERSION", "1.2.3\n"},
}

func TestRegistryKindsRoundTrip(t *testing.T) {
	for _, k := range Registry {
		t.Run(k.Name, func(t *testing.T) {
			s, ok := seed[k.Name]
			if !ok {
				t.Fatalf("no seed for registered kind %q: add one to this table", k.Name)
			}
			if k.Match == nil || k.Read == nil || k.Write == nil {
				t.Fatalf("kind %q lacks Match, Read or Write", k.Name)
			}
			dir := t.TempDir()
			path := filepath.Join(dir, s.rel)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(s.text), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := KindFromName(s.rel); got != k.Name {
				t.Errorf("KindFromName(%q) = %q, want %q", s.rel, got, k.Name)
			}
			if v, err := ReadVersion(dir, s.rel, k.Name); err != nil || v != "1.2.3" {
				t.Fatalf("read = %q, %v", v, err)
			}
			if err := WriteVersion(dir, s.rel, k.Name, "2.0.0"); err != nil {
				t.Fatal(err)
			}
			if v, err := ReadVersion(dir, s.rel, k.Name); err != nil || v != "2.0.0" {
				t.Fatalf("read after write = %q, %v", v, err)
			}
		})
	}
}

func TestBumpGrammarChecked(t *testing.T) {
	dir := t.TempDir()
	s := seed["plain"]
	os.WriteFile(filepath.Join(dir, s.rel), []byte(s.text), 0o644)
	for _, bad := range []string{"", "1.2", "latest", "v1.2.3"} {
		if _, err := Bump(dir, s.rel, "plain", bad, true); err == nil {
			t.Errorf("Bump(%q) accepted", bad)
		}
		if _, err := NextVersion("1.2.3", bad); err == nil {
			t.Errorf("NextVersion(%q) accepted", bad)
		}
	}
	if v, err := Bump(dir, s.rel, "plain", "patch", true); err != nil || v != "1.2.4" {
		t.Errorf("patch = %q, %v", v, err)
	}
}
