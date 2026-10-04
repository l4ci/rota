package host

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

var bg = context.Background()

func TestNewSelectsByDispatch(t *testing.T) {
	for dispatch, want := range map[string]string{"herdr": "herdr", "tmux": "tmux", "subagent": "tmux", "": "tmux"} {
		if got := New(dispatch, Deps{}).Name(); got != want {
			t.Errorf("New(%q) = %s, want %s", dispatch, got, want)
		}
	}
}

func TestInSession(t *testing.T) {
	f := &fake{}
	c := &clock{}
	cases := []struct {
		dispatch string
		env      map[string]string
		want     bool
	}{
		{"tmux", map[string]string{}, false},
		{"tmux", map[string]string{"TMUX": "/tmp/tmux-1/default,1,0"}, true},
		{"herdr", map[string]string{}, false},
		{"herdr", map[string]string{"HERDR_ENV": "1"}, false},
		{"herdr", map[string]string{"HERDR_WORKSPACE_ID": "w9"}, false},
		{"herdr", map[string]string{"HERDR_ENV": "1", "HERDR_WORKSPACE_ID": "w9"}, true},
	}
	for _, tc := range cases {
		if got := New(tc.dispatch, deps(f, tc.env, c)).InSession(); got != tc.want {
			t.Errorf("%s %v: InSession = %v, want %v", tc.dispatch, tc.env, got, tc.want)
		}
	}
	if len(f.calls) != 0 {
		t.Errorf("InSession must not run commands, ran %v", f.calls)
	}
}

func TestRequire(t *testing.T) {
	d := deps(&fake{}, nil, &clock{})
	d.LookPath = func(string) (string, error) { return "", fmt.Errorf("nope") }
	if err := New("herdr", d).Require(); err == nil || err.Error() != "herdr is not installed" {
		t.Errorf("herdr Require = %v", err)
	}
	if err := New("tmux", d).Require(); err == nil || err.Error() != "tmux is not installed" {
		t.Errorf("tmux Require = %v", err)
	}
}

func TestWhere(t *testing.T) {
	f := &fake{handler: func(string, []string) Result { return Result{Stdout: "main\n"} }}
	if got := New("tmux", deps(f, nil, &clock{})).Where(); got != "main" {
		t.Errorf("tmux Where = %q", got)
	}
	f.handler = func(string, []string) Result { return Result{ExitCode: 1} }
	if got := New("tmux", deps(f, nil, &clock{})).Where(); got != "?" {
		t.Errorf("tmux Where on failure = %q", got)
	}
	if got := New("herdr", deps(f, map[string]string{"HERDR_WORKSPACE_ID": "w9"}, &clock{})).Where(); got != "herdr workspace w9" {
		t.Errorf("herdr Where = %q", got)
	}
}

func TestAgentName(t *testing.T) {
	if got := AgentName("w1", "w9:t7"); got != "rota-w1-w9-t7" {
		t.Errorf("AgentName = %q", got)
	}
}

func TestLaunchArgs(t *testing.T) {
	kind, env, args, err := LaunchArgs(`FOO=bar BAZ=1 /usr/bin/claude --model sonnet --dangerously-skip-permissions`)
	if err != nil || kind != "claude" || !reflect.DeepEqual(env, []string{"FOO=bar", "BAZ=1"}) ||
		!reflect.DeepEqual(args, []string{"--model", "sonnet", "--dangerously-skip-permissions"}) {
		t.Errorf("LaunchArgs = %q %q %q %v", kind, env, args, err)
	}
	for _, bad := range []string{"gemini --yolo", "", `claude "oops`, "FOO=1", "FOO=1 /bin/sh -c claude"} {
		if _, _, _, err := LaunchArgs(bad); err == nil {
			t.Errorf("LaunchArgs(%q) should fail", bad)
		}
	}
}

func TestLaunchArgsKindFromBasename(t *testing.T) {
	kind, env, args, err := LaunchArgs(`A=1 /opt/bin/codex --model gpt-x --no-daemon`)
	if err != nil || kind != "codex" || !reflect.DeepEqual(env, []string{"A=1"}) ||
		!reflect.DeepEqual(args, []string{"--model", "gpt-x", "--no-daemon"}) {
		t.Errorf("LaunchArgs = %q %q %q %v", kind, env, args, err)
	}
}

