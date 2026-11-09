package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/cfgdir"
	"github.com/l4ci/rota/internal/golden"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotatree"
)

// Each testdata/<case>/ directory is a fixture: Go's Load must produce the
// JSON that the retired Python load_config recorded for it in testdata/golden.
// fixtureRoot lays the config files found in dir out as a project root and
// returns that root with the files' contents by name.
func fixtureRoot(t *testing.T, dir string) (string, map[string]string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(rotatree.Dir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, n := range []string{rotatree.ConfigFile, rotatree.ConfigLocalFile} {
		if raw, err := os.ReadFile(filepath.Join(dir, n)); err == nil {
			files[n] = string(raw)
			if err := os.WriteFile(rotatree.File(root, n), raw, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root, files
}

func TestLoadMatchesPython(t *testing.T) {
	cases, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if c.Name() == "golden" { // the recorded outputs, not a fixture
			continue
		}
		t.Run(c.Name(), func(t *testing.T) {
			root, files := fixtureRoot(t, filepath.Join("testdata", c.Name()))
			got, err := jsonx.Marshal(Load(cfgdir.OSEnv(), root))
			if err != nil {
				t.Fatal(err)
			}
			golden.Check(t, map[string]any{"files": files}, string(got))
		})
	}
}

// Divergences from Python, kept on purpose and listed in the conventions doc
// (Config). Python's side of each is noted in a comment.
func TestDivergentInputs(t *testing.T) {
	t.Run("NaN and Infinity", func(t *testing.T) {
		path, _ := fixtureRoot(t, "divergent/nan")
		// Go: not valid JSON, so the file counts as absent.
		if got, _ := jsonx.MarshalCompact(Load(cfgdir.OSEnv(), path)); string(got) != "{}" {
			t.Fatalf("Go Load = %s, want {}", got)
		}
		// Python: json.loads accepts NaN and Infinity.
	})
	t.Run("invalid UTF-8", func(t *testing.T) {
		path, _ := fixtureRoot(t, "divergent/badutf8")
		// Go: loads, with U+FFFD for the bad byte.
		v, ok := Lookup(Load(cfgdir.OSEnv(), path), "round.dispatch")
		if !ok || v != "tm\uFFFDux" {
			t.Fatalf("Go Load round.dispatch = %q", v)
		}
		// Python: read_text raises UnicodeDecodeError, which load_json does not catch.
	})
}

func TestMergeDoesNotMutateInputs(t *testing.T) {
	base, _ := jsonx.Decode([]byte(`{"a": {"b": 1}}`))
	over, _ := jsonx.Decode([]byte(`{"a": {"c": 2}}`))
	Merge(base, over)
	got, _ := jsonx.MarshalCompact(base)
	if string(got) != `{"a": {"b": 1}}` {
		t.Fatalf("base changed: %s", got)
	}
}

func TestLookup(t *testing.T) {
	cfg, _ := jsonx.Decode([]byte(`{"work": {"mergeStrategy": "pr"}, "x": 1}`))
	if v, ok := Lookup(cfg, "work.mergeStrategy"); !ok || v != "pr" {
		t.Fatalf("got %v %v", v, ok)
	}
	for _, k := range []string{"work.missing", "x.y", "nope"} {
		if _, ok := Lookup(cfg, k); ok {
			t.Errorf("Lookup(%q) should miss", k)
		}
	}
}
