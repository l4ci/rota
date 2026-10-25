package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
)

func decode(t *testing.T, s string) any {
	t.Helper()
	v, err := jsonx.Decode([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDispatch(t *testing.T) {
	for in, want := range map[string]string{
		`{}`:                             "subagent",
		`{"round":{"dispatch":null}}`:    "subagent",
		`{"round":{"dispatch":"herdr"}}`: "herdr",
		`{"round":{"dispatch":"tmux"}}`:  "tmux",
		`{"round":{"dispatch":3}}`:       "",
		`{"round":"x"}`:                  "subagent",
	} {
		if got := Dispatch(decode(t, in)); got != want {
			t.Errorf("Dispatch(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestAccounts(t *testing.T) {
	cfg := decode(t, `{"round":{"accounts":[{"kind":"codex","name":"c","dir":"/c"},"junk",{"kind":"opencode","name":"o","dir":"/o"},`+
		`{"name":"legacy","configDir":"/l"},{"kind":"claude","name":"a","dir":"/a","configDir":"/ignored"},{"kind":"claude"},{"dir":7}]}}`)
	want := []Account{
		{KindCodex, "c", "/c"}, {KindOpencode, "o", "/o"}, {KindClaude, "legacy", "/l"},
		{KindClaude, "a", "/a"}, {KindClaude, "", ""}, {KindClaude, "", ""},
	}
	if got := Accounts(cfg); !reflect.DeepEqual(got, want) {
		t.Errorf("Accounts = %v, want %v", got, want)
	}
	if got := AccountsOf(cfg, KindCodex); !reflect.DeepEqual(got, want[:1]) {
		t.Errorf("AccountsOf(codex) = %v, want %v", got, want[:1])
	}
	if got := AccountsOf(cfg, KindClaude); len(got) != 4 {
		t.Errorf("AccountsOf(claude) = %v, want the four claude entries", got)
	}
	for _, in := range []string{`{}`, `{"round":{"accounts":null}}`} {
		if got := Accounts(decode(t, in)); len(got) != 0 {
			t.Errorf("Accounts(%s) = %v, want none", in, got)
		}
	}
}

func TestAccountDirProblemsCoverEveryKind(t *testing.T) {
	good := `{"round":{"accounts":[{"kind":"claude","name":"a","dir":"~/.claude-a"},{"kind":"codex","name":"c","dir":"~"},{"kind":"opencode","name":"o","dir":"/abs"}]}}`
	if got := AccountDirProblems(decode(t, good)); len(got) != 0 {
		t.Errorf("valid dirs flagged: %v", got)
	}
	bad := `{"round":{"accounts":[{"kind":"claude","name":"a","dir":".claude-a"},{"kind":"codex","name":"c","dir":"$HOME/.codex"},` +
		`{"kind":"opencode","name":"o","dir":"~bob/x"},{"kind":"gemini","name":"g","dir":"/g"}]}}`
	got := AccountDirProblems(decode(t, bad))
	if len(got) != 4 {
		t.Fatalf("want 4 problems, got %v", got)
	}
	for i, name := range []string{`"a"`, `"c"`, `"o"`} {
		if !strings.Contains(got[i], "round.accounts") || !strings.Contains(got[i], name) || !strings.Contains(got[i], "dir must be absolute or start with ~") {
			t.Errorf("problem %d does not name round.accounts, %s and the rule: %q", i, name, got[i])
		}
	}
	if !strings.Contains(got[3], `kind "gemini"`) || !strings.Contains(got[3], "claude, codex or opencode") {
		t.Errorf("unknown kind not named: %q", got[3])
	}
}

// One name is one dir (#1861): the same name under two dirs is flagged, the
// same name and dir under two kinds is not.
func TestAccountDirProblemsFlagsNameWithTwoDirs(t *testing.T) {
	same := `{"round":{"accounts":[{"kind":"claude","name":"a","dir":"/x"},{"kind":"codex","name":"a","dir":"/x"}]}}`
	if got := AccountDirProblems(decode(t, same)); len(got) != 0 {
		t.Errorf("same name and dir flagged: %v", got)
	}
	split := `{"round":{"accounts":[{"kind":"claude","name":"a","dir":"/x"},{"kind":"opencode","name":"a","dir":"/y"}]}}`
	got := AccountDirProblems(decode(t, split))
	if len(got) != 1 || !strings.Contains(got[0], `"a"`) || !strings.Contains(got[0], "/x") || !strings.Contains(got[0], "/y") {
		t.Errorf("name with two dirs = %v, want one problem naming a, /x and /y", got)
	}
}

func TestIntRange(t *testing.T) {
	for _, tc := range []struct {
		cfg     string
		min     int
		max     int
		want    int
		wantErr bool
	}{
		{`{}`, 1, 100, 90, false}, // schema default
		{`{"orchestrator":{"usageThreshold":50}}`, 1, 100, 50, false},
		{`{"orchestrator":{"usageThreshold":0}}`, 1, 100, 0, true},
		{`{"orchestrator":{"usageThreshold":101}}`, 1, 100, 0, true},
		{`{"orchestrator":{"usageThreshold":"x"}}`, 1, 100, 0, true},
		{`{"orchestrator":{"usageThreshold":1.5}}`, 1, 100, 0, true},
	} {
		got, err := Int(decode(t, tc.cfg), "orchestrator.usageThreshold", tc.min, tc.max)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("Int(%s) = %d, %v; want %d, err=%v", tc.cfg, got, err, tc.want, tc.wantErr)
		}
	}
	if _, err := Int(decode(t, `{}`), "no.such.key", 0, 1); err == nil {
		t.Error("unknown key: want error")
	}
}

func TestBool(t *testing.T) {
	for cfg, want := range map[string]bool{`{}`: false, `{"orchestrator":{"switchOnUsage":true}}`: true} {
		if got, err := SwitchOnUsage(decode(t, cfg)); err != nil || got != want {
			t.Errorf("SwitchOnUsage(%s) = %v, %v; want %v", cfg, got, err, want)
		}
	}
	if _, err := SwitchOnUsage(decode(t, `{"orchestrator":{"switchOnUsage":"yes"}}`)); err == nil {
		t.Error("non-bool: want error")
	}
}

func TestStrings(t *testing.T) {
	cfg := decode(t, `{"round":{"tiers":{"claude":{"standard":null}}},"qa":{"gate":5}}`)
	if got := String(cfg, "round.tiers.claude.standard"); got != "sonnet" {
		t.Errorf("String default = %q, want sonnet", got)
	}
	if got := String(cfg, "qa.gate"); got != "" {
		t.Errorf("String non-string = %q, want empty", got)
	}
	if got := StringIfSet(decode(t, `{}`), "round.tiers.claude.standard"); got != "" {
		t.Errorf("StringIfSet unset = %q, want empty", got)
	}
	if got := StringIfSet(decode(t, `{"orchestrator":{"model":"haiku"}}`), "orchestrator.model"); got != "haiku" {
		t.Errorf("StringIfSet = %q, want haiku", got)
	}
}

func TestSharedRanges(t *testing.T) {
	cases := []struct {
		name string
		key  string
		fn   func(any) (int, error)
		bad  []string
		ok   string
		want int
	}{
		{"usageThreshold", "orchestrator.usageThreshold", UsageThreshold, []string{"0", "101"}, "100", 100},
	}
	build := func(key, v string) any {
		parts := strings.Split(key, ".")
		return decode(t, `{"`+parts[0]+`":{"`+parts[1]+`":`+v+`}}`)
	}
	for _, c := range cases {
		for _, b := range c.bad {
			if _, err := c.fn(build(c.key, b)); err == nil {
				t.Errorf("%s=%s: want error", c.name, b)
			}
		}
		if got, err := c.fn(build(c.key, c.ok)); err != nil || got != c.want {
			t.Errorf("%s=%s: got %d, %v", c.name, c.ok, got, err)
		}
	}
}

func TestEscalateIssueReadsNumbersAndKeys(t *testing.T) {
	read := func(v string) (string, error) {
		return EscalateIssue(decode(t, `{"orchestrator":{"escalateIssue":`+v+`}}`))
	}
	for v, want := range map[string]string{"7": "7", "0": "", `""`: "", `"PROJ-123"`: "PROJ-123", `" proj-1 "`: "proj-1"} {
		if got, err := read(v); err != nil || got != want {
			t.Errorf("%s: got %q, %v, want %q", v, got, err, want)
		}
	}
	for _, v := range []string{"-1", "2000000000", "true"} {
		if _, err := read(v); err == nil {
			t.Errorf("%s: want error", v)
		}
	}
}

func TestStringOrDefault(t *testing.T) {
	cases := []struct{ cfg, want string }{
		{`{}`, "sonnet"},
		{`{"round":{"tiers":{"claude":{"standard":null}}}}`, "sonnet"},
		{`{"round":{"tiers":{"claude":{"standard":""}}}}`, "sonnet"},
		{`{"round":{"tiers":{"claude":{"standard":"haiku"}}}}`, "haiku"},
	}
	for _, c := range cases {
		if got := StringOrDefault(decode(t, c.cfg), "round.tiers.claude.standard"); got != c.want {
			t.Errorf("StringOrDefault(%s) = %q, want %q", c.cfg, got, c.want)
		}
	}
}

func TestIntOrFallsBackToSchemaDefault(t *testing.T) {
	cases := []struct {
		name string
		cfg  string
		want int
	}{
		{"unset", `{}`, 20},
		{"set", `{"map":{"softcap_subsystems":7}}`, 7},
		{"below min", `{"map":{"softcap_subsystems":0}}`, 20},
		{"not a number", `{"map":{"softcap_subsystems":"many"}}`, 20},
	}
	for _, tc := range cases {
		cfg := decode(t, tc.cfg)
		if got := IntOr(cfg, "map.softcap_subsystems", 1, MaxInt); got != tc.want {
			t.Errorf("%s: IntOr = %d, want %d", tc.name, got, tc.want)
		}
	}
	if got := IntOr(nil, "map.softcap_subsystems", 1, MaxInt); got != 20 {
		t.Errorf("nil cfg: IntOr = %d, want 20", got)
	}
}

func TestIntOrClampsAboveMaxAndRejectsFloats(t *testing.T) {
	cases := []struct {
		name     string
		cfg      string
		key      string
		min, max int
		want     int
	}{
		{"above max", `{"doctor":{"minFreeDiskPercent":101}}`, "doctor.minFreeDiskPercent", 0, 100, 10},
		{"at max", `{"doctor":{"minFreeDiskPercent":100}}`, "doctor.minFreeDiskPercent", 0, 100, 100},
		{"float", `{"map":{"softcap_subsystems":2.5}}`, "map.softcap_subsystems", 1, MaxInt, 20},
	}
	for _, tc := range cases {
		if got := IntOr(decode(t, tc.cfg), tc.key, tc.min, tc.max); got != tc.want {
			t.Errorf("%s: IntOr = %d, want %d", tc.name, got, tc.want)
		}
	}
}
