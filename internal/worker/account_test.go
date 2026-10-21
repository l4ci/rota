package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/l4ci/rota/internal/exitcode"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/golden"
	"github.com/l4ci/rota/internal/proc"
)

const acctConfig = `{"round":{"accounts":[
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
	acc := &Accounts{Env: Env{Getenv: func(k string) string {
		if k == "ROTA_ACCOUNT_USAGE_DIR" {
			return usage
		}
		return ""
	}}}
	return acc.Meters(bg, dir)
}

func TestAccountListMeters(t *testing.T) {
	dir, usage := newProject(t, acctConfig), usageDir(t)
	got := goMeters(t, dir, usage)
	rows := make([]map[string]any, 0, len(got))
	for _, m := range got {
		var resets any
		if m.ResetsAt != nil {
			resets = ISOFormat(*m.ResetsAt)
		}
		rows = append(rows, map[string]any{"name": m.Name, "configDir": m.ConfigDir, "verdict": m.Verdict, "reason": m.Reason,
			"fiveHour": m.FiveHour, "sevenDay": m.SevenDay, "headroom": m.Headroom, "resetsAt": resets})
	}
	golden.Check(t, map[string]any{"argv": []string{"list", "--json"}, "config": acctConfig, "usage": usageFixtures()}, rows)
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

// loggedIn is a Credentials whose Keychain holds a usable login for every
// config dir, so tests with made-up dirs pass the Pick login check.
func loggedIn() Credentials {
	c, _ := fakeKeychain("darwin", nil)
	c.Run = func(context.Context, proc.Cmd) (proc.Result, error) {
		return proc.Result{Stdout: `{"claudeAiOauth":{"accessToken":"t"}}` + "\n"}, nil
	}
	return c
}

func TestAccountPick(t *testing.T) {
	dir, usage := newProject(t, acctConfig), usageDir(t)
	acc := &Accounts{Login: loggedIn(), Env: Env{Getenv: func(k string) string {
		if k == "ROTA_ACCOUNT_USAGE_DIR" {
			return usage
		}
		return ""
	}}}
	excludes := []string{"", "gamma", "gamma,alpha", " gamma , alpha ", "gamma,alpha,epsilon,zeta", "alpha,beta,gamma,delta,epsilon,zeta"}
	picks := []string{} // "" where nothing was eligible
	for _, excl := range excludes {
		var skip []string
		if excl != "" {
			skip = strings.Split(excl, ",")
		}
		name, _ := acc.Pick(bg, dir, skip)
		picks = append(picks, name)
	}
	golden.Check(t, map[string]any{"config": acctConfig, "usage": usageFixtures(), "excludes": excludes}, picks)
}

func TestAccountPickRotatesWhenNoMeterIsReadable(t *testing.T) {
	dir := newProject(t, acctConfig)
	empty := t.TempDir()
	acc := &Accounts{Login: loggedIn(), Env: Env{Getenv: func(string) string { return empty }}}
	name, ok := acc.Pick(bg, dir, nil)
	if !ok || name != "alpha" {
		t.Errorf("unknown meters must stay eligible: go %q", name)
	}
}

func TestAccountPickSkipsAccountWithoutLogin(t *testing.T) {
	empty := t.TempDir()
	// alpha is the best pick; its config dir has no login, so beta must win.
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, n := range []string{"beta", "gamma", "delta", "epsilon", "zeta"} {
		d := filepath.Join(home, "acct", n)
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"t"}}`), 0o600)
	}
	cfg := strings.ReplaceAll(acctConfig, `"/acct/`, `"~/acct/`)
	dir := newProject(t, cfg)
	acc := &Accounts{Env: Env{Getenv: func(string) string { return empty }}}
	name, ok := acc.Pick(bg, dir, nil)
	if !ok || name != "beta" {
		t.Errorf("Pick = %q, %v; want beta (alpha has no login)", name, ok)
	}
	if name, ok := acc.Pick(bg, dir, []string{"beta", "gamma", "delta", "epsilon", "zeta"}); ok {
		t.Errorf("only login-less alpha left, Pick = %q", name)
	}
}

