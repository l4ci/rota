package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
)

// #1774 AC-1: one verb on main turns the support orchestrator on with a
// name, in the tracked config, and takes it off again.
func TestRoundSupportEnable(t *testing.T) {
	root := trackerProject(t, `{"issues":{"source":"github"}}`+"\n")
	tracked := filepath.Join(root, ".rota", "config.json")

	code, env, stderr := rotaRun(t, "--json", "-C", root, "round", "support", "enable", "mac")
	if code != ExitOK {
		t.Fatalf("enable: exit %d: %s %v", code, stderr, env)
	}
	data := envObj(env, "data")
	if data["name"] != "mac" || data["changed"] != true || data["file"] != ".rota/config.json" {
		t.Errorf("enable data = %v", data)
	}
	if raw, _ := os.ReadFile(tracked); !strings.Contains(string(raw), `"support": "mac"`) {
		t.Errorf("config.json = %s", raw)
	}
	if _, err := os.Stat(filepath.Join(root, ".rota", "config.local.json")); err == nil {
		t.Error("enable wrote config.local.json")
	}
	code, env, _ = rotaRun(t, "--json", "-C", root, "config", "show", "round.support")
	entries, _ := envObj(env, "data")["entries"].([]any)
	if code != ExitOK || len(entries) != 1 {
		t.Fatalf("config show round.support: exit %d: %v", code, envObj(env, "data"))
	}
	if e, _ := entries[0].(*jsonx.Object); e == nil {
		t.Errorf("entry = %v", entries[0])
	} else if v, _ := e.Get("value"); v != "mac" {
		t.Errorf("config show round.support value = %v", v)
	}

	code, env, _ = rotaRun(t, "--json", "-C", root, "round", "support", "enable", "mac")
	if code != ExitOK || envObj(env, "data")["changed"] != false {
		t.Errorf("enable again: exit %d: %v", code, env)
	}

	code, env, stderr = rotaRun(t, "--json", "-C", root, "round", "support", "disable")
	if code != ExitOK {
		t.Fatalf("disable: exit %d: %s %v", code, stderr, env)
	}
	if data := envObj(env, "data"); data["name"] != "mac" || data["changed"] != true {
		t.Errorf("disable data = %v", data)
	}
	if raw, _ := os.ReadFile(tracked); strings.Contains(string(raw), "support") {
		t.Errorf("config.json after disable = %s", raw)
	}
	code, env, _ = rotaRun(t, "--json", "-C", root, "round", "support", "disable")
	if code != ExitOK || envObj(env, "data")["changed"] != false {
		t.Errorf("disable again: exit %d: %v", code, env)
	}
}

// A support round needs a tracker: both subverbs refuse in file mode and
// leave the config alone.
func TestRoundSupportEnableRefusedInFileMode(t *testing.T) {
	root := trackerProject(t, `{"round":{"support":"mac"}}`+"\n")
	for _, args := range [][]string{{"enable", "mac"}, {"disable"}} {
		code, env, _ := rotaRun(t, append([]string{"--json", "-C", root, "round", "support"}, args...)...)
		if code != ExitRefused {
			t.Fatalf("%v: exit %d, want %d: %v", args, code, ExitRefused, env)
		}
		if got := envObj(env, "data")["blockedBy"]; got != "backend" {
			t.Errorf("%v: blockedBy = %v", args, got)
		}
	}
	if raw, _ := os.ReadFile(filepath.Join(root, ".rota", "config.json")); !strings.Contains(string(raw), `"support":"mac"`) {
		t.Errorf("config.json changed: %s", raw)
	}
}

func TestRoundSupportEnableBadName(t *testing.T) {
	root := trackerProject(t, `{"issues":{"source":"github"}}`+"\n")
	for _, args := range [][]string{{"enable"}, {"enable", ""}, {"enable", "Mac"}, {"enable", "my mac"}, {"enable", "-mac"}, {"enable", "a", "b"}, {"disable", "mac"}} {
		code, env, _ := rotaRun(t, append([]string{"--json", "-C", root, "round", "support"}, args...)...)
		if code != ExitUsage {
			t.Errorf("%q: exit %d, want %d: %v", args, code, ExitUsage, env)
		}
	}
	if raw, _ := os.ReadFile(filepath.Join(root, ".rota", "config.json")); strings.Contains(string(raw), "support") {
		t.Errorf("config.json = %s", raw)
	}
}

// Only main names the support machine: the support side is fenced.
func TestRoundSupportEnableRefusedOnSupport(t *testing.T) {
	root := supportProject(t)
	for _, args := range [][]string{{"enable", "mac"}, {"disable"}} {
		code, env, _ := rotaRun(t, append([]string{"--json", "-C", root, "round", "support"}, args...)...)
		if code != ExitRefused {
			t.Fatalf("%v: exit %d, want %d: %v", args, code, ExitRefused, env)
		}
		if got := envObj(env, "data")["blockedBy"]; got != "role" {
			t.Errorf("%v: blockedBy = %v", args, got)
		}
	}
}
