package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
)

func project(t *testing.T, cfg, local string) string {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".rota"), 0o755)
	if cfg != "" {
		os.WriteFile(filepath.Join(root, ".rota", "config.json"), []byte(cfg), 0o644)
	}
	if local != "" {
		os.WriteFile(filepath.Join(root, ".rota", "config.local.json"), []byte(local), 0o644)
	}
	return root
}

func read(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, ".rota", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestShowAllKeysInSchemaOrder(t *testing.T) {
	es, err := Show(project(t, "", ""), "", false)
	if err != nil || len(es) != len(Keys) {
		t.Fatalf("%d entries, %v", len(es), err)
	}
	for i, e := range es {
		if e.Key != Keys[i].Name || e.Source != "default" {
			t.Fatalf("entry %d = %+v", i, e)
		}
	}
}

func TestShowSources(t *testing.T) {
	root := project(t,
		`{"models": {"worker": "haiku", "orchestrator": null}, "work": {"isolation": "worktree"}}`,
		`{"work": {"isolation": "branch"}, "docs": {"path": "d"}}`)
	for key, want := range map[string]Entry{
		"work.isolation":      {"work.isolation", "branch", "local"},
		"models.worker":       {"models.worker", "haiku", "project"},
		"models.orchestrator": {"models.orchestrator", "opus", "default"}, // null counts as unset
		"docs.path":           {"docs.path", "d", "local"},
		"docs.afterWork":      {"docs.afterWork", false, "default"},
	} {
		es, err := Show(root, key, true)
		if err != nil || len(es) != 1 || !reflect.DeepEqual(es[0], want) {
			t.Errorf("%s: %+v, %v; want %+v", key, es, err, want)
		}
	}
}

func TestShowMatchesRuntime(t *testing.T) {
	// Populate every schema key so replacement cases cannot pass merely because
	// the project value already equals the default.
	base := map[string]any{}
	for _, k := range Keys {
		setPath(base, k.Name, "project")
	}
	encode := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	for _, tc := range []struct {
		name, project, local string
		source               string
	}{
		{"missing", "", "", "default"},
		{"project", encode(base), "", "project"},
		{"empty-merge", encode(base), `{}`, "project"},
		{"root-null-ignored", encode(base), `null`, "project"},
		{"corrupt-local-ignored", encode(base), `{`, "project"},
		{"root-scalar", encode(base), `false`, "default"},
		{"root-array", encode(base), `[]`, "default"},
		{"leaf-null", encode(base), "", "default"},
		{"parent-null", encode(base), "", "default"},
		{"parent-scalar", encode(base), "", "default"},
		{"parent-array", encode(base), "", "default"},
		{"local", encode(base), encode(base), "local"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			local := map[string]any{}
			for _, k := range Keys {
				switch tc.name {
				case "leaf-null":
					setPath(local, k.Name, nil)
				case "parent-null", "parent-scalar", "parent-array":
					parent := strings.Split(k.Name, ".")[0]
					var v any
					if tc.name == "parent-scalar" {
						v = false
					} else if tc.name == "parent-array" {
						v = []any{}
					}
					local[parent] = v
				}
			}
			if len(local) > 0 {
				tc.local = encode(local)
			}
			root := project(t, tc.project, tc.local)
			cfg := Load(configPath(root))
			rows, err := Show(root, "", false)
			if err != nil || len(rows) != len(Keys) {
				t.Fatalf("Show: %d rows, %v", len(rows), err)
			}
			for _, row := range rows {
				want, err := Value(cfg, row.Key)
				if err != nil || !reflect.DeepEqual(row.Value, want) || row.Source != tc.source {
					t.Errorf("%s: show=%v (%s), runtime=%v; want source %s, err=%v", row.Key, row.Value, row.Source, want, tc.source, err)
				}
				one, err := Show(root, row.Key, true)
				if err != nil || len(one) != 1 || !reflect.DeepEqual(one[0], row) {
					t.Errorf("single-key Show disagrees: %v, %v", one, err)
				}
			}
		})
	}
}

func TestShowDeepMergeSources(t *testing.T) {
	root := project(t,
		`{"ship":{"qa":true,"review":false},"issues":{"labels":{"types":{"bug":"defect","task":"chore"}}}}`,
		`{"ship":{"qa":null},"issues":{"labels":{"types":{"bug":"bug"}}}}`)
	for key, want := range map[string]Entry{
		"ship.qa":                  {"ship.qa", false, "default"},
		"ship.review":              {"ship.review", false, "project"},
		"issues.labels.types.bug":  {"issues.labels.types.bug", "bug", "local"},
		"issues.labels.types.task": {"issues.labels.types.task", "chore", "project"},
	} {
		rows, err := Show(root, key, true)
		if err != nil || len(rows) != 1 || !reflect.DeepEqual(rows[0], want) {
			t.Errorf("%s: %v, %v; want %v", key, rows, err, want)
		}
	}
}