func TestDialogKeys(t *testing.T) {
	trust := " Do you trust the files in this folder?\n ❯ 1. Yes, I trust this folder\n   2. No, exit\n"
	bypass := " WARNING: Bypass Permissions mode\n   1. No, exit\n ❯ 2. Yes, I accept\n"
	cursorOnNo := "   1. Yes, proceed\n ❯ 2. No\n"
	noCursor := "   1. Yes, I trust this folder\n   2. No, exit\n"
	cases := []struct {
		name, pane string
		want       []string
		ok         bool
	}{
		{"trust", trust, []string{"enter"}, true},
		{"bypass", bypass, []string{"enter"}, true},
		{"accept above cursor", cursorOnNo, []string{"up", "enter"}, true},
		{"accept below cursor", "❯ 1. No, exit\n  2. Other\n  3. Yes, I accept\n", []string{"down", "down", "enter"}, true},
		{"no cursor defaults to 1", noCursor, []string{"enter"}, true},
		// Smoke fixtures: bypass puts the cursor on "No, exit", trust on "Yes".
		{"bypass cursor on No, exit", "WARNING: Claude Code running in Bypass Permissions mode\n ❯ 1. No, exit\n   2. Yes, I accept\n", []string{"down", "enter"}, true},
		{"trust dialog", "Do you trust this folder?\n ❯ 1. Yes, I trust this folder\n   2. No, exit\n", []string{"enter"}, true},
		{"unknown dialog", "Pick a colour\n ❯ 1. Red\n   2. Blue\n", nil, false},
		{"unknown", "1. Something else\n2. Other\n", nil, false},
		// Claude Code v2.1.288 dropped the numbers (#209).
		{"unnumbered trust", " ❯ No, exit\n   Yes, I trust this folder\n\n Enter to confirm · Esc to cancel\n", []string{"down", "enter"}, true},
		{"unnumbered, cursor on yes", " Quick safety check\n\n   No, exit\n ❯ Yes, I trust this folder\n", []string{"enter"}, true},
		{"unnumbered unknown", " ❯ Red\n   Blue\n", nil, false},
		{"empty", "", nil, false},
	}
	for _, c := range cases {
		got, ok := DialogKeys(c.pane)
		if ok != c.ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: DialogKeys = %v %v, want %v %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

// TestDialogKeysRealTrustDialog pins the folder-trust dialog exactly as
// Claude Code v2.1.288 drew it under herdr 0.9.3 (captured in round 4's live
// check, #209): unnumbered options, cursor on "No, exit".
func TestDialogKeysRealTrustDialog(t *testing.T) {
	b, err := os.ReadFile("testdata/trust-dialog-2.1.288.txt")
	if err != nil {
		t.Fatal(err)
	}
	if keys, ok := DialogKeys(string(b)); !ok || !reflect.DeepEqual(keys, []string{"down", "enter"}) {
		t.Errorf("DialogKeys = %v %v, want [down enter] true", keys, ok)
	}
}

func TestJget(t *testing.T) {
	doc := `{"result":{"tab":{"tab_id":"w9:t7"},"n":3,"nil":null,"ok":true}}`
	for path, want := range map[string]string{
		"result.tab.tab_id": "w9:t7", "result.n": "3", "result.nil": "", "result.ok": "true", "result.x.y": "", "a": "",
	} {
		if got := jget(doc, path); got != want {
			t.Errorf("jget(%s) = %q, want %q", path, got, want)
		}
	}
	if jget("not json", "a") != "" {
		t.Error("bad JSON must read as empty")
	}
}

// ── tmux ────────────────────────────────────────────────────────────────────

func TestTmuxSpawn(t *testing.T) {
	booted := false
	f := &fake{handler: func(name string, a []string) Result {
		switch a[0] {
		case "has-session":
			return Result{ExitCode: 1}
		case "capture-pane":
			if booted {
				return Result{Stdout: "? for shortcuts"}
			}
			booted = true
			return Result{Stdout: "$ "}
		}
		return Result{}
	}}
	c := &clock{}
	h := New("tmux", deps(f, nil, c))
	got, err := h.Spawn(bg, SpawnOpts{Slot: "w1", Session: "rota", Cwd: "/wt", ConfigDir: "/acct", Launch: "claude --model sonnet", BootTimeout: 10})
	if err != nil || got != "rota:w1" {
		t.Fatalf("Spawn = %q, %v", got, err)
	}
	for _, want := range []string{
		"tmux new-session -d -s rota -c /wt",
		"tmux new-window -d -t rota -n w1 -c /wt",
		"tmux send-keys -t rota:w1 CLAUDE_CONFIG_DIR=/acct claude --model sonnet C-m",
	} {
		if !strings.Contains(f.log(), want) {
			t.Errorf("missing call %q in\n%s", want, f.log())
		}
	}
}

func TestTmuxSpawnTimesOut(t *testing.T) {
	f := &fake{handler: func(_ string, a []string) Result {
		if a[0] == "capture-pane" {
			return Result{Stdout: "$ "}
		}
		return Result{}
	}}
	_, err := New("tmux", deps(f, nil, &clock{})).Spawn(bg, SpawnOpts{Slot: "w1", Session: "rota", Cwd: "/wt", Launch: "claude", BootTimeout: 4})
	if err == nil || err.Error() != "slot 'w1' session did not come up within 4s" {
		t.Errorf("err = %v", err)
	}
}

func TestTmuxSpawnWindowFailure(t *testing.T) {
	f := &fake{handler: func(_ string, a []string) Result {
		if a[0] == "new-window" {
			return Result{ExitCode: 1}
		}
		return Result{}
	}}
	_, err := New("tmux", deps(f, nil, &clock{})).Spawn(bg, SpawnOpts{Slot: "w1", Session: "rota", Cwd: "/wt", Launch: "claude", BootTimeout: 4})
	if err == nil || err.Error() != "could not create tmux window rota:w1" {
		t.Errorf("err = %v", err)
	}
}

func TestTmuxSend(t *testing.T) {
	file := filepath.Join(t.TempDir(), "p.md")
	os.WriteFile(file, []byte("hi"), 0o644)
	changed := false
	f := &fake{handler: func(_ string, a []string) Result {
		switch a[0] {
		case "send-keys":
			changed = true
		case "capture-pane":
			if changed {
				return Result{Stdout: "after"}
			}
			return Result{Stdout: "before"}
		}
		return Result{}
	}}
	if err := New("tmux", deps(f, nil, &clock{})).Send(bg, "w1", "rota:w1", file); err != nil {
		t.Fatalf("Send = %v", err)
	}
	for _, want := range []string{"tmux load-buffer -b rota-w1 " + file, "tmux paste-buffer -b rota-w1 -t rota:w1", "tmux delete-buffer -b rota-w1", "tmux send-keys -t rota:w1 C-m"} {
		if !strings.Contains(f.log(), want) {
			t.Errorf("missing %q in\n%s", want, f.log())
		}
	}
}

func TestTmuxSendNeverSubmitted(t *testing.T) {
	f := &fake{handler: func(_ string, a []string) Result { return Result{Stdout: "static"} }}
	err := New("tmux", deps(f, nil, &clock{})).Send(bg, "w1", "rota:w1", "/f")
	if err != ErrNotSubmitted {
		t.Fatalf("Send = %v, want ErrNotSubmitted", err)
	}
	if n := f.count("tmux send-keys"); n != 4 {
		t.Errorf("send-keys attempts = %d, want 4", n)
	}
}

func TestTmuxSendPasteFails(t *testing.T) {
	f := &fake{handler: func(_ string, a []string) Result {
		if a[0] == "paste-buffer" {
			return Result{ExitCode: 1}
		}
		return Result{}
	}}
	if err := New("tmux", deps(f, nil, &clock{})).Send(bg, "w1", "rota:w1", "/f"); err != ErrNotSubmitted {
		t.Errorf("Send = %v", err)
	}
	if f.count("tmux send-keys") != 0 {
		t.Error("must not press Enter after a failed paste")
	}
}

func TestTmuxCaptureEmptyHandle(t *testing.T) {
	f := &fake{handler: func(string, []string) Result { return Result{Stdout: "CALLER PANE"} }}
	h := New("tmux", deps(f, nil, &clock{}))
	if h.Capture(bg, "w1", "", 40) != "" || len(f.calls) != 0 {
		t.Error("an empty handle would capture the caller's pane; must return nothing and run nothing")
	}
	if h.Capture(bg, "w1", "rota:w1", 40) != "CALLER PANE" || !strings.Contains(f.log(), "capture-pane -pJ -t rota:w1") {
		t.Errorf("capture call wrong: %s", f.log())
	}
	if h.Status(bg, "w1", "rota:w1") != "" {
		t.Error("tmux has no native status")
	}
}

func windows(list string) func(string, []string) Result {
	return func(_ string, a []string) Result {
		if a[0] == "list-windows" {
			return Result{Stdout: list}
		}
		return Result{}
	}
}

func TestTmuxKillExactWindowMatch(t *testing.T) {
	// w10 exists but w1 does not: a prefix match would call w1 alive.
	f := &fake{handler: windows("w10 4242\nother 1\n")}
	h := New("tmux", deps(f, nil, &clock{}))
	if err := h.Kill(bg, "w1", "rota:w1"); err != nil {
		t.Fatalf("Kill = %v", err)
	}
}

func TestTmuxKillProvesWindowGone(t *testing.T) {
	var killed bool
	f := &fake{}
	f.handler = func(_ string, a []string) Result {
		switch a[0] {
		case "kill-window":
			killed = true
			return Result{}
		case "list-windows":
			if killed {
				return Result{}
			}
			return Result{Stdout: "w1 4242\n"}
		}
		return Result{}
	}
	d := deps(f, nil, &clock{})
	var treeOf int
	d.Tree = func(p int) []int { treeOf = p; return []int{p, p + 1} }
	if err := New("tmux", d).Kill(bg, "w1", "rota:w1"); err != nil {
		t.Fatalf("Kill = %v", err)
	}
	if treeOf != 4242 {
		t.Errorf("pid tree taken from %d, want the pane pid 4242, before the close", treeOf)
	}
	if !strings.Contains(f.log(), "tmux kill-window -t rota:w1") {
		t.Errorf("no kill-window: %s", f.log())
	}
}

func TestTmuxKillSurvivingWindowIsAnError(t *testing.T) {
	f := &fake{handler: windows("w1 4242\n")} // kill-window changes nothing
	c := &clock{}
	d := deps(f, nil, c)
	d.Alive = func(p int) bool { return p == 4242 }
	err := New("tmux", d).Kill(bg, "w1", "rota:w1")
	want := "slot 'w1' previous session is still running (window rota:w1, pids 4242); not spawning a second one"
	if err == nil || err.Error() != want {
		t.Fatalf("Kill = %v, want %q", err, want)
	}
	if c.slept.Seconds() != 2 { // KillWait 3 -> two sleeps between three checks
		t.Errorf("slept %v, want 2s", c.slept)
	}
}

func TestTmuxKillSurvivingPidIsAnError(t *testing.T) {
	var killed bool
	f := &fake{}
	f.handler = func(_ string, a []string) Result {
		if a[0] == "kill-window" {
			killed = true
		}
		if a[0] == "list-windows" && !killed {
			return Result{Stdout: "w1 77\n"}
		}
		return Result{}
	}
	d := deps(f, nil, &clock{})
	d.Alive = func(p int) bool { return p == 77 }
	if err := New("tmux", d).Kill(bg, "w1", "rota:w1"); err == nil || !strings.Contains(err.Error(), "pids 77") {
		t.Fatalf("a window that is gone with a live pid must still fail: %v", err)
	}
}

func TestKillEmptyHandleIsNoop(t *testing.T) {
	f := &fake{handler: func(string, []string) Result { t.Error("ran a command"); return Result{} }}
	for _, d := range []string{"tmux", "herdr"} {
		if err := New(d, deps(f, nil, &clock{})).Kill(bg, "w1", ""); err != nil {
			t.Errorf("%s Kill with no handle = %v", d, err)
		}
	}
}

// ── herdr ───────────────────────────────────────────────────────────────────

var herdrEnv = map[string]string{"HERDR_ENV": "1", "HERDR_WORKSPACE_ID": "w9"}

const tabCreated = `{"id":"cli","result":{"type":"tab_created","tab":{"tab_id":"w9:t7"},"root_pane":{"pane_id":"w9:p17"}}}`

func herdrErr(code string) string {
	return fmt.Sprintf(`{"error":{"code":%q,"message":"fake"},"id":"cli"}`, code)
}

func agentJSON(status string) string {
	return fmt.Sprintf(`{"id":"cli","result":{"agent":{"agent_status":%q,"pane_id":"w9:p17"}}}`, status)
}

func TestHerdrSpawn(t *testing.T) {
	f := &fake{handler: func(_ string, a []string) Result {
		if a[0] == "tab" {
			return Result{Stdout: tabCreated}
		}
		return Result{Stdout: agentJSON("idle")}
	}}
	h := New("herdr", deps(f, herdrEnv, &clock{}))
	got, err := h.Spawn(bg, SpawnOpts{Slot: "w1", Cwd: "/wt", ConfigDir: "/acct/one", Launch: "FOO=1 claude --model sonnet --dangerously-skip-permissions", BootTimeout: 60})
	if err != nil || got != "w9:t7" {
		t.Fatalf("Spawn = %q, %v", got, err)
	}
	want := []string{
		"herdr tab create --workspace w9 --cwd /wt --label w1 --no-focus --env FOO=1 --env CLAUDE_CONFIG_DIR=/acct/one",
		"herdr agent start rota-w1-w9-t7 --kind claude --pane w9:p17 --timeout 60000 -- --model sonnet --dangerously-skip-permissions",
	}
	if !reflect.DeepEqual(f.calls, want) {
		t.Errorf("calls =\n%s\nwant\n%s", strings.Join(f.calls, "\n"), strings.Join(want, "\n"))
	}
}

func TestHerdrSpawnRejectsUnknownLaunch(t *testing.T) {
	f := &fake{handler: func(string, []string) Result { t.Error("ran a command"); return Result{} }}
	_, err := New("herdr", deps(f, herdrEnv, &clock{})).Spawn(bg, SpawnOpts{Slot: "w1", Launch: "gemini --yolo", BootTimeout: 5})
	if err == nil || !strings.Contains(err.Error(), "must run one of them, got: gemini --yolo") {
		t.Errorf("err = %v", err)
	}
}

// A codex launch: the tab gets CODEX_HOME and no CLAUDE_CONFIG_DIR, and agent
// start runs with --kind codex.
func TestHerdrSpawnCodex(t *testing.T) {
	f := &fake{handler: func(_ string, a []string) Result {
		if a[0] == "tab" {
			return Result{Stdout: tabCreated}
		}
		return Result{Stdout: agentJSON("idle")}
	}}
	h := New("herdr", deps(f, herdrEnv, &clock{}))
	got, err := h.Spawn(bg, SpawnOpts{Slot: "w1", Cwd: "/wt", ConfigDir: "/acct/one", CodexHome: "/cd/rota/codex/w1",
		Launch: "codex --model gpt-x --dangerously-bypass-approvals-and-sandbox --no-daemon", BootTimeout: 60})
	if err != nil || got != "w9:t7" {
		t.Fatalf("Spawn = %q, %v", got, err)
	}
	want := []string{
		"herdr tab create --workspace w9 --cwd /wt --label w1 --no-focus --env CODEX_HOME=/cd/rota/codex/w1",
		"herdr agent start rota-w1-w9-t7 --kind codex --pane w9:p17 --timeout 60000 -- --model gpt-x --dangerously-bypass-approvals-and-sandbox --no-daemon",
	}
	if !reflect.DeepEqual(f.calls, want) {
		t.Errorf("calls =\n%s\nwant\n%s", strings.Join(f.calls, "\n"), strings.Join(want, "\n"))
	}
}

// A claude launch ignores CodexHome, so the claude path is unchanged.
func TestHerdrSpawnClaudeIgnoresCodexHome(t *testing.T) {
	f := &fake{handler: func(_ string, a []string) Result {
		if a[0] == "tab" {
			return Result{Stdout: tabCreated}
		}
		return Result{Stdout: agentJSON("idle")}
	}}
	_, err := New("herdr", deps(f, herdrEnv, &clock{})).Spawn(bg, SpawnOpts{Slot: "w1", Cwd: "/wt", ConfigDir: "/acct", CodexHome: "/h", Launch: "claude", BootTimeout: 5})
	if err != nil || strings.Contains(f.log(), "CODEX_HOME") || !strings.Contains(f.calls[0], "--env CLAUDE_CONFIG_DIR=/acct") {
		t.Errorf("%v\n%s", err, f.log())
	}
}

func TestHerdrSpawnTabCreateFailures(t *testing.T) {
	for name, res := range map[string]Result{
		"fails":    {ExitCode: 1, Stderr: "boom\n"},
		"no ids":   {Stdout: `{"result":{}}`},
		"bad json": {Stdout: "garbage"},
		"only tab": {Stdout: `{"result":{"tab":{"tab_id":"w9:t7"}}}`},
	} {
		f := &fake{handler: func(string, []string) Result { return res }}
		_, err := New("herdr", deps(f, herdrEnv, &clock{})).Spawn(bg, SpawnOpts{Slot: "w1", Launch: "claude", BootTimeout: 5})
		if err == nil {
			t.Errorf("%s: want error", name)
		}
		if f.count("herdr agent start") != 0 {
			t.Errorf("%s: must not start an agent without a tab", name)
		}
	}
}

func TestHerdrSpawnAnswersStartupDialogs(t *testing.T) {
	dialogs := []string{
		"Do you trust?\n ❯ 1. Yes, I trust this folder\n   2. No, exit\n",
		"Bypass\n   1. No, exit\n ❯ 2. Yes, I accept\n",
	}
	read, waits := 0, 0
	f := &fake{handler: func(_ string, a []string) Result {
		switch a[0] + " " + a[1] {
		case "tab create":
			return Result{Stdout: tabCreated}
		case "agent start":
			return Result{ExitCode: 1, Stderr: herdrErr("agent_not_ready")}
		case "agent read":
			txt := dialogs[read]
			read++
			return Result{Stdout: txt} // herdr 0.9.x prints pane text, not JSON
		case "agent wait":
			waits++
			if waits == 1 {
				return Result{Stdout: agentJSON("blocked")}
			}
			return Result{Stdout: agentJSON("idle")}
		}
		return Result{}
	}}
	got, err := New("herdr", deps(f, herdrEnv, &clock{})).Spawn(bg, SpawnOpts{Slot: "w1", Cwd: "/wt", Launch: "claude", BootTimeout: 30})
	if err != nil || got != "w9:t7" {
		t.Fatalf("Spawn = %q, %v", got, err)
	}
	if f.count("herdr agent send-keys rota-w1-w9-t7 enter") != 2 {
		t.Errorf("want two answered dialogs:\n%s", f.log())
	}
	if !strings.Contains(f.log(), "agent wait rota-w1-w9-t7 --until idle --until blocked --timeout 30000") {
		t.Errorf("wait call wrong:\n%s", f.log())
	}
}

func TestHerdrSpawnDialogFailures(t *testing.T) {
	mk := func(pane string, status string) *fake {
		return &fake{handler: func(_ string, a []string) Result {
			switch a[0] + " " + a[1] {
			case "tab create":
				return Result{Stdout: tabCreated}
			case "agent start":
				return Result{ExitCode: 1, Stderr: herdrErr("agent_not_ready")}
			case "agent read":
				return Result{Stdout: pane}
			case "agent wait":
				return Result{Stdout: agentJSON(status)}
			}
			return Result{}
		}}
	}
	opts := SpawnOpts{Slot: "w1", Cwd: "/wt", Launch: "claude", BootTimeout: 5}
	_, err := New("herdr", deps(mk("What is this?\n1. Foo\n", "idle"), herdrEnv, &clock{})).Spawn(bg, opts)
	if err == nil || !strings.Contains(err.Error(), "unrecognised startup dialog in tab w9:t7") {
		t.Errorf("unknown dialog: %v", err)
	}
	f := mk("❯ 1. Yes, proceed\n", "blocked")
	_, err = New("herdr", deps(f, herdrEnv, &clock{})).Spawn(bg, opts)
	if err == nil || !strings.Contains(err.Error(), "did not reach idle after its startup dialogs") {
		t.Errorf("never idle: %v", err)
	}
	if f.count("herdr agent send-keys") != 3 {
		t.Errorf("want 3 dialog rounds, got %d", f.count("herdr agent send-keys"))
	}
}

func TestHerdrSpawnOtherStartFailure(t *testing.T) {
	f := &fake{handler: func(_ string, a []string) Result {
		if a[0] == "tab" {
			return Result{Stdout: tabCreated}
		}
		return Result{ExitCode: 1, Stderr: herdrErr("server_down")}
	}}
	_, err := New("herdr", deps(f, herdrEnv, &clock{})).Spawn(bg, SpawnOpts{Slot: "w1", Launch: "claude", BootTimeout: 5})
	if err == nil || !strings.Contains(err.Error(), "herdr agent start failed for slot 'w1' (w9:t7; tab closed)") {
		t.Errorf("err = %v", err)
	}
	if f.count("herdr tab close w9:t7") != 1 {
		t.Errorf("a failed start must close the tab it created:\n%s", f.log())
	}
	if f.count("herdr agent read") != 0 {
		t.Error("only agent_not_ready may enter the dialog loop")
	}
}

func TestHerdrSend(t *testing.T) {
	file := filepath.Join(t.TempDir(), "p.md")
	os.WriteFile(file, []byte("do the task\n\n"), 0o644)
	cases := []struct {
		name string
		res  Result
		want error
	}{
		{"picked up", Result{Stdout: agentJSON("working")}, nil},
		{"dialog", Result{ExitCode: 1, Stderr: herdrErr("agent_blocked")}, ErrDialogOpen},
		{"other error", Result{ExitCode: 1, Stderr: herdrErr("timeout")}, ErrNotSubmitted},
		{"garbage error", Result{ExitCode: 1, Stderr: "x"}, ErrNotSubmitted},
	}
	for _, c := range cases {
		f := &fake{handler: func(_ string, a []string) Result {
			if a[0] == "agent" && a[1] == "get" {
				return Result{Stdout: agentJSON("idle")}
			}
			if a[0] == "agent" && a[1] == "read" {
				return Result{} // nothing on the prompt line
			}
			return c.res
		}}
		err := New("herdr", deps(f, herdrEnv, &clock{})).Send(bg, "w1", "w9:t7", file)
		if err != c.want {
			t.Errorf("%s: Send = %v, want %v", c.name, err, c.want)
		}
		want := "herdr agent prompt rota-w1-w9-t7 do the task --wait --until working --until blocked --timeout 60000"
		if f.count(want) != 1 || f.count("herdr agent send-keys") != 0 || f.count("herdr agent get") != 0 {
			t.Errorf("%s: calls =\n%s", c.name, f.log())
		}
	}
}

// promptHost is a fake herdr session whose Enter can be lost: `agent prompt`
// types the text and submits it, `send-keys enter` submits whatever is on the
// prompt line. The first dropEnters Enters (prompt's own included) do nothing.
type promptHost struct {
	dropEnters int
	typed      string // text on the prompt line
	working    bool
	typedTimes int
}

func (p *promptHost) handle(_ string, a []string) Result {
	switch a[1] {
	case "get":
		if p.working {
			return Result{Stdout: agentJSON("working")}
		}
		return Result{Stdout: agentJSON("idle")}
	case "read":
		return Result{Stdout: "\u276f " + p.typed + "\n"}
	case "prompt":
		p.typed += a[3]
		p.typedTimes++
		return p.enter(Result{ExitCode: 1, Stderr: herdrErr("agent_prompt_stalled")})
	case "send-keys":
		p.enter(Result{})
		return Result{}
	case "wait":
		if p.working {
			return Result{Stdout: agentJSON("working")}
		}
		return Result{Stdout: agentJSON("idle")}
	}
	return Result{}
}

func (p *promptHost) enter(lost Result) Result {
	if p.dropEnters > 0 {
		p.dropEnters--
		return lost
	}
	p.working, p.typed = true, ""
	return Result{Stdout: agentJSON("working")}
}

func TestHerdrSendSubmitsBriefWhenEnterDropped(t *testing.T) {
	file := filepath.Join(t.TempDir(), "p.md")
	os.WriteFile(file, []byte("--- ORCHESTRATOR (round 1) ---\nline one that wraps\nthe last line of the brief\n"), 0o644)
	p := &promptHost{dropEnters: 1}
	f := &fake{handler: p.handle}
	if err := New("herdr", deps(f, herdrEnv, &clock{})).Send(bg, "w1", "w9:t7", file); err != nil {
		t.Fatalf("Send = %v, want nil after Enter retry\n%s", err, f.log())
	}
	if p.typedTimes != 1 || !p.working {
		t.Errorf("typed %d times, working=%v\n%s", p.typedTimes, p.working, f.log())
	}
	if n := f.count("herdr agent send-keys rota-w1-w9-t7 enter"); n != 1 {
		t.Errorf("enter presses = %d, want 1\n%s", n, f.log())
	}
}

func TestHerdrSubmitPendingNeverTypesAgain(t *testing.T) {
	file := filepath.Join(t.TempDir(), "p.md")
	os.WriteFile(file, []byte("sig\nthe last line of the brief\n"), 0o644)
	p := &promptHost{dropEnters: 5} // every Enter of the first call is lost
	f := &fake{handler: p.handle}
	h := New("herdr", deps(f, herdrEnv, &clock{}))
	if err := h.Send(bg, "w1", "w9:t7", file); err != ErrNotSubmitted {
		t.Fatalf("first Send = %v, want ErrNotSubmitted", err)
	}
	if n := f.count("herdr agent send-keys"); n != submitRetries {
		t.Errorf("enter presses = %d, want bounded at %d", n, submitRetries)
	}
	// The resend finds the brief on the prompt line and only submits it.
	p.dropEnters = 1
	handled, err := h.(Resubmitter).SubmitPending(bg, "w1", "w9:t7", file)
	if !handled || err != nil {
		t.Fatalf("SubmitPending = %v, %v", handled, err)
	}
	if p.typedTimes != 1 || !p.working {
		t.Errorf("typed %d times, working=%v\n%s", p.typedTimes, p.working, f.log())
	}
}

func TestHerdrSubmitPendingNothingPending(t *testing.T) {
	file := filepath.Join(t.TempDir(), "p.md")
	os.WriteFile(file, []byte("sig\nthe last line of the brief\n"), 0o644)
	p := &promptHost{} // empty prompt line
	f := &fake{handler: p.handle}
	handled, err := New("herdr", deps(f, herdrEnv, &clock{})).(Resubmitter).SubmitPending(bg, "w1", "w9:t7", file)
	if handled || err != nil || f.count("herdr agent send-keys") != 0 {
		t.Errorf("handled=%v err=%v\n%s", handled, err, f.log())
	}
}

func TestHerdrCaptureAndStatus(t *testing.T) {
	f := &fake{handler: func(_ string, a []string) Result {
		switch a[1] {
		case "read":
			return Result{Stdout: "line1\nline2\n"} // herdr 0.9.3: plain text, no envelope
		case "get":
			return Result{Stdout: agentJSON("working")}
		}
		return Result{}
	}}
	h := New("herdr", deps(f, herdrEnv, &clock{}))
	if got := h.Capture(bg, "w1", "w9:t7", 60); got != "line1\nline2\n" {
		t.Errorf("Capture = %q", got)
	}
	if !strings.Contains(f.log(), "herdr agent read rota-w1-w9-t7 --source recent-unwrapped --lines 60 --format text") {
		t.Errorf("read call wrong: %s", f.log())
	}
	if got := h.Status(bg, "w1", "w9:t7"); got != "working" {
		t.Errorf("Status = %q", got)
	}
	if h.Capture(bg, "w1", "", 60) != "" || h.Status(bg, "w1", "") != "" {
		t.Error("a never-dispatched slot has no capture or status")
	}
	f.handler = func(string, []string) Result { return Result{ExitCode: 1, Stderr: herdrErr("agent_not_found")} }
	if got := h.Status(bg, "w1", "w9:t7"); got != "gone" {
		t.Errorf("a tab with no agent must read gone, got %q", got)
	}
}

func TestHerdrKill(t *testing.T) {
	closed := false
	f := &fake{}
	f.handler = func(_ string, a []string) Result {
		switch a[0] + " " + a[1] {
		case "agent get":
			return Result{Stdout: agentJSON("idle")}
		case "pane process-info":
			return Result{Stdout: `{"result":{"process_info":{"foreground_processes":[{"pid":10},{"pid":11}],"shell_pid":9}}}`}
		case "tab close":
			closed = true
		case "tab get":
			if closed {
				return Result{ExitCode: 1, Stderr: herdrErr("tab_not_found")}
			}
			return Result{Stdout: "{}"}
		}
		return Result{}
	}
	var checked []int
	d := deps(f, herdrEnv, &clock{})
	d.Alive = func(p int) bool { checked = append(checked, p); return false }
	if err := New("herdr", d).Kill(bg, "w1", "w9:t7"); err != nil {
		t.Fatalf("Kill = %v", err)
	}
	log := f.log()
	iExit, iClose := strings.Index(log, "agent prompt rota-w1-w9-t7 /exit"), strings.Index(log, "tab close w9:t7")
	if iExit < 0 || iClose < iExit {
		t.Errorf("want /exit before tab close:\n%s", log)
	}
	if !reflect.DeepEqual(checked, []int{10, 11, 9}) {
		t.Errorf("pids checked = %v, want [10 11 9]", checked)
	}
}

func TestHerdrKillSurvivors(t *testing.T) {
	f := &fake{handler: func(_ string, a []string) Result {
		if a[0]+" "+a[1] == "tab get" {
			return Result{Stdout: "{}"} // tab never goes away
		}
		return Result{Stdout: agentJSON("idle")}
	}}
	err := New("herdr", deps(f, herdrEnv, &clock{})).Kill(bg, "w1", "w9:t7")
	want := "slot 'w1' previous session is still running (tab w9:t7); not spawning a second one"
	if err == nil || err.Error() != want {
		t.Errorf("Kill = %v, want %q", err, want)
	}
}

func TestHerdrNotify(t *testing.T) {
	f := &fake{handler: func(string, []string) Result { return Result{} }}
	New("herdr", deps(f, herdrEnv, &clock{})).Notify(bg, "T", "B")
	if f.log() != "herdr notification show T --body B --sound request" {
		t.Errorf("call = %s", f.log())
	}
	g := &fake{handler: func(string, []string) Result { t.Error("tmux has no notification surface"); return Result{} }}
	New("tmux", deps(g, nil, &clock{})).Notify(bg, "T", "B")
}

// A zombie has exited and only waits to be reaped; it must read as gone, and a
// live process as alive. The zombie's parent (an exec'd sleep) never reaps it.
func TestPidAliveZombieIsGone(t *testing.T) {
	zf := filepath.Join(t.TempDir(), "zombie.pid")
	parent := exec.Command("sh", "-c", `sh -c "echo \$\$ > '`+zf+`'" & exec sleep 300`)
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { parent.Process.Kill(); parent.Wait() })
	var zpid int
	for i := 0; i < 50 && zpid == 0; i++ {
		if b, err := os.ReadFile(zf); err == nil {
			zpid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		time.Sleep(100 * time.Millisecond)
	}
	if zpid == 0 {
		t.Fatal("zombie child never wrote its pid")
	}
	// Wait until the child has exited and shows as a zombie.
	for i := 0; i < 50; i++ {
		r, _ := ExecRunner(bg, "ps", []string{"-o", "stat=", "-p", strconv.Itoa(zpid)})
		if strings.HasPrefix(strings.TrimSpace(r.Stdout), "Z") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if pidAlive(ExecRunner, zpid) {
		t.Errorf("zombie pid %d must read as exited", zpid)
	}
	if !pidAlive(ExecRunner, parent.Process.Pid) {
		t.Error("the live parent must read as alive")
	}
}

func TestPidTreeParsesPS(t *testing.T) {
	f := &fake{handler: func(string, []string) Result {
		return Result{Stdout: "  1     0\n 10     1\n 11    10\n 12    10\n 13    11\n 99     1\n"}
	}}
	got := pidTree(f.run, 10)
	if !reflect.DeepEqual(got, []int{10, 11, 12, 13}) {
		t.Errorf("pidTree = %v", got)
	}
}

func TestTmuxEnsureOperator(t *testing.T) {
	file := filepath.Join(t.TempDir(), "i.md")
	os.WriteFile(file, []byte("go"), 0o644)
	keys := 0
	f := &fake{handler: func(_ string, a []string) Result {
		switch a[0] {
		case "has-session":
			return Result{ExitCode: 1}
		case "list-windows":
			return Result{Stdout: "scratch\noperator\n"} // a stale operator from an interrupted run
		case "send-keys":
			keys++
		case "capture-pane":
			if keys > 1 { // the launch keypress is the first; the paste's Enter is the second
				return Result{Stdout: "? for shortcuts, after"}
			}
			return Result{Stdout: "? for shortcuts"}
		}
		return Result{}
	}}
	op := New("tmux", deps(f, nil, &clock{})).(Operator)
	err := op.EnsureOperator(bg, OperatorOpts{Session: "ops", Root: "/proj", Command: "claude --continue", Instruction: file, BootTimeout: 10})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"tmux has-session -t ops",
		"tmux new-session -d -s ops -c /proj -n scratch",
		"tmux list-windows -t ops -F #{window_name}",
		"tmux kill-window -t ops:operator",
		"tmux new-window -d -t ops -n operator -c /proj",
		"tmux send-keys -t ops:operator claude --continue C-m",
	}
	if !reflect.DeepEqual(f.calls[:len(want)], want) {
		t.Errorf("calls =\n%s", f.log())
	}
	if !strings.Contains(f.log(), "tmux load-buffer -b rota-operator "+file) {
		t.Errorf("instruction not pasted:\n%s", f.log())
	}
}

func TestTmuxEnsureOperatorFailures(t *testing.T) {
	cases := map[string]struct {
		fail string
		want error
	}{
		"session": {"new-session", ErrOperatorSession},
		"window":  {"new-window", ErrOperatorWindow},
	}
	for name, c := range cases {
		f := &fake{handler: func(_ string, a []string) Result {
			if a[0] == "has-session" || a[0] == c.fail {
				return Result{ExitCode: 1}
			}
			return Result{}
		}}
		err := New("tmux", deps(f, nil, &clock{})).(Operator).EnsureOperator(bg, OperatorOpts{Session: "s", Root: "/p"})
		if err != c.want {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	f := &fake{handler: func(_ string, a []string) Result {
		if a[0] == "capture-pane" {
			return Result{Stdout: "$ "}
		}
		return Result{}
	}}
	err := New("tmux", deps(f, nil, &clock{})).(Operator).EnsureOperator(bg, OperatorOpts{Session: "s", Root: "/p", Instruction: "/x", BootTimeout: 4})
	if err != ErrOperatorBoot {
		t.Errorf("boot: %v", err)
	}
	f = &fake{handler: func(_ string, a []string) Result {
		if a[0] == "capture-pane" {
			return Result{Stdout: "? for shortcuts"}
		}
		return Result{}
	}}
	err = New("tmux", deps(f, nil, &clock{})).(Operator).EnsureOperator(bg, OperatorOpts{Session: "s", Root: "/p", Instruction: "/x", BootTimeout: 4})
	if err != ErrOperatorSend {
		t.Errorf("send: %v", err)
	}
	if _, ok := New("herdr", deps(f, nil, &clock{})).(Operator); ok {
		t.Error("herdr has no operator window to open")
	}
}

// TestHerdrPaneTextIsPlain pins herdr 0.9.3's `agent read`: the pane text on
// stdout as is (even text that looks like JSON), and on failure a JSON error
// on stderr with exit 1, which reads as no text.
func TestHerdrPaneTextIsPlain(t *testing.T) {
	for _, tc := range []struct {
		r    Result
		want string
	}{
		{Result{Stdout: "ROTA-DONE PR https://github.com/o/r/pull/7\n"}, "ROTA-DONE PR https://github.com/o/r/pull/7\n"},
		{Result{Stdout: `{"result":{"read":{"text":"x"}}}`}, `{"result":{"read":{"text":"x"}}}`},
		{Result{ExitCode: 1, Stderr: herdrErr("agent_not_found")}, ""},
	} {
		if got := paneText(tc.r); got != tc.want {
			t.Errorf("paneText(%+v) = %q, want %q", tc.r, got, tc.want)
		}
	}
}

// TestAgentNameIsWhatHerdrAccepts pins herdr 0.9.3's rule, [a-z][a-z0-9_-]{0,31}:
// a name that already passes keeps its old form, anything else is hashed (#204).
func TestAgentNameIsWhatHerdrAccepts(t *testing.T) {
	if got := AgentName("ben", "w1:t4"); got != "rota-ben-w1-t4" {
		t.Errorf("a valid name keeps its old form, got %q", got)
	}
	seen := map[string]string{}
	for _, tc := range []struct{ slot, handle string }{
		{"lr1", "w1W:t4"},
		{"lr1", "w1w:t4x"},
		{"Ben", "w1:t4"},
		{"a-very-long-slot-name-indeed", "w12:t345"},
		{"dots.and spaces", "wA:tB"},
		{"lr1", "w1X:t4"},
	} {
		got := AgentName(tc.slot, tc.handle)
		if !agentNameRe.MatchString(got) {
			t.Errorf("AgentName(%q, %q) = %q, which herdr rejects", tc.slot, tc.handle, got)
		}
		if prev, dup := seen[got]; dup {
			t.Errorf("AgentName(%q, %q) = %q collides with %s", tc.slot, tc.handle, got, prev)
		}
		seen[got] = tc.slot + " " + tc.handle
	}
	if AgentName("lr1", "w1W:t4") == AgentName("lr1", "w1w:t4") {
		t.Error("workspace ids that differ only in case must not share an agent name")
	}
}

// An Enter into a dialog would answer it: a blocked agent gets no keypress.
func TestHerdrSubmitPendingNeverEntersADialog(t *testing.T) {
	file := filepath.Join(t.TempDir(), "p.md")
	os.WriteFile(file, []byte("sig\nthe last line of the brief\n"), 0o644)
	f := &fake{handler: func(_ string, a []string) Result {
		if a[1] == "get" {
			return Result{Stdout: agentJSON("blocked")}
		}
		return Result{Stdout: "the last line of the brief"}
	}}
	handled, _ := New("herdr", deps(f, herdrEnv, &clock{})).(Resubmitter).SubmitPending(bg, "w1", "w9:t7", file)
	if handled || f.count("herdr agent send-keys") != 0 {
		t.Errorf("handled=%v\n%s", handled, f.log())
	}
}

// A brief already sent stays in scrollback above an empty prompt: no Enter,
// which could submit Claude Code's ghost suggestion.
func TestHerdrSubmitPendingIgnoresSentBriefInScrollback(t *testing.T) {
	file := filepath.Join(t.TempDir(), "p.md")
	os.WriteFile(file, []byte("sig\nthe last line of the brief\n"), 0o644)
	f := &fake{handler: func(_ string, a []string) Result {
		if a[1] == "get" {
			return Result{Stdout: agentJSON("idle")}
		}
		return Result{Stdout: "❯ sig\nthe last line of the brief\n⏺ working on it\n❯ \n"}
	}}
	handled, err := New("herdr", deps(f, herdrEnv, &clock{})).(Resubmitter).SubmitPending(bg, "w1", "w9:t7", file)
	if handled || err != nil || f.count("herdr agent send-keys") != 0 {
		t.Errorf("handled=%v err=%v\n%s", handled, err, f.log())
	}
}

// Claude Code collapses a long paste to a placeholder, so the text never shows.
func TestHerdrSubmitPendingSeesPastePlaceholder(t *testing.T) {
	file := filepath.Join(t.TempDir(), "p.md")
	os.WriteFile(file, []byte("sig\nthe last line of the brief\n"), 0o644)
	working := false
	f := &fake{handler: func(_ string, a []string) Result {
		switch a[1] {
		case "get", "wait":
			if working {
				return Result{Stdout: agentJSON("working")}
			}
			return Result{Stdout: agentJSON("idle")}
		case "send-keys":
			working = true
			return Result{}
		}
		return Result{Stdout: "❯ [Pasted text #1 +16 lines]\n"}
	}}
	handled, err := New("herdr", deps(f, herdrEnv, &clock{})).(Resubmitter).SubmitPending(bg, "w1", "w9:t7", file)
	if !handled || err != nil || f.count("herdr agent send-keys rota-w1-w9-t7 enter") != 1 {
		t.Errorf("handled=%v err=%v\n%s", handled, err, f.log())
	}
}

// TestLooksBooted pins the tmux boot check against pane text captured from
// the real UIs (#102): Claude Code 2.1.289 dropped the "? for shortcuts" line
// and the box frame, so only its banner marks a boot. Dialogs must not match.
func TestLooksBooted(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	cases := []struct {
		name string
		pane string
		want bool
	}{
		{"claude 2.1.289", read("claude-booted-2.1.289.txt"), true},
		{"codex 0.159.2", read("codex-booted-0.159.2.txt"), true},
		{"claude older hint", "? for shortcuts", true},
		{"claude older banner", "Welcome to Claude Code", true},
		{"older box frame", "╭──────╮", true},
		{"claude trust dialog", read("trust-dialog-2.1.288.txt"), false},
		{"shell", "$ claude --version\n2.1.289 (Claude Code)\n", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		if got := looksBooted(c.pane); got != c.want {
			t.Errorf("%s: looksBooted = %v, want %v", c.name, got, c.want)
		}
	}
}
