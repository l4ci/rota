package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
)

func TestLookupLauncher(t *testing.T) {
	for kind, want := range map[string]string{"": Claude, Claude: Claude, Codex: Codex} {
		h, ok := LookupLauncher(kind)
		if !ok || h.Kind() != want {
			t.Errorf("LookupLauncher(%q) = %v, %v; want %s", kind, h, ok, want)
		}
	}
	if _, ok := LookupLauncher("hermes"); ok {
		t.Error("hermes runs the orchestrator only: it is not a worker harness")
	}
	if Valid("") || !Valid(Claude) || !Valid(Codex) || !Valid(OpenCode) || KindList() != "claude, codex or opencode" {
		t.Errorf("Valid/KindList: %v %q", Kinds(), KindList())
	}
	if h, ok := ForBinary("codex"); !ok || h.Kind() != Codex {
		t.Error("the codex binary is the codex harness")
	}
	if _, ok := ForBinary("wrap"); ok {
		t.Error("an unknown binary is no harness")
	}
}

func TestAccountEnv(t *testing.T) {
	both := Account{ConfigDir: "/acct", Home: "/home/w1"}
	for _, c := range []struct {
		h    Accounts
		a    Account
		want string
	}{
		{claude{}, both, "CLAUDE_CONFIG_DIR=/acct"},
		{claude{}, Account{Home: "/h"}, ""},
		{codex{}, both, "CODEX_HOME=/home/w1"},
		{codex{}, Account{ConfigDir: "/acct"}, ""},
	} {
		if got := strings.Join(c.h.AccountEnv(c.a), " "); got != c.want {
			t.Errorf("%s %+v: %q want %q", c.h.Kind(), c.a, got, c.want)
		}
	}
	if !(claude{}).WorkAccounts() || (codex{}).WorkAccounts() {
		t.Error("round.accounts rotates claude slots, never codex ones")
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
	home = CodexSlotDir(cd, "w1")
	os.MkdirAll(home, 0o700)
	launch, key, err := (codex{}).Prepare("codex --model x --dangerously-bypass-hook-trust", func() (string, error) { return "/opt/rota", nil }, Setup{StateDir: home, Worktree: "/wt"})
	if err != nil || len(key) != 32 || !strings.Contains(launch, "prompt-check") || !strings.Contains(launch, "codex -c 'projects.") && strings.Contains(launch, "-c features.hooks=true") {
		t.Fatalf("Prepare: %q %v %v", launch, key, err)
	}
	signed := (codex{}).Sign(key, payload)
	if ok, why := CheckPrompt(key, signed); !ok {
		t.Errorf("signed payload blocked: %s", why)
	}
	if _, _, err := (codex{}).Prepare("codex", func() (string, error) { return "", errors.New("no exe") }, Setup{StateDir: home, Worktree: "/wt"}); err == nil {
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
	help     string
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
			case "codex --help":
				return Result{Stdout: s.help}, nil
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
		{"codex-cli 0.158.0\n", ""},
		{"codex-cli 0.160.0\n", ""},
		{"codex-cli 9.9.9\n", ""},
		{"hello", ""},
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

func TestCheckCodexFlags(t *testing.T) {
	run := func(s *scripted, flags ...string) string {
		var names []string
		for _, f := range CheckCodexFlags(context.Background(), s.probe(), "/bin/codex", "/h", flags) {
			if f.Code != FlagMissing {
				t.Errorf("code = %q", f.Code)
			}
			names = append(names, f.Flag)
		}
		return strings.Join(names, ",")
	}
	s := &scripted{help: "  --a\n  --b <X>\n"}
	if got := run(s, "--a", "--b"); got != "" {
		t.Errorf("listed flags: missing %q", got)
	}
	if got := run(s, "--a", "--c", "--d"); got != "--c,--d" {
		t.Errorf("missing flags: %q", got)
	}
	n := len(s.calls)
	if got := run(s); got != "" || len(s.calls) != n {
		t.Errorf("no flags needs no probe: %q, %d calls", got, len(s.calls)-n)
	}
	e := &scripted{runErr: map[string]error{"codex --help": errors.New("boom")}}
	if got := run(e, "--a"); got != "--a" {
		t.Errorf("help that cannot run lists nothing: %q", got)
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
		if argv, err := o.Command(ConfigFor(nil, o.Name())); err != nil || len(argv) == 0 {
			t.Errorf("%s: %v %v", o.Name(), argv, err)
		}
	}
	if strings.Join(names, " ") != "claude codex hermes opencode" {
		t.Errorf("table: %v", names)
	}
	for _, n := range names[:2] {
		if _, ok := LookupLauncher(n); !ok {
			t.Errorf("%s runs workers too", n)
		}
	}
}

// OrchestratorCommand is the one builder the harness and the round session
// share: resume adds --continue to the default line only.
func TestOrchestratorCommandResume(t *testing.T) {
	for _, c := range []struct {
		doc    string
		resume bool
		want   string
	}{
		{``, false, "claude --model opus --permission-mode auto"},
		{`{"orchestrator":{"model":"sonnet"}}`, false, "claude --model sonnet --permission-mode auto"},
		{``, true, "claude --continue --model opus --permission-mode auto"},
		{`{"orchestrator":{"command":"wrap claude -x"}}`, true, "wrap claude -x"},
	} {
		argv, err := OrchestratorCommand(cfgOf(t, c.doc), c.resume)
		if err != nil || strings.Join(argv, " ") != c.want {
			t.Errorf("%s resume=%v: %q, %v want %q", c.doc, c.resume, argv, err, c.want)
		}
	}
	if _, err := OrchestratorCommand(cfgOf(t, `{"orchestrator":{"command":"wrap \"oops"}}`), true); err == nil {
		t.Error("an unbalanced orchestrator.command must be an error")
	}
}

// The config table cannot import this package (harness imports config), so
// the plan.criticHarness enum lists the kinds by hand. It must stay Kinds().
func TestWorkerKindEnumIsKinds(t *testing.T) {
	for _, k := range config.Keys {
		if k.Name == "plan.criticHarness" && !slices.Equal(k.Choices, Kinds()) {
			t.Errorf("plan.criticHarness enum %v, want %v", k.Choices, Kinds())
		}
	}
}

// config names the kinds of round.accounts entries itself (import cycle), so
// its list must stay Kinds().
func TestAccountKindsAreKinds(t *testing.T) {
	if !slices.Equal(config.AccountKinds, Kinds()) {
		t.Errorf("config.AccountKinds %v, want %v", config.AccountKinds, Kinds())
	}
}
