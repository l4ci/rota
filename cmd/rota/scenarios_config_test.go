package main

// Go-only scenarios for rota update, rota config show|set|check and rota repo
// which|resolve|umbrella (#48), on the harness of harness_test.go. Each runs
// the Go binary and is checked against its frozen record in
// testdata/frozen/config.jsonl (regenerate with -update-frozen).
//
// Behaviour worth knowing: config show reads keys outside the schema; config
// check exits 1 unless up to date; repo umbrella and repo resolve walk up to
// the nearest .rota/; rota update falls back to the stamped version only when no
// install root resolves.
//
// Safety: rota update must never reach the network. Every update scenario sets
// ROTA_TEST_LATEST_VERSION; the one scenario that leaves it unset runs with
// test/fakes first on PATH and proves, through the fake's call log, that gh
// resolved there.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/l4ci/rota/internal/config"
)

// ---- config fixtures ---------------------------------------------------------------

// nest turns dotted keys into the nested JSON object a config file holds.
func nest(kv map[string]any) string {
	root := map[string]any{}
	for k, v := range kv {
		segs := strings.Split(k, ".")
		cur := root
		for _, s := range segs[:len(segs)-1] {
			next, ok := cur[s].(map[string]any)
			if !ok {
				next = map[string]any{}
				cur[s] = next
			}
			cur = next
		}
		cur[segs[len(segs)-1]] = v
	}
	b, _ := json.MarshalIndent(root, "", "  ")
	return string(b) + "\n"
}

// fullConfig has every required key at its default: an up-to-date config.
func fullConfig(drop ...string) string {
	kv := map[string]any{}
	for _, k := range config.Keys {
		if k.Required {
			kv[k.Name] = k.Default
		}
	}
	for _, d := range drop {
		delete(kv, d)
	}
	return nest(kv)
}

func withFile(path, content string) fx { return fx{files: map[string]string{path: content}} }

func noConfigFile(t *testing.T, dir string, in *info) {
	os.Remove(filepath.Join(dir, ".rota", "config.json"))
}

// ---- config checks -----------------------------------------------------------------

// checkShow asserts the number of data.entries rows.
func checkShow(wantRows int) func(t *testing.T, e envl) {
	return func(t *testing.T, e envl) {
		t.Helper()
		rows, _ := at(e, "data.entries").([]any)
		if wantRows >= 0 && len(rows) != wantRows {
			t.Fatalf("%d entries, want %d", len(rows), wantRows)
		}
	}
}

// checkVerdict asserts the config check verdict in data.
func checkVerdict(status string, missing ...string) func(t *testing.T, e envl) {
	return func(t *testing.T, e envl) {
		t.Helper()
		eq(t, e, "data.status", status)
		eq(t, e, "data.upToDate", status == "upToDate")
		got := strs(at(e, "data.missing"))
		if got == nil || len(got) != len(missing) || (len(missing) > 0 && !reflect.DeepEqual(got, missing)) {
			t.Errorf("data.missing = %v, want %v", got, missing)
		}
	}
}

// ---- repo checks -------------------------------------------------------------------

// dirNorm replaces a project directory, as written and with symlinks resolved,
// by <root>.
func dirNorm(s, dir string) string {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		real = dir
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, real, "<root>"), dir, "<root>")
}

func goDirOf(e envl) string { d, _ := e["__godir"].(string); return d }

// checkWhich asserts the repo name and its path relative to the umbrella root.
func checkWhich(name, rel string) func(t *testing.T, e envl) {
	return func(t *testing.T, e envl) {
		t.Helper()
		eq(t, e, "data.name", name)
		got, _ := at(e, "data.path").(string)
		if want := "<root>/" + rel; dirNorm(got, goDirOf(e)) != want {
			t.Errorf("path = %s, want %s", dirNorm(got, goDirOf(e)), want)
		}
	}
}

func registry(reg string) fx {
	return withFx(umbFx, func(f *fx) {
		f.after = func(t *testing.T, dir string, in *info) { write(t, dir, ".rota/repos.json", reg) }
	})
}

func afterAll(fs ...func(t *testing.T, dir string, in *info)) func(t *testing.T, dir string, in *info) {
	return func(t *testing.T, dir string, in *info) {
		for _, f := range fs {
			f(t, dir, in)
		}
	}
}

// ---- update fixtures ---------------------------------------------------------------

const instVersion = "1.2.3"

var (
	updOnce sync.Once
	upd     struct {
		bin      string // Go binary stamped with instVersion
		brewBin  string // a copy under a Homebrew-looking prefix
		homeNone string
	}
)

func updFixtures(t *testing.T) {
	t.Helper()
	updOnce.Do(func() {
		mk := func(name string) string {
			d := filepath.Join(harnessTmp, name)
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
			return d
		}
		upd.bin = filepath.Join(harnessTmp, "rota-stamped")
		build := exec.Command("go", "build", "-ldflags", "-X github.com/l4ci/rota/internal/version.Version="+instVersion, "-o", upd.bin, ".")
		build.Dir = filepath.Join(repoDir, "cmd", "rota")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("go build: %v\n%s", err, out)
		}
		upd.homeNone = mk("home-none")
		upd.brewBin = filepath.Join(mk("Cellar/rota/9.9.9/bin"), "rota")
		if out, err := exec.Command("cp", upd.bin, upd.brewBin).CombinedOutput(); err != nil {
			t.Fatalf("cp: %v\n%s", err, out)
		}
	})
}

// checkUpdate asserts the install type, status and the shape of data.
func checkUpdate(installType, status string) func(t *testing.T, e envl) {
	return func(t *testing.T, e envl) {
		t.Helper()
		got, _ := at(e, "data").(map[string]any)
		eq(t, e, "data.installType", installType)
		eq(t, e, "data.status", status)
		for _, k := range []string{"installType", "installRoot", "currentVersion", "latestVersion", "status", "updateCommand"} {
			if _, ok := got[k].(string); !ok {
				t.Errorf("data.%s = %#v, want a string", k, got[k])
			}
		}
	}
}

