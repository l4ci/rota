package harness

import (
	"reflect"
	"testing"
)

func TestRoundHarnesses(t *testing.T) {
	cases := []struct {
		cfg     string
		want    []string
		wantErr bool
	}{
		{``, nil, false},
		{`{"round":{"harnesses":[]}}`, nil, false},
		{`{"round":{"harnesses":["claude","codex"]}}`, []string{"claude", "codex"}, false},
		{`{"round":{"harnesses":["  codex ","", "claude"]}}`, []string{"codex", "claude"}, false},
		{`{"round":{"harnesses":"claude"}}`, nil, true},
		{`{"round":{"harnesses":["codex",1]}}`, []string{"codex"}, true},
	}
	for _, c := range cases {
		got, err := RoundHarnesses(cfgOf(t, c.cfg))
		if (err != nil) != c.wantErr || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, %v want %v, err %v", c.cfg, got, err, c.want, c.wantErr)
		}
	}
}

func TestRoundProfile(t *testing.T) {
	for cfg, want := range map[string]string{
		``:                                 "",
		`{"round":{"profile":""}}`:         "",
		`{"round":{"profile":" budget "}}`: "budget",
		`{"round":{"profile":"inherit"}}`:  "inherit",
		`{"round":{"profile":"adaptive"}}`: "adaptive",
		`{"round":{"profile":7}}`:          "",
	} {
		if got := RoundProfile(cfgOf(t, cfg)); got != want {
			t.Errorf("%s: %q want %q", cfg, got, want)
		}
	}
}

func TestProjectKindTolerant(t *testing.T) {
	for cfg, want := range map[string]string{
		``: "",
		`{"round":{"harnesses":["codex","claude"]}}`: "codex",
		`{"round":{"harnesses":[" opencode "]}}`:     "opencode",
		`{"round":{"harnesses":"codex"}}`:            "",
		`{"round":{"harnesses":["codex",1]}}`:        "codex",
	} {
		if got := ProjectKind(cfgOf(t, cfg)); got != want {
			t.Errorf("%s: %q want %q", cfg, got, want)
		}
	}
}
