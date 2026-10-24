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
		`{"orchestrator": {"model": "haiku", "harness": null}, "work": {"isolation": "worktree"}}`,
		`{"work": {"isolation": "branch"}, "docs": {"path": "d"}}`)
	for key, want := range map[string]Entry{
		"work.isolation":       {"work.isolation", "branch", "local", nil, nil, false},
		"orchestrator.model":   {"orchestrator.model", "haiku", "project", nil, nil, false},
		"orchestrator.harness": {"orchestrator.harness", "claude", "default", nil, nil, false}, // null counts as unset
		"docs.path":            {"docs.path", "d", "local", nil, nil, false},
		"docs.afterWork":       {"docs.afterWork", false, "default", nil, nil, false},
	} {
		es, err := Show(root, key, true)
		if err == nil && len(es) == 1 {
			if es[0].Schema == nil || es[0].Schema.Name != key {
				t.Errorf("%s: schema row %+v", key, es[0].Schema)
			}
			es[0].Schema = nil
		}
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
		"ship.qa":                  {"ship.qa", false, "default", nil, nil, false},
		"ship.review":              {"ship.review", false, "project", nil, nil, false},
		"issues.labels.types.bug":  {"issues.labels.types.bug", "bug", "local", nil, nil, false},
		"issues.labels.types.task": {"issues.labels.types.task", "chore", "project", nil, nil, false},
	} {
		rows, err := Show(root, key, true)
		if err == nil && len(rows) == 1 {
			rows[0].Schema = nil
		}
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
	e := Entry{Key: "round.accounts", Value: []any{"a", "b"}, Source: "default"}
	if got := e.Line(); got != `round.accounts = ["a", "b"]  (source: default)` {
		t.Errorf("Line = %s", got)
	}
}

