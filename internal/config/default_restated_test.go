package config

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
)

// keys.go owns the defaults of the tracker labels.
// round and tracker read them from the loaded config; a copy of one in their
// production code would drift when keys.go changes.
//
// issues.labels.inProgress is not guarded: round and tracker also write
// "in-progress", "changes-requested", "blocked" and "released" as backlog
// states and release markers, different things that happen to share the
// spelling. A future FirstNonEmpty(e.Label, "in-progress") would pass.
// Concatenated literals ("not" + "-planned") are not caught either.
func TestNoLabelOrRetryDefaultRestatedOutsideConfig(t *testing.T) {
	banned := restatedLabelDefaults()
	for _, pkg := range []string{"round", "tracker"} {
		dir := filepath.Join("..", pkg)
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no sources under %s: %v", dir, err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for _, msg := range restatedDefaults(t, f, src, banned) {
				t.Error(msg)
			}
		}
	}
}

func restatedLabelDefaults() map[string]string {
	banned := map[string]string{}
	for _, role := range []string{"needsReview", "notPlanned", "needsHuman"} {
		banned[Label(nil, role)] = "issues.labels." + role
	}
	return banned
}

// restatedDefaults lists each place src spells a banned label.
func restatedDefaults(t *testing.T, name string, src []byte, banned map[string]string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BasicLit:
			if x.Kind != token.STRING {
				break
			}
			// Unquote so a raw string matches the interpreted default.
			if val, err := strconv.Unquote(x.Value); err == nil {
				if key, ok := banned[val]; ok {
					out = append(out, fmt.Sprintf("%s: %s restates the default of %s; read it from the loaded config", fset.Position(x.Pos()), x.Value, key))
				}
			}
		}
		return true
	})
	return out
}

func TestRestatedDefaultsSpellings(t *testing.T) {
	banned := restatedLabelDefaults()
	for name, line := range map[string]string{
		"interpreted": `var _ = "needs-human"`,
		"raw string":  "var _ = `needs-human`",
	} {
		got := restatedDefaults(t, "x.go", []byte("package x\n"+line+"\n"), banned)
		if len(got) != 1 {
			t.Errorf("%s: want 1 finding, got %v", name, got)
		}
	}
	for name, line := range map[string]string{
		"other label": `var _ = "in-progress"`,
		"60 seconds":  `var _ = 60 * time.Second`, // issues.retryWaitSeconds became a constant (#1825)
	} {
		if got := restatedDefaults(t, "x.go", []byte("package x\n"+line+"\n"), banned); len(got) != 0 {
			t.Errorf("%s: want no finding, got %v", name, got)
		}
	}
}

func TestLabelExplicitEmptyDoesNotFallBack(t *testing.T) {
	cfg, err := jsonx.Decode([]byte(`{"issues":{"labels":{"needsHuman":"","notPlanned":7}}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"needsHuman", "notPlanned"} {
		if got := Label(cfg, role); got != "" {
			t.Errorf("Label(%s) = %q, want \"\" (no fallback to %q)", role, got, Label(nil, role))
		}
	}
	if Label(nil, "needsHuman") == "" {
		t.Error("unset needsHuman must still read its default")
	}
}
