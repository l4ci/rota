package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLookup(t *testing.T) {
	for kind, want := range map[string]string{"": Claude, Claude: Claude, Codex: Codex} {
		h, ok := Lookup(kind)
		if !ok || h.Kind() != want {
			t.Errorf("Lookup(%q) = %v, %v; want %s", kind, h, ok, want)
		}
	}
	if _, ok := Lookup("hermes"); ok {
		t.Error("hermes runs the orchestrator only: it is not a worker harness")
	}
	if Valid("") || !Valid(Claude) || !Valid(Codex) || KindList() != "claude or codex" {
		t.Errorf("Valid/KindList: %v %q", Kinds, KindList())
	}
	if h, ok := ForBinary("codex"); !ok || h.Kind() != Codex {
		t.Error("the codex binary is the codex harness")
	}
	if _, ok := ForBinary("wrap"); ok {
		t.Error("an unknown binary is no harness")
	}
}

func TestAccountEnv(t *testing.T) {
	both := Account{ConfigDir: "/acct", CodexHome: "/home/w1"}
	for _, c := range []struct {
		h    Harness
		a    Account
		want string
	}{
		{claude{}, both, "CLAUDE_CONFIG_DIR=/acct"},
		{claude{}, Account{CodexHome: "/h"}, ""},
		{codex{}, both, "CODEX_HOME=/home/w1"},
		{codex{}, Account{ConfigDir: "/acct"}, ""},
	} {
		if got := strings.Join(c.h.AccountEnv(c.a), " "); got != c.want {
			t.Errorf("%s %+v: %q want %q", c.h.Kind(), c.a, got, c.want)
		}
	}
	if !(claude{}).WorkAccounts() || (codex{}).WorkAccounts() {
		t.Error("work.accounts rotates claude slots, never codex ones")
	}
}

// Claude signs nothing; codex signs every payload and loads the session's key
// for a relay.
func TestSigning(t *testing.T) {
	const payload = "--- ORCHESTRATOR (round 1) ---\nDo it."
	if got := (claude{}).Sign(nil, payload); got != payload {
		t.Errorf("claude signed: %q", got)
	}
	if k, err := (claude{}).RelayKey(func() (string, error) { t.Error("claude needs no git dir"); return "", nil }, "w1"); k != nil || err != nil {
		t.Errorf("claude relay key: %v %v", k, err)
	}

	cd, home := t.TempDir(), ""
	home = CodexHome(cd, "w1")
	os.MkdirAll(home, 0o700)
	launch, key, err := (codex{}).Prepare("codex --model x --dangerously-bypass-hook-trust", func() (string, error) { return "/opt/rota", nil }, Setup{Home: home})
	if err != nil || len(key) != 32 || !strings.Contains(launch, "prompt-check") || !strings.HasPrefix(launch, "codex -c features.hooks=true") {
		t.Fatalf("Prepare: %q %v %v", launch, key, err)
	}
	signed := (codex{}).Sign(key, payload)
	if ok, why := CheckPrompt(key, signed); !ok {
		t.Errorf("signed payload blocked: %s", why)
	}
	got, err := (codex{}).RelayKey(func() (string, error) { return cd, nil }, "w1")
	if err != nil || string(got) != string(key) {
		t.Errorf("relay key: %v %v", got, err)
	}
	os.Remove(filepath.Join(home, PromptKeyFile))
	_, err = (codex{}).RelayKey(func() (string, error) { return cd, nil }, "w1")
	var r *Refusal
	if !errors.As(err, &r) || r.Class != Unavailable || r.Hint == "" {
		t.Errorf("a missing key must be an unavailable refusal with a hint: %v", err)
	}
	if _, _, err := (codex{}).Prepare("codex", func() (string, error) { return "", errors.New("no exe") }, Setup{Home: home}); err == nil {
		t.Error("no rota binary must refuse")
	}
}

func TestCodexCheckLaunch(t *testing.T) {
	for launch, ok := range map[string]bool{
		DefaultCodexCommand: true,
		"A=1 /opt/codex --dangerously-bypass-hook-trust": true,
		"codex --yolo": false,
		"codex \"oops": false,
		"A=1":          false,
	} {
		err := (codex{}).CheckLaunch(launch)
		if (err == nil) != ok {
			t.Errorf("%q: %v, want ok=%v", launch, err, ok)
		}
		var r *Refusal
		if err != nil && (!errors.As(err, &r) || r.Class != Unavailable) {
			t.Errorf("%q: want an unavailable refusal, got %v", launch, err)
		}
	}
	if err := (claude{}).CheckLaunch("claude --x"); err != nil {
		t.Error(err)
	}
}

