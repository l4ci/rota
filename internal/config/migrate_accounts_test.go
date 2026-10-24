package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMigrateAccountsDoc(t *testing.T) {
	belowCodex := []Account{{KindCodex, "g", "/g"}}
	cases := []struct {
		name, in, want string
		below          []Account
		from           []string
	}{
		{"folds all three keys, claude first",
			`{"round":{"opencodeAccounts":[{"name":"o","configDir":"/o"}],"codexAccounts":[{"name":"c","codexHome":"/c"}],"accounts":[{"name":"a","configDir":"/a"}]}}`,
			`{"round":{"accounts":[{"kind":"claude","name":"a","dir":"/a"},{"kind":"codex","name":"c","dir":"/c"},{"kind":"opencode","name":"o","dir":"/o"}]}}`,
			nil, []string{"round.accounts", "round.codexAccounts", "round.opencodeAccounts"}},
		{"legacy claude entries alone are rewritten in place",
			`{"round":{"slots":2,"accounts":[{"name":"a","configDir":"~/.claude-a"}]}}`,
			`{"round":{"slots":2,"accounts":[{"kind":"claude","name":"a","dir":"~/.claude-a"}]}}`,
			nil, []string{"round.accounts"}},
		{"codex key alone creates round.accounts",
			`{"round":{"slots":2,"codexAccounts":[{"name":"c","codexHome":"~/.codex"}]}}`,
			`{"round":{"slots":2,"accounts":[{"kind":"codex","name":"c","dir":"~/.codex"}]}}`,
			nil, []string{"round.codexAccounts"}},
		{"an empty old key is just dropped",
			`{"round":{"slots":2,"codexAccounts":[]}}`,
			`{"round":{"slots":2}}`,
			nil, []string{"round.codexAccounts"}},
		{"kinds this file lacks come from the layers below",
			`{"round":{"accounts":[{"name":"a","configDir":"/a"}]}}`,
			`{"round":{"accounts":[{"kind":"claude","name":"a","dir":"/a"},{"kind":"codex","name":"g","dir":"/g"}]}}`,
			belowCodex, []string{"round.accounts"}},
		{"a kind this file defines replaces the one below, as the old key did",
			`{"round":{"codexAccounts":[{"name":"c","codexHome":"/c"}]}}`,
			`{"round":{"accounts":[{"kind":"codex","name":"c","dir":"/c"}]}}`,
			belowCodex, []string{"round.codexAccounts"}},
		{"already migrated is a no-op",
			`{"round":{"accounts":[{"kind":"codex","name":"c","dir":"/c"}]}}`,
			`{"round":{"accounts":[{"kind":"codex","name":"c","dir":"/c"}]}}`,
			belowCodex, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, from, conflicts := migrateAccountsDoc(decodeCfg(t, tc.in), tc.below)
			if len(conflicts) > 0 {
				t.Fatalf("conflicts: %v", conflicts)
			}
			if s, w := compact(t, got), compact(t, decodeCfg(t, tc.want)); s != w {
				t.Fatalf("got  %s\nwant %s", s, w)
			}
			if !reflect.DeepEqual(from, tc.from) {
				t.Errorf("from = %v, want %v", from, tc.from)
			}
			again, f2, _ := migrateAccountsDoc(got, tc.below)
			if s := compact(t, again); s != compact(t, got) || len(f2) > 0 {
				t.Errorf("second run changed the config: %s %v", s, f2)
			}
		})
	}
}

func TestMigrateAccountsDocConflict(t *testing.T) {
	in := `{"round":{"codexAccounts":[{"name":"x","codexHome":"/c"}],"opencodeAccounts":[{"name":"x","configDir":"/o"}]}}`
	doc := decodeCfg(t, in)
	got, from, conflicts := migrateAccountsDoc(doc, nil)
	if len(conflicts) != 1 || conflicts[0].Name != "x" || conflicts[0].A != "/c" || conflicts[0].B != "/o" {
		t.Fatalf("conflicts = %+v, want one on x (/c vs /o)", conflicts)
	}
	if len(from) != 0 || compact(t, got) != compact(t, decodeCfg(t, in)) {
		t.Errorf("a conflict must leave the doc untouched: %s", compact(t, got))
	}
	// The same name at the same dir under two kinds is no disagreement.
	ok := `{"round":{"codexAccounts":[{"name":"x","codexHome":"/h"}],"opencodeAccounts":[{"name":"x","configDir":"/h"}]}}`
	if _, _, c := migrateAccountsDoc(decodeCfg(t, ok), nil); len(c) != 0 {
		t.Errorf("same name and dir flagged: %v", c)
	}
}

type acctRig struct{ root, gdir string }

