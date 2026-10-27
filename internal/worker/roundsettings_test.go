package worker

import (
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
)

func decodeCfg(t *testing.T, raw string) any {
	t.Helper()
	v, err := jsonx.Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestParseHostConfig(t *testing.T) {
	cases := []struct {
		name, raw string
		dispatch  host.Kind
		group     bool
		groupErr  bool
	}{
		{"defaults", `{}`, "", true, false},
		{"set", `{"round":{"dispatch":"tmux","herdrGroupSlots":false}}`, host.Tmux, false, false},
		{"non-string dispatch reads unset", `{"round":{"dispatch":7}}`, "", true, false},
		{"non-boolean group is an error", `{"round":{"herdrGroupSlots":"yes"}}`, "", false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseHostConfig(decodeCfg(t, c.raw))
			if got.Dispatch != c.dispatch || got.GroupSlots != c.group || (got.GroupErr != nil) != c.groupErr {
				t.Errorf("ParseHostConfig = %+v, want dispatch %q group %v err %v", got, c.dispatch, c.group, c.groupErr)
			}
		})
	}
}

func TestParsePortConfig(t *testing.T) {
	cases := []struct {
		name, raw string
		want      PortConfig
	}{
		{"defaults", `{}`, PortConfig{DefaultPortBase, DefaultPortBlock}},
		{"set", `{"round":{"portBase":30000,"portBlock":50}}`, PortConfig{30000, 50}},
		{"out of range reads as default", `{"round":{"portBase":70000,"portBlock":0}}`, PortConfig{DefaultPortBase, DefaultPortBlock}},
		{"wrong type reads as default", `{"round":{"portBase":"x"}}`, PortConfig{DefaultPortBase, DefaultPortBlock}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ParsePortConfig(decodeCfg(t, c.raw)); got != c.want {
				t.Errorf("ParsePortConfig = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestParseDepsConfig(t *testing.T) {
	cases := []struct {
		name, raw string
		want      DepsConfig
	}{
		{"defaults", `{}`, DepsConfig{}},
		{"set trims and drops blanks", `{"round":{"envSetup":"  make deps ","sharedDirs":[" node_modules ","",7]}}`, DepsConfig{"make deps", []string{"node_modules"}}},
		{"wrong type reads empty", `{"round":{"envSetup":3,"sharedDirs":"node_modules"}}`, DepsConfig{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ParseDepsConfig(decodeCfg(t, c.raw)); !reflect.DeepEqual(got, c.want) {
				t.Errorf("ParseDepsConfig = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestParseVacuousConfig(t *testing.T) {
	cases := []struct {
		name, raw string
		enabled   bool
		errHas    string
	}{
		{"defaults are off", `{}`, false, ""},
		{"valid", `{"test":{"vacuousCheck":true,"filePattern":"_test\\.go$","vacuousCommand":"go test {packages}"}}`, true, ""},
		{"non-boolean flag", `{"test":{"vacuousCheck":"yes"}}`, false, "vacuousCheck"},
		{"bad pattern", `{"test":{"vacuousCheck":true,"filePattern":"(","vacuousCommand":"go test {packages}"}}`, true, "test.filePattern"},
		{"command without placeholder", `{"test":{"vacuousCheck":true,"filePattern":"x","vacuousCommand":"go test"}}`, true, "{files} or {packages}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseVacuousConfig(decodeCfg(t, c.raw))
			if got.enabled != c.enabled && c.errHas == "" {
				t.Errorf("enabled = %v, want %v", got.enabled, c.enabled)
			}
			if c.errHas == "" && got.err != nil {
				t.Errorf("unexpected error %v", got.err)
			}
			if c.errHas != "" && (got.err == nil || !strings.Contains(got.err.Error(), c.errHas)) {
				t.Errorf("err = %v, want one naming %q", got.err, c.errHas)
			}
		})
	}
}
