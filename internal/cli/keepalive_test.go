package cli

import (
	"github.com/l4ci/rota/internal/limits"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/roundlease"
)

// kaProject is a git project whose config zeroes the backoff.
func kaProject(t *testing.T, cfg string) string {
	t.Helper()
	dir := gitRepo(t)
	if cfg == "" {
		cfg = `{"git":{"baseBranch":"feat/x"},"orchestrator":{"keepaliveBackoffSeconds":0}}`
	}
	os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(cfg), 0o644)
	return dir
}

func kaHandoff(dir string) string {
	return handoffFile(dir, config.Load(filepath.Join(dir, ".rota", "config.json")))
}

// kaChild is a shell child that counts its starts, records its arguments and
// writes a handoff on its first start and consumes it on the next, as the
// SessionStart hook does.
func kaChild(t *testing.T, dir string) []string {
	t.Helper()
	script := `n=$(cat "$1/count" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "$1/count"
echo "$*" >> "$1/args"; echo "$ROTA_ROUND_HOLDER_PID" > "$1/holder"
if [ "$n" = 1 ]; then mkdir -p "$(dirname "$2")"; echo "<!-- rota-handoff: orchestrator -->" > "$2"; else rm -f "$2"; fi`
	return []string{"sh", "-c", script, "sh", t.TempDir(), kaHandoff(dir)}
}

func TestKeepaliveRunUsageErrors(t *testing.T) {
	dir := kaProject(t, "")
	for _, argv := range [][]string{
		{"keepalive", "run"},
		{"keepalive", "run", "claude"},
		{"keepalive", "run", "--"},
		{"keepalive", "run", "x", "--", "claude"},
		{"keepalive", "run", "--breaker", "0", "--", "true"},
		{"keepalive", "run", "--max-restarts", "-1", "--", "true"},
		{"keepalive", "run", "--backoff", "abc", "--", "true"},
		{"keepalive", "run", "--prompt", "", "--", "true"},
	} {
		if code, _, _ := rotaIn(t, dir, argv...); code != 2 {
			t.Errorf("%v: exit %d, want 2", argv, code)
		}
	}
	if code, _, _ := rotaIn(t, t.TempDir(), "keepalive", "run", "--", "true"); code != 3 {
		t.Errorf("outside a project: exit %d, want 3", code)
	}
	if code, _, _ := rotaIn(t, t.TempDir(), "keepalive", "status"); code != 3 {
		t.Errorf("status outside a project: exit %d, want 3", code)
	}
}

func TestKeepaliveRunRestartsOnFreshHandoff(t *testing.T) {
	dir := kaProject(t, "")
	child := kaChild(t, dir)
	code, out, errOut := rotaIn(t, dir, append([]string{"keepalive", "run", "--prompt", "go on", "--json", "--"}, child...)...)
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	d := data(t, out)
	if d["stopReason"] != "no-handoff" || d["restarts"] != float64(1) || d["changed"] != true {
		t.Fatalf("data %v", d)
	}
	if n, _ := os.ReadFile(filepath.Join(child[4], "count")); strings.TrimSpace(string(n)) != "2" {
		t.Errorf("starts: %q", n)
	}
	args, _ := os.ReadFile(filepath.Join(child[4], "args"))
	lines := strings.Split(strings.TrimSpace(string(args)), "\n")
	if len(lines) != 2 || strings.HasSuffix(lines[0], "go on") || !strings.HasSuffix(lines[1], "go on") {
		t.Errorf("the prompt goes last, on the restart only: %q", lines)
	}
	if h, _ := os.ReadFile(filepath.Join(child[4], "holder")); strings.TrimSpace(string(h)) != strconv.Itoa(os.Getpid()) {
		t.Errorf("child must see ROTA_ROUND_HOLDER_PID: %q", h)
	}
	cd, _ := roundlease.CommonDir(dir)
	if _, st, _ := roundlease.DefaultEnv().Read(cd); st != roundlease.None {
		t.Errorf("lease must be released: %v", st)
	}
	code, out, _ = rotaIn(t, dir, "keepalive", "status", "--json")
	sd := data(t, out)
	ks, _ := sd["keepalive"].(map[string]any)
	if code != 0 || sd["running"] != false || ks["status"] != "stopped" || ks["stopReason"] != "no-handoff" {
		t.Errorf("status after: %v", sd)
	}
	if l := sd["lease"].(map[string]any); l["state"] != "none" || l["holderPid"] != nil {
		t.Errorf("lease: %v", l)
	}
}

