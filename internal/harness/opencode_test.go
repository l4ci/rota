package harness

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestOpenCodeLaunch(t *testing.T) {
	cases := []struct {
		cfg, model, want string
		refused          bool
	}{
		{``, "opencode-go/qwen3.8-flash", "opencode --model opencode-go/qwen3.8-flash", false},
		{``, "", "opencode", false},
		{`{"round":{"opencodeCommand":"wrap opencode -m {model}"}}`, "p/m", "wrap opencode -m p/m", false},
		{`{"round":{"opencodeCommand":"wrap opencode -m {model}"}}`, "", "", true},
		{`{"round":{"opencodeCommand":"opencode --agent build"}}`, "p/m", "opencode --agent build", false},
		{`{"round":{"opencodeCommand":"opencode --agent build"}}`, "", "opencode --agent build", false},
		{`{"round":{"codexCommand":"codex -x"}}`, "", "opencode", false},
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
	for cfg, want := range map[string]bool{``: true, `{"round":{"opencodeCommand":"opencode -x"}}`: false, `{"round":{"opencodeCommand":"opencode -m {model}"}}`: true} {
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
		t.Error("opencode: no round.accounts, no usage meter, optional tier map")
	}
	for cfg, want := range map[string]bool{
		``:                                    false,
		`{"round":{"workerKind":"opencode"}}`: true,
		`{"round":{"tiers":{"opencode":{"light":"p/m"}}}}`:               true,
		`{"round":{"opencodeCommand":"opencode"}}`:                       true,
		`{"round":{"opencodeAccounts":[{"name":"a","configDir":"/x"}]}}`: true,
	} {
		if got := h.Configured(cfgOf(t, cfg)); got != want {
			t.Errorf("Configured(%s) = %v, want %v", cfg, got, want)
		}
	}
	l := h.Accounts(cfgOf(t, `{"round":{"opencodeAccounts":[{"name":"a","configDir":"/x"}]}}`))
	if l.Key != "round.opencodeAccounts" || len(l.Entries) != 1 || l.Entries[0] != (AccountEntry{Name: "a", Dir: "/x"}) {
		t.Errorf("Accounts = %+v", l)
	}
}

// opencodeProbe answers the opencode and herdr calls a preflight makes.
type opencodeProbe struct {
	missing  map[string]bool
	help     string
	models   string
	modelsRC int
	modelsEr error
	stale    bool // herdr reports the opencode integration not current
	calls    []string
}

func (o *opencodeProbe) probe() Probe {
	return Probe{
		Look: func(n string) (string, bool) { return "/bin/" + n, !o.missing[n] },
		Run: func(_ context.Context, bin string, args, env []string) (Result, error) {
			call := strings.TrimPrefix(bin, "/bin/") + " " + strings.Join(args, " ")
			o.calls = append(o.calls, call+" | "+strings.Join(env, " "))
			switch call {
			case "opencode --version":
				return Result{Stdout: "1.17.9\n"}, nil
			case "opencode --help":
				return Result{Stdout: o.help}, nil
			case "opencode models":
				return Result{Stdout: o.models, ExitCode: o.modelsRC}, o.modelsEr
			case "herdr integration status":
				if o.stale {
					return Result{Stdout: "opencode: not installed (/x)\n"}, nil
				}
				return Result{Stdout: "opencode: current (v13) (/x)\n"}, nil
			}
			return Result{}, nil
		},
	}
}

const opencodeHelp = "  -m, --model   model to use\n      --prompt  prompt to use\n"

func opencodePreflight(t *testing.T, o *opencodeProbe, po PreflightOpts) (Setup, error) {
	t.Helper()
	cd := t.TempDir()
	po.Slot, po.CommonDir = "ben", func() (string, error) { return cd, nil }
	return (opencode{}).Preflight(context.Background(), o.probe(), po)
}

func TestOpenCodePreflight(t *testing.T) {
	refusal := func(err error) *Refusal {
		r, _ := err.(*Refusal)
		return r
	}
	// The flags of the launch line must be in `opencode --help`; the version
	// is shown, never compared.
	o := &opencodeProbe{help: opencodeHelp}
	set, err := opencodePreflight(t, o, PreflightOpts{Launch: "opencode --model p/m --prompt"})
	if err != nil || set.Version != "1.17.9" || !strings.HasSuffix(set.StateDir, "/rota/opencode/ben") {
		t.Fatalf("%+v %v", set, err)
	}
	if !slices.ContainsFunc(o.calls, func(c string) bool { return strings.HasPrefix(c, "opencode --help") }) {
		t.Errorf("the launch flags are probed through --help: %v", o.calls)
	}
	_, err = opencodePreflight(t, &opencodeProbe{help: opencodeHelp}, PreflightOpts{Launch: "opencode --model p/m --new-flag"})
	if r := refusal(err); r == nil || r.Class != Refused || r.BlockedBy != BlockOpencodeFlags || !strings.Contains(r.Msg, "--new-flag") {
		t.Errorf("a launch flag --help does not list is refused as %q: %v", BlockOpencodeFlags, err)
	}
	_, err = opencodePreflight(t, &opencodeProbe{missing: map[string]bool{"opencode": true}}, PreflightOpts{Launch: "opencode"})
	if r := refusal(err); r == nil || r.Class != Refused || r.BlockedBy != BlockOpencodeBinary {
		t.Errorf("a missing binary is refused as %q: %v", BlockOpencodeBinary, err)
	}
}

func TestOpenCodePreflightWarnsOnAModelItDoesNotList(t *testing.T) {
	launch := "opencode --model opencode-go/qwen3.6-plus"
	for name, c := range map[string]struct {
		o    opencodeProbe
		warn bool
	}{
		"unlisted":   {opencodeProbe{models: "opencode-go/qwen3.8-flash\nopencode-go/minimax-m3\n"}, true},
		"listed":     {opencodeProbe{models: "opencode-go/qwen3.6-plus\n"}, false},
		"offline":    {opencodeProbe{modelsRC: 1}, false},
		"empty list": {opencodeProbe{}, false},
		"cannot run": {opencodeProbe{modelsEr: errors.New("timeout")}, false},
	} {
		o := c.o
		o.help = opencodeHelp
		set, err := opencodePreflight(t, &o, PreflightOpts{Launch: launch})
		if err != nil {
			t.Errorf("%s: a model list never refuses: %v", name, err)
		}
		if got := len(set.Warnings) > 0; got != c.warn {
			t.Errorf("%s: warnings = %v", name, set.Warnings)
		}
	}
	// No tier model, no `opencode models` call.
	o := &opencodeProbe{help: opencodeHelp}
	if _, err := opencodePreflight(t, o, PreflightOpts{Launch: "opencode"}); err != nil || strings.Contains(strings.Join(o.calls, "\n"), "opencode models") {
		t.Errorf("%v %v", err, o.calls)
	}
}

func TestOpenCodePreflightAccountAndHerdr(t *testing.T) {
	o := &opencodeProbe{help: opencodeHelp, stale: true}
	set, err := opencodePreflight(t, o, PreflightOpts{
		Launch: "opencode", Herdr: true,
		Accounts: []HomeAccount{{Name: "a", Home: "/cfg/a"}, {Name: "b", Home: "/cfg/b"}},
		Load:     map[string]int{"a": 2},
	})
	if err != nil || set.Account != "b" || set.Home != "/cfg/b" {
		t.Fatalf("the least loaded account: %+v %v", set, err)
	}
	if len(set.Warnings) != 1 || !strings.Contains(set.Warnings[0], "herdr integration install opencode") {
		t.Errorf("a stale herdr integration warns: %v", set.Warnings)
	}
	if !strings.Contains(strings.Join(o.calls, "\n"), "opencode --version | OPENCODE_CONFIG_DIR=/cfg/b") {
		t.Errorf("probes run under the account's config dir: %v", o.calls)
	}
	_, err = opencodePreflight(t, &opencodeProbe{help: opencodeHelp}, PreflightOpts{Launch: "opencode", Accounts: []HomeAccount{{Name: "a"}}})
	if !usageRefusal(err) {
		t.Errorf("an account without a configDir is a usage error: %v", err)
	}
	// tmux needs no herdr: no integration call.
	o = &opencodeProbe{help: opencodeHelp, stale: true}
	if set, err := opencodePreflight(t, o, PreflightOpts{Launch: "opencode"}); err != nil || len(set.Warnings) != 0 || strings.Contains(strings.Join(o.calls, "\n"), "herdr") {
		t.Errorf("%+v %v %v", set, err, o.calls)
	}
}

func TestOpenCodeDoctorPlan(t *testing.T) {
	d := (opencode{}).Doctor()
	if d.Check != "opencode" || d.Binary != "opencode" || !d.Unguarded || d.Runnable == nil || d.Home == nil {
		t.Fatalf("%+v", d)
	}
	o := &opencodeProbe{help: opencodeHelp}
	if v, ok := d.Runnable(context.Background(), o.probe(), "/bin/opencode", ""); !ok || v != "1.17.9" {
		t.Errorf("Runnable = %q %v", v, ok)
	}
	if got := d.Home(context.Background(), o.probe(), "/bin/opencode", "", Home{}); got != nil {
		t.Errorf("without herdr there is nothing to check: %v", got)
	}
}