func TestSetCoercions(t *testing.T) {
	for raw, want := range map[string]string{
		"true": "true", "42": "42", `"x"`: `"x"`, "[1, 2]": "[1, 2]", `{"a": 1}`: `{"a": 1}`,
		"opus": `"opus"`, "": `""`, "1.50": "1.5", "True": `"True"`, " 7 ": "7",
	} {
		root := project(t, "", "")
		res, err := Set(root, "orchestrator.model", raw)
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
	root := project(t, `{"orchestrator":{"model":"sonnet","harness":"codex"},"keep":[1]}`, `{"orchestrator":{"model":"local"}}`)
	res, err := Set(root, "orchestrator.model", "opus")
	if err != nil || !res.Changed || !res.HadPrevious || res.Previous != "sonnet" {
		t.Fatalf("%+v %v", res, err)
	}
	want := "{\n  \"orchestrator\": {\n    \"model\": \"opus\",\n    \"harness\": \"codex\"\n  },\n  \"keep\": [\n    1\n  ]\n}\n"
	if got := read(t, root); got != want {
		t.Errorf("file:\n%s", got)
	}
	if b, _ := os.ReadFile(filepath.Join(root, ".rota", "config.local.json")); string(b) != `{"orchestrator":{"model":"local"}}` {
		t.Errorf("config.local.json was touched: %s", b)
	}
	res, err = Set(root, "orchestrator.model", "opus")
	if err != nil || res.Changed || res.Previous != "opus" {
		t.Errorf("idempotent set: %+v %v", res, err)
	}
}

func TestSetCreatesAndReplacesParents(t *testing.T) {
	root := project(t, `{"round": "scalar"}`, "")
	res, err := Set(root, "round.dispatch", "tmux")
	if err != nil || !res.Changed || res.HadPrevious {
		t.Fatalf("%+v %v", res, err)
	}
	if got := read(t, root); got != "{\n  \"round\": {\n    \"dispatch\": \"tmux\"\n  }\n}\n" {
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
	for _, key := range []string{"nope", "gate", "orchestrator.model.extra"} {
		if _, err := Set(root, key, "1"); !errors.Is(err, ErrNotSchemaKey) {
			t.Errorf("%q: %v", key, err)
		}
	}
	for _, body := range []string{"[1]", "null", `"s"`, "7"} {
		root = project(t, body, "")
		if _, err := Set(root, "orchestrator.model", "x"); !errors.Is(err, ErrNotObject) {
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
	if st != Stale || len(m) == 0 || m[0] != "learn.promoteThreshold" {
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

func TestCheckCountsLocalFile(t *testing.T) {
	root := project(t, "{}", `{"learn": {"promoteThreshold": 1}}`)
	if st, m := Check(root); st != Stale || m[0] != "docs.path" {
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

func TestCheckFlagsLegacyVerifyCommands(t *testing.T) {
	root := project(t, "", "")
	write := func(cfg *jsonx.Object) {
		b, _ := jsonx.Marshal(cfg)
		os.WriteFile(filepath.Join(root, ".rota", "config.json"), b, 0o644)
	}
	legacy := func(cfg *jsonx.Object, cmds []any) {
		ref, _ := getObject(cfg, "refactor")
		if ref == nil {
			ref = jsonx.NewObject()
			cfg.Set("refactor", ref)
		}
		ref.Set("verifyCommands", cmds)
	}
	setFull := func(cfg *jsonx.Object, cmds []any) {
		test, _ := getObject(cfg, "test")
		test.Set("full", cmds)
	}

	// All required keys present, test.full empty, legacy key holds commands.
	cfg := fullConfig()
	legacy(cfg, []any{"make test"})
	write(cfg)
	st, m := Check(root)
	if st != Stale || !reflect.DeepEqual(m, []string{LegacyVerifyKey}) {
		t.Errorf("legacy only: %s %v", st, m)
	}
	// Fill moves it, so check is up to date afterwards.
	if _, err := Fill(root); err != nil {
		t.Fatal(err)
	}
	if st, m := Check(root); st != UpToDate || len(m) != 0 {
		t.Errorf("after fill: %s %v", st, m)
	}

	// A non-empty test.full makes the legacy key inert, an empty legacy list moves nothing.
	cfg = fullConfig()
	setFull(cfg, []any{"make test"})
	legacy(cfg, []any{"old"})
	write(cfg)
	if st, _ := Check(root); st != UpToDate {
		t.Errorf("test.full set: %s", st)
	}
	cfg = fullConfig()
	legacy(cfg, []any{})
	write(cfg)
	if st, _ := Check(root); st != UpToDate {
		t.Errorf("empty legacy list: %s", st)
	}
}

func TestSetVersionFileValidatesProjectPath(t *testing.T) {
	for _, bad := range []string{"/etc/VERSION", "../VERSION", "a/../../VERSION", "..", "3", "true"} {
		root := project(t, "", "")
		if _, err := Set(root, "release.versionFile", bad); !errors.Is(err, ErrBadValue) {
			t.Errorf("%q: err = %v, want ErrBadValue", bad, err)
		}
	}
	for _, good := range []string{"VERSION", "sub/package.json", `""`} {
		root := project(t, "", "")
		if _, err := Set(root, "release.versionFile", good); err != nil {
			t.Errorf("%q: %v", good, err)
		}
	}
}

func TestSetInLocalAndUnsetPruneEmptyParents(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := SetIn(root, "work.isolation", "worktree", LayerLocal); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, ".rota", "config.local.json")); !strings.Contains(string(b), `"isolation": "worktree"`) {
		t.Fatalf("local file: %s", b)
	}
	if _, err := os.Stat(filepath.Join(root, ".rota", "config.json")); err == nil {
		t.Errorf("a local write created config.json")
	}
	if ok, err := Unset(root, "work.isolation", LayerLocal); err != nil || !ok {
		t.Fatalf("unset = %v, %v", ok, err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, ".rota", "config.local.json")); strings.TrimSpace(string(b)) != "{}" {
		t.Errorf("empty parent kept: %s", b)
	}
	if ok, err := Unset(root, "work.isolation", LayerProject); err != nil || ok {
		t.Errorf("unset of an absent key = %v, %v", ok, err)
	}
	if err := Validate("nope.key", "1"); !errors.Is(err, ErrNotSchemaKey) {
		t.Errorf("Validate = %v", err)
	}
}

func TestSetRejectsNegativePlanLimits(t *testing.T) {
	for _, key := range []string{"plan.maxTasks", "plan.maxFilesPerTask"} {
		for _, bad := range []string{"-1", "2.5", `"many"`, "true"} {
			if _, err := Set(t.TempDir(), key, bad); !errors.Is(err, ErrBadValue) {
				t.Errorf("%s=%s: err = %v, want ErrBadValue", key, bad, err)
			}
		}
		for _, ok := range []string{"0", "7"} {
			if _, err := Set(t.TempDir(), key, ok); err != nil {
				t.Errorf("%s=%s: %v", key, ok, err)
			}
		}
	}
}

func TestSetContextGuardValidatesEnum(t *testing.T) {
	for _, bad := range []string{"bogus", "true", "3", ""} {
		root := project(t, "", "")
		_, err := Set(root, "work.contextGuard", bad)
		if !errors.Is(err, ErrBadValue) || !strings.Contains(err.Error(), "warn, off, auto") {
			t.Errorf("%q: err = %v, want ErrBadValue naming the choices", bad, err)
		}
	}
	for _, good := range []string{"off", "warn", "auto"} {
		root := project(t, "", "")
		if _, err := Set(root, "work.contextGuard", good); err != nil {
			t.Errorf("%q: %v", good, err)
		}
	}
}

func TestShipUATDefaultsOffAndIsBool(t *testing.T) {
	root := project(t, `{}`, `{}`)
	rows, err := Show(root, "ship.uat", true)
	if err != nil || len(rows) != 1 {
		t.Fatalf("ship.uat: %v, %v", rows, err)
	}
	if rows[0].Value != false || rows[0].Source != "default" {
		t.Errorf("ship.uat = %v (%s), want false from default", rows[0].Value, rows[0].Source)
	}
	if _, err := Set(root, "ship.uat", "true"); err != nil {
		t.Errorf("Set ship.uat true: %v", err)
	}
}

func TestPlanCriticHarnessIsOptInEnum(t *testing.T) {
	rows, err := Show(project(t, `{}`, `{}`), "plan.criticHarness", true)
	if err != nil || len(rows) != 1 {
		t.Fatalf("plan.criticHarness: %v, %v", rows, err)
	}
	if rows[0].Value != "" || rows[0].Source != "default" {
		t.Errorf("plan.criticHarness = %v (%s), want unset from default", rows[0].Value, rows[0].Source)
	}
	for _, bad := range []string{"bogus", "hermes", "true", "3"} {
		_, err := Set(project(t, "", ""), "plan.criticHarness", bad)
		if !errors.Is(err, ErrBadValue) || !strings.Contains(err.Error(), "claude, codex, opencode") {
			t.Errorf("%q: err = %v, want ErrBadValue naming the choices", bad, err)
		}
	}
	for _, good := range []string{"claude", "codex", "opencode", ""} {
		if _, err := Set(project(t, "", ""), "plan.criticHarness", good); err != nil {
			t.Errorf("%q: %v", good, err)
		}
	}
}

func TestSetRejectsNegativeMapStaleCommits(t *testing.T) {
	for _, bad := range []string{"-1", "2.5", `"many"`, "true"} {
		if _, err := Set(t.TempDir(), "map.staleCommits", bad); !errors.Is(err, ErrBadValue) {
			t.Errorf("map.staleCommits=%s: err = %v, want ErrBadValue", bad, err)
		}
	}
	for _, ok := range []string{"0", "50"} {
		if _, err := Set(t.TempDir(), "map.staleCommits", ok); err != nil {
			t.Errorf("map.staleCommits=%s: %v", ok, err)
		}
	}
}

func TestMapStaleCommitsDefault(t *testing.T) {
	for _, key := range Keys {
		if key.Name == "map.staleCommits" {
			if key.Default != num("50") || key.Type != TypeInt || key.Required {
				t.Errorf("map.staleCommits = %+v, want optional int default 50", key)
			}
			return
		}
	}
	t.Error("map.staleCommits is not a known key")
}

func TestSoftcapSubsystemsIsDeclared(t *testing.T) {
	key, ok := SchemaKey("map.softcap_subsystems")
	if !ok {
		t.Fatal("map.softcap_subsystems has no schema row")
	}
	if key.Type != TypeInt || key.Default != num("20") {
		t.Errorf("row = %+v, want int default 20", key)
	}
	for _, bad := range []string{"0", "-1", "2.5", `"many"`, "true"} {
		if _, err := Set(t.TempDir(), "map.softcap_subsystems", bad); !errors.Is(err, ErrBadValue) {
			t.Errorf("map.softcap_subsystems=%s: err = %v, want ErrBadValue", bad, err)
		}
	}
	if _, err := Set(t.TempDir(), "map.softcap_subsystems", "7"); err != nil {
		t.Errorf("map.softcap_subsystems=7: %v", err)
	}
}

func TestSetScopeOverlapRefusesNonChoices(t *testing.T) {
	root := project(t, "", "")
	if _, err := Set(root, "round.scopeOverlap", "off"); !errors.Is(err, ErrBadValue) {
		t.Errorf("off: err = %v, want ErrBadValue", err)
	}
	if _, err := Set(root, "round.scopeOverlap", ScopeOverlapBlock); err != nil {
		t.Errorf("block: %v", err)
	}
}

// An account dir that is neither absolute nor ~-prefixed would resolve against
// whatever directory the launch runs in, so config check fails on it (#1777).
func TestRetiredRejectsRelativeAccountDirs(t *testing.T) {
	good := `{"round":{"accounts":[{"kind":"claude","name":"a","dir":"~/.claude-a"},{"kind":"codex","name":"c","dir":"~"},{"kind":"opencode","name":"o","dir":"~/.oc"}]}}`
	if got := Retired(project(t, good, "")); len(got) != 0 {
		t.Errorf("valid dirs flagged: %v", got)
	}
	bad := `{"round":{"accounts":[{"kind":"claude","name":"a","dir":".claude-a"},{"kind":"codex","name":"c","dir":"$HOME/.codex"},{"kind":"opencode","name":"o","dir":"~bob/x"}]}}`
	got := Retired(project(t, bad, ""))
	if len(got) != 3 {
		t.Fatalf("want 3 problems, got %v", got)
	}
	for i, name := range []string{`"a"`, `"c"`, `"o"`} {
		if !strings.Contains(got[i], "round.accounts") || !strings.Contains(got[i], name) || !strings.Contains(got[i], "must be absolute or start with ~") {
			t.Errorf("problem %d does not name round.accounts, %s and the rule: %q", i, name, got[i])
		}
	}
}
