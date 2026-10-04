package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/pytest"
)

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
			path, _ := filepath.Abs(filepath.Join("testdata", c.Name(), "config.json"))
			got, err := jsonx.Marshal(Load(path))
			if err != nil {
				t.Fatal(err)
			}
			files := map[string]string{}
			for _, n := range []string{"config.json", "config.local.json"} {
				if raw, err := os.ReadFile(filepath.Join("testdata", c.Name(), n)); err == nil {
					files[n] = string(raw)
				}
			}
			var want string
			pytest.Golden(t, map[string]any{"files": files}, &want)
			if string(got) != want {
				t.Fatalf("\n--- go\n%s\n--- golden\n%s", got, want)
			}
		})
	}
}

// Divergences from Python, kept on purpose and listed in the conventions doc
// (Config). Python's side of each is noted in a comment.
func TestDivergentInputs(t *testing.T) {
	t.Run("NaN and Infinity", func(t *testing.T) {
		path, _ := filepath.Abs("divergent/nan/config.json")
		// Go: not valid JSON, so the file counts as absent.
		if got, _ := jsonx.MarshalCompact(Load(path)); string(got) != "{}" {
			t.Fatalf("Go Load = %s, want {}", got)
		}
		// Python: json.loads accepts NaN and Infinity.
	})
	t.Run("invalid UTF-8", func(t *testing.T) {
		path, _ := filepath.Abs("divergent/badutf8/config.json")
		// Go: loads, with U+FFFD for the bad byte.
		v, ok := Lookup(Load(path), "work.dispatch")
		if !ok || v != "tm\uFFFDux" {
			t.Fatalf("Go Load work.dispatch = %q", v)
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
