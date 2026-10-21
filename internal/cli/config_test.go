package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/update"
)

func TestConfigUpdateIsPinned(t *testing.T) {
	deps := testDeps()
	deps.UpdateEnv = func() update.Env {
		return update.Env{ExeDir: "", Current: "1.0.0",
			Latest: func() string { return "2.0.0" }}
	}
	code, env, _ := umbRunWith(t, deps, "--json", "update")
	d := dataOf(env)
	// No binary path resolves, so the install type is unknown.
	if code != 0 || get(d, "status") != "behind" || get(d, "currentVersion") != "1.0.0" || get(d, "latestVersion") != "2.0.0" || get(d, "installType") != "unknown" {
		t.Errorf("code=%d env=%v", code, env)
	}
	if code, _, _ := umbRunWith(t, deps, "--json", "update", "extra"); code != ExitUsage {
		t.Errorf("positional: %d", code)
	}
	if code, _, _ := umbRunWith(t, deps, "--json", "update", "--repo", "x"); code != ExitUsage {
		t.Errorf("--repo: %d", code)
	}
}

func TestConfigRoundTrip(t *testing.T) {
	root := trackerProject(t, "{}\n")
	code, env, _ := rotaRun(t, "--json", "-C", root, "config", "set", "round.slots", "5")
	if code != 0 || get(dataOf(env), "changed") != true {
		t.Fatalf("set: %d %v", code, env)
	}
	code, env, _ = rotaRun(t, "--json", "-C", root, "config", "show", "round.slots")
	d := dataOf(env)
	rows, _ := get(d, "entries").([]any)
	if code != 0 || len(rows) != 1 || get(rows[0], "source") != "project" {
		t.Errorf("show: %d %v", code, env)
	}
	if code, _, _ = rotaRun(t, "--json", "-C", root, "config", "show", "nope"); code != ExitResolution {
		t.Errorf("unknown key: %d", code)
	}
	if code, _, _ = rotaRun(t, "--json", "-C", root, "config", "show", "a", "b"); code != ExitUsage {
		t.Errorf("two keys: %d", code)
	}
	if code, _, _ = rotaRun(t, "--json", "-C", root, "config", "set", "nope", "1"); code != ExitUsage {
		t.Errorf("set non-schema: %d", code)
	}
	if code, _, _ = rotaRun(t, "--json", "-C", root, "config", "set", "work."); code != ExitUsage {
		t.Errorf("set one arg: %d", code)
	}
	code, env, _ = rotaRun(t, "--json", "-C", root, "config", "check")
	if code != ExitFailed || get(dataOf(env), "status") != "stale" {
		t.Errorf("check: %d %v", code, env)
	}
}

func TestConfigNeedsProjectAndRegisteredRepo(t *testing.T) {
	if code, _, _ := rotaRun(t, "--json", "-C", t.TempDir(), "config", "check"); code != ExitResolution {
		t.Errorf("no .rota: %d", code)
	}
	root := trackerProject(t, "{}\n")
	if code, _, _ := rotaRun(t, "--json", "-C", root, "config", "check", "--repo", "web"); code != ExitResolution {
		t.Errorf("--repo outside umbrella: %d", code)
	}
}

func TestConfigSetNotObjectIs70(t *testing.T) {
	root := trackerProject(t, "[1]\n")
	if code, _, stderr := rotaRun(t, "--json", "-C", root, "config", "set", "docs.path", "x"); code != ExitInternal || !strings.Contains(stderr, "not a JSON object") {
		t.Errorf("code=%d %s", code, stderr)
	}
}

// A9 G1: fill brings a stale config up to date, then is a no-op.
func TestA9ConfigFill(t *testing.T) {
	root := trackerProject(t, `{"models": {"worker": "haiku"}}`+"\n")
	code, env, _ := rotaRun(t, "--json", "-C", root, "config", "fill")
	d := dataOf(env)
	filled, _ := get(d, "filled").([]any)
	if code != 0 || get(d, "changed") != true || len(filled) == 0 || filled[0] != "models.orchestrator" {
		t.Fatalf("fill: %d %v", code, env)
	}
	if code, _, _ := rotaRun(t, "--json", "-C", root, "config", "check"); code != 0 {
		t.Errorf("check after fill: %d", code)
	}
	code, env, _ = rotaRun(t, "--json", "-C", root, "config", "fill")
	if d := dataOf(env); code != 0 || get(d, "changed") != false || len(get(d, "filled").([]any)) != 0 {
		t.Errorf("second fill: %d %v", code, env)
	}
	if code, _, _ := rotaRun(t, "--json", "-C", root, "config", "fill", "x"); code != ExitUsage {
		t.Errorf("positional: %d", code)
	}
	if code, _, _ := rotaRun(t, "--json", "-C", t.TempDir(), "config", "fill"); code != ExitResolution {
		t.Errorf("no .rota: %d", code)
	}
	bad := trackerProject(t, "{oops\n")
	if code, _, stderr := rotaRun(t, "--json", "-C", bad, "config", "fill"); code != ExitInternal || !strings.Contains(stderr, "not a valid JSON object") {
		t.Errorf("corrupt: %d %s", code, stderr)
	}
}

