package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/golden"
)

const acctConfig = `{"work":{"accounts":[
 {"name":"alpha","configDir":"/acct/alpha"},
 {"name":"beta","configDir":"/acct/beta"},
 {"name":"gamma","configDir":"/acct/gamma"},
 {"name":"delta","configDir":"/acct/delta"},
 {"name":"epsilon","configDir":"/acct/epsilon"},
 {"name":"zeta","configDir":"/acct/zeta"}]}}`

// future is a fixed reset far ahead, so the goldens recorded against it stay
// deterministic and the cooling windows never lapse.
const future = "2099-01-01T00:00:00.000000+00:00"

// usageFixtures are the per-account payloads read from ROTA_ACCOUNT_USAGE_DIR.
// alpha: 30% / 10%; beta: five-hour spent with a future
// reset (cooling); gamma: weekly spent but extra usage live (free, discounted);
// delta: weekly spent, no extra usage, future reset (cooling); epsilon:
// spent window with no reset (free); zeta: no fixture (unknown).
func usageFixtures() map[string]string {
	return map[string]string{
		"alpha":   `{"five_hour":{"utilization":30,"resets_at":null},"seven_day":{"utilization":10.5,"resets_at":null}}`,
		"beta":    `{"five_hour":{"utilization":100,"resets_at":"` + future + `"},"seven_day":{"utilization":5,"resets_at":null}}`,
		"gamma":   `{"five_hour":{"utilization":20,"resets_at":null},"seven_day":{"utilization":100,"resets_at":"` + future + `"},"extra_usage":{"is_enabled":true,"spend_limit_reached":false}}`,
		"delta":   `{"five_hour":{"utilization":1,"resets_at":null},"seven_day":{"utilization":100,"resets_at":"` + future + `"},"extra_usage":{"is_enabled":true,"spend_limit_reached":true}}`,
		"epsilon": `{"five_hour":{"utilization":100,"resets_at":null},"seven_day":{"utilization":57.45,"resets_at":null}}`,
	}
}

// usageDir writes usageFixtures into a fresh directory.
func usageDir(t *testing.T) string {
	dir := t.TempDir()
	for name, body := range usageFixtures() {
		os.WriteFile(filepath.Join(dir, name+".json"), []byte(body), 0o644)
	}
	return dir
}

func goMeters(t *testing.T, dir, usage string) []Meter {
	t.Helper()
	acc := &Accounts{Getenv: func(k string) string {
		if k == "ROTA_ACCOUNT_USAGE_DIR" {
			return usage
		}
		return ""
	}}
	return acc.Meters(bg, dir)
}

func TestAccountListMeters(t *testing.T) {
	dir, usage := newProject(t, acctConfig), usageDir(t)
	var old []map[string]any
	golden.Golden(t, map[string]any{"argv": []string{"list", "--json"}, "config": acctConfig, "usage": usageFixtures()}, &old)
	got := goMeters(t, dir, usage)
	if len(got) != len(old) {
		t.Fatalf("%d rows, old %d", len(got), len(old))
	}
	for i, m := range got {
		o := old[i]
		if m.Name != o["name"] || m.ConfigDir != o["configDir"] || m.Verdict != o["verdict"] || m.Reason != o["reason"] {
			t.Errorf("row %d: %+v vs old %v", i, m, o)
		}
		for key, v := range map[string]*float64{"fiveHour": m.FiveHour, "sevenDay": m.SevenDay, "headroom": m.Headroom} {
			if (o[key] == nil) != (v == nil) || (v != nil && o[key].(float64) != *v) {
				t.Errorf("%s %s = %v, old %v", m.Name, key, v, o[key])
			}
		}
		oldReset, _ := o["resetsAt"].(string)
		gotReset := ""
		if m.ResetsAt != nil {
			gotReset = ISOFormat(*m.ResetsAt)
		}
		if oldReset != gotReset {
			t.Errorf("%s resetsAt = %q, old %q", m.Name, gotReset, oldReset)
		}
	}
	verdicts := map[string]string{}
	for _, m := range got {
		verdicts[m.Name] = m.Verdict
	}
	want := map[string]string{"alpha": "free", "beta": "cooling", "gamma": "free", "delta": "cooling", "epsilon": "free", "zeta": "unknown"}
	for k, v := range want {
		if verdicts[k] != v {
			t.Errorf("%s verdict = %s, want %s", k, verdicts[k], v)
		}
	}
	// gamma's weekly window is discounted, so its headroom comes from five_hour alone
	for _, m := range got {
		if m.Name == "gamma" && (m.Headroom == nil || *m.Headroom != 80) {
			t.Errorf("gamma headroom = %v, want 80", m.Headroom)
		}
		if m.Name == "epsilon" && (m.Headroom == nil || *m.Headroom != 0) {
			t.Errorf("epsilon headroom = %v", m.Headroom)
		}
	}
}