func TestShowUnknownAndHandEditedKeys(t *testing.T) {
	root := project(t, `{"custom": {"x": 1}, "nul": null}`, `{"custom": {"y": 2}}`)
	if _, err := Show(root, "nope", true); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("unknown key: %v", err)
	}
	if _, err := Show(root, "nul", true); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("null key: %v", err)
	}
	es, err := Show(root, "custom.x", true)
	if err != nil || es[0].Source != "project" || es[0].Value.(interface{ String() string }).String() != "1" {
		t.Errorf("custom.x: %+v %v", es, err)
	}
	es, _ = Show(root, "custom.y", true)
	if len(es) != 1 || es[0].Source != "local" {
		t.Errorf("custom.y: %+v", es)
	}
}

func TestShowLine(t *testing.T) {
	e := Entry{"work.accounts", []any{"a", "b"}, "default"}
	if got := e.Line(); got != `work.accounts = ["a", "b"]  (source: default)` {
		t.Errorf("Line = %s", got)
	}
}

func TestSetCoercions(t *testing.T) {
	for raw, want := range map[string]string{
		"true": "true", "42": "42", `"x"`: `"x"`, "[1, 2]": "[1, 2]", `{"a": 1}`: `{"a": 1}`,
		"opus": `"opus"`, "": `""`, "1.50": "1.5", "True": `"True"`, " 7 ": "7",
	} {
		root := project(t, "", "")
		res, err := Set(root, "models.worker", raw)
		if err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
		b, _ := jsonx.MarshalCompact(res.Value)
		if string(b) != want {
			t.Errorf("Set %q stored %s, want %s", raw, b, want)
		}
	}
}

func TestSetWritesCanonicalAndReportsPrevious(t *testing.T) {
	root := project(t, `{"models":{"worker":"sonnet","orchestrator":"opus"},"keep":[1]}`, `{"models":{"worker":"local"}}`)
	res, err := Set(root, "models.worker", "opus")
	if err != nil || !res.Changed || !res.HadPrevious || res.Previous != "sonnet" {
		t.Fatalf("%+v %v", res, err)
	}
	want := "{\n  \"models\": {\n    \"worker\": \"opus\",\n    \"orchestrator\": \"opus\"\n  },\n  \"keep\": [\n    1\n  ]\n}\n"
	if got := read(t, root); got != want {
		t.Errorf("file:\n%s", got)
	}
	if b, _ := os.ReadFile(filepath.Join(root, ".rota", "config.local.json")); string(b) != `{"models":{"worker":"local"}}` {
		t.Errorf("config.local.json was touched: %s", b)
	}
	res, err = Set(root, "models.worker", "opus")
	if err != nil || res.Changed || res.Previous != "opus" {
		t.Errorf("idempotent set: %+v %v", res, err)
	}
}

