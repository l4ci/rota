package harness

import (
	"slices"
	"testing"
)

func TestOpenCodeLaunch(t *testing.T) {
	cases := []struct {
		cfg, model, want string
		refused          bool
	}{
		{``, "opencode-go/qwen3.8-flash", "opencode --model opencode-go/qwen3.8-flash", false},
		{``, "", "opencode", false},
		{`{"work":{"opencodeCommand":"wrap opencode -m {model}"}}`, "p/m", "wrap opencode -m p/m", false},
		{`{"work":{"opencodeCommand":"wrap opencode -m {model}"}}`, "", "", true},
		{`{"work":{"opencodeCommand":"opencode --agent build"}}`, "p/m", "opencode --agent build", false},
		{`{"work":{"opencodeCommand":"opencode --agent build"}}`, "", "opencode --agent build", false},
		{`{"work":{"codexCommand":"codex -x"}}`, "", "opencode", false},
	}
	for _, c := range cases {
		got, err := (opencode{}).Launch(cfgOf(t, c.cfg), c.model)
		if c.refused {
			if !usageRefusal(err) {
				t.Errorf("%s + %q: err = %v, want a usage refusal", c.cfg, c.model, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s + %q: %q, %v; want %q", c.cfg, c.model, got, err, c.want)
		}
	}
}

func TestOpenCodeModelApplies(t *testing.T) {
	for cfg, want := range map[string]bool{``: true, `{"work":{"opencodeCommand":"opencode -x"}}`: false, `{"work":{"opencodeCommand":"opencode -m {model}"}}`: true} {
		if got := (opencode{}).ModelApplies(cfgOf(t, cfg)); got != want {
			t.Errorf("%s: %v want %v", cfg, got, want)
		}
	}
}

func TestOpenCodeResume(t *testing.T) {
	for cmd, want := range map[string]string{
		"opencode --model p/m":          "",
		"opencode -c":                   "-c",
		"opencode --continue":           "--continue",
		"opencode --model p/m -s ses_1": "-s",
		"opencode --session ses_1":      "--session",
		"opencode --fork":               "--fork",
		"FOO=1 opencode --model p/m":    "",
		`sh -c "opencode --continue"`:   "--continue",
		"wrap -c opencode --model p/m":  "",
	} {
		hit, err := (opencode{}).Resume(cmd)
		if err != nil || hit.Token != want {
			t.Errorf("%q: %q, %v; want %q", cmd, hit.Token, err, want)
		}
	}
	if _, err := (opencode{}).Resume(`opencode "oops`); err == nil {
		t.Error("an unbalanced command must be an error")
	}
}

func TestOpenCodeKinds(t *testing.T) {
	if !slices.Contains(Kinds(), OpenCode) || !Valid(OpenCode) {
		t.Fatalf("kinds = %v", Kinds())
	}
	h, ok := Lookup(OpenCode)
	if !ok || h.Kind() != OpenCode {
		t.Fatalf("Lookup(%q) = %v, %v", OpenCode, h, ok)
	}
	if h, ok := ForBinary("opencode"); !ok || h.Kind() != OpenCode {
		t.Errorf("ForBinary(opencode) = %v, %v", h, ok)
	}
	if h.WorkAccounts() || h.UsageMeter() || h.Tiers() != (TierRule{}) {
		t.Error("opencode: no work.accounts, no usage meter, optional tier map")
	}
	for cfg, want := range map[string]bool{
		``:                                    false,
		`{"round":{"workerKind":"opencode"}}`: true,
		`{"round":{"tiers":{"opencode":{"light":"p/m"}}}}`:              true,
		`{"work":{"opencodeCommand":"opencode"}}`:                       true,
		`{"work":{"opencodeAccounts":[{"name":"a","configDir":"/x"}]}}`: true,
	} {
		if got := h.Configured(cfgOf(t, cfg)); got != want {
			t.Errorf("Configured(%s) = %v, want %v", cfg, got, want)
		}
	}
	l := h.Accounts(cfgOf(t, `{"work":{"opencodeAccounts":[{"name":"a","configDir":"/x"}]}}`))
	if l.Key != "work.opencodeAccounts" || len(l.Entries) != 1 || l.Entries[0] != (AccountEntry{Name: "a", Dir: "/x"}) {
		t.Errorf("Accounts = %+v", l)
	}
}