func TestAccountListWithoutAccounts(t *testing.T) {
	if got := goMeters(t, newProject(t, `{}`), ""); len(got) != 0 {
		t.Errorf("%v", got)
	}
}

func TestAccountPick(t *testing.T) {
	dir, usage := newProject(t, acctConfig), usageDir(t)
	acc := &Accounts{Getenv: func(k string) string {
		if k == "ROTA_ACCOUNT_USAGE_DIR" {
			return usage
		}
		return ""
	}}
	excludes := []string{"", "gamma", "gamma,alpha", " gamma , alpha ", "gamma,alpha,epsilon,zeta", "alpha,beta,gamma,delta,epsilon,zeta"}
	var picks []string // "" where the helper exited non-zero: nothing eligible
	golden.Golden(t, map[string]any{"config": acctConfig, "usage": usageFixtures(), "excludes": excludes}, &picks)
	for i, excl := range excludes {
		var skip []string
		if excl != "" {
			skip = strings.Split(excl, ",")
		}
		name, ok := acc.Pick(bg, dir, skip)
		if picks[i] == "" {
			if ok {
				t.Errorf("exclude %q: nothing was eligible, go picked %q", excl, name)
			}
		} else if !ok || name != picks[i] {
			t.Errorf("exclude %q: go %q/%v, want %q", excl, name, ok, picks[i])
		}
	}
}

func TestAccountPickRotatesWhenNoMeterIsReadable(t *testing.T) {
	dir := newProject(t, acctConfig)
	empty := t.TempDir()
	acc := &Accounts{Getenv: func(string) string { return empty }}
	name, ok := acc.Pick(bg, dir, nil)
	if !ok || name != "alpha" {
		t.Errorf("unknown meters must stay eligible: go %q", name)
	}
}

func TestAccountAssign(t *testing.T) {
	b := newProject(t, acctConfig)
	goInit(t, b, InitOpts{Slots: 2, Base: "main"})
	var want map[string]string // workers.json the helper left after each assign
	golden.Golden(t, map[string]any{"config": acctConfig, "usage": usageFixtures(), "pool": "init --slots 2 --base main",
		"steps": []string{"assign --slot w1 --account beta", "assign --slot w2"}}, &want)
	usage := usageDir(t)
	acc := &Accounts{Getenv: func(k string) string {
		if k == "ROTA_ACCOUNT_USAGE_DIR" {
			return usage
		}
		return ""
	}}

	name, changed, err := acc.Assign(bg, b, "w1", "beta")
	if err != nil || name != "beta" || !changed {
		t.Fatalf("go %v %v %v", name, changed, err)
	}
	mustEqual(t, "workers.json", want["after w1 beta"], registry(t, b))

	if _, changed, _ := acc.Assign(bg, b, "w1", "beta"); changed {
		t.Error("assigning the same account again must report changed=false")
	}

	// no --account: the best pick
	name, _, err = acc.Assign(bg, b, "w2", "")
	if err != nil || name == "" {
		t.Fatalf("go %v %v", name, err)
	}
	mustEqual(t, "workers.json after pick", want["after w2 pick"], registry(t, b))

	exitOf := func(err error) int {
		if we, ok := err.(*Error); ok {
			return we.Exit
		}
		return -1
	}
	_, _, err = acc.Assign(bg, b, "w1", "nope")
	if exitOf(err) != ExitResolution || !strings.Contains(err.Error(), "account 'nope' is not in work.accounts") {
		t.Errorf("unknown account: %v", err)
	}
	_, _, err = acc.Assign(bg, b, "w9", "alpha")
	if exitOf(err) != ExitResolution || !strings.Contains(err.Error(), "slot 'w9' is not in the pool") {
		t.Errorf("unknown slot: %v", err)
	}
	_, _, err = acc.Assign(bg, newProject(t, acctConfig), "w1", "alpha")
	if exitOf(err) != ExitResolution || !strings.Contains(err.Error(), "no worker pool") {
		t.Errorf("no registry: %v", err)
	}
}

func TestAccountAssignWithEveryAccountCoolingIsRefused(t *testing.T) {
	cfg := `{"work":{"accounts":[{"name":"beta","configDir":"/acct/beta"}]}}`
	dir := newProject(t, cfg)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	acc := &Accounts{Getenv: func(k string) string {
		if k == "ROTA_ACCOUNT_USAGE_DIR" {
			return usageDir(t)
		}
		return ""
	}}
	_, _, err := acc.Assign(bg, dir, "w1", "")
	if we, ok := err.(*Error); !ok || we.Exit != ExitRefused {
		t.Errorf("err = %v, want exit 4", err)
	}
}

