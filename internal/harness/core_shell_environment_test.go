package harness

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCoreShellCommandKeepsOtherOrchestrators(t *testing.T) {
	for _, h := range Orchestrators() {
		if h.Name() == Codex {
			continue
		}
		command := []string{h.Name(), "literal argument"}
		got, err := CoreShellCommand(h.Name(), command)
		if err != nil || !reflect.DeepEqual(got, command) {
			t.Errorf("%s native command changed: %v %v", h.Name(), got, err)
		}
	}
}

// Exercise actual shell tokenization under the child terminal's environment:
// a worker's HC identity is created after parent-side Prepare, not before it.
func TestCoreCodexWorkerShellIdentityLaunch(t *testing.T) {
	o := coreOpts(t)
	o.Launch = `ROTA_TEST_PREFIX='literal value' codex --dangerously-bypass-hook-trust -c 'shell_environment_policy.inherit="none"' -c 'shell_environment_policy.exclude=["*TOKEN*"]'`
	p := Probe{Look: func(n string) (string, bool) { return n, n == Codex }, Run: func(_ context.Context, _ string, args, env []string) (Result, error) {
		if len(args) > 0 && args[0] == "--help" {
			return Result{Stdout: "--dangerously-bypass-hook-trust"}, nil
		}
		return Result{}, nil
	}}
	set, err := (codex{}).Preflight(context.Background(), p, o)
	if err != nil {
		t.Fatal(err)
	}
	launch, _, err := (codex{}).Prepare(o.Launch, func() (string, error) { return "/rota", nil }, set)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	if err = os.WriteFile(filepath.Join(dir, Codex), []byte("#!/bin/sh\n[ \"$ROTA_TEST_PREFIX\" = 'literal value' ] || exit 91\nprintf '%s\\0' \"$@\" > \"$ARGV_LOG\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"HC_TERMINAL_ID": "t23", "HC_TERMINAL_SOCKET": "/tmp/a space/quote\"'\\$(touch nope).sock", "ROTA_SLOT": "ben", "ROTA_ATTEMPT": "attempt-2", "ROTA_ROUND_HOLDER_PID": "9876", "ROTA_CONFIG_DIR": "/tmp/config \"quoted\" rötä\n\t\x01\x7f", "ROTA_CORE_STATE_DIR": "/tmp/core", "ROTA_PORT_BASE": "40000", "ROTA_PORT_BLOCK": "100", "ROTA_DB_SUFFIX": "_ben"}
	cmd := exec.Command("sh", "-c", launch)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + dir + ":/usr/bin:/bin", "ARGV_LOG=" + log, "API_TOKEN=must-not-copy", "CODEX_HOME=/default/account", "ROTA_UNSAFE_UNKNOWN=must-not-copy"}
	for k, v := range want {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("launch shell: %v %s", err, out)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	got := map[string]string{}
	native := map[string]bool{}
	for i, arg := range args {
		if arg != "-c" || i+1 >= len(args) {
			continue
		}
		key, value, ok := strings.Cut(args[i+1], "=")
		if !ok {
			continue
		}
		native[args[i+1]] = true
		if name, ok := strings.CutPrefix(key, "shell_environment_policy.set."); ok {
			var v string
			if err := json.Unmarshal([]byte(value), &v); err != nil {
				t.Fatalf("identity must be a quoted native string: %s: %v", args[i+1], err)
			}
			got[name] = v
		}
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("runtime child identity %s=%q want %q", k, got[k], v)
		}
	}
	for _, k := range []string{"API_TOKEN", "CODEX_HOME", "ROTA_UNSAFE_UNKNOWN"} {
		if _, ok := got[k]; ok {
			t.Errorf("copied nonidentity %s", k)
		}
	}
	if !native[`shell_environment_policy.inherit="none"`] || !native[`shell_environment_policy.exclude=["*TOKEN*"]`] {
		t.Fatal("existing native environment filters changed")
	}
	if native[`shell_environment_policy.inherit="all"`] {
		t.Fatal("broadened shell inheritance")
	}
}