func newAcctRig(t *testing.T, global, project, local string) acctRig {
	t.Helper()
	r := acctRig{root: t.TempDir(), gdir: t.TempDir()}
	t.Setenv("ROTA_CONFIG_DIR", r.gdir)
	for path, body := range map[string]string{
		GlobalPath(r.gdir): global,
		filepath.Join(r.root, ".rota", "config.json"):       project,
		filepath.Join(r.root, ".rota", "config.local.json"): local,
	} {
		if body == "" {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func (r acctRig) read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return compact(t, decodeCfg(t, string(b)))
}

func TestMigrateAccountsFilesRewritesEveryLayer(t *testing.T) {
	const (
		global  = `{"round":{"codexAccounts":[{"name":"g","codexHome":"/g"}]}}`
		project = `{"round":{"opencodeAccounts":[{"name":"p","configDir":"/p"}]}}`
		local   = `{"round":{"accounts":[{"name":"l","configDir":"/l"}]}}`
	)
	r := newAcctRig(t, global, project, local)
	cfgPath := filepath.Join(r.root, ".rota", "config.json")
	locPath := filepath.Join(r.root, ".rota", "config.local.json")

	m, err := MigrateAccountsFiles(r.root, r.gdir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Changes) != 3 || !m.Changed() {
		t.Fatalf("preview changes = %+v, want one per layer", m.Changes)
	}
	if got := r.read(t, cfgPath); got != compact(t, decodeCfg(t, project)) {
		t.Errorf("preview wrote config.json: %s", got)
	}

	if _, err := MigrateAccountsFiles(r.root, r.gdir, true); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		GlobalPath(r.gdir): `{"round":{"accounts":[{"kind":"codex","name":"g","dir":"/g"}]}}`,
		cfgPath:            `{"round":{"accounts":[{"kind":"opencode","name":"p","dir":"/p"}]}}`,
		locPath:            `{"round":{"accounts":[{"kind":"claude","name":"l","dir":"/l"},{"kind":"codex","name":"g","dir":"/g"},{"kind":"opencode","name":"p","dir":"/p"}]}}`,
	} {
		if got := r.read(t, path); got != compact(t, decodeCfg(t, want)) {
			t.Errorf("%s:\n got  %s\n want %s", path, got, want)
		}
	}
	if m2, err := MigrateAccountsFiles(r.root, r.gdir, true); err != nil || m2.Changed() {
		t.Errorf("second run = %+v, %v; want nothing", m2, err)
	}
}

func TestMigrateAccountsFilesConflictWritesNothing(t *testing.T) {
	project := `{"round":{"codexAccounts":[{"name":"x","codexHome":"/c"}]}}`
	local := `{"round":{"accounts":[{"name":"y","configDir":"/y"}],"opencodeAccounts":[{"name":"y","configDir":"/z"}]}}`
	r := newAcctRig(t, "", project, local)
	_, err := MigrateAccountsFiles(r.root, r.gdir, true)
	var ce *AccountConflictError
	if !errors.As(err, &ce) || len(ce.Conflicts) != 1 || ce.Conflicts[0].File != "config.local.json" || ce.Conflicts[0].Name != "y" {
		t.Fatalf("err = %v, want one conflict on y in config.local.json", err)
	}
	if !strings.Contains(err.Error(), "nothing written") {
		t.Errorf("message does not say nothing was written: %v", err)
	}
	if got := r.read(t, filepath.Join(r.root, ".rota", "config.json")); got != compact(t, decodeCfg(t, project)) {
		t.Errorf("config.json written despite the conflict: %s", got)
	}
}

func TestOldAccountKeysAreRemoved(t *testing.T) {
	for _, k := range []string{"round.codexAccounts", "round.opencodeAccounts"} {
		if !contains(RemovedKeys, k) {
			t.Errorf("%s missing from RemovedKeys", k)
		}
		if _, ok := SchemaKey(k); ok {
			t.Errorf("%s is still a schema key", k)
		}
	}
	root := project(t, `{"round":{"slots":2,"codexAccounts":[{"name":"c","codexHome":"/c"}]}}`, "")
	if got := Removed(root); !reflect.DeepEqual(got, []string{"round.codexAccounts"}) {
		t.Errorf("Removed = %v, want the old key named", got)
	}
}

// fill migrates before it strips, so deleting a removed key never loses a login.
func TestFillMigratesAccountsBeforeStripping(t *testing.T) {
	r := newAcctRig(t, "", `{"round":{"codexAccounts":[{"name":"c","codexHome":"/c"}]}}`, "")
	if _, err := Fill(r.root); err != nil {
		t.Fatal(err)
	}
	got := Accounts(Load(filepath.Join(r.root, ".rota", "config.json")))
	if want := []Account{{KindCodex, "c", "/c"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("accounts after fill = %v, want %v", got, want)
	}
}

// The tracked config.json is shared: neither verb may copy a machine-scope
// login from the global layer into it.
func TestAccountsStepKeepsOtherLayersOutOfTrackedConfig(t *testing.T) {
	const (
		global  = `{"round":{"accounts":[{"kind":"claude","name":"a1","dir":"~/.claude-a1"}]}}`
		project = `{"round":{"codexAccounts":[{"name":"c1","codexHome":"/c1"}]}}`
	)
	want := compact(t, decodeCfg(t, `{"round":{"accounts":[{"kind":"codex","name":"c1","dir":"/c1"}]}}`))
	for name, run := range map[string]func(r acctRig) error{
		"migrate": func(r acctRig) error { _, err := MigrateAccountsFiles(r.root, r.gdir, true); return err },
		"fill":    func(r acctRig) error { _, err := Fill(r.root); return err },
	} {
		t.Run(name, func(t *testing.T) {
			r := newAcctRig(t, global, project, "")
			if err := run(r); err != nil {
				t.Fatal(err)
			}
			got := r.read(t, filepath.Join(r.root, ".rota", "config.json"))
			if !strings.Contains(got, `"c1"`) || strings.Contains(got, "a1") {
				t.Errorf("tracked config.json = %s, want only c1 (%s)", got, want)
			}
		})
	}
}
