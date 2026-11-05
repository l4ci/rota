package config

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
)

func TestSetSchemaTypesAcrossLayers(t *testing.T) {
	for _, layer := range []string{"project", "local", "global"} {
		t.Run(layer, func(t *testing.T) {
			root, dir := t.TempDir(), t.TempDir()
			set := func(raw string) (SetResult, error) {
				if layer == "global" {
					return SetGlobal(dir, "test.full", raw)
				}
				where := LayerProject
				if layer == "local" {
					where = LayerLocal
				}
				return SetIn(root, "test.full", raw, where)
			}
			if _, err := set("go test ./..."); err == nil || !strings.Contains(err.Error(), "expected list") {
				t.Fatalf("bare string: %v", err)
			}
			if _, err := set(`["go test ./..."]`); err != nil {
				t.Fatal(err)
			}
			path := configPath(root)
			if layer == "local" {
				path = localPath(root)
			} else if layer == "global" {
				path = GlobalPath(dir)
			}
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			doc, err := jsonx.Decode(body)
			value, present := Lookup(doc, "test.full")
			if err != nil || !present || !reflect.DeepEqual(value, []any{"go test ./..."}) {
				t.Fatalf("saved value=%v, present=%v, err=%v", value, present, err)
			}
		})
	}
}

func TestTypeProblemsStoredLayers(t *testing.T) {
	root, env := layerRig(t,
		`{"test":{"full":42}}`,
		`{"test":{"full":"go test ./..."},"orchestrator":{"model":[]}}`,
		`{"test":{"full":[]},"docs":{"path":null},"ship":{"review":false}}`)
	before := read(t, root)
	problems := TypeProblems(env, root)
	if len(problems) != 3 {
		t.Fatalf("problems=%v, want three mismatches including shadowed values", problems)
	}
	for i, want := range []string{
		"global config: invalid value for test.full: expected list",
		".rota/config.json: invalid value for orchestrator.model: expected string",
		".rota/config.json: invalid value for test.full: expected list",
	} {
		if !strings.HasPrefix(problems[i], want) {
			t.Errorf("problem %d=%q, want prefix %q", i, problems[i], want)
		}
	}
	if got := read(t, root); got != before {
		t.Fatalf("diagnostics changed config: %s", got)
	}
}

func TestSchemaDefaultsAndReviewFormsKeepWorking(t *testing.T) {
	for _, key := range Keys {
		body, err := jsonx.MarshalCompact(Default(key))
		if err != nil {
			t.Fatal(err)
		}
		if err := Validate(key.Name, string(body)); err != nil {
			t.Errorf("default for %s refused: %v", key.Name, err)
		}
	}
	for _, raw := range []string{`true`, `false`, `"light"`, `{"default":"full"}`, `null`} {
		if _, err := Set(t.TempDir(), ReviewKey, raw); err != nil {
			t.Errorf("review %s: %v", raw, err)
		}
	}
}