// pool init spreads slots across accounts by headroom, resetting the
// exclusion list when accounts run out.
func TestPoolInitSpreadsAccounts(t *testing.T) {
	b := newProject(t, acctConfig)
	usage := usageDir(t)
	var want map[string]string
	golden.Golden(t, map[string]any{"config": acctConfig, "usage": usageFixtures(), "argv": "init --slots 5 --base main"}, &want)
	acc := &Accounts{Getenv: func(k string) string {
		if k == "ROTA_ACCOUNT_USAGE_DIR" {
			return usage
		}
		return ""
	}}
	if _, err := (Env{}).PoolInit(bg, b, InitOpts{Slots: 5, Base: "main"}, acc); err != nil {
		t.Fatal(err)
	}
	mustEqual(t, "workers.json", want["workers.json"], registry(t, b))
	if !strings.Contains(registry(t, b), `"account": "alpha"`) {
		t.Error("no slot was assigned an account")
	}
}

func TestISOFormatMatchesPython(t *testing.T) {
	for in, want := range map[string]string{
		"2026-10-02T15:00:00Z":             "2026-10-02T15:00:00+00:00",
		"2026-10-02T15:00:00.5Z":           "2026-10-02T15:00:00.500000+00:00",
		"2026-10-02T17:00:00.123456+02:00": "2026-10-02T17:00:00.123456+02:00",
	} {
		tm := parseReset(in)
		if tm == nil || ISOFormat(*tm) != want {
			t.Errorf("%s -> %v, want %s", in, tm, want)
		}
	}
	if parseReset("garbage") != nil || parseReset(nil) != nil || parseReset("") != nil {
		t.Error("unparseable resets must read as none")
	}
}

func TestExpiredTokenReportsUnknownWithoutNetwork(t *testing.T) {
	cfgDir := t.TempDir()
	os.WriteFile(filepath.Join(cfgDir, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"x","expiresAt":1000}}`), 0o600)
	acc := &Accounts{HTTP: nil}
	if tok, why := acc.token(cfgDir); tok != "" || why != "token expired" {
		t.Errorf("token = %q, %q", tok, why)
	}
	if tok, why := acc.token(t.TempDir()); tok != "" || why != "no credentials file" {
		t.Errorf("token = %q, %q", tok, why)
	}
}

func TestOrchestratorTarget(t *testing.T) {
	dir, usage := newProject(t, acctConfig), usageDir(t)
	acc := &Accounts{Getenv: func(k string) string {
		if k == "ROTA_ACCOUNT_USAGE_DIR" {
			return usage
		}
		return ""
	}}
	// From alpha: gamma has the most headroom (80). beta and delta cool, epsilon
	// has none, zeta has no reading and so is not a candidate.
	m, ok, others := acc.OrchestratorTarget(bg, dir, "/acct/alpha", 90)
	if !ok || m.Name != "gamma" {
		t.Fatalf("%v %v", m.Name, ok)
	}
	joined := strings.Join(others, "; ")
	for _, want := range []string{"alpha: current account", "beta: cooling", "epsilon: 0% headroom", "zeta: unknown"} {
		if !strings.Contains(joined, want) {
			t.Errorf("others lack %q: %s", want, joined)
		}
	}
	// The current account is never its own target, and gamma's 80 beats alpha's 69.5.
	if m, _, _ := acc.OrchestratorTarget(bg, dir, "/acct/gamma", 90); m.Name != "alpha" {
		t.Errorf("from gamma: %s", m.Name)
	}
	// The target must be below the threshold: alpha has 69.5 headroom, so a
	// threshold of 20 (headroom above 80 needed) takes neither.
	if _, ok, _ := acc.OrchestratorTarget(bg, dir, "/acct/zeta", 20); ok {
		t.Error("an account above the threshold was taken")
	}
	// An account with no configDir is not a candidate.
	d2 := newProject(t, `{"work":{"accounts":[{"name":"alpha","configDir":"/acct/alpha"},{"name":"gamma"}]}}`)
	if _, ok, o := acc.OrchestratorTarget(bg, d2, "/acct/alpha", 90); ok || !strings.Contains(strings.Join(o, ";"), "gamma: no configDir") {
		t.Errorf("no configDir: %v %v", ok, o)
	}
}

func TestSameConfigDirAndAccountOf(t *testing.T) {
	home, _ := os.UserHomeDir()
	if !SameConfigDir("", filepath.Join(home, ".claude")) || !SameConfigDir("~/.x/", filepath.Join(home, ".x")) || SameConfigDir("/a", "/b") {
		t.Error("SameConfigDir")
	}
	dir := newProject(t, acctConfig)
	if AccountOf(dir, "/acct/beta/") != "beta" || AccountOf(dir, "/elsewhere") != "" {
		t.Error("AccountOf")
	}
}