func TestKeepaliveRunBreakerExitsOneWithoutEscalateIssue(t *testing.T) {
	dir := kaProject(t, `{"git":{"baseBranch":"feat/x"},"orchestrator":{"keepaliveBackoffSeconds":0,"keepaliveBreaker":2}}`)
	h := kaHandoff(dir)
	os.MkdirAll(filepath.Dir(h), 0o755)
	os.WriteFile(h, []byte("x"), 0o644)
	code, out, errOut := rotaIn(t, dir, "keepalive", "run", "--json", "--", "true")
	if code != 1 {
		t.Fatalf("exit %d: %s", code, out)
	}
	d := data(t, out)
	if d["stopReason"] != "breaker" || d["restarts"] != float64(1) || d["noProgress"] != float64(2) || d["escalation"] != nil {
		t.Fatalf("data %v", d)
	}
	if !strings.Contains(out, "escalateIssue is unset") || !strings.Contains(errOut, "escalateIssue is unset") {
		t.Errorf("the warning is missing: %s | %s", out, errOut)
	}
}

func TestKeepaliveRunRefusedWhileLeaseHeldAndCommandMissing(t *testing.T) {
	dir := kaProject(t, "")
	cd, _ := roundlease.CommonDir(dir)
	env := roundlease.DefaultEnv()
	// pid 1 is alive and never us.
	if _, _, _, err := env.Acquire(cd, dir, roundlease.Holder{PID: 1, Start: mustStart(env, 1)}, 3); err != nil {
		t.Fatal(err)
	}
	code, out, _ := rotaIn(t, dir, "keepalive", "run", "--json", "--", "true")
	d := data(t, out)
	if code != 4 || d["blockedBy"] != "lease held" || d["changed"] != false {
		t.Fatalf("exit %d data %v", code, d)
	}
	_, out, _ = rotaIn(t, dir, "keepalive", "status", "--json")
	if l := data(t, out)["lease"].(map[string]any); l["state"] != "live" || l["holderPid"] != float64(1) || l["round"] != float64(3) {
		t.Errorf("status lease: %v", l)
	}
	os.Remove(roundlease.Path(cd))
	if code, _, _ := rotaIn(t, dir, "keepalive", "run", "--", "/nonexistent/claude"); code != 5 {
		t.Errorf("a command that cannot run: exit %d, want 5", code)
	}
	if _, st, _ := env.Read(cd); st != roundlease.None {
		t.Errorf("a failed start must not leave the lease: %v", st)
	}
}

func mustStart(e roundlease.Env, pid int) uint64 { s, _ := e.StartTime(pid); return s }

func TestKeepaliveRunsTheLimitsLoopUnlessTold(t *testing.T) {
	for _, noLimits := range []bool{false, true} {
		dir := kaProject(t, "")
		cd, _ := roundlease.CommonDir(dir)
		out := filepath.Join(t.TempDir(), "seen")
		// the child waits for the loop's record to appear, or gives up
		script := `i=0; while [ ! -e "$1" ] && [ $i -lt 50 ]; do sleep 0.1; i=$((i+1)); done; if [ -e "$1" ]; then echo yes > "$2"; else echo no > "$2"; fi`
		argv := []string{"keepalive", "run", "--json"}
		if noLimits {
			argv = append(argv, "--no-limits")
		}
		argv = append(argv, "--", "sh", "-c", script, "sh", limits.WatchPath(cd), out)
		if code, o, e := rotaIn(t, dir, argv...); code != 0 {
			t.Fatalf("exit %d: %s %s", code, o, e)
		}
		b, _ := os.ReadFile(out)
		want := "yes"
		if noLimits {
			want = "no"
		}
		if strings.TrimSpace(string(b)) != want {
			t.Errorf("--no-limits=%v: the watcher record was %q, want %q", noLimits, b, want)
		}
		if _, found := limits.ReadWatching(cd); found {
			t.Errorf("--no-limits=%v: the record must be gone after the run", noLimits)
		}
	}
	dir := kaProject(t, `{"git":{"baseBranch":"feat/x"},"limits":{"maxResumes":0}}`)
	if code, _, _ := rotaIn(t, dir, "keepalive", "run", "--", "true"); code != 70 {
		t.Errorf("a bad limits key must exit 70, got %d", code)
	}
	if code, _, _ := rotaIn(t, dir, "keepalive", "run", "--no-limits", "--", "true"); code != 0 {
		t.Errorf("--no-limits must not read the limits keys, got %d", code)
	}
}

func TestKeepaliveGapOnlyWithSwitch(t *testing.T) {
	if keepaliveGap(false) != nil {
		t.Error("switching off must give D3's loop no gap flag")
	}
	if g := keepaliveGap(true); g == nil || g.Load() {
		t.Errorf("switching on: %v", g)
	}
}