func updScn(name, latest string, installType, status string) scn {
	return scn{name: "update/" + name, fx: fx{noHV: true}, argv: j("update"), want: 0,
		env:   []string{"HOME=" + upd.homeNone, "ROTA_TEST_LATEST_VERSION=" + latest},
		bin:   upd.bin,
		check: both(checkUpdate(installType, status), eqCheck("data.currentVersion", instVersion))}
}

// ---- the scenarios -----------------------------------------------------------------

func suiteConfig(t *testing.T) {
	updFixtures(t)
	var all []scn
	add := func(s ...scn) { all = append(all, s...) }
	stdFx := fx{}

	// ---- config show: every schema key, in each of the three sources
	show := func(name string, f fx, rows int, args ...string) scn {
		return scn{name: "config-show/" + name, fx: f, argv: j(append([]string{"config", "show"}, args...)...),
			want: 0, text: true, check: checkShow(rows)}
	}
	showE := func(name string, f fx, want int, args ...string) scn {
		return scn{name: "config-show/" + name, fx: f, argv: j(append([]string{"config", "show"}, args...)...),
			want: want}
	}
	for _, k := range config.Keys {
		add(
			show(k.Name+"/default", stdFx, 1, k.Name),
			show(k.Name+"/project", fx{config: nest(map[string]any{k.Name: "proj"})}, 1, k.Name),
			show(k.Name+"/local", fx{files: map[string]string{".rota/config.local.json": nest(map[string]any{k.Name: "loc"})}}, 1, k.Name),
		)
	}
	allProj, allLoc := map[string]any{}, map[string]any{}
	for i, k := range config.Keys {
		switch i % 3 {
		case 0:
			allProj[k.Name] = fmt.Sprintf("p%d", i)
		case 1:
			allLoc[k.Name] = []any{i, "l"}
			allProj[k.Name] = "shadowed"
		}
	}
	n := len(config.Keys)
	add(
		show("all/std", stdFx, n),
		show("all/no-config-file", withFx(stdFx, func(f *fx) { f.after = noConfigFile }), n),
		show("all/mixed-layers", fx{config: nest(allProj), files: map[string]string{".rota/config.local.json": nest(allLoc)}}, n),
		show("all/null-counts-as-unset", fx{config: `{"models": {"worker": null, "orchestrator": "o"}}`}, n),
		show("all/null-in-local-falls-to-project", fx{config: `{"models": {"worker": "p"}}`, files: map[string]string{".rota/config.local.json": `{"models": {"worker": null}}`}}, n),
		show("all/parent-is-scalar", fx{config: `{"models": "x", "work": 3}`}, n),
		show("all/config-is-list", fx{config: `[1]`}, n),
		show("all/config-corrupt", fx{config: `{oops`}, n),
		show("all/local-is-list-ignored", fx{files: map[string]string{".rota/config.local.json": `[1]`}}, n),
		show("all/local-corrupt", fx{files: map[string]string{".rota/config.local.json": `{`}}, n),
		show("all/nested-object-values", fx{config: `{"work": {"accounts": [{"a": 1}, "b"], "workerCommand": "kü \"q\""}}`}, n),
		show("all/number-forms", fx{config: `{"work": {"workerSlots": 1.50}, "learn": {"promoteThreshold": 1e2}, "issues": {"bulkPaceMs": 12345678901234567890}}`}, n),
		show("all/unicode-and-escapes", fx{config: `{"docs": {"path": "dé😀\n\t/"}}`}, n),
		show("all/from-subdir", withFile("sub/x.txt", "x\n"), n),
		show("one/from-subdir", withFile("sub/x.txt", "x\n"), 1, "docs.path"),
		showE("one/unknown-key", stdFx, 3, "nope.nope"),
		showE("one/empty-key", stdFx, 3, ""),
		showE("one/case-matters", stdFx, 3, "Models.Worker"),
		showE("one/hand-edited-null-key", fx{config: `{"custom": null}`}, 3, "custom"),
		showE("two-keys-usage", stdFx, 2, "docs.path", "docs.afterWork"),
		show("one/under-issues-backend", fx{config: issuesConfig}, 1, "backlog.backend"),
		showE("no-hv", fx{noHV: true}, 3),
		scn{name: "config-show/repo-flag-outside-umbrella", goOnly: true, want: 3, argv: j("config", "show", "--repo", "web")},
		scn{name: "config-show/repo-flag-registered", fx: umbFx, argv: j("config", "show", "--repo", "web", "docs.path"), want: 0, text: true, check: checkShow(1)},
		scn{name: "config-show/repo-flag-unregistered", goOnly: true, want: 3, fx: umbFx, argv: j("config", "show", "--repo", "nope")},
		// config show reads keys outside the schema
		scn{name: "config-show/hand-edited-key-is-readable", want: 0,
			fx: fx{config: `{"custom": {"x": 7}}`}, argv: j("config", "show", "custom.x"),
			check: both(eqCheck("data.entries.0.value", float64(7)), eqCheck("data.entries.0.source", "project"), eqCheck("data.entries.0.key", "custom.x"))},
		scn{name: "config-show/hand-edited-key-local-source", want: 0,
			fx: fx{config: `{"custom": 1}`, files: map[string]string{".rota/config.local.json": `{"custom": 2}`}}, argv: j("config", "show", "custom"),
			check: both(eqCheck("data.entries.0.value", float64(2)), eqCheck("data.entries.0.source", "local"))},
		scn{name: "config-show/hand-edited-parent-object", want: 0,
			fx: fx{config: `{"models": {"worker": "w"}}`}, argv: j("config", "show", "models"),
			check: eqCheck("data.entries.0.value", map[string]any{"worker": "w"})},
	)

	// ---- config set
	set := func(name string, f fx, key, val string, check func(t *testing.T, e envl)) scn {
		return scn{name: "config-set/" + name, fx: f, argv: j("config", "set", key, val),
			want: 0, check: check}
	}
	for _, c := range []struct {
		name, raw string
		want      any
	}{
		{"true", "true", true}, {"false", "false", false}, {"int", "42", float64(42)}, {"negative", "-7", float64(-7)},
		{"float", "1.50", 1.5}, {"exponent", "1e2", float64(100)}, {"json-string", `"x"`, "x"},
		{"array", `[1, "a", null]`, []any{float64(1), "a", nil}}, {"object", `{"a": {"b": [1]}}`, map[string]any{"a": map[string]any{"b": []any{float64(1)}}}},
		{"raw-string", "opus", "opus"}, {"capital-true-is-a-string", "True", "True"}, {"quoted-true-stays-string", `"true"`, "true"},
		{"unicode", "kü\U0001F600", "kü\U0001F600"}, {"padded-int", " 7 ", float64(7)}, {"leading-zero-is-a-string", "0123", "0123"},
		{"underscore-int-is-a-string", "1_000", "1_000"}, {"broken-object", "{bad", "{bad"}, {"broken-array", "[1,", "[1,"},
		{"whitespace-only", "  ", "  "}, {"big-int", "9999999999999999999999", float64(9999999999999999999999)}, {"minus-zero", "-0", float64(0)},
		{"trailing-garbage", "5 x", "5 x"}, {"two-values", "1 2", "1 2"}, {"hash", "#x", "#x"}, {"dash-value", "-x", "-x"},
		{"escaped-newline", `"a\nb"`, "a\nb"}, {"empty-object", "{}", map[string]any{}}, {"empty-array", "[]", []any{}},
		{"surrogate-pair-escape", `"😀"`, "\U0001F600"}, {"null", "null", nil},
	} {
		s := set("coerce/"+c.name, stdFx, "models.worker", c.raw, func(t *testing.T, e envl) {
			t.Helper()
			eq(t, e, "data.value", c.want)
			eq(t, e, "data.changed", true)
		})
		if strings.HasPrefix(c.raw, "-") {
			// a value that starts with - is a flag until -- (conventions, rule 7)
			s.argv = j("config", "set", "models.worker", "--", c.raw)
		}
		add(s)
	}
	add(
		set("key/models.orchestrator", stdFx, "models.orchestrator", "haiku", eqCheck("data.key", "models.orchestrator")),
		set("key/four-segments", stdFx, "issues.labels.types.bug", "defect", eqCheck("data.key", "issues.labels.types.bug")),
		set("key/three-segments", stdFx, "issues.providers.github", "false", eqCheck("data.value", false)),
		set("key/rota.version", stdFx, "rota.version", "5.0.0", eqCheck("data.value", "5.0.0")),
		set("previous/scalar", fx{config: `{"models": {"worker": "sonnet"}}`}, "models.worker", "opus",
			both(eqCheck("data.previous", "sonnet"), eqCheck("data.changed", true))),
		set("previous/object", fx{config: `{"work": {"accounts": ["a"]}}`}, "work.accounts", `["b"]`, eqCheck("data.previous", []any{"a"})),
		set("previous/null-is-present", fx{config: `{"models": {"worker": null}}`}, "models.worker", "x", eqCheck("data.previous", nil)),
		set("previous/same-value-is-a-no-op", fx{config: "{\n  \"models\": {\n    \"worker\": \"opus\"\n  }\n}\n"}, "models.worker", "opus",
			both(eqCheck("data.changed", false), eqCheck("data.previous", "opus"))),
		set("previous/same-value-compact-file-is-reformatted", fx{config: `{"models":{"worker":"opus"}}`}, "models.worker", "opus", eqCheck("data.changed", false)),
		set("previous/int-vs-float-differs", fx{config: `{"work": {"workerSlots": 3}}`}, "work.workerSlots", "3.0", eqCheck("data.changed", true)),
		set("parent/created", fx{config: `{"backlog": {"backend": "file"}}`}, "docs.path", "d", eqCheck("data.changed", true)),
		set("parent/scalar-replaced", fx{config: `{"work": "scalar"}`}, "work.dispatch", "tmux", eqCheck("data.previous", nil)),
		set("parent/list-replaced", fx{config: `{"work": [1, 2]}`}, "work.dispatch", "tmux", nil),
		set("parent/null-replaced", fx{config: `{"work": null}`}, "work.dispatch", "tmux", nil),
		set("parent/sibling-keys-kept", fx{config: `{"work": {"isolation": "worktree", "extra": [1]}, "keep": true}`}, "work.dispatch", "tmux", nil),
		set("order/replace-keeps-position", fx{config: `{"z": 1, "models": {"b": 1, "worker": "a", "a": 2}, "a": 0}`}, "models.worker", "b", nil),
		set("order/new-key-is-last", fx{config: `{"z": 1, "models": {"b": 1}, "a": 0}`}, "models.worker", "b", nil),
		set("order/duplicate-keys-collapse", fx{config: `{"models": {"worker": "a", "orchestrator": "o", "worker": "b"}}`}, "models.worker", "c", nil),
		set("file/missing-is-created", withFx(stdFx, func(f *fx) { f.after = noConfigFile }), "ship.qa", "true", eqCheck("data.changed", true)),
		set("file/empty", fx{config: " "}, "ship.qa", "true", nil),
		set("file/corrupt-counts-as-empty", fx{config: `{oops`}, "ship.qa", "true", nil),
		set("file/unicode-kept-escaped", fx{config: `{"note": "kü😀", "x": "café"}`}, "ship.qa", "true", nil),
		set("file/numbers-normalised", fx{config: `{"a": 1.50, "b": 1e2, "c": 10000000000000000000000, "d": -0}`}, "ship.qa", "true", nil),
		set("file/deeply-nested-kept", fx{config: `{"x": {"y": {"z": [1, {"w": null}]}}}`}, "ship.qa", "true", nil),
		set("file/local-is-never-touched", fx{files: map[string]string{".rota/config.local.json": `{"models":{"worker":"local"}}`}}, "models.worker", "opus", nil),
		set("file/set-in-config-even-when-local-overrides", fx{files: map[string]string{".rota/config.local.json": `{"models":{"worker":"local"}}`}, config: `{"models": {"worker": "p"}}`}, "models.worker", "q", eqCheck("data.previous", "p")),
		set("from-subdir", withFile("sub/x.txt", "x\n"), "docs.path", "d", nil),
	)
	for _, body := range []string{"[1]", "null", `"s"`, "7", "true"} {
		body := body
		add(scn{name: "config-set/not-an-object/" + body, fx: fx{config: body}, argv: j("config", "set", "models.worker", "x"),
			want: 70})
	}
	for _, key := range []string{"", ".a", "a.", "a..b", "."} {
		key := key
		add(scn{name: "config-set/malformed-key/" + fmt.Sprintf("%q", key), argv: j("config", "set", key, "1"),
			want: 2})
	}
	add(
		scn{name: "config-set/missing-value", argv: j("config", "set", "models.worker"), want: 2},
		scn{name: "config-set/missing-both", argv: j("config", "set"), want: 2},
		scn{name: "config-set/no-hv", fx: fx{noHV: true}, argv: j("config", "set", "models.worker", "x"), want: 3},
		scn{name: "config-set/umbrella-repo-flag-writes-the-root-config", fx: umbFx, argv: j("config", "set", "--repo", "web", "docs.path", "d"),
			want: 0, check: eqCheck("data.changed", true)},
		scn{name: "config-set/repo-flag-unregistered", goOnly: true, want: 3, fx: umbFx, argv: j("config", "set", "--repo", "nope", "docs.path", "d")},
		scn{name: "config-set/repo-flag-outside-umbrella", goOnly: true, want: 3, argv: j("config", "set", "--repo", "web", "docs.path", "d")},
		scn{name: "config-set/value-with-leading-dash-without-double-dash-is-a-flag", goOnly: true, want: 2, argv: j("config", "set", "models.worker", "-5")},
		scn{name: "config-set/key-outside-schema-is-refused", goOnly: true, want: 2, argv: j("config", "set", "nope.key", "1"),
			check: func(t *testing.T, e envl) {
				b, _ := os.ReadFile(filepath.Join(goDirOf(e), ".rota", "config.json"))
				if string(b) != stdConfig {
					t.Errorf("config.json changed: %s", b)
				}
			}},
		scn{name: "config-set/parent-key-outside-schema-is-refused", goOnly: true, want: 2, argv: j("config", "set", "models", `{"worker": "x"}`)},
		scn{name: "config-set/key-below-a-leaf-is-refused", goOnly: true, want: 2, argv: j("config", "set", "models.worker.deep", "1")},
		scn{name: "config-set/three-positionals", goOnly: true, want: 2, argv: j("config", "set", "models.worker", "a", "b")},
		scn{name: "config-set/empty-string-value-is-stored", goOnly: true, want: 0, argv: j("config", "set", "git.baseBranch", ""),
			check: both(eqCheck("data.value", ""), eqCheck("data.changed", true))},
	)

	// ---- config check
	// the contract exits 1 unless the config is up to date
	chk := func(name string, f fx, want int, status string, missing ...string) scn {
		return scn{name: "config-check/" + name, fx: f, argv: j("config", "check"), want: want,
			text: true, check: checkVerdict(status, missing...)}
	}
	allReq := []string{}
	for _, k := range config.Keys {
		if k.Required {
			allReq = append(allReq, k.Name)
		}
	}
	add(
		chk("up-to-date", fx{config: fullConfig()}, 0, "upToDate"),
		chk("up-to-date-extra-keys", fx{config: strings.Replace(fullConfig(), "{\n", "{\n  \"extra\": [1],\n", 1)}, 0, "upToDate"),
		chk("up-to-date-null-in-optional-key", fx{config: strings.Replace(fullConfig(), "{\n", "{\n  \"issues\": {\"label\": null},\n", 1)}, 0, "upToDate"),
		chk("fresh", withFx(stdFx, func(f *fx) { f.after = noConfigFile }), 1, "fresh"),
		chk("stale-std-config", stdFx, 1, "stale", allReq...),
		chk("stale-empty-object", fx{config: "{}"}, 1, "stale", allReq...),
		chk("stale-one-key", fx{config: fullConfig("umbrella.enabled")}, 1, "stale", "umbrella.enabled"),
		chk("stale-schema-order", fx{config: fullConfig("rota.version", "models.orchestrator", "docs.path")}, 1, "stale", "models.orchestrator", "docs.path", "rota.version"),
		chk("stale-null-value", fx{config: strings.Replace(fullConfig(), `"enabled": false`, `"enabled": null`, 1)}, 1, "stale", "umbrella.enabled"),
		chk("stale-false-is-present", fx{config: fullConfig("ship.qa")}, 1, "stale", "ship.qa"),
		chk("stale-parent-scalar", fx{config: strings.Replace(fullConfig(), "\"models\": {", "\"models\": 5, \"zzz\": {", 1)}, 1, "stale", "models.orchestrator", "models.worker"),
		chk("stale-parent-null", fx{config: `{"models": null}`}, 1, "stale", allReq...),
		chk("stale-ignores-local-file", withFx(fx{config: "{}"}, func(f *fx) { f.files = map[string]string{".rota/config.local.json": fullConfig()} }), 1, "stale", allReq...),
		chk("stale-optional-keys-never-count", fx{config: fullConfig("docs.afterWork")}, 1, "stale", "docs.afterWork"),
		chk("corrupt-invalid-json", fx{config: `{oops`}, 1, "corrupt"),
		chk("corrupt-list", fx{config: `[1, 2]`}, 1, "corrupt"),
		chk("corrupt-null", fx{config: `null`}, 1, "corrupt"),
		chk("corrupt-scalar", fx{config: `7`}, 1, "corrupt"),
		chk("corrupt-empty-file", fx{config: " "}, 1, "corrupt"),
		chk("corrupt-truncated", fx{config: fullConfig()[:40]}, 1, "corrupt"),
		chk("from-subdir", withFile("sub/x.txt", "x\n"), 1, "stale", allReq...),
		scn{name: "config-check/no-hv", fx: fx{noHV: true}, argv: j("config", "check"), want: 3},
		scn{name: "config-check/repo-flag-registered", fx: withFx(umbFx, func(f *fx) { f.config = fullConfig() }), argv: j("config", "check", "--repo", "api"),
			want: 0, check: checkVerdict("upToDate")},
		scn{name: "config-check/repo-flag-unregistered", goOnly: true, want: 3, fx: umbFx, argv: j("config", "check", "--repo", "nope")},
		scn{name: "config-check/positional", goOnly: true, want: 2, argv: j("config", "check", "x")},
		scn{name: "config-check/failure-data-keeps-the-verdict", goOnly: true, want: 1, fx: fx{config: "{}"}, argv: j("config", "check"),
			check: func(t *testing.T, e envl) {
				eq(t, e, "ok", false)
				eq(t, e, "error.code", "failed")
				eq(t, e, "data.status", "stale")
				if got := len(strs(at(e, "data.missing"))); got != len(allReq) {
					t.Errorf("missing has %d keys, want %d", got, len(allReq))
				}
			}},
	)

	// ---- repo umbrella
	umb := func(name string, f fx, want bool) scn {
		w := 1
		if want {
			w = 0
		}
		return scn{name: "repo-umbrella/" + name, fx: f, argv: j("repo", "umbrella"), want: w,
			text: true, check: eqCheck("data.umbrella", want)}
	}
	add(
		umb("two-sub-repos", umbFx, true),
		umb("single-project", stdFx, false),
		umb("empty-registry", registry(`{"repos": []}`+"\n"), false),
		umb("no-repos-key", registry(`{}`), false),
		umb("corrupt-registry", registry(`{oops`), false),
		scn{name: "repo-umbrella/registry-is-a-list", fx: registry(`[1]`), argv: j("repo", "umbrella"), want: 1,
			check: eqCheck("data.umbrella", false)},
		umb("entries-without-name-or-path", registry(`{"repos": [{"name": "a"}, {"path": "b"}, {"name": "", "path": "c"}]}`), false),
		umb("one-valid-entry-among-junk", registry(`{"repos": [{"name": "a"}, {"name": "ok", "path": "api"}]}`), true),
		umb("missing-path-still-counts", registry(`{"repos": [{"name": "ghost", "path": "not/there"}]}`), true),
		umb("config-flag-is-ignored-on", withFx(umbFx, func(f *fx) { f.config = `{"umbrella": {"enabled": false}}` }), true),
		umb("config-flag-is-ignored-off", withFx(stdFx, func(f *fx) { f.config = `{"umbrella": {"enabled": true}}` }), false),
		umb("no-registry-file-with-hv", withFx(stdFx, func(f *fx) {
			f.after = func(t *testing.T, d string, in *info) { os.Remove(filepath.Join(d, ".rota/repos.json")) }
		}), false),
		umb("no-rota-at-all", fx{noHV: true}, false),
		// the contract walks up to the nearest .rota/
		scn{name: "repo-umbrella/from-a-sub-repo-walks-up", fx: umbFx, cwd: "web", argv: j("repo", "umbrella"), want: 0,
			check: eqCheck("data.umbrella", true)},
		scn{name: "repo-umbrella/stray-rota-in-a-sub-repo-is-the-nearest", cwd: "web", want: 1, fx: withFx(umbFx, func(f *fx) {
			f.after = func(t *testing.T, d string, in *info) { os.MkdirAll(filepath.Join(d, "web", ".rota"), 0o755) }
		}), argv: j("repo", "umbrella"), check: eqCheck("data.umbrella", false)},
		scn{name: "repo-umbrella/with-C-on-the-root", fx: umbFx, cwd: "web", goOnly: true, want: 0, argv: j("-C", "..", "repo", "umbrella"), check: eqCheck("data.umbrella", true)},
		scn{name: "repo-umbrella/repo-flag-rejected", goOnly: true, want: 2, fx: umbFx, argv: j("repo", "umbrella", "--repo", "web")},
		scn{name: "repo-umbrella/positional-rejected", goOnly: true, want: 2, argv: j("repo", "umbrella", "x")},
		scn{name: "repo-umbrella/failure-carries-data", goOnly: true, want: 1, argv: j("repo", "umbrella"),
			check: func(t *testing.T, e envl) { eq(t, e, "error.code", "failed"); eq(t, e, "data.umbrella", false) }},
		scn{name: "repo-umbrella/no-root-is-no-not-3", goOnly: true, want: 1, fx: fx{noHV: true}, argv: j("repo", "umbrella")},
	)

	// ---- repo resolve
	res := func(name string, f fx, want int, args ...string) scn {
		return scn{name: "repo-resolve/" + name, fx: f, argv: j(append([]string{"repo", "resolve"}, args...)...),
			want: want}
	}
	linkFx := withFx(umbFx, func(f *fx) {
		f.after = func(t *testing.T, dir string, in *info) {
			if err := os.Symlink("web", filepath.Join(dir, "link")); err != nil {
				t.Fatal(err)
			}
			write(t, dir, ".rota/repos.json", `{"repos": [{"name": "alias", "path": "link"}, {"name": "web", "path": "web"}, {"name": "ghost", "path": "not/there/yet"}, {"name": "deep", "path": "apps/deep"}, {"name": "up", "path": "../elsewhere"}]}`+"\n")
			os.MkdirAll(filepath.Join(dir, "apps", "deep"), 0o755)
		}
	})
	add(
		res("one", umbFx, 0, "web"),
		res("two-in-argument-order", umbFx, 0, "web", "api"),
		res("reversed", umbFx, 0, "api", "web"),
		res("duplicates-preserved", umbFx, 0, "web", "api", "web", "web"),
		res("zero-names", umbFx, 0),
		res("zero-names-outside-umbrella", stdFx, 0),
		res("zero-names-without-hv", fx{noHV: true}, 0),
		res("csv-in-one-argument", umbFx, 0, "web,api"),
		res("csv-with-spaces", umbFx, 0, "web, api ,web"),
		res("empty-argument-is-dropped", umbFx, 0, "", "web"),
		res("blank-arguments-give-none", umbFx, 0, " ", ""),
		res("trailing-comma", umbFx, 0, "web,"),
		res("symlinked-path", linkFx, 0, "alias", "web"),
		res("path-that-does-not-exist-yet", linkFx, 0, "ghost"),
		res("nested-path", linkFx, 0, "deep"),
		res("path-above-the-root", linkFx, 0, "up"),
		res("all-five", linkFx, 0, "alias", "web", "ghost", "deep", "up"),
		res("unknown-one", umbFx, 3, "nope"),
		res("unknown-two", umbFx, 3, "x", "web", "y"),
		res("unknown-all", umbFx, 3, "x", "y", "z"),
		res("unknown-is-case-sensitive", umbFx, 3, "Web"),
		res("not-an-umbrella", stdFx, 3, "web"),
		res("no-hv", fx{noHV: true}, 3, "web"),
		res("empty-registry", registry(`{"repos": []}`), 3, "web"),
		res("corrupt-registry", registry(`{oops`), 3, "web"),
		res("entries-without-path-are-not-registered", registry(`{"repos": [{"name": "web"}]}`), 3, "web"),
		res("duplicate-name-last-path-wins", registry(`{"repos": [{"name": "x", "path": "web"}, {"name": "y", "path": "api"}, {"name": "x", "path": "api"}]}`), 0, "x", "y"),
		res("absolute-registry-path", withFx(umbFx, func(f *fx) {
			f.after = func(t *testing.T, dir string, in *info) {
				write(t, dir, ".rota/repos.json", fmt.Sprintf(`{"repos": [{"name": "abs", "path": %q}]}`, filepath.Join(dir, "api")))
			}
		}), 0, "abs"),
		// the contract walks up to the nearest .rota/
		scn{name: "repo-resolve/from-a-sub-repo-walks-up", fx: umbFx, cwd: "web", argv: j("repo", "resolve", "api"),
			want: 0,
			check: func(t *testing.T, e envl) {
				eq(t, e, "data.repos.0.name", "api")
				if p, _ := at(e, "data.repos.0.path").(string); dirNorm(p, goDirOf(e)) != "<root>/api" {
					t.Errorf("path = %s", p)
				}
			}},
		scn{name: "repo-resolve/error-names-every-unregistered-name", goOnly: true, want: 3, fx: umbFx, argv: j("repo", "resolve", "x", "web", "y"),
			check: func(t *testing.T, e envl) {
				m, _ := at(e, "error.message").(string)
				if !strings.Contains(m, "x, y") || strings.Contains(m, "web") {
					t.Errorf("message = %q", m)
				}
			}},
		scn{name: "repo-resolve/repo-flag-rejected", goOnly: true, want: 2, fx: umbFx, argv: j("repo", "resolve", "--repo", "web", "web")},
		scn{name: "repo-resolve/zero-names-envelope", goOnly: true, want: 0, fx: umbFx, argv: j("repo", "resolve"),
			check: func(t *testing.T, e envl) {
				if l, ok := at(e, "data.repos").([]any); !ok || len(l) != 0 {
					t.Errorf("data.repos = %#v", at(e, "data.repos"))
				}
			}},
	)

	// ---- repo which
	worktree := func(t *testing.T, dir string) {
		git(t, filepath.Join(dir, "web"), "worktree", "add", "-q", "-b", "feat/x", filepath.Join(dir, "web-wt"))
	}
	subdirs := func(t *testing.T, dir string, in *info) {
		write(t, dir, "web/src/deep/x.txt", "x\n")
		write(t, dir, "api/lib/x.txt", "x\n")
		write(t, dir, "docs/x.txt", "x\n")
		if err := os.Symlink("web", filepath.Join(dir, "weblink")); err != nil {
			t.Fatal(err)
		}
	}
	whichFx := withFx(umbFx, func(f *fx) { f.after = subdirs })
	which := func(name string, f fx, cwd string, want int, wn, wr string) scn {
		s := scn{name: "repo-which/" + name, fx: f, cwd: cwd, argv: j("repo", "which"), want: want}
		if want == 0 {
			s.check = checkWhich(wn, wr)
		}
		return s
	}
	maskedFx := withFx(whichFx, func(f *fx) {
		prev := f.after
		f.after = afterAll(prev, func(t *testing.T, dir string, in *info) { os.MkdirAll(filepath.Join(dir, "web", ".rota"), 0o755) })
	})
	nested := withFx(umbFx, func(f *fx) {
		f.after = func(t *testing.T, dir string, in *info) {
			os.MkdirAll(filepath.Join(dir, "apps", "deep"), 0o755)
			git(t, filepath.Join(dir, "apps", "deep"), "init", "-q", "-b", "main")
			git(t, filepath.Join(dir, "apps", "deep"), "config", "user.name", "F")
			git(t, filepath.Join(dir, "apps", "deep"), "config", "user.email", "f@e")
			commitFile(t, filepath.Join(dir, "apps", "deep"), "c", "f.txt", "x\n")
			write(t, dir, "apps/deep/src/x.txt", "x\n")
			write(t, dir, ".rota/repos.json", `{"repos": [{"name": "deep", "path": "apps/deep"}, {"name": "web", "path": "web"}]}`+"\n")
		}
	})
	add(
		which("inside-web", whichFx, "web", 0, "web", "web"),
		which("inside-api", whichFx, "api", 0, "api", "api"),
		which("deep-in-web", whichFx, "web/src/deep", 0, "web", "web"),
		which("deep-in-api", whichFx, "api/lib", 0, "api", "api"),
		which("through-a-symlinked-cwd", whichFx, "weblink", 0, "web", "web"),
		which("nested-registered-path", nested, "apps/deep", 0, "deep", "apps/deep"),
		which("nested-registered-path-deeper", nested, "apps/deep/src", 0, "deep", "apps/deep"),
		which("sibling-of-nested-path", nested, "web", 0, "web", "web"),
		which("umbrella-root-is-not-registered", whichFx, "", 3, "", ""),
		which("umbrella-subdir-in-the-root-repo", whichFx, "docs", 3, "", ""),
		which("not-an-umbrella", stdFx, "", 3, "", ""),
		which("no-hv", fx{noHV: true}, "", 3, "", ""),
		which("empty-registry", withFx(whichFx, func(f *fx) {
			prev := f.after
			f.after = afterAll(prev, func(t *testing.T, dir string, in *info) { write(t, dir, ".rota/repos.json", `{"repos": []}`+"\n") })
		}), "web", 3, "", ""),
		which("corrupt-registry", withFx(whichFx, func(f *fx) {
			prev := f.after
			f.after = afterAll(prev, func(t *testing.T, dir string, in *info) { write(t, dir, ".rota/repos.json", "{oops") })
		}), "web", 3, "", ""),
		which("git-repo-not-in-the-registry", withFx(whichFx, func(f *fx) {
			prev := f.after
			f.after = afterAll(prev, func(t *testing.T, dir string, in *info) {
				write(t, dir, ".rota/repos.json", `{"repos": [{"name": "api", "path": "api"}]}`+"\n")
			})
		}), "web", 3, "", ""),
		which("masked-by-a-stray-hv", maskedFx, "web", 3, "", ""),
		which("masked-from-a-subdir-of-the-sub-repo", maskedFx, "web/src/deep", 3, "", ""),
		which("stray-rota-in-another-sub-repo-does-not-mask", maskedFx, "api", 0, "api", "api"),
		which("stray-rota-in-the-root-subdir-docs-is-not-registered", withFx(whichFx, func(f *fx) {
			prev := f.after
			f.after = afterAll(prev, func(t *testing.T, dir string, in *info) { os.MkdirAll(filepath.Join(dir, "docs", ".rota"), 0o755) })
		}), "docs", 3, "", ""),
		scn{name: "repo-which/layout-b-worktree-maps-to-the-main-repo", fx: whichFx, prep: worktree, cwd: "web-wt", argv: j("repo", "which"),
			want: 0, check: checkWhich("web", "web")},
		scn{name: "repo-which/layout-b-worktree-subdir", fx: whichFx, prep: func(t *testing.T, dir string) {
			worktree(t, dir)
			write(t, dir, "web-wt/pkg/x.txt", "x\n")
		}, cwd: "web-wt/pkg", argv: j("repo", "which"), want: 0, check: checkWhich("web", "web")},
		scn{name: "repo-which/worktree-of-an-unregistered-repo", fx: whichFx, prep: func(t *testing.T, dir string) {
			git(t, filepath.Join(dir, "api"), "worktree", "add", "-q", "-b", "feat/y", filepath.Join(dir, "api-wt"))
			write(t, dir, ".rota/repos.json", `{"repos": [{"name": "web", "path": "web"}]}`+"\n")
		}, cwd: "api-wt", argv: j("repo", "which"), want: 3},
		scn{name: "repo-which/worktree-inside-the-sub-repo-with-stray-rota-masks", fx: whichFx, prep: func(t *testing.T, dir string) {
			git(t, filepath.Join(dir, "web"), "worktree", "add", "-q", "-b", "feat/z", filepath.Join(dir, "web", "wt"))
			os.MkdirAll(filepath.Join(dir, "web", ".rota"), 0o755)
		}, cwd: "web/wt", argv: j("repo", "which"), want: 3},
		scn{name: "repo-which/git-missing-is-5", goOnly: true, fx: whichFx, cwd: "web", argv: j("repo", "which"), want: 5,
			env:   []string{"PATH=/nonexistent"},
			check: func(t *testing.T, e envl) { eq(t, e, "error.code", "unavailable") }},
		scn{name: "repo-which/repo-flag-rejected", goOnly: true, want: 2, fx: whichFx, cwd: "web", argv: j("repo", "which", "--repo", "web")},
		scn{name: "repo-which/positional-rejected", goOnly: true, want: 2, argv: j("repo", "which", "x")},
		scn{name: "repo-which/masked-error-hint-names-the-stray-dir", goOnly: true, want: 3, fx: maskedFx, cwd: "web", argv: j("repo", "which"),
			check: func(t *testing.T, e envl) {
				eq(t, e, "error.code", "resolution")
				if m, _ := at(e, "error.message").(string); !strings.Contains(m, "stray .rota/") || !strings.Contains(m, "web") {
					t.Errorf("message = %q", m)
				}
				if h, _ := at(e, "error.hint").(string); !strings.Contains(h, filepath.Join("web", ".rota")) {
					t.Errorf("hint = %q", h)
				}
			}},
		scn{name: "repo-which/with-C", goOnly: true, want: 0, fx: whichFx, argv: j("-C", "web/src", "repo", "which"),
			check: eqCheck("data.name", "web")},
		scn{name: "repo-which/not-in-a-git-repo-message", goOnly: true, want: 3, fx: fx{noHV: false, noCommit: false}, argv: j("-C", "/", "repo", "which"),
			check: func(t *testing.T, e envl) { eq(t, e, "error.code", "resolution") }},
	)

	// ---- update: install types, statuses and the safety net
	add(
		updScn("script/behind", "1.2.4", "script", "behind"),
		updScn("script/current", "1.2.3", "script", "current"),
		updScn("script/ahead", "1.2.2", "script", "ahead"),
		updScn("script/numeric-not-lexical", "1.10.0", "script", "behind"),
		updScn("script/major", "2.0.0", "script", "behind"),
		updScn("script/two-part-latest", "1.2", "script", "ahead"),
		updScn("script/v-prefixed-latest-from-the-variable", "v1.3.0", "script", "behind"),
		updScn("script/prerelease-suffix-ignored", "1.2.3-rc1", "script", "current"),
		updScn("script/four-parts-only-first-three-count", "1.2.3.9", "script", "current"),
		updScn("script/huge-component", "1.2.99999999999999999999999", "script", "behind"),
		updScn("script/latest-without-digits", "abc", "script", "ahead"),
		scn{name: "update/script-command-reruns-the-installer", fx: fx{noHV: true}, argv: j("update"), want: 0, bin: upd.bin,
			env: []string{"HOME=" + upd.homeNone, "ROTA_TEST_LATEST_VERSION=1.2.4"},
			check: func(t *testing.T, e envl) {
				if c, _ := at(e, "data.updateCommand").(string); !strings.Contains(c, "install.sh") || !strings.HasSuffix(c, "rota skills update") {
					t.Errorf("updateCommand = %q", c)
				}
			}},
		scn{name: "update/brew-binary", fx: fx{noHV: true}, argv: j("update"), want: 0, bin: upd.brewBin,
			env:   []string{"HOME=" + upd.homeNone, "ROTA_TEST_LATEST_VERSION=1.2.4"},
			check: both(checkUpdate("brew", "behind"), eqCheck("data.updateCommand", "brew update && brew upgrade rota && rota skills update"))},
		scn{name: "update/runs-inside-a-project", argv: j("update"), want: 0, bin: upd.bin,
			env:   []string{"HOME=" + upd.homeNone, "ROTA_TEST_LATEST_VERSION=1.2.4"},
			check: checkUpdate("script", "behind")},
		scn{name: "update/runs-from-a-subdirectory", fx: withFile("sub/x.txt", "x\n"), cwd: "sub", argv: j("update"), want: 0, bin: upd.bin,
			env:   []string{"HOME=" + upd.homeNone, "ROTA_TEST_LATEST_VERSION=1.2.4"},
			check: checkUpdate("script", "behind")},
		scn{name: "update/dev-build-compares-as-zero", fx: fx{noHV: true}, argv: j("update"), goOnly: true, want: 0,
			env:   []string{"HOME=" + upd.homeNone, "ROTA_TEST_LATEST_VERSION=0.0.0"},
			check: both(eqCheck("data.currentVersion", "dev"), eqCheck("data.status", "current"), eqCheck("data.installType", "dev"))},
		scn{name: "update/without-the-test-variable-only-the-fake-gh-runs", fx: fx{noHV: true}, argv: j("update"), want: 0, bin: upd.bin,
			env: []string{"HOME=" + upd.homeNone, "FAKE_TRACKER_LOG=" + filepath.Join(harnessTmp, "update-gh.log")},
			check: func(t *testing.T, e envl) {
				checkUpdate("script", "unknown")(t, e)
				b, err := os.ReadFile(filepath.Join(harnessTmp, "update-gh.log"))
				if err != nil {
					t.Fatalf("the fake gh was not called: %v", err)
				}
				for _, l := range lines(string(b)) {
					if !strings.HasPrefix(l, "api repos/l4ci/rota/releases/latest") {
						t.Errorf("gh was called with %q", l)
					}
				}
			}},
		scn{name: "update/positional-rejected", goOnly: true, want: 2, argv: j("update", "x"), env: []string{"ROTA_TEST_LATEST_VERSION=1.0.0"}},
		scn{name: "update/repo-flag-rejected", goOnly: true, want: 2, argv: j("update", "--repo", "web"), env: []string{"ROTA_TEST_LATEST_VERSION=1.0.0"}},
		scn{name: "update/data-shape", goOnly: true, want: 0, argv: j("update"), bin: upd.bin,
			env: []string{"HOME=" + upd.homeNone, "ROTA_TEST_LATEST_VERSION=1.2.4"},
			check: func(t *testing.T, e envl) {
				d, _ := at(e, "data").(map[string]any)
				var keys []string
				for k := range d {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				want := []string{"currentVersion", "installRoot", "installType", "latestVersion", "status", "updateCommand"}
				if !reflect.DeepEqual(keys, want) {
					t.Errorf("keys = %v", keys)
				}
			}},
	)

	finish(t, all)
}

// TestConfigSetStoresNaNAsString: NaN is not JSON, so config set stores it as
// the string "NaN" (the old helper wrote a bare NaN).
func TestConfigSetStoresNaNAsString(t *testing.T) {
	dir, _ := fx{}.build(t)
	if r := exec1e(t, dir, "", nil, rotaBin, "--json", "config", "set", "git.baseBranch", "NaN"); r.code != 0 || !strings.Contains(r.stdout, `"value": "NaN"`) {
		t.Errorf("exit %d: %s", r.code, r.stdout)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".rota", "config.json"))
	if !strings.Contains(string(b), `"NaN"`) {
		t.Errorf("config.json does not hold the string \"NaN\":\n%s", b)
	}
}
