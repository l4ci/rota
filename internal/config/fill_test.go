package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
)

func requiredNames() []string {
	var out []string
	for _, k := range Keys {
		if k.Required {
			out = append(out, k.Name)
		}
	}
	return out
}

func TestFillCreatesMissingFileInSchemaOrder(t *testing.T) {
	root := project(t, "", "")
	filled, err := Fill(root)
	if err != nil || !reflect.DeepEqual(filled, requiredNames()) {
		t.Fatalf("filled %v, err %v", filled, err)
	}
	want, _ := jsonx.Marshal(fullConfig())
	if got := read(t, root); got != string(want)+"\n" {
		t.Errorf("file:\n%s", got)
	}
	if st, _ := Check(root); st != UpToDate {
		t.Errorf("check after fill: %s", st)
	}
}

// The seed rota init writes (issues-only keys in schema order) filled out is
// byte-identical to a full config written in schema order (G7).
func TestFillCompletesTheSeedInSchemaOrder(t *testing.T) {
	seed := `{"issues": {"providers": {"github": true, "gitlab": true}, "label": "in-progress", "autoCreateLabel": true, "filterMineOnly": false}}`
	root := project(t, seed, "")
	filled, err := Fill(root)
	if err != nil || len(filled) != len(requiredNames())-2 {
		t.Fatalf("filled %v, err %v", filled, err)
	}
	full := fullConfig()
	issues, _ := getObject(full, "issues")
	issues.Set("label", "in-progress")
	issues.Set("autoCreateLabel", true)
	issues.Set("filterMineOnly", false)
	want, _ := jsonx.Marshal(full)
	if got := read(t, root); got != string(want)+"\n" {
		t.Errorf("file:\n%s\nwant:\n%s", got, want)
	}
}

func TestFillKeepsPresentAndUnknownKeys(t *testing.T) {
	cfg := `{"zzz": 1, "work": {"custom": "x", "dispatch": "herdr"}, "models": {"worker": "haiku"}, "umbrella": {"enabled": null}, "hvSkills": "scalar"}`
	root := project(t, cfg, `{"models": {"orchestrator": "local"}}`)
	if _, err := Fill(root); err != nil {
		t.Fatal(err)
	}
	doc, _ := jsonx.Decode([]byte(read(t, root)))
	o := doc.(*jsonx.Object)
	// present keys keep their order; added ones go before the first later sibling
	want := []string{"zzz", "work", "models", "refactor", "learn", "ship", "qa", "autonomy", "docs", "git", "umbrella", "hvSkills", "issues", "rota"}
	if got := o.Keys(); !reflect.DeepEqual(got, want) {
		t.Errorf("top keys %v", got)
	}
	models, _ := getObject(o, "models")
	if !reflect.DeepEqual(models.Keys(), []string{"orchestrator", "worker"}) {
		t.Errorf("models keys %v", models.Keys())
	}
	if v, _ := Value(o, "models.worker"); v != "haiku" {
		t.Errorf("present key overwritten: %v", v)
	}
	if v, _ := Value(o, "models.orchestrator"); v != "opus" {
		t.Errorf("local layer leaked into config.json: %v", v)
	}
	work, _ := getObject(o, "work")
	if k := work.Keys(); !reflect.DeepEqual(k, []string{"custom", "isolation", "mergeStrategy", "dispatch", "workerSlots", "workerCommand", "accounts", "operatorCommand"}) {
		t.Errorf("work keys %v", k)
	}
	if v, _ := Value(o, "work.dispatch"); v != "herdr" {
		t.Errorf("dispatch %v", v)
	}
	// null and a scalar in the way are replaced in place
	if v, _ := Value(o, "umbrella.enabled"); v != false {
		t.Errorf("umbrella.enabled %v", v)
	}
	if v, _ := Value(o, "rota.version"); v != "" {
		t.Errorf("rota.version %v", v)
	}
	if st, m := Check(root); st != UpToDate {
		t.Errorf("check: %s %v", st, m)
	}
}

func TestFillNothingMissingDoesNotRewrite(t *testing.T) {
	full, _ := jsonx.MarshalCompact(fullConfig())
	root := project(t, string(full), "")
	filled, err := Fill(root)
	if err != nil || filled == nil || len(filled) != 0 {
		t.Fatalf("filled %v, err %v", filled, err)
	}
	if got := read(t, root); got != string(full) {
		t.Errorf("file rewritten:\n%s", got)
	}
}

func TestFillRefusesCorrupt(t *testing.T) {
	for _, body := range []string{"{oops", "[1]", "null", "", "\xff{}"} {
		root := project(t, "", "")
		p := filepath.Join(root, ".rota", "config.json")
		os.WriteFile(p, []byte(body), 0o644)
		if _, err := Fill(root); !errors.Is(err, ErrCorrupt) {
			t.Errorf("%q: %v", body, err)
		}
		if b, _ := os.ReadFile(p); string(b) != body {
			t.Errorf("%q: file changed to %q", body, b)
		}
	}
}

