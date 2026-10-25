package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
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
		want := "backend"
		if cfg == `{}` {
			want = "config"
		} // An unnamed support role refuses before round setup.
		if got := envObj(env, "data")["blockedBy"]; got != want {
			t.Errorf("%s: blockedBy = %v, want %s", cfg, got, want)
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

// An unrelated bad round.* key must not open the fence: it reads round.role alone.
func TestSupportFenceIgnoresUnrelatedBadKey(t *testing.T) {
	root := trackerProject(t, `{"issues":{"source":"github"},"round":{"support":"mac","scope":"nonsense"}}`+"\n")
	writeLocalConfig(t, root, `{"round":{"role":"support"}}`+"\n")
	code, env, _ := rotaRun(t, "--json", "-C", root, "worker", "gate", "ben")
	if code != ExitRefused {
		t.Fatalf("exit %d, want %d: %v", code, ExitRefused, env)
	}
	if got := envObj(env, "data")["blockedBy"]; got != "role" {
		t.Errorf("blockedBy = %v", got)
	}
}

// A round.role that cannot be read refuses a fenced verb instead of running it.
func TestSupportFenceRefusesUnreadableRole(t *testing.T) {
	root := trackerProject(t, `{"issues":{"source":"github"},"round":{"support":"mac"}}`+"\n")
	writeLocalConfig(t, root, `{"round":{"role":"helper"}}`+"\n")
	code, env, _ := rotaRun(t, "--json", "-C", root, "worker", "gate", "ben")
	if code != ExitRefused {
		t.Fatalf("exit %d, want %d: %v", code, ExitRefused, env)
	}
	if got := envObj(env, "data")["blockedBy"]; got != "config" {
		t.Errorf("blockedBy = %v", got)
	}
	hint, _ := envObj(env, "error")["hint"].(string)
	if !strings.Contains(hint, "round.role") {
		t.Errorf("hint = %q", hint)
	}
}

// On support, config set refuses a tracked key and allows round.role and --global.
func TestSupportConfigSetTrackedKeyRefused(t *testing.T) {
	root := supportProject(t)
	code, env, _ := rotaRun(t, "--json", "-C", root, "config", "set", "round.maxBounces", "5")
	if code != ExitRefused {
		t.Fatalf("tracked key: exit %d, want %d: %v", code, ExitRefused, env)
	}
	if got := envObj(env, "data")["blockedBy"]; got != "role" {
		t.Errorf("blockedBy = %v", got)
	}
	if code, env, _ := rotaRun(t, "--json", "-C", root, "config", "set", "round.role", "support"); code != ExitOK {
		t.Errorf("round.role: exit %d: %v", code, env)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if code, env, _ := rotaRun(t, "--json", "-C", root, "config", "set", "--global", "round.maxBounces", "5"); code != ExitOK {
		t.Errorf("--global: exit %d: %v", code, env)
	}
}

// A config.local.json that does not decode must not read as role main: the
// fence refuses instead of opening.
func TestSupportFenceRefusesMalformedLocalConfig(t *testing.T) {
	root := trackerProject(t, `{"issues":{"source":"github"},"round":{"support":"mac"}}`+"\n")
	writeLocalConfig(t, root, `{"round":{"role":"support"`+"\n")
	code, env, _ := rotaRun(t, "--json", "-C", root, "worker", "gate", "ben")
	if code != ExitRefused {
		t.Fatalf("exit %d, want %d: %v", code, ExitRefused, env)
	}
	if got := envObj(env, "data")["blockedBy"]; got != "config" {
		t.Errorf("blockedBy = %v", got)
	}
	msg, _ := envObj(env, "error")["message"].(string)
	if !strings.Contains(msg, "config.local.json") {
		t.Errorf("message = %q", msg)
	}
}

// #1598 AC-2: only main splits the work; the support side cannot offer.
func TestRoundSupportOfferRefusedOnSupport(t *testing.T) {
	root := supportProject(t)
	code, env, _ := rotaRun(t, "--json", "-C", root, "round", "support", "offer", "12")
	if code != ExitRefused {
		t.Fatalf("exit %d, want %d: %v", code, ExitRefused, env)
	}
	if got := envObj(env, "data")["blockedBy"]; got != "role" {
		t.Errorf("blockedBy = %v", got)
	}
}

// The work split needs a tracker: file mode refuses it (D3).
func TestRoundSupportOfferRefusedInFileMode(t *testing.T) {
	root := trackerProject(t, `{"round":{"support":"mac"}}`+"\n")
	code, env, _ := rotaRun(t, "--json", "-C", root, "round", "support", "offer", "B01")
	if code != ExitRefused {
		t.Fatalf("exit %d, want %d: %v", code, ExitRefused, env)
	}
	if got := envObj(env, "data")["blockedBy"]; got != "backend" {
		t.Errorf("blockedBy = %v", got)
	}
}

func TestRoundSupportOfferUsage(t *testing.T) {
	root := trackerProject(t, `{"issues":{"source":"github"},"round":{"support":"mac"}}`+"\n")
	if code, env, _ := rotaRun(t, "--json", "-C", root, "round", "support", "offer"); code != ExitUsage {
		t.Errorf("no IDs: exit %d: %v", code, env)
	}
}

type noTrustIssues struct{}

func (noTrustIssues) Comments(context.Context, tracker.IssueKey) ([]tracker.Comment, error) {
	return nil, nil
}
func (noTrustIssues) AddComment(context.Context, tracker.IssueKey, string) (string, error) {
	return "1", nil
}
func (noTrustIssues) DeleteComment(context.Context, tracker.IssueKey, string) error { return nil }

// A forge that cannot say who has write access (GitLab) cannot back a support
// round: round start refuses with blockedBy forge and names GitHub.
func TestRoundStartSupportNeedsPushTrust(t *testing.T) {
	root := trackerProject(t, `{"issues":{"source":"gitlab"},"round":{"support":"mac"}}`+"\n")
	deps := testDeps()
	deps.WorkerEnv = func() worker.Env {
		return worker.Env{IssueComments: func(string, tracker.Settings) (worker.IssueComments, error) { return noTrustIssues{}, nil }}
	}
	code, env, _ := rotaRunWith(t, deps, "--json", "-C", root, "round", "start")
	if code != ExitRefused {
		t.Fatalf("exit %d: %v", code, env)
	}
	if got := envObj(env, "data")["blockedBy"]; got != "forge" {
		t.Errorf("blockedBy = %v", got)
	}
	hint, _ := envObj(env, "error")["hint"].(string)
	if !strings.Contains(hint, "GitHub") {
		t.Errorf("hint = %q", hint)
	}
}

// Any BounceStore failure other than a missing push trust stops round start
// instead of surfacing later.
func TestRoundStartSupportSurfacesStoreError(t *testing.T) {
	root := trackerProject(t, `{"issues":{"source":"github"},"round":{"support":"mac"}}`+"\n")
	deps := testDeps()
	deps.WorkerEnv = func() worker.Env {
		return worker.Env{IssueComments: func(string, tracker.Settings) (worker.IssueComments, error) {
			return nil, errors.New("tracker unreachable")
		}}
	}
	code, env, stderr := rotaRunWith(t, deps, "--json", "-C", root, "round", "start")
	if code == ExitOK || !strings.Contains(stderr+fmt.Sprint(env), "tracker unreachable") {
		t.Fatalf("exit %d: %v %s", code, env, stderr)
	}
}