// ── readiness ───────────────────────────────────────────────────────────────

type scripted struct {
	version  string
	loggedIn bool
	current  bool
	runErr   map[string]error
	calls    []string
}

func (s *scripted) probe() Probe {
	return Probe{
		Look: func(n string) (string, bool) { return "/bin/" + n, true },
		Run: func(_ context.Context, bin string, args, env []string) (Result, error) {
			key := filepath.Base(bin) + " " + strings.Join(args, " ")
			s.calls = append(s.calls, key)
			if err := s.runErr[key]; err != nil {
				return Result{}, err
			}
			switch key {
			case "codex --version":
				return Result{Stdout: s.version}, nil
			case "codex login status":
				if !s.loggedIn {
					return Result{ExitCode: 1}, nil
				}
			case "herdr integration status":
				if s.current {
					return Result{Stdout: "codex: current (v8)"}, nil
				}
				return Result{Stdout: "codex: not installed"}, nil
			}
			return Result{}, nil
		},
	}
}

func TestCheckCodexVersion(t *testing.T) {
	for _, c := range []struct {
		out  string
		want Code
	}{
		{"codex-cli 0.159.2\n", ""},
		{"codex-cli 0.158.0\n", VersionOutOfRange},
		{"codex-cli 0.160.0\n", VersionOutOfRange},
		{"hello", VersionUnreadable},
	} {
		s := &scripted{version: c.out}
		_, f := CheckCodexVersion(context.Background(), s.probe(), "/bin/codex", "/h")
		switch {
		case c.want == "" && f != nil, c.want != "" && (f == nil || f.Code != c.want):
			t.Errorf("%q: %+v, want %q", c.out, f, c.want)
		}
	}
	s := &scripted{runErr: map[string]error{"codex --version": errors.New("boom")}}
	if _, f := CheckCodexVersion(context.Background(), s.probe(), "/bin/codex", ""); f == nil || f.Code != VersionUnreadable {
		t.Errorf("a version that cannot run is unreadable: %+v", f)
	}
}

func TestCheckCodexHome(t *testing.T) {
	h := Home{Slot: "w1", Dir: "/h"}
	codes := func(fs []Finding) string {
		var out []string
		for _, f := range fs {
			out = append(out, string(f.Code))
		}
		return strings.Join(out, ",")
	}
	for _, c := range []struct {
		name  string
		s     scripted
		herdr string
		want  string
	}{
		{"ready", scripted{loggedIn: true, current: true}, "/bin/herdr", ""},
		{"stale integration and no login", scripted{}, "/bin/herdr", "integration-stale,not-logged-in"},
		{"no herdr: integration is not looked at", scripted{loggedIn: true}, "", ""},
		{"status cannot run", scripted{loggedIn: true, runErr: map[string]error{"herdr integration status": errors.New("x")}}, "/bin/herdr", "integration-unrunnable"},
	} {
		s := c.s
		if got := codes(CheckCodexHome(context.Background(), s.probe(), "/bin/codex", c.herdr, h)); got != c.want {
			t.Errorf("%s: %q want %q", c.name, got, c.want)
		}
	}
}

func TestOrchestratorsAreAWiderSetThanWorkers(t *testing.T) {
	var names []string
	for _, o := range Orchestrators() {
		names = append(names, o.Name())
		if o.Prompt() == "" {
			t.Errorf("%s has no prompt", o.Name())
		}
		if argv, err := o.Command(nil); err != nil || len(argv) == 0 {
			t.Errorf("%s: %v %v", o.Name(), argv, err)
		}
	}
	if strings.Join(names, " ") != "claude codex hermes opencode" {
		t.Errorf("table: %v", names)
	}
	for _, n := range names[:2] {
		if _, ok := Lookup(n); !ok {
			t.Errorf("%s runs workers too", n)
		}
	}
}

func TestClaudeOrchestratorCommand(t *testing.T) {
	for doc, want := range map[string]string{
		``:                                     "claude --model opus --permission-mode auto",
		`{"models":{"orchestrator":"sonnet"}}`: "claude --model sonnet --permission-mode auto",
		`{"work":{"operatorCommand":"wrap claude -x"}}`: "wrap claude -x",
	} {
		argv, err := (claude{}).Command(cfgOf(t, doc))
		if err != nil || strings.Join(argv, " ") != want {
			t.Errorf("%s: %q, %v want %q", doc, argv, err, want)
		}
	}
	if _, err := (claude{}).Command(cfgOf(t, `{"work":{"operatorCommand":"wrap \"oops"}}`)); err == nil {
		t.Error("an unbalanced operatorCommand must be an error")
	}
}