func TestConfigRepoVerbs(t *testing.T) {
	root := trackerProject(t, "")
	os.WriteFile(filepath.Join(root, ".rota", "repos.json"), []byte(`{"repos": [{"name": "web", "path": "web"}]}`), 0o644)
	code, env, _ := rotaRun(t, "--json", "-C", root, "repo", "umbrella")
	if code != 0 || get(dataOf(env), "umbrella") != true {
		t.Errorf("umbrella: %d %v", code, env)
	}
	code, env, _ = rotaRun(t, "--json", "-C", root, "repo", "resolve", "web", "web")
	rows, _ := get(dataOf(env), "repos").([]any)
	if code != 0 || len(rows) != 2 || get(rows[1], "name") != "web" {
		t.Errorf("resolve: %d %v", code, env)
	}
	if code, _, stderr := rotaRun(t, "--json", "-C", root, "repo", "resolve", "web", "x", "y"); code != ExitResolution || !strings.Contains(stderr, "x, y") {
		t.Errorf("resolve unknown: %d %s", code, stderr)
	}
	code, env, _ = rotaRun(t, "--json", "-C", root, "repo", "resolve")
	if code != 0 || len(get(dataOf(env), "repos").([]any)) != 0 {
		t.Errorf("resolve none: %d %v", code, env)
	}
	if code, _, _ = rotaRun(t, "--json", "-C", root, "repo", "resolve", "--repo", "web"); code != ExitUsage {
		t.Errorf("--repo on repo verb: %d", code)
	}
	code, env, _ = rotaRun(t, "--json", "-C", t.TempDir(), "repo", "umbrella")
	if code != ExitFailed || get(dataOf(env), "umbrella") != false {
		t.Errorf("no root umbrella: %d %v", code, env)
	}
}

// autonomy.level "loop" was removed (#70): config check fails and names it.
func TestConfigCheckFailsOnRetiredLoop(t *testing.T) {
	root := trackerProject(t, `{"autonomy": {"level": "loop"}}`+"\n")
	code, env, _ := rotaRun(t, "--json", "-C", root, "config", "check")
	retired, _ := get(dataOf(env), "retired").([]any)
	if code != ExitFailed || len(retired) != 1 || !strings.Contains(retired[0].(string), `"loop" was removed`) {
		t.Errorf("check: %d %v", code, env)
	}
	if code, out, _ := rotaIn(t, root, "config", "check"); code != ExitFailed || !strings.HasPrefix(out, "RETIRED: ") {
		t.Errorf("text mode: %d %q", code, out)
	}
	root = trackerProject(t, `{"autonomy": {"level": "auto"}}`+"\n")
	if _, env, _ := rotaRun(t, "--json", "-C", root, "config", "check"); get(dataOf(env), "retired") != nil {
		t.Errorf("auto flagged: %v", env)
	}
}

// config show --json carries each schema key's metadata, additively.
func TestConfigShowJSONMetadata(t *testing.T) {
	root := trackerProject(t, "{}\n")
	code, env, _ := rotaRun(t, "--json", "-C", root, "config", "show", "work.mergeStrategy")
	rows, _ := get(dataOf(env), "entries").([]any)
	if code != 0 || len(rows) != 1 {
		t.Fatalf("show: %d %v", code, env)
	}
	want := map[string]any{"type": "enum", "group": "work", "default": "direct", "value": "direct", "source": "default"}
	for k, v := range want {
		if got := get(rows[0], k); got != v {
			t.Errorf("%s = %v, want %v", k, got, v)
		}
	}
	if d, _ := get(rows[0], "desc").(string); d == "" {
		t.Error("desc is empty")
	}
	if c, _ := get(rows[0], "choices").([]any); len(c) != 2 || c[0] != "direct" || c[1] != "pr" {
		t.Errorf("choices = %v", get(rows[0], "choices"))
	}
	_, env, _ = rotaRun(t, "--json", "-C", root, "config", "show", "round.slots")
	rows, _ = get(dataOf(env), "entries").([]any)
	if has := get(rows[0], "choices"); has != nil {
		t.Errorf("a non-enum key lists choices: %v", rows[0])
	}
}

