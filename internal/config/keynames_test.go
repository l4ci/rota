package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #4047: cli and initproj reach these keys through config's names, never a
// string literal, so a schema rename is a compile error there.
func TestKeyConstantsAreSchemaKeys(t *testing.T) {
	for _, name := range []string{IssuesSourceKey, UmbrellaEnabledKey} {
		if _, ok := SchemaKey(name); !ok {
			t.Errorf("%s: not a schema key", name)
		}
	}
}

func TestCallersUseKeyConstants(t *testing.T) {
	files := []string{
		"../cli/init.go",
		"../cli/init_umbrella.go",
		"../initproj/check.go",
	}
	for _, f := range files {
		b, err := os.ReadFile(filepath.FromSlash(f))
		if err != nil {
			t.Fatal(err)
		}
		for _, lit := range []string{`"issues.source"`, `"umbrella.enabled"`} {
			if strings.Contains(string(b), lit) {
				t.Errorf("%s spells %s as a literal; use the config key constant", f, lit)
			}
		}
	}
}