func TestAccountAssign(t *testing.T) {
	b := newProject(t, acctConfig)
	goInit(t, b, InitOpts{Slots: 2, Base: "main"})
	got := map[string]string{} // workers.json after each assign
	usage := usageDir(t)
	acc := &Accounts{Login: loggedIn(), Env: Env{Getenv: func(k string) string {
		if k == "ROTA_ACCOUNT_USAGE_DIR" {
			return usage
		}
		return ""
	}}}

	name, changed, err := acc.Assign(bg, b, "w1", "beta")
	if err != nil || name != "beta" || !changed {
		t.Fatalf("go %v %v %v", name, changed, err)
	}
	got["after w1 beta"] = registry(t, b)

	if _, changed, _ := acc.Assign(bg, b, "w1", "beta"); changed {
		t.Error("assigning the same account again must report changed=false")
	}

	// no --account: the best pick
	name, _, err = acc.Assign(bg, b, "w2", "")
	if err != nil || name == "" {
		t.Fatalf("go %v %v", name, err)
	}
	got["after w2 pick"] = registry(t, b)
	golden.Check(t, map[string]any{"config": acctConfig, "usage": usageFixtures(), "pool": "init --slots 2 --base main",
		"steps": []string{"assign --slot w1 --account beta", "assign --slot w2"}}, got)

	exitOf := func(err error) int {
		if we, ok := err.(*exitcode.Error); ok {
			return we.Exit
		}
		return -1
	}
	_, _, err = acc.Assign(bg, b, "w1", "nope")
	if exitOf(err) != exitcode.ExitResolution || !strings.Contains(err.Error(), "account 'nope' is not in round.accounts") {
		t.Errorf("unknown account: %v", err)
	}
	_, _, err = acc.Assign(bg, b, "w9", "alpha")
	if exitOf(err) != exitcode.ExitResolution || !strings.Contains(err.Error(), "slot 'w9' is not in the pool") {
		t.Errorf("unknown slot: %v", err)
	}
	_, _, err = acc.Assign(bg, newProject(t, acctConfig), "w1", "alpha")
	if exitOf(err) != exitcode.ExitResolution || !strings.Contains(err.Error(), "no worker pool") {
		t.Errorf("no registry: %v", err)
	}
}

func TestAccountAssignWithEveryAccountCoolingIsRefused(t *testing.T) {
	cfg := `{"round":{"accounts":[{"name":"beta","configDir":"/acct/beta"}]}}`
	dir := newProject(t, cfg)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	acc := &Accounts{Env: Env{Getenv: func(k string) string {
		if k == "ROTA_ACCOUNT_USAGE_DIR" {
			return usageDir(t)
		}
		return ""
	}}}
	_, _, err := acc.Assign(bg, dir, "w1", "")
	if we, ok := err.(*exitcode.Error); !ok || we.Exit != exitcode.ExitRefused {
		t.Errorf("err = %v, want exit 4", err)
	}
}

// pool init spreads slots across accounts by headroom, resetting the
// exclusion list when accounts run out.
func TestPoolInitSpreadsAccounts(t *testing.T) {
	b := newProject(t, acctConfig)
	usage := usageDir(t)
	acc := &Accounts{Login: loggedIn(), Env: Env{Getenv: func(k string) string {
		if k == "ROTA_ACCOUNT_USAGE_DIR" {
			return usage
		}
		return ""
	}}}
	if _, err := (Env{}).PoolInit(bg, b, InitOpts{Slots: 5, Base: "main"}, acc); err != nil {
		t.Fatal(err)
	}
	golden.Check(t, map[string]any{"config": acctConfig, "usage": usageFixtures(), "argv": "init --slots 5 --base main"}, map[string]string{"workers.json": registry(t, b)})
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
	acc := &Accounts{Env: Env{Getenv: func(k string) string {
		if k == "ROTA_ACCOUNT_USAGE_DIR" {
			return usage
		}
		return ""
	}}}
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
	d2 := newProject(t, `{"round":{"accounts":[{"name":"alpha","configDir":"/acct/alpha"},{"name":"gamma"}]}}`)
	if _, ok, o := acc.OrchestratorTarget(bg, d2, "/acct/alpha", 90); ok || !strings.Contains(strings.Join(o, ";"), "gamma: no configDir") {
		t.Errorf("no configDir: %v %v", ok, o)
	}
}

func TestSameConfigDirAndAccountOf(t *testing.T) {
	home, _ := os.UserHomeDir()
	if !SameConfigDir("", filepath.Join(home, ".claude")) || !SameConfigDir("~/.x/", filepath.Join(home, ".x")) || SameConfigDir("/a", "/b") {
		t.Error("SameConfigDir")
	}
	acc := &Accounts{Env: Env{Config: memConfig(t, acctConfig)}}
	if acc.AccountOf("", "/acct/beta/") != "beta" || acc.AccountOf("", "/elsewhere") != "" {
		t.Error("AccountOf")
	}
}

func TestCredentialsProblem(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"usable", `{"claudeAiOauth":{"accessToken":"t"}}`, ""},
		{"empty object", `{}`, "no access token"},
		{"empty token", `{"claudeAiOauth":{"accessToken":""}}`, "no access token"},
		{"not json", `nope`, "unreadable credentials"},
	} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(tc.body), 0o600)
		if got := (Credentials{}).Problem(dir); got != tc.want {
			t.Errorf("%s: Credentials.Problem = %q, want %q", tc.name, got, tc.want)
		}
		tok, why := (&Accounts{}).token(dir)
		if (tok == "") != (tc.want != "") || (tc.want != "" && why != tc.want) {
			t.Errorf("%s: token = %q, %q disagrees with Credentials.Problem %q", tc.name, tok, why, tc.want)
		}
	}
	if got := (Credentials{}).Problem(t.TempDir()); got != "no credentials file" {
		t.Errorf("missing file: %q", got)
	}
}