func TestSetCreatesAndReplacesParents(t *testing.T) {
	root := project(t, `{"work": "scalar"}`, "")
	res, err := Set(root, "work.dispatch", "tmux")
	if err != nil || !res.Changed || res.HadPrevious {
		t.Fatalf("%+v %v", res, err)
	}
	if got := read(t, root); got != "{\n  \"work\": {\n    \"dispatch\": \"tmux\"\n  }\n}\n" {
		t.Errorf("file:\n%s", got)
	}
	root = project(t, "", "")
	if _, err := Set(root, "issues.labels.types.bug", "x"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(Load(filepath.Join(root, ".rota", "config.json")).(*jsonx.Object).Keys(), []string{"issues"}) {
		t.Error("no nested object")
	}
}

func TestSetNullPreviousIsPresent(t *testing.T) {
	root := project(t, `{"docs": {"path": null}}`, "")
	res, err := Set(root, "docs.path", "x")
	if err != nil || !res.HadPrevious || res.Previous != nil {
		t.Errorf("%+v %v", res, err)
	}
}

func TestSetRefusals(t *testing.T) {
	root := project(t, "", "")
	for _, key := range []string{"", ".a", "a.", "a..b"} {
		if _, err := Set(root, key, "1"); !errors.Is(err, ErrMalformedKey) {
			t.Errorf("%q: %v", key, err)
		}
	}
	for _, key := range []string{"nope", "models", "models.worker.extra"} {
		if _, err := Set(root, key, "1"); !errors.Is(err, ErrNotSchemaKey) {
			t.Errorf("%q: %v", key, err)
		}
	}
	for _, body := range []string{"[1]", "null", `"s"`, "7"} {
		root = project(t, body, "")
		if _, err := Set(root, "models.worker", "x"); !errors.Is(err, ErrNotObject) {
			t.Errorf("%s: %v", body, err)
		}
		if read(t, root) != body {
			t.Errorf("%s: file changed", body)
		}
	}
}

func TestSetCorruptOrEmptyFileCountsAsEmpty(t *testing.T) {
	for _, body := range []string{"{oops", ""} {
		root := project(t, body, "")
		if body == "" {
			os.WriteFile(filepath.Join(root, ".rota", "config.json"), nil, 0o644)
		}
		if _, err := Set(root, "docs.path", "x"); err != nil {
			t.Fatal(err)
		}
		if got := read(t, root); got != "{\n  \"docs\": {\n    \"path\": \"x\"\n  }\n}\n" {
			t.Errorf("%q: %s", body, got)
		}
	}
}

func TestSetCreatesMissingFile(t *testing.T) {
	root := project(t, "", "")
	res, err := Set(root, "ship.qa", "true")
	if err != nil || !res.Changed {
		t.Fatalf("%+v %v", res, err)
	}
	if got := read(t, root); got != "{\n  \"ship\": {\n    \"qa\": true\n  }\n}\n" {
		t.Errorf("file:\n%s", got)
	}
}

func fullConfig() *jsonx.Object {
	root := jsonx.NewObject()
	for _, k := range Keys {
		if !k.Required {
			continue
		}
		cur := root
		segs := splitDots(k.Name)
		for _, s := range segs[:len(segs)-1] {
			next, ok := getObject(cur, s)
			if !ok {
				next = jsonx.NewObject()
				cur.Set(s, next)
			}
			cur = next
		}
		cur.Set(segs[len(segs)-1], k.Default)
	}
	return root
}

func splitDots(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '.' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return out
}

func TestCheckVerdicts(t *testing.T) {
	root := project(t, "", "")
	if st, m := Check(root); st != Fresh || len(m) != 0 || m == nil {
		t.Errorf("fresh: %s %v", st, m)
	}
	for _, body := range []string{"{oops", "[1]", "null", "", "\xff{}", `{"a": NaN}`} {
		os.WriteFile(filepath.Join(root, ".rota", "config.json"), []byte(body), 0o644)
		if st, _ := Check(root); st != Corrupt {
			t.Errorf("%q: %s", body, st)
		}
	}
	os.WriteFile(filepath.Join(root, ".rota", "config.json"), []byte("{}"), 0o644)
	st, m := Check(root)
	if st != Stale || len(m) == 0 || m[0] != "models.orchestrator" {
		t.Errorf("stale: %s %v", st, m)
	}
	cfg := fullConfig()
	b, _ := jsonx.Marshal(cfg)
	os.WriteFile(filepath.Join(root, ".rota", "config.json"), b, 0o644)
	if st, m := Check(root); st != UpToDate || len(m) != 0 {
		t.Errorf("up to date: %s %v", st, m)
	}
	// null and a scalar parent both count as missing, in schema order
	o, _ := getObject(cfg, "umbrella")
	o.Set("enabled", nil)
	cfg.Set("rota", "scalar")
	b, _ = jsonx.Marshal(cfg)
	os.WriteFile(filepath.Join(root, ".rota", "config.json"), b, 0o644)
	if st, m := Check(root); st != Stale || !reflect.DeepEqual(m, []string{"umbrella.enabled", "rota.version"}) {
		t.Errorf("null/scalar: %s %v", st, m)
	}
}

func TestCheckIgnoresLocalFile(t *testing.T) {
	root := project(t, "{}", `{"models": {"orchestrator": "x"}}`)
	if st, m := Check(root); st != Stale || m[0] != "models.orchestrator" {
		t.Errorf("%s %v", st, m)
	}
}

func TestRetired(t *testing.T) {
	for _, cfg := range []string{"", "{}", `{"autonomy": {"level": "auto"}}`, `{"autonomy": {"level": "off"}}`} {
		if got := Retired(project(t, cfg, "")); len(got) != 0 || got == nil {
			t.Errorf("%q: %v", cfg, got)
		}
	}
	got := Retired(project(t, `{"autonomy": {"level": "loop"}}`, ""))
	if len(got) != 1 || !strings.Contains(got[0], `autonomy.level "loop" was removed`) {
		t.Errorf("loop: %v", got)
	}
}
