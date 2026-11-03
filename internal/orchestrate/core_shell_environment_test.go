package orchestrate

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/harness"
)

func TestCoreCodexOrchestratorShellIdentityLaunch(t *testing.T) {
	values := map[string]string{"HC_TERMINAL_ID": "t42", "HC_TERMINAL_SOCKET": "/tmp/core \"quoted\".sock", "ROTA_CONFIG_DIR": "/tmp/config", "ROTA_CORE_STATE_DIR": "/tmp/state", "API_TOKEN": "not-for-tools", "CODEX_HOME": "/default/account"}
	r := newRig(values)
	p, err := r.launcher().Resolve("/project", cfgOf(t, map[string]any{"orchestrator.harness": harness.Codex}))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	if err := os.WriteFile(filepath.Join(dir, harness.Codex), []byte("#!/bin/sh\nprintf '%s\\0' \"$@\" > \"$ARGV_LOG\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	separator := -1
	for i, arg := range p.Supervisor {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 || separator+1 >= len(p.Supervisor) {
		t.Fatalf("no supervised native command: %v", p.Supervisor)
	}
	command := append(append([]string(nil), p.Supervisor[separator+1:]...), "$rota-orchestrate")
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Env = []string{"PATH=" + dir + ":/usr/bin:/bin", "ARGV_LOG=" + log}
	for k, v := range values {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	// The supervisor injects this only after Resolve. A planned argv snapshot
	// would omit it and lose round ownership inside Codex tools.
	values["ROTA_ROUND_HOLDER_PID"] = "9876"
	cmd.Env = append(cmd.Env, "ROTA_ROUND_HOLDER_PID=9876")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("supervised launch: %v %s", err, out)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	if len(args) == 0 || args[len(args)-1] != "$rota-orchestrate" {
		t.Fatalf("keepalive first prompt lost or moved: %q", args)
	}
	got := map[string]string{}
	for i, arg := range args {
		if arg != "-c" || i+1 >= len(args) {
			continue
		}
		key, value, ok := strings.Cut(args[i+1], "=")
		if !ok {
			continue
		}
		if name, ok := strings.CutPrefix(key, "shell_environment_policy.set."); ok {
			var valueString string
			if err := json.Unmarshal([]byte(value), &valueString); err != nil {
				t.Fatal(err)
			}
			got[name] = valueString
		}
	}
	for _, k := range []string{"HC_TERMINAL_ID", "HC_TERMINAL_SOCKET", "ROTA_CONFIG_DIR", "ROTA_CORE_STATE_DIR", "ROTA_ROUND_HOLDER_PID"} {
		if got[k] != values[k] {
			t.Errorf("normal orchestrator tool identity %s=%q want %q", k, got[k], values[k])
		}
	}
	for _, k := range []string{"API_TOKEN", "CODEX_HOME"} {
		if _, ok := got[k]; ok {
			t.Errorf("copied account/secret %s", k)
		}
	}
	if p.ConfigDir != "" || p.Account != "" {
		t.Fatal("default account changed")
	}
}
