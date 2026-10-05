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
		`{}`:                            "subagent",
		`{"work":{"dispatch":null}}`:    "subagent",
		`{"work":{"dispatch":"herdr"}}`: "herdr",
		`{"work":{"dispatch":"tmux"}}`:  "tmux",
		`{"work":{"dispatch":3}}`:       "",
		`{"work":"x"}`:                  "subagent",
	} {
		if got := Dispatch(decode(t, in)); got != want {
			t.Errorf("Dispatch(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestAccounts(t *testing.T) {
	cfg := decode(t, `{"work":{"accounts":[{"name":"a","configDir":"/x"},"junk",{"name":"b"},{"configDir":7}]}}`)
	want := []Account{{"a", "/x"}, {"b", ""}, {"", ""}}
	if got := Accounts(cfg); !reflect.DeepEqual(got, want) {
		t.Errorf("Accounts = %v, want %v", got, want)
	}
	for _, in := range []string{`{}`, `{"work":{"accounts":null}}`} {
		if got := Accounts(decode(t, in)); len(got) != 0 {
			t.Errorf("Accounts(%s) = %v, want none", in, got)
		}
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
	cfg := decode(t, `{"models":{"worker":null},"qa":{"gate":5}}`)
	if got := String(cfg, "models.worker"); got != "sonnet" {
		t.Errorf("String default = %q, want sonnet", got)
	}
	if got := String(cfg, "qa.gate"); got != "" {
		t.Errorf("String non-string = %q, want empty", got)
	}
	if got := StringIfSet(decode(t, `{}`), "models.worker"); got != "" {
		t.Errorf("StringIfSet unset = %q, want empty", got)
	}
	if got := StringIfSet(decode(t, `{"models":{"worker":"haiku"}}`), "models.worker"); got != "haiku" {
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
		{"escalateIssue", "orchestrator.escalateIssue", EscalateIssue, []string{"-1", "2000000000"}, "7", 7},
		{"handoffMaxAge", "orchestrator.handoffMaxAgeSeconds", HandoffMaxAgeSeconds, []string{"0"}, "30", 30},
		{"usageThreshold", "orchestrator.usageThreshold", UsageThreshold, []string{"0", "101"}, "100", 100},
		{"fallbackSleep", "limits.fallbackSleepSeconds", FallbackSleepSeconds, []string{"0"}, "5", 5},
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
