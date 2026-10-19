package worker

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/host"
)

// opencodeRig answers the opencode and herdr calls a dispatch makes.
type opencodeRig struct {
	missing map[string]bool
	help    string
	calls   []string
}

const opencodeHelp = "  -m, --model   model to use\n      --prompt  prompt to use\n"

func (r *opencodeRig) env(h host.Host) Env {
	e := envWith(h)
	e.Executable = func() (string, error) { return "/opt/rota", nil }
	e.LookPath = func(n string) (string, error) {
		if r.missing[n] {
			return "", errors.New("not found")
		}
		return "/fake/" + n, nil
	}
	e.Run = func(_ context.Context, name string, args, env []string) (host.Result, error) {
		call := strings.TrimPrefix(name, "/fake/") + " " + strings.Join(args, " ")
		r.calls = append(r.calls, call)
		switch call {
		case "opencode --version":
			return host.Result{Stdout: "1.17.9\n"}, nil
		case "opencode --help":
			help := r.help
			if help == "" {
				help = opencodeHelp
			}
			return host.Result{Stdout: help}, nil
		case "herdr integration status":
			return host.Result{Stdout: "opencode: current (v13) (/x)\n"}, nil
		}
		return host.Result{ExitCode: 98}, nil
	}
	return e
}

func opencodeProject(t *testing.T, cfg string) string {
	t.Helper()
	dir := newProject(t, cfg)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	return dir
}

// A task dispatch of an opencode slot guards the session: the launch line
// carries the plugin as inline config, the key sits in the slot's state dir
// and the brief is signed with it.
func TestDispatchOpenCodeGuardsTheSession(t *testing.T) {
	dir := opencodeProject(t, `{"work":{"dispatch":"herdr","opencodeAccounts":[{"name":"a","configDir":"/cfg/a"}]}}`)
	rig := &opencodeRig{}
	f := herdrFake()
	res, err := rig.env(f).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "go\n"), Task: "T1", Kind: "opencode", Model: "p/m"})
	if err != nil || res.Kind != harness.OpenCode {
		t.Fatalf("%+v %v", res, err)
	}
	slotDir := filepath.Join(dir, ".git", "rota", "opencode", "w1")
	kind, env, args, lerr := host.LaunchArgs(f.spawnOpts.Launch)
	if lerr != nil || kind != harness.OpenCode || !slices.Equal(args, []string{"--model", "p/m"}) {
		t.Fatalf("launch = %q: %q %q %v", f.spawnOpts.Launch, kind, args, lerr)
	}
	if len(env) != 1 || env[0] != `OPENCODE_CONFIG_CONTENT={"plugin":["file://`+filepath.Join(slotDir, harness.GuardFile)+`"]}` {
		t.Errorf("the guard rides on the launch line: %q", env)
	}
	if !slices.Contains(f.spawnOpts.Env, "OPENCODE_CONFIG_DIR=/cfg/a") || accountEnv(f.spawnOpts) != "OPENCODE_CONFIG_DIR=/cfg/a" {
		t.Errorf("the account reaches the pane as its config dir: %v", f.spawnOpts.Env)
	}
	if got := LoadRegistryTolerant(dir).Slot("w1").HomeAccount(); got != "a" {
		t.Errorf("account recorded = %q", got)
	}
	key, err := harness.LoadPromptKey(filepath.Join(slotDir, harness.PromptKeyFile))
	if err != nil {
		t.Fatalf("prompt key: %v", err)
	}
	i := strings.LastIndex(f.sent, "\n--- ROTA-SIG ")
	if !strings.HasPrefix(f.sent, "--- ORCHESTRATOR (round 1) ---\n") || i < 0 || f.sent != harness.Resolve(harness.OpenCode).Sign(key, f.sent[:i]) {
		t.Errorf("the brief is signed with the slot's key: %q", f.sent)
	}
}

// A refused launch touches nothing: no state, no spawn.
func TestDispatchOpenCodeRefusals(t *testing.T) {
	for name, c := range map[string]struct {
		cfg     string
		rig     opencodeRig
		blocked string
		exit    int
	}{
		"binary missing": {`{"work":{"dispatch":"tmux"}}`, opencodeRig{missing: map[string]bool{"opencode": true}}, harness.BlockOpencodeBinary, exitcode.ExitRefused},
		"flag missing":   {`{"work":{"dispatch":"tmux"}}`, opencodeRig{help: "  --prompt\n"}, harness.BlockOpencodeFlags, exitcode.ExitRefused},
		"pure launch":    {`{"work":{"dispatch":"tmux","opencodeCommand":"opencode --pure"}}`, opencodeRig{}, "", exitcode.ExitUnavailable},
	} {
		dir := opencodeProject(t, c.cfg)
		rig := c.rig
		f := tmuxFake()
		_, err := rig.env(f).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "go\n"), Task: "T1", Kind: "opencode", Model: "p/m"})
		var xe *exitcode.Error
		if !errors.As(err, &xe) || xe.Exit != c.exit {
			t.Errorf("%s: %v, want exit %d", name, err, c.exit)
			continue
		}
		if bd, _ := xe.Data.(BlockData); bd.BlockedBy != c.blocked {
			t.Errorf("%s: blockedBy %q, want %q", name, bd.BlockedBy, c.blocked)
		}
		if len(f.calls) != 0 {
			t.Errorf("%s: the host was touched: %v", name, f.calls)
		}
	}
}
