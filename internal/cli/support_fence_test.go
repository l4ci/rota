package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeLocalConfig(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, ".rota", "config.local.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// supportProject is a tracker project whose config.local.json makes this
// machine the support machine.
func supportProject(t *testing.T) string {
	t.Helper()
	root := trackerProject(t, `{"issues":{"source":"github"},"round":{"support":"mac"}}`+"\n")
	writeLocalConfig(t, root, `{"round":{"role":"support"}}`+"\n")
	return root
}

func TestSupportRoleConfigSetWritesLocal(t *testing.T) {
	root := trackerProject(t, `{"issues":{"source":"github"}}`+"\n")
	code, env, stderr := rotaRun(t, "--json", "-C", root, "config", "set", "round.role", "support")
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got := envObj(env, "data")["scope"]; got != "local" {
		t.Errorf("scope = %v", got)
	}
	local, _ := os.ReadFile(filepath.Join(root, ".rota", "config.local.json"))
	if !strings.Contains(string(local), `"role": "support"`) {
		t.Errorf("config.local.json = %s", local)
	}
	tracked, _ := os.ReadFile(filepath.Join(root, ".rota", "config.json"))
	if strings.Contains(string(tracked), "role") {
		t.Errorf("config.json holds role: %s", tracked)
	}
	code, _, _ = rotaRun(t, "--json", "-C", root, "config", "set", "round.role", "main")
	if code != ExitOK {
		t.Fatalf("set main: exit %d", code)
	}
}

func TestRoleInTrackedConfigRefused(t *testing.T) {
	root := trackerProject(t, `{"round":{"role":"support","support":"mac"}}`+"\n")
	code, env, _ := rotaRun(t, "--json", "-C", root, "round", "status")
	if code != ExitRefused {
		t.Fatalf("exit %d, want %d: %v", code, ExitRefused, env)
	}
	if got := envObj(env, "data")["blockedBy"]; got != "config" {
		t.Errorf("blockedBy = %v", got)
	}
	hint, _ := envObj(env, "error")["hint"].(string)
	if !strings.Contains(hint, "config.local.json") {
		t.Errorf("hint = %q", hint)
	}
	// config and migrate verbs stay usable so the user can fix it.
	if code, _, _ := rotaRun(t, "--json", "-C", root, "config", "show"); code == ExitRefused {
		t.Error("config show refused over tracked round.role")
	}
}

func TestSupportFileModeRefused(t *testing.T) {
	root := trackerProject(t, "") // file backend
	for _, kv := range [][]string{{"round.support", "mac"}, {"round.role", "support"}} {
		code, env, _ := rotaRun(t, "--json", "-C", root, "config", "set", kv[0], kv[1])
		if code != ExitRefused {
			t.Fatalf("%v: exit %d: %v", kv, code, env)
		}
		if got := envObj(env, "data")["blockedBy"]; got != "backend" {
			t.Errorf("%v: blockedBy = %v", kv, got)
		}
		hint, _ := envObj(env, "error")["hint"].(string)
		if !strings.Contains(hint, "issues.source") {
			t.Errorf("%v: hint = %q", kv, hint)
		}
	}
	// The defaults stay settable in file mode.
	if code, _, _ := rotaRun(t, "--json", "-C", root, "config", "set", "round.role", "main"); code != ExitOK {
		t.Errorf("set round.role main in file mode: exit %d", code)
	}
	for _, cfg := range []string{
		`{"round":{"support":"mac"}}`,
		`{}`,
	} {
		root := trackerProject(t, cfg+"\n")
		if cfg == `{}` {
			writeLocalConfig(t, root, `{"round":{"role":"support"}}`+"\n")
		}
		code, env, _ := rotaRun(t, "--json", "-C", root, "round", "start")
		if code != ExitRefused {
			t.Fatalf("%s: exit %d: %v", cfg, code, env)
		}
		if got := envObj(env, "data")["blockedBy"]; got != "backend" {
			t.Errorf("%s: blockedBy = %v", cfg, got)
		}
	}
}

func TestRoundStartSupportNeedsName(t *testing.T) {
	root := trackerProject(t, `{"issues":{"source":"github"}}`+"\n")
	writeLocalConfig(t, root, `{"round":{"role":"support"}}`+"\n")
	code, env, _ := rotaRun(t, "--json", "-C", root, "round", "start")
	if code != ExitRefused {
		t.Fatalf("exit %d: %v", code, env)
	}
	if got := envObj(env, "data")["blockedBy"]; got != "config" {
		t.Errorf("blockedBy = %v", got)
	}
	hint, _ := envObj(env, "error")["hint"].(string)
	if !strings.Contains(hint, "set round.support to this machine's name") {
		t.Errorf("hint = %q", hint)
	}
}

func TestSupportRoleFencesWorkerGate(t *testing.T) {
	root := supportProject(t)
	code, env, _ := rotaRun(t, "--json", "-C", root, "worker", "gate", "ben")
	if code != ExitRefused {
		t.Fatalf("exit %d: %v", code, env)
	}
	data := envObj(env, "data")
	if data["blockedBy"] != "role" || data["role"] != "support" || data["changed"] != false {
		t.Errorf("data = %v", data)
	}
	e := envObj(env, "error")
	msg, _ := e["message"].(string)
	if !strings.Contains(msg, "rota worker gate is refused on a support machine (round.role support)") {
		t.Errorf("message = %q", msg)
	}
	hint, _ := e["hint"].(string)
	if !strings.Contains(hint, "unset round.role in .rota/config.local.json") {
		t.Errorf("hint = %q", hint)
	}
}

func TestMainRoleDoesNotHitSupportFence(t *testing.T) {
	root := trackerProject(t, "")
	_, env, _ := rotaRun(t, "--json", "-C", root, "worker", "gate", "ben")
	if got := envObj(env, "data")["blockedBy"]; got == "role" {
		t.Errorf("worker gate hit the support fence with no role set: %v", env)
	}
}