// fakeKeychain is a Credentials for goos whose `security` runner answers from
// entries keyed by service name (exit 44 for a miss); reads counts the calls.
func fakeKeychain(goos string, entries map[string]string) (c Credentials, reads *int) {
	reads = new(int)
	return Credentials{GOOS: goos, Run: func(_ context.Context, cmd proc.Cmd) (proc.Result, error) {
		*reads++
		for i, a := range cmd.Args {
			if a == "-s" {
				if v, ok := entries[cmd.Args[i+1]]; ok {
					return proc.Result{Stdout: v + "\n"}, nil
				}
			}
		}
		return proc.Result{ExitCode: 44}, nil
	}}, reads
}

func TestKeychainService(t *testing.T) {
	home, _ := os.UserHomeDir()
	if got := keychainService(filepath.Join(home, ".claude")); got != "Claude Code-credentials" {
		t.Errorf("default dir = %q, want no suffix", got)
	}
	// sha256("/acct/alpha") = 5c3a1b3b...; the suffix is its first 8 hex chars.
	want := "Claude Code-credentials-" + sha256Hex8("/acct/alpha")
	if got := keychainService("/acct/alpha"); got != want || len(got) != len("Claude Code-credentials-")+8 {
		t.Errorf("custom dir = %q, want %q", got, want)
	}
}

func sha256Hex8(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

func TestKeychainOnlyCredentialsAreUsableOnDarwin(t *testing.T) {
	dir := t.TempDir()
	kc, _ := fakeKeychain("darwin", map[string]string{keychainService(dir): `{"claudeAiOauth":{"accessToken":"kc"}}`})
	if got := kc.Problem(dir); got != "" {
		t.Errorf("Problem = %q, want usable", got)
	}
	if tok, why := (&Accounts{Login: kc}).token(dir); tok != "kc" || why != "" {
		t.Errorf("token = %q, %q", tok, why)
	}
}

func TestExpiredFileYieldsToKeychain(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"old","expiresAt":1000}}`), 0o600)
	kc, _ := fakeKeychain("darwin", map[string]string{keychainService(dir): `{"claudeAiOauth":{"accessToken":"kc"}}`})
	if tok, why := (&Accounts{Login: kc}).token(dir); tok != "kc" || why != "" {
		t.Errorf("token = %q, %q, want the Keychain token", tok, why)
	}
}

func TestExpiredFileWithoutKeychainStaysExpired(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"old","expiresAt":1000}}`), 0o600)
	kc, _ := fakeKeychain("darwin", nil)
	if tok, why := (&Accounts{Login: kc}).token(dir); tok != "" || why != "token expired" {
		t.Errorf("token = %q, %q", tok, why)
	}
}

func TestValidFileWinsAndSkipsKeychain(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"file"}}`), 0o600)
	kc, calls := fakeKeychain("darwin", map[string]string{keychainService(dir): `{"claudeAiOauth":{"accessToken":"kc"}}`})
	if tok, why := (&Accounts{Login: kc}).token(dir); tok != "file" || why != "" {
		t.Errorf("token = %q, %q, want the file token", tok, why)
	}
	if got := kc.Problem(dir); got != "" || *calls != 0 {
		t.Errorf("Problem = %q, keychain reads = %d, want usable with none", got, *calls)
	}
}

func TestMissingKeychainEntryNamesKeychain(t *testing.T) {
	dir := t.TempDir()
	kc, _ := fakeKeychain("darwin", nil)
	got := kc.Problem(dir)
	if !strings.Contains(got, "Keychain") {
		t.Errorf("Credentials.Problem = %q, want it to name the Keychain", got)
	}
	// A file still serves when the Keychain has nothing.
	os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"f"}}`), 0o600)
	if got := kc.Problem(dir); got != "" {
		t.Errorf("file fallback = %q, want usable", got)
	}
}

func TestKeychainIgnoredOffDarwin(t *testing.T) {
	dir := t.TempDir()
	kc, calls := fakeKeychain("linux", map[string]string{keychainService(dir): `{"claudeAiOauth":{"accessToken":"kc"}}`})
	if got := kc.Problem(dir); got != "no credentials file" || *calls != 0 {
		t.Errorf("Problem = %q, want the file-only reason", got)
	}
}

func TestKeychainReadFailureIsNamedNotMissing(t *testing.T) {
	dir := t.TempDir()
	kc := Credentials{GOOS: "darwin", Run: func(context.Context, proc.Cmd) (proc.Result, error) {
		return proc.Result{ExitCode: 51, Stderr: "boom\n"}, nil
	}}
	got := kc.Problem(dir)
	if !strings.Contains(got, "Keychain unavailable") || !strings.Contains(got, "exit 51") {
		t.Errorf("Problem = %q, want the Keychain read failure named", got)
	}
	if strings.Contains(got, "no credentials file or Keychain entry") {
		t.Errorf("Problem = %q, must not claim a missing entry", got)
	}
}
