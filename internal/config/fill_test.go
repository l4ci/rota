package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
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
	seed := `{"issues": {"label": "in-progress", "autoCreateLabel": true}}`
	root := project(t, seed, "")
	filled, err := Fill(root)
	if err != nil || len(filled) != len(requiredNames()) {
		t.Fatalf("filled %v, err %v", filled, err)
	}
	full := fullConfig()
	full = fillKey(full, "", []string{"issues", "label"}, "in-progress")
	full = fillKey(full, "", []string{"issues", "autoCreateLabel"}, true)
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
	// work.dispatch is migrated to round.dispatch first (#1097).
	want := []string{"zzz", "work", "models", "round", "refactor", "learn", "ship", "qa", "autonomy", "docs", "git", "umbrella", "hvSkills", "rota", "test"}
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
	if k := work.Keys(); !reflect.DeepEqual(k, []string{"custom", "isolation", "mergeStrategy"}) {
		t.Errorf("work keys %v", k)
	}
	if v, _ := Value(o, "round.dispatch"); v != "herdr" {
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
	if !slices.Contains(filled, VersionKey) {
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

// refactor.verifyCommands moved to test.full: Fill copies it, drops the old
// key, lists test.full, and leaves the rest of refactor alone.
func TestFillMovesVerifyCommandsToTestFull(t *testing.T) {
	root := project(t, `{"refactor":{"confirmBeforeExecute":true,"verifyCommands":["x"]}}`, "")
	filled, err := Fill(root)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(filled, "test.full") {
		t.Errorf("filled %v lacks test.full", filled)
	}
	doc, _ := jsonx.Decode([]byte(read(t, root)))
	o := doc.(*jsonx.Object)
	if v, ok := Lookup(o, "test.full"); !ok || !reflect.DeepEqual(v, []any{"x"}) {
		t.Errorf("test.full = %v", v)
	}
	if _, ok := Lookup(o, "refactor.verifyCommands"); ok {
		t.Error("refactor.verifyCommands left")
	}
	if _, ok := Lookup(o, "refactor.confirmBeforeExecute"); !ok {
		t.Error("confirmBeforeExecute dropped")
	}
}

func TestFillKeepsNonEmptyTestFull(t *testing.T) {
	root := project(t, `{"test":{"full":["keep"]},"refactor":{"verifyCommands":["old"]}}`, "")
	if _, err := Fill(root); err != nil {
		t.Fatal(err)
	}
	doc, _ := jsonx.Decode([]byte(read(t, root)))
	o := doc.(*jsonx.Object)
	if v, _ := Lookup(o, "test.full"); !reflect.DeepEqual(v, []any{"keep"}) {
		t.Errorf("test.full = %v", v)
	}
	if _, ok := Lookup(o, "refactor.verifyCommands"); ok {
		t.Error("old key left")
	}
}

// A config older `rota init` seeded still holds the removed keys: check names
// them without failing, fill deletes them (and the emptied providers object)
// and keeps the rest.
func TestFillStripsRemovedKeys(t *testing.T) {
	old := `{"issues": {"providers": {"github": true, "gitlab": true}, "label": "mine", "autoCreateLabel": true, "filterMineOnly": true}}`
	root := project(t, old, "")
	want := []string{"issues.filterMineOnly", "issues.providers.github", "issues.providers.gitlab"}
	if got := Removed(root); !reflect.DeepEqual(got, want) {
		t.Fatalf("Removed before fill: %v", got)
	}
	if _, err := Fill(root); err != nil {
		t.Fatal(err)
	}
	if got := Removed(root); len(got) != 0 {
		t.Errorf("Removed after fill: %v", got)
	}
	doc, _ := jsonx.Decode([]byte(read(t, root)))
	issues, _ := getObject(doc.(*jsonx.Object), "issues")
	if got := issues.Keys(); !reflect.DeepEqual(got, []string{"label", "autoCreateLabel"}) {
		t.Errorf("issues keys %v", got)
	}
	if v, _ := issues.Get("label"); v != "mine" {
		t.Errorf("label %v", v)
	}
}

func TestFillRewritesAFileHoldingOnlyRemovedKeys(t *testing.T) {
	root := project(t, `{"issues": {"filterMineOnly": false}}`, "")
	if _, err := Fill(root); err != nil {
		t.Fatal(err)
	}
	if got := Removed(root); len(got) != 0 {
		t.Errorf("Removed after fill: %v", got)
	}
}

// Fill migrates the legacy source keys once and leaves the file alone after.
func TestFillMigratesSourceKeys(t *testing.T) {
	root := project(t, `{"backlog": {"backend": "issues"}, "issues": {"provider": "auto"}}`, "")
	if got := Legacy(root); !reflect.DeepEqual(got, []string{"backlog.backend", "issues.provider"}) {
		t.Fatalf("Legacy before fill: %v", got)
	}
	// No origin: the legacy keys stay and keep deciding.
	if _, sm, _, err := FillDetect(root, func() string { return "" }); err != nil || !sm.Unresolved {
		t.Fatalf("no origin: %+v %v", sm, err)
	}
	if got, _ := IssueSource(Load(configPath(root))); got != SourceIssues {
		t.Errorf("unmigrated source = %q, want %q", got, SourceIssues)
	}
	if _, sm, _, err := FillDetect(root, func() string { return "gitlab" }); err != nil || !sm.Changed || sm.Source != "gitlab" {
		t.Fatalf("with origin: %+v %v", sm, err)
	}
	if got := Legacy(root); len(got) != 0 {
		t.Errorf("Legacy after fill: %v", got)
	}
	before := read(t, root)
	if _, sm, _, err := FillDetect(root, func() string { return "gitlab" }); err != nil || sm.Changed {
		t.Fatalf("second run: %+v %v", sm, err)
	}
	if read(t, root) != before {
		t.Error("second run rewrote the file")
	}
	if got, _ := IssueSource(Load(configPath(root))); got != SourceGitLab {
		t.Errorf("migrated source = %q", got)
	}
}

func TestLegacyKeysStillDecide(t *testing.T) {
	cases := []struct {
		cfg, source, forge string
	}{
		{`{}`, "file", "auto"},
		{`{"backlog":{"backend":"file"}}`, "file", "auto"},
		{`{"backlog":{"backend":"issues"}}`, "issues", "auto"},
		{`{"backlog":{"backend":"issues"},"issues":{"provider":"gitlab"}}`, "gitlab", "gitlab"},
		{`{"issues":{"provider":"github"}}`, "file", "github"},
		{`{"issues":{"source":"jira"},"backlog":{"backend":"file"}}`, "jira", "auto"},
		{`{"forge":{"provider":"local"},"issues":{"provider":"github"}}`, "file", "local"},
	}
	for _, tc := range cases {
		cfg, err := jsonx.Decode([]byte(tc.cfg))
		if err != nil {
			t.Fatal(err)
		}
		if got, err := IssueSource(cfg); err != nil || got != tc.source {
			t.Errorf("%s: IssueSource = %q, %v; want %q", tc.cfg, got, err, tc.source)
		}
		if got, err := ForgeProvider(cfg); err != nil || got != tc.forge {
			t.Errorf("%s: ForgeProvider = %q, %v; want %q", tc.cfg, got, err, tc.forge)
		}
	}
	for _, bad := range []string{`{"issues":{"source":"svn"}}`, `{"issues":{"source":3}}`} {
		cfg, _ := jsonx.Decode([]byte(bad))
		if _, err := IssueSource(cfg); err == nil || !strings.Contains(err.Error(), "invalid issues.source") {
			t.Errorf("%s: err = %v", bad, err)
		}
	}
	cfg, _ := jsonx.Decode([]byte(`{"forge":{"provider":"bitbucket"}}`))
	if _, err := ForgeProvider(cfg); err == nil || !strings.Contains(err.Error(), "invalid forge.provider") {
		t.Errorf("err = %v", err)
	}
}

// A removed key held only in config.local.json is as inert as one in
// config.json: check names it, marked local, and fill deletes it from the
// local file.
func TestRemovedNamesLocalOnlyKeys(t *testing.T) {
	root := project(t, `{"issues": {"label": "mine"}}`,
		`{"round": {"autopilot": true}, "issues": {"filterMineOnly": true, "label": "x"}}`)
	want := []string{"issues.filterMineOnly (config.local.json)", "round.autopilot (config.local.json)"}
	if got := Removed(root); !reflect.DeepEqual(got, want) {
		t.Fatalf("Removed before fill: %v, want %v", got, want)
	}
	if _, err := Fill(root); err != nil {
		t.Fatal(err)
	}
	if got := Removed(root); len(got) != 0 {
		t.Errorf("Removed after fill: %v", got)
	}
	local := readLocal(t, root)
	if strings.Contains(local, "filterMineOnly") || strings.Contains(local, "autopilot") || !strings.Contains(local, `"label"`) {
		t.Errorf("config.local.json after fill:\n%s", local)
	}
}

// The invalid-value errors show the offending value as compact JSON, so a
// string is quoted and a number, list or object reads as it was written.
func TestInvalidValueErrorsNameTheValue(t *testing.T) {
	for _, tc := range []struct{ cfg, want string }{
		{`{"issues":{"source":"svn"}}`, `invalid issues.source "svn" (expected `},
		{`{"issues":{"source":3}}`, `invalid issues.source 3 (expected `},
		{`{"issues":{"source":["a",null]}}`, `invalid issues.source ["a", null] (expected `},
		{`{"issues":{"source":"a\"b\\c\nd"}}`, `invalid issues.source "a\"b\\c\nd" (expected `},
		{`{"issues":{"source":{"k":1}}}`, `invalid issues.source {"k": 1} (expected `},
		{`{"backlog":{"backend":"git"}}`, `invalid backlog.backend "git" (expected file|issues)`},
	} {
		cfg, _ := jsonx.Decode([]byte(tc.cfg))
		if _, err := IssueSource(cfg); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.cfg, err, tc.want)
		}
	}
	cfg, _ := jsonx.Decode([]byte(`{"forge":{"provider":false}}`))
	if _, err := ForgeProvider(cfg); err == nil || !strings.Contains(err.Error(), "invalid forge.provider false (expected ") {
		t.Errorf("err = %v", err)
	}
}
