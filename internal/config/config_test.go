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

// Each testdata/<case>/ directory is a fixture: Go's Load must produce the
// JSON that the retired Python load_config recorded for it in testdata/golden.
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
		root, _ := fixtureRoot(t, "divergent/nan")
		// Go: not valid JSON, so the file counts as absent.
		if got, _ := jsonx.MarshalCompact(Load(cfgdir.OSEnv(), root)); string(got) != "{}" {
			t.Fatalf("Go Load = %s, want {}", got)
		}
		// Python: json.loads accepts NaN and Infinity.
	})
	t.Run("invalid UTF-8", func(t *testing.T) {
		root, _ := fixtureRoot(t, "divergent/badutf8")
		// Go: loads, with U+FFFD for the bad byte.
		v, ok := Lookup(Load(cfgdir.OSEnv(), root), "round.dispatch")
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

func TestWalkKey(t *testing.T) {
	cfg, _ := jsonx.Decode([]byte(`{"work": {"mergeStrategy": "pr", "nil": null}, "x": 1, "n": null}`))
	cases := []struct {
		key                         string
		want                        any
		wantPresent, wantNullAbsent bool
	}{
		{"work.mergeStrategy", "pr", true, true},
		{"work.missing", nil, false, false},
		{"nope", nil, false, false},
		{"x.y", nil, false, false},
		{"work.nil", nil, true, false},
		{"n", nil, true, false},
		{"n.deeper", nil, false, false},
	}
	for _, c := range cases {
		v, ok := walkKey(cfg, c.key, nullPresent)
		if ok != c.wantPresent || v != c.want {
			t.Errorf("nullPresent %q = %v,%v want %v,%v", c.key, v, ok, c.want, c.wantPresent)
		}
		if lv, lok := Lookup(cfg, c.key); lok != ok || lv != v {
			t.Errorf("Lookup %q diverges from nullPresent", c.key)
		}
		v, ok = walkKey(cfg, c.key, nullAbsent)
		if ok != c.wantNullAbsent || v != c.want {
			t.Errorf("nullAbsent %q = %v,%v want %v,%v", c.key, v, ok, c.want, c.wantNullAbsent)
		}
		if wv, wok := walk(cfg, c.key); wok != ok || wv != v {
			t.Errorf("walk %q diverges from nullAbsent", c.key)
		}
	}
}