func fillLegacy(t *testing.T, cfg string) (string, []string) {
	t.Helper()
	root := project(t, cfg, "")
	filled, err := Fill(root)
	if err != nil {
		t.Fatal(err)
	}
	return read(t, root), filled
}

func TestFillMigratesLegacyVersionKey(t *testing.T) {
	root := project(t, `{"hv":{"version":"4.2.0"}}`, "")
	filled, err := Fill(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(filled, requiredNames()) {
		t.Errorf("filled %v", filled)
	}
	doc, _ := jsonx.Decode([]byte(read(t, root)))
	o := doc.(*jsonx.Object)
	if _, ok := o.Get("hv"); ok {
		t.Errorf("hv left: %s", read(t, root))
	}
	if v, _ := Value(o, VersionKey); v != "4.2.0" {
		t.Errorf("rota.version %v", v)
	}
	if st, m := Check(root); st != UpToDate {
		t.Errorf("check: %s %v", st, m)
	}
}

// The hvSkills stamp is migrate hv's to move (#236): fill leaves it alone.
func TestFillLeavesTheHvSkillsStamp(t *testing.T) {
	out, _ := fillLegacy(t, `{"hvSkills":{"version":"4.2.0"}}`)
	doc, _ := jsonx.Decode([]byte(out))
	o := doc.(*jsonx.Object)
	if v, _ := Value(o, VersionKey); v != "" {
		t.Errorf("rota.version %v", v)
	}
	if _, ok := o.Get("hvSkills"); !ok {
		t.Errorf("hvSkills dropped: %s", out)
	}
}

// A file that is otherwise complete is still rewritten for the move alone,
// and lists exactly rota.version.
func TestFillLegacyKeyAloneRewrites(t *testing.T) {
	cfg := fullConfig()
	rota, _ := getObject(cfg, "rota")
	rota.Delete("version")
	cfg.Delete("rota")
	h, _ := jsonx.Decode([]byte(`{"version":"4.2.0"}`))
	cfg.Set("hv", h)
	b, _ := jsonx.Marshal(cfg)
	root := project(t, string(b), "")
	filled, err := Fill(root)
	if err != nil || !reflect.DeepEqual(filled, []string{VersionKey}) {
		t.Fatalf("filled %v err %v", filled, err)
	}
	doc, _ := jsonx.Decode([]byte(read(t, root)))
	o := doc.(*jsonx.Object)
	if _, ok := o.Get("hv"); ok {
		t.Error("hv left")
	}
	if v, _ := Value(o, VersionKey); v != "4.2.0" {
		t.Errorf("rota.version %v", v)
	}
}

func TestFillLegacyKeepsExistingNewValue(t *testing.T) {
	out, filled := fillLegacy(t, `{"rota":{"version":"5.0.0"},"hv":{"version":"4.2.0"}}`)
	doc, _ := jsonx.Decode([]byte(out))
	o := doc.(*jsonx.Object)
	if v, _ := Value(o, VersionKey); v != "5.0.0" {
		t.Errorf("rota.version %v", v)
	}
	if _, ok := o.Get("hv"); ok {
		t.Error("hv left")
	}
	// dropping the legacy key counts as filling rota.version either way
	if filled[len(filled)-1] != VersionKey {
		t.Errorf("rota.version not listed: %v", filled)
	}
}

func TestFillLegacyKeepsOtherHvKeys(t *testing.T) {
	out, _ := fillLegacy(t, `{"hv":{"version":"4.2.0","note":"x"}}`)
	doc, _ := jsonx.Decode([]byte(out))
	o := doc.(*jsonx.Object)
	hs, ok := getObject(o, "hv")
	if !ok || !reflect.DeepEqual(hs.Keys(), []string{"note"}) {
		t.Errorf("hv %v: %s", ok, out)
	}
	if v, _ := Value(o, VersionKey); v != "4.2.0" {
		t.Errorf("rota.version %v", v)
	}
}

func TestStampedVersion(t *testing.T) {
	cases := []struct{ name, cfg, want string }{
		{"new", `{"rota":{"version":"5.0.0"}}`, "5.0.0"},
		{"legacy", `{"hv":{"version":"4.2.0"}}`, "4.2.0"},
		{"hvSkills stamp is not read", `{"hvSkills":{"version":"4.2.0"}}`, ""},
		{"both prefers new", `{"rota":{"version":"5.0.0"},"hv":{"version":"4.2.0"}}`, "5.0.0"},
		{"empty new falls back", `{"rota":{"version":""},"hv":{"version":"4.2.0"}}`, "4.2.0"},
		{"neither", `{}`, ""},
	}
	for _, tc := range cases {
		doc, _ := jsonx.Decode([]byte(tc.cfg))
		if got := StampedVersion(doc); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
	if StampedVersion(nil) != "" {
		t.Error("nil cfg")
	}
}