func TestConfigShowReportsEffectiveTierModels(t *testing.T) {
	root := trackerProject(t, `{"models":{"worker":"opus"},"round":{"profile":"budget","tiers":{"claude":{"heavy":"fable"},"codex":{"light":"c1","standard":"c2","heavy":"c3"}}}}`+"\n")
	code, env, _ := rotaRun(t, "--json", "-C", root, "config", "show")
	if code != 0 {
		t.Fatalf("show: %d %v", code, env)
	}
	got := map[string][2]any{}
	rows, _ := get(dataOf(env), "entries").([]any)
	for _, r := range rows {
		k, _ := get(r, "key").(string)
		if strings.HasPrefix(k, "round.tiers.") {
			got[k] = [2]any{get(r, "effective"), get(r, "effectiveSource")}
		}
	}
	want := map[string][2]any{
		"round.tiers.claude.light":    {"haiku", "profile"},
		"round.tiers.claude.standard": {"haiku", "profile"},
		"round.tiers.claude.heavy":    {"fable", "explicit"},
		"round.tiers.codex.standard":  {"c2", "explicit"},
		"round.tiers.opencode.light":  {"opencode/claude-haiku-5-5", "default"},
		"round.tiers.opencode.heavy":  {"opencode/claude-opus-5-5", "default"},
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s: got %v, want %v", k, got[k], w)
		}
	}
	if len(got) != 9 {
		t.Errorf("want all nine tier keys, got %d", len(got))
	}
}

// #1511: config set and save-global say when they rewrite an old auto.
func TestConfigSetAndSaveGlobalSayAutoRewrite(t *testing.T) {
	t.Setenv("ROTA_CONFIG_DIR", t.TempDir())
	stale := `{"rota": {"version": "0.20.0"}, "autonomy": {"level": "auto"}}` + "\n"
	root := trackerProject(t, stale)
	code, out := textRun(t, "-C", root, "config", "save-global")
	if code != 0 || !strings.Contains(out, "rewrote: autonomy.level auto -> roundtrip") {
		t.Errorf("save-global: %d %q", code, out)
	}
	root = trackerProject(t, stale)
	code, out = textRun(t, "-C", root, "config", "set", "autonomy.level", "off")
	if code != 0 || !strings.Contains(out, "rewrote: autonomy.level auto -> roundtrip in config.json") {
		t.Errorf("set: %d %q", code, out)
	}
}

// #1552: a set that fails after the migration rewrote an old auto still warns
// the rewrite line, once, on stderr (#1524). config.json.lock is a directory,
// so the set's own write fails after config.local.json was already rewritten.
func TestConfigSetWarnsRewriteOnFailedSet(t *testing.T) {
	t.Setenv("ROTA_CONFIG_DIR", t.TempDir())
	root := trackerProject(t, `{"rota": {"version": "0.20.0"}}`+"\n")
	rota := filepath.Join(root, ".rota")
	write(t, filepath.Join(rota, "config.local.json"), `{"autonomy": {"level": "auto"}}`+"\n")
	if err := os.Mkdir(filepath.Join(rota, "config.json.lock"), 0o755); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := rotaIn(t, root, "config", "set", "autonomy.level", "off")
	if code == 0 {
		t.Fatalf("set succeeded, want a failure: %q", out)
	}
	const line = "rewrote: autonomy.level auto -> roundtrip in config.local.json"
	if n := strings.Count(errOut, line); n != 1 {
		t.Errorf("rewrite line warned %d times, want once: stdout %q stderr %q", n, out, errOut)
	}
}

// textRun runs rota without --json and returns the exit code and stdout.
func textRun(t *testing.T, args ...string) (int, string) {
	t.Helper()
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	var out, errb bytes.Buffer
	code := mainWith(testDeps(), args, strings.NewReader(""), &out, &errb)
	return code, out.String()
}
