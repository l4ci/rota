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

func TestMigrateRoundKeys(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"moves value unchanged, keeps work.isolation",
			`{"work":{"isolation":"branch","workerSlots":3,"dispatch":"herdr","accounts":[{"name":"b","configDir":"/b"},{"name":"a","configDir":"/a"}]}}`,
			`{"work":{"isolation":"branch"},"round":{"dispatch":"herdr","slots":3,"accounts":[{"name":"b","configDir":"/b"},{"name":"a","configDir":"/a"}]}}`},
		{"joins an existing round object at schema position",
			`{"work":{"portBase":21000,"herdrGroupSlots":false},"round":{"roster":["x"]}}`,
			`{"round":{"roster":["x"],"portBase":21000,"herdrGroupSlots":false}}`},
		{"equal values drop the old name",
			`{"work":{"dispatch":"tmux"},"round":{"dispatch":"tmux"}}`,
			`{"round":{"dispatch":"tmux"}}`},
		{"null moves as null",
			`{"work":{"envSetup":null}}`,
			`{"round":{"envSetup":null}}`},
		{"already migrated is a no-op",
			`{"work":{"isolation":"worktree"},"round":{"slots":2}}`,
			`{"work":{"isolation":"worktree"},"round":{"slots":2}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, moved, conflicts := MigrateRoundKeys(decodeCfg(t, tc.in))
			if len(conflicts) > 0 {
				t.Fatalf("conflicts: %v", conflicts)
			}
			if s, w := compact(t, got), compact(t, decodeCfg(t, tc.want)); s != w {
				t.Fatalf("got  %s\nwant %s", s, w)
			}
			if wantMoved := tc.in != tc.want; (len(moved) > 0) != wantMoved {
				t.Errorf("moved = %v, want moved=%v", moved, wantMoved)
			}
			again, m2, _ := MigrateRoundKeys(got)
			if s := compact(t, again); s != compact(t, got) || len(m2) > 0 {
				t.Errorf("second run changed the config: %s %v", s, m2)
			}
		})
	}
}

func TestMigrateRoundKeysConflictKeepsBoth(t *testing.T) {
	in := `{"work":{"workerSlots":3,"dispatch":"tmux"},"round":{"slots":5,"dispatch":"tmux"}}`
	got, moved, conflicts := MigrateRoundKeys(decodeCfg(t, in))
	if len(conflicts) != 1 || conflicts[0].Old != "work.workerSlots" || conflicts[0].New != "round.slots" {
		t.Fatalf("conflicts = %+v, want one on work.workerSlots", conflicts)
	}
	if len(moved) != 0 || compact(t, got) != compact(t, decodeCfg(t, in)) {
		t.Errorf("a conflict must leave the config untouched: %s moved=%v", compact(t, got), moved)
	}
}

// writeLayers lays out .rota/config.json and, unless local is "-",
// .rota/config.local.json.
func writeLayers(t *testing.T, cfg, local string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	if cfg != "-" {
		writeFile(t, configPath(root), cfg)
	}
	if local != "-" {
		writeFile(t, localPath(root), local)
	}
	return root
}

func writeFile(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func decodeFile(t *testing.T, path string) any {
	t.Helper()
	v, err := jsonx.Decode([]byte(readFile(t, path)))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestMigrateRoundKeysBothLayers(t *testing.T) {
	root := writeLayers(t, `{"work":{"isolation":"branch","workerSlots":3}}`,
		`{"work":{"accounts":[{"name":"z","configDir":"/z"},{"name":"a","configDir":"/a"}]}}`)
	before := Load(configPath(root))
	m, err := MigrateRoundKeysFiles(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m.Config, []Rename{{"work.workerSlots", "round.slots"}}) ||
		!reflect.DeepEqual(m.Local, []Rename{{"work.accounts", "round.accounts"}}) {
		t.Fatalf("preview = %+v", m)
	}
	if strings.Contains(readFile(t, configPath(root)), "round") {
		t.Fatal("preview wrote config.json")
	}
	if _, err := MigrateRoundKeysFiles(root, true); err != nil {
		t.Fatal(err)
	}
	after := Load(configPath(root))
	for _, r := range []Rename{{"work.workerSlots", "round.slots"}, {"work.accounts", "round.accounts"}} {
		ov, _ := Lookup(before, r.Old)
		nv, ok := Lookup(after, r.New)
		if !ok || !sameJSON(ov, nv) {
			t.Errorf("%s -> %s: before %v, after %v", r.Old, r.New, ov, nv)
		}
		if _, still := Lookup(after, r.Old); still {
			t.Errorf("%s still present", r.Old)
		}
	}
	if s, w := compact(t, decodeFile(t, localPath(root)).(*jsonx.Object)), compact(t, decodeCfg(t, `{"round":{"accounts":[{"name":"z","configDir":"/z"},{"name":"a","configDir":"/a"}]}}`)); s != w {
		t.Errorf("local = %s", s)
	}
	if got := LegacyRoundKeys(after); len(got) != 0 {
		t.Errorf("LegacyRoundKeys after migration = %v", got)
	}
	cfgBytes, locBytes := readFile(t, configPath(root)), readFile(t, localPath(root))
	m2, err := MigrateRoundKeysFiles(root, true)
	if err != nil || m2.Changed() {
		t.Fatalf("second run: %+v %v", m2, err)
	}
	if readFile(t, configPath(root)) != cfgBytes || readFile(t, localPath(root)) != locBytes {
		t.Error("second run rewrote a file")
	}
}

func TestMigrateRoundKeysConflict(t *testing.T) {
	cfg := `{"work":{"dispatch":"tmux"}}`
	local := `{"work":{"workerSlots":2},"round":{"slots":4}}`
	root := writeLayers(t, cfg, local)
	_, err := MigrateRoundKeysFiles(root, true)
	var ce *ConflictError
	if !errors.As(err, &ce) || len(ce.Conflicts) != 1 || ce.Conflicts[0].File != "config.local.json" {
		t.Fatalf("err = %v, want one conflict in config.local.json", err)
	}
	if !strings.Contains(err.Error(), "work.workerSlots is 2") || !strings.Contains(err.Error(), "round.slots is 4") {
		t.Errorf("error should list both values: %v", err)
	}
	if readFile(t, configPath(root)) != cfg || readFile(t, localPath(root)) != local {
		t.Error("a conflict wrote a file")
	}
}

func TestMigrateRoundKeysCorruptLocal(t *testing.T) {
	cfg := `{"work":{"dispatch":"tmux"}}`
	for name, local := range map[string]string{"invalid": `{"work":`, "list": `[1]`} {
		t.Run(name, func(t *testing.T) {
			root := writeLayers(t, cfg, local)
			_, err := MigrateRoundKeysFiles(root, true)
			if !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "config.local.json") {
				t.Fatalf("err = %v, want ErrCorrupt naming config.local.json", err)
			}
			if readFile(t, configPath(root)) != cfg {
				t.Error("config.json written despite a corrupt local file")
			}
		})
	}
}

func TestMigrateRoundKeysReadErrorIsNotCorrupt(t *testing.T) {
	root := writeLayers(t, `{"work":{"dispatch":"tmux"}}`, "-")
	if err := os.Mkdir(localPath(root), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := MigrateRoundKeysFiles(root, true)
	if err == nil || errors.Is(err, ErrCorrupt) {
		t.Fatalf("err = %v, want a read error that is not ErrCorrupt", err)
	}
	if !strings.Contains(err.Error(), "config.local.json") {
		t.Errorf("err = %v, want it to name config.local.json", err)
	}
}

func TestMigrateRoundKeysMissingAndEmptyLayers(t *testing.T) {
	for name, layers := range map[string][2]string{
		"no files":          {"-", "-"},
		"no local":          {`{"work":{"dispatch":"tmux"}}`, "-"},
		"empty local":       {`{"work":{"dispatch":"tmux"}}`, ""},
		"local only, other": {"-", `{"models":{"worker":"haiku"}}`},
	} {
		t.Run(name, func(t *testing.T) {
			root := writeLayers(t, layers[0], layers[1])
			if _, err := MigrateRoundKeysFiles(root, true); err != nil {
				t.Fatal(err)
			}
			if layers[0] != "-" && strings.Contains(readFile(t, configPath(root)), "work") {
				t.Errorf("config.json not migrated: %s", readFile(t, configPath(root)))
			}
			if layers[1] != "-" && readFile(t, localPath(root)) != layers[1] {
				t.Errorf("local rewritten: %q", readFile(t, localPath(root)))
			}
		})
	}
}

// Fill migrates before it fills, so a fresh repo gets the new names and an
// old config never ends up holding both.
func TestMigrateRoundKeysFillOnEmpty(t *testing.T) {
	root := writeLayers(t, "-", "-")
	if _, err := Fill(root); err != nil {
		t.Fatal(err)
	}
	cfg := Load(configPath(root))
	for _, k := range []string{"round.slots", "round.dispatch", "round.workerCommand", "round.accounts", "round.operatorCommand"} {
		if _, ok := Lookup(cfg, k); !ok {
			t.Errorf("fill on empty did not write %s", k)
		}
	}
	if got := LegacyRoundKeys(cfg); len(got) != 0 {
		t.Errorf("fill wrote old names: %v", got)
	}

	old := writeLayers(t, `{"work":{"workerSlots":7}}`, `{"work":{"accounts":[{"name":"a"}]}}`)
	if _, err := Fill(old); err != nil {
		t.Fatal(err)
	}
	cfg = Load(configPath(old))
	if v, _ := Lookup(cfg, "round.slots"); !sameJSON(v, json7) {
		t.Errorf("fill lost work.workerSlots: round.slots = %v", v)
	}
	if got := LegacyRoundKeys(cfg); len(got) != 0 {
		t.Errorf("fill left old names: %v", got)
	}
	if !strings.Contains(readFile(t, localPath(old)), `"round"`) {
		t.Errorf("fill did not migrate config.local.json: %s", readFile(t, localPath(old)))
	}
}

var json7 = json.Number("7")

func TestLegacyRoundKeysBothLayers(t *testing.T) {
	root := writeLayers(t, `{"work":{"workerSlots":3,"isolation":"branch"}}`, `{"work":{"accounts":[]}}`)
	got := LegacyRoundKeys(Load(configPath(root)))
	if !reflect.DeepEqual(got, []string{"work.workerSlots", "work.accounts"}) {
		t.Errorf("LegacyRoundKeys = %v", got)
	}
}
