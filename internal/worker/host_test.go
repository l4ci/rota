package worker

import (
	"context"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/host"
)

// ── fakes ───────────────────────────────────────────────────────────────────

// fakeHost is a scripted host.Host.
type fakeHost struct {
	name       string
	inSession  bool
	where      string
	requireErr error
	spawnErr   error
	killErr    error
	sendErr    error
	status     map[string]string
	explain    map[string]string   // per slot; "" is a failing explain
	panes      map[string][]string // captures returned in order, per slot
	calls      []string
	sent       string
	spawnOpts  host.SpawnOpts
}

func (f *fakeHost) Name() string    { return f.name }
func (f *fakeHost) Require() error  { return f.requireErr }
func (f *fakeHost) InSession() bool { return f.inSession }
func (f *fakeHost) Where() string   { return f.where }
func (f *fakeHost) Spawn(_ context.Context, o host.SpawnOpts) (string, error) {
	f.calls = append(f.calls, "spawn "+o.Slot)
	f.spawnOpts = o
	if f.spawnErr != nil {
		return "", f.spawnErr
	}
	return "w9:t7", nil
}
func (f *fakeHost) Send(_ context.Context, slot, handle, file string) error {
	f.calls = append(f.calls, "send "+slot+" "+handle)
	b, _ := os.ReadFile(file)
	f.sent = string(b)
	return f.sendErr
}
func (f *fakeHost) Capture(_ context.Context, slot, handle string, _ int) string {
	f.calls = append(f.calls, "capture "+slot)
	q := f.panes[slot]
	if len(q) == 0 {
		return ""
	}
	f.panes[slot] = q[1:]
	return q[0]
}
func (f *fakeHost) Status(_ context.Context, slot, handle string) string { return f.status[slot] }
func (f *fakeHost) Kill(_ context.Context, slot, handle string) error {
	f.calls = append(f.calls, "kill "+slot+" "+handle)
	return f.killErr
}
func (f *fakeHost) Explain(_ context.Context, slot, handle string) string {
	f.calls = append(f.calls, "explain "+slot)
	return f.explain[slot]
}
func (f *fakeHost) Notify(_ context.Context, title, body string) {
	f.calls = append(f.calls, "notify "+title+" | "+body)
}

// sweepHost is a herdr-like host that can close leftover shell panes.
type sweepHost struct {
	*fakeHost
}

func (s *sweepHost) SweepShells(_ context.Context, cwd string) (int, error) {
	s.calls = append(s.calls, "sweep "+filepath.Base(cwd))
	return 1, nil
}

func envWith(h host.Host) Env {
	return Env{NewHost: func(string) host.Host { return h }, Sleep: func(time.Duration) {}, Now: func() time.Time {
		return time.Date(2026, 10, 2, 15, 4, 5, 0, time.UTC)
	}}
}

func tmuxFake() *fakeHost { return &fakeHost{name: "tmux", inSession: true, where: "main"} }

func writeBrief(t *testing.T, text string) string {
	p := filepath.Join(t.TempDir(), "brief.md")
	os.WriteFile(p, []byte(text), 0o644)
	return p
}

func slotField(t *testing.T, dir, slot, key string) string {
	t.Helper()
	s := LoadRegistry(dir).Slot(slot)
	if s == nil {
		t.Fatalf("no slot %s", slot)
	}
	v, _ := s.Raw().Get(key)
	if v == nil {
		return "<null>"
	}
	return fmt.Sprint(v)
}

func exitOf(err error) int {
	if we, ok := err.(*exitcode.Error); ok {
		return we.Exit
	}
	return -1
}

// ── dispatch ────────────────────────────────────────────────────────────────

func TestDispatchTaskSpawnsWithSlotEnv(t *testing.T) {
	dir := newProject(t, `{"work":{"workerCommand":"claude --model haiku","portBase":31000}}`)
	goInit(t, dir, InitOpts{Slots: 2, Base: "main"})
	f := tmuxFake()
	round := 3
	if _, err := envWith(f).Dispatch(bg, dir, DispatchOpts{Slot: "w2", BodyFile: writeBrief(t, "x\n"), Task: "T1", Round: &round}); err != nil {
		t.Fatal(err)
	}
	want := "ROTA_SLOT=w2,ROTA_PORT_BASE=31100,ROTA_DB_SUFFIX=_w2"
	if got := strings.Join(f.spawnOpts.Env, ","); got != want {
		t.Errorf("spawn env = %q, want %q", got, want)
	}
}

func TestDispatchTaskRecreatesTheSession(t *testing.T) {
	dir := newProject(t, `{"work":{"workerCommand":"claude --model haiku"}}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	f := tmuxFake()
	round := 3
	res, err := envWith(f).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "do the thing\n"), Task: "T1", Round: &round})
	if err != nil {
		t.Fatal(err)
	}
	if res.Handle != "w9:t7" || res.Task != "T1" {
		t.Errorf("%+v", res)
	}
	want := []string{"kill w1 rota:w1", "spawn w1", "send w1 w9:t7"}
	if strings.Join(f.calls, ",") != strings.Join(want, ",") {
		t.Errorf("calls = %v, want %v", f.calls, want)
	}
	if f.spawnOpts.Launch != "claude --model haiku" || f.spawnOpts.Cwd != filepath.Join(dir, ".worktrees", "w1") ||
		f.spawnOpts.Session != "rota" || f.spawnOpts.BootTimeout != 60 {
		t.Errorf("spawn opts = %+v", f.spawnOpts)
	}
	if f.sent != "--- ORCHESTRATOR (round 3) ---\ndo the thing\n" {
		t.Errorf("payload = %q", f.sent)
	}
	for k, want := range map[string]string{"handle": "w9:t7", "state": "busy", "task": "T1", "pr": "<null>", "branch": "rota-worker/w1-t1"} {
		if got := slotField(t, dir, "w1", k); got != want {
			t.Errorf("slot.%s = %s, want %s", k, got, want)
		}
	}
	if n, ok := LoadRegistry(dir).Round(); !ok || n != 3 {
		t.Errorf("round = %v", n)
	}
}

func TestDispatchDefaultLaunchCommand(t *testing.T) {
	dir := newProject(t, `{"models":{"worker":"opus"}}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	f := tmuxFake()
	if _, err := envWith(f).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "x"), Task: "T1"}); err != nil {
		t.Fatal(err)
	}
	if f.spawnOpts.Launch != "claude --model opus --dangerously-skip-permissions" {
		t.Errorf("launch = %q", f.spawnOpts.Launch)
	}
	dir2 := newProject(t, `{}`)
	goInit(t, dir2, InitOpts{Slots: 1, Base: "main"})
	f2 := tmuxFake()
	envWith(f2).Dispatch(bg, dir2, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "x"), Task: "T1"})
	if f2.spawnOpts.Launch != "claude --model sonnet --dangerously-skip-permissions" {
		t.Errorf("launch = %q", f2.spawnOpts.Launch)
	}
}

func TestDispatchDoesNotSignTwiceAndDefaultsRound(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	f := tmuxFake()
	signed := "--- ORCHESTRATOR (round 1) ---\nbody line\n"
	if _, err := envWith(f).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, signed), Task: "T1"}); err != nil {
		t.Fatal(err)
	}
	if f.sent != signed {
		t.Errorf("payload = %q", f.sent)
	}
}

func TestDispatchPassesTheAccountConfigDir(t *testing.T) {
	cfg := `{"work":{"accounts":[{"name":"a","configDir":"/acct/a"}]}}`
	dir := newProject(t, cfg)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	(&Accounts{Getenv: func(string) string { return t.TempDir() }}).Assign(bg, dir, "w1", "a")
	f := tmuxFake()
	envWith(f).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "x"), Task: "T1"})
	if f.spawnOpts.ConfigDir != "/acct/a" {
		t.Errorf("config dir = %q", f.spawnOpts.ConfigDir)
	}
}

func TestDispatchRelayGoesIntoTheRunningSession(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	f := tmuxFake()
	e := envWith(f)
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "task\n"), Task: "T1"}); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	round := 2
	brief := writeBrief(t, "\n  the maintainer says use B  \nmore\n")
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: brief, Relay: true, Round: &round}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.calls, ",") != "send w1 w9:t7" {
		t.Errorf("a relay must not kill or spawn: %v", f.calls)
	}
	if !strings.HasPrefix(f.sent, "--- ORCHESTRATOR (round 2) ---\n[ORCHESTRATOR RELAY — this text was forwarded by the /rota-work orchestrator.\n") ||
		!strings.Contains(f.sent, "attribute it as 'orchestrator relay round 2'") || !strings.HasSuffix(f.sent, "sign-off in your session.]\n\n\n  the maintainer says use B  \nmore\n") {
		t.Errorf("payload = %q", f.sent)
	}
	relays, _ := LoadRegistry(dir).Slot("w1").Raw().Get("relays")
	if len(relays.([]any)) != 1 {
		t.Fatalf("relays = %v", relays)
	}
	r := relays.([]any)[0]
	if fmt.Sprint(r) == "" {
		t.Fatal()
	}
	b, _ := os.ReadFile(RegistryPath(dir))
	for _, want := range []string{`"round": 2`, `"ts": "2026-10-02T15:04:05Z"`, `"summary": "the maintainer says use B"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("registry missing %s:\n%s", want, b)
		}
	}
	// a relay does not clear the task or the PR
	if slotField(t, dir, "w1", "task") != "T1" {
		t.Error("relay cleared the task")
	}
}

func TestDispatchRelayLoggingFollowsWhatMayHaveBeenSent(t *testing.T) {
	for name, tc := range map[string]struct {
		err    error
		exit   int
		logged int
	}{
		"never submitted: may have landed, so logged": {host.ErrNotSubmitted, exitcode.ExitRetry, 1},
		"dialog open: certainly not sent":             {host.ErrDialogOpen, exitcode.ExitUnavailable, 0},
		"human draft: certainly not sent":             {host.ErrDraftOnPrompt, exitcode.ExitUnavailable, 0},
	} {
		t.Run(name, func(t *testing.T) {
			dir := newProject(t, `{}`)
			goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
			f := tmuxFake()
			e := envWith(f)
			e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "t"), Task: "T1"})
			f.sendErr = tc.err
			_, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "q"), Relay: true})
			if exitOf(err) != tc.exit {
				t.Fatalf("err = %v, want exit %d", err, tc.exit)
			}
			relays, _ := LoadRegistry(dir).Slot("w1").Raw().Get("relays")
			if len(relays.([]any)) != tc.logged {
				t.Errorf("relays logged = %d, want %d", len(relays.([]any)), tc.logged)
			}
		})
	}
}

// The contract's central split: a brief that was never submitted is safe to
// resend (6); an open dialog needs inspection first (5).
func TestDispatchTaskSendFailureExits(t *testing.T) {
	for _, tc := range []struct {
		err  error
		exit int
		msg  string
	}{
		{host.ErrNotSubmitted, exitcode.ExitRetry, "never picked up the brief — inspect the session before resending"},
		{host.ErrDialogOpen, exitcode.ExitUnavailable, "has a dialog open and refused input — inspect it before resending"},
		{host.ErrDraftOnPrompt, exitcode.ExitUnavailable, "has a human draft on its prompt line and nothing was sent — resend once it is submitted or cleared"},
	} {
		dir := newProject(t, `{}`)
		goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
		f := tmuxFake()
		f.sendErr = tc.err
		_, err := envWith(f).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "t"), Task: "T1"})
		if exitOf(err) != tc.exit || !strings.Contains(err.Error(), tc.msg) {
			t.Errorf("%v: err = %v", tc.err, err)
		}
	}
}

func TestDispatchRelayWithoutASessionIsAResolutionError(t *testing.T) {
	dir := newProject(t, `{"work":{"dispatch":"herdr"}}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	f := &fakeHost{name: "herdr", inSession: true}
	_, err := envWith(f).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "q"), Relay: true})
	if exitOf(err) != exitcode.ExitResolution || !strings.Contains(err.Error(), "has no session to relay into") {
		t.Errorf("err = %v", err)
	}
	if len(f.calls) != 0 {
		t.Errorf("calls = %v", f.calls)
	}
}

func TestDispatchHerdrOutsideAPaneIsUnavailable(t *testing.T) {
	dir := newProject(t, `{"work":{"dispatch":"herdr"}}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	f := &fakeHost{name: "herdr", inSession: false}
	_, err := envWith(f).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "t"), Task: "T1"})
	if exitOf(err) != exitcode.ExitUnavailable || !strings.Contains(err.Error(), "must run from inside a herdr pane") {
		t.Errorf("err = %v", err)
	}
	if len(f.calls) != 0 {
		t.Errorf("a herdr command ran outside a pane: %v", f.calls)
	}
}

func TestDispatchHostMissingIsUnavailable(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	f := tmuxFake()
	f.requireErr = fmt.Errorf("tmux is not installed")
	_, err := envWith(f).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "t"), Task: "T1"})
	if exitOf(err) != exitcode.ExitUnavailable {
		t.Errorf("err = %v", err)
	}
}

func TestDispatchResolutionFailures(t *testing.T) {
	dir := newProject(t, `{}`)
	f := tmuxFake()
	e := envWith(f)
	brief := writeBrief(t, "t")
	check := func(what string, err error, msg string) {
		t.Helper()
		if exitOf(err) != exitcode.ExitResolution || !strings.Contains(err.Error(), msg) {
			t.Errorf("%s: err = %v", what, err)
		}
	}
	_, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: "/nonexistent/brief", Task: "T"})
	check("missing body", err, "body file not found")
	_, err = e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: brief, Task: "T"})
	check("no pool", err, "no worker pool")
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	_, err = e.Dispatch(bg, dir, DispatchOpts{Slot: "w9", BodyFile: brief, Task: "T"})
	check("unknown slot", err, "slot 'w9' is not in the pool")
	os.RemoveAll(filepath.Join(dir, ".worktrees", "w1"))
	_, err = e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: brief, Task: "T"})
	check("missing worktree", err, "worktree missing")
	if len(f.calls) != 0 {
		t.Errorf("host touched before resolution succeeded: %v", f.calls)
	}
}

func TestDispatchRejectsResumeAndUnparseableWorkerCommands(t *testing.T) {
	for cfg, tc := range map[string]struct {
		exit int
		msg  string
	}{
		`{"work":{"workerCommand":"claude -c"}}`:         {exitcode.ExitRefused, "contains '-c', which reopens the previous conversation"},
		`{"work":{"workerCommand":"claude --resume=x"}}`: {exitcode.ExitRefused, "contains '--resume=x'"},
		`{"work":{"workerCommand":"claude \"oops"}}`:     {exitcode.ExitUsage, "cannot be parsed (unbalanced quote?)"},
	} {
		dir := newProject(t, cfg)
		goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
		f := tmuxFake()
		_, err := envWith(f).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "t"), Task: "T1"})
		if exitOf(err) != tc.exit || !strings.Contains(err.Error(), tc.msg) {
			t.Errorf("%s: err = %v", cfg, err)
		}
		if len(f.calls) != 0 {
			t.Errorf("%s: the old session was touched: %v", cfg, f.calls)
		}
	}
	// a relay does not launch anything, so the command is not judged
	dir := newProject(t, `{"work":{"workerCommand":"claude -c"}}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	f := tmuxFake()
	e := envWith(f)
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "q"), Relay: true}); err != nil {
		t.Errorf("relay with a resume workerCommand: %v", err)
	}
}

// #38: a slot that still holds work is refused BEFORE its session is killed.
func TestDispatchRefusesASlotHoldingWorkBeforeKilling(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	os.WriteFile(filepath.Join(dir, ".worktrees", "w1", "wip.txt"), []byte("x"), 0o644)
	f := tmuxFake()
	_, err := envWith(f).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "t"), Task: "T2"})
	if exitOf(err) != exitcode.ExitRefused || !strings.Contains(err.Error(), "REFUSED w1 — uncommitted changes") {
		t.Fatalf("err = %v", err)
	}
	if len(f.calls) != 0 {
		t.Errorf("the session was touched: %v", f.calls)
	}
	if got := slotField(t, dir, "w1", "state"); got != "idle" {
		t.Errorf("state = %s", got)
	}
}

// #38: the old session must be provably gone before a new one is spawned.
func TestDispatchDoesNotSpawnBesideASessionThatWillNotClose(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	f := tmuxFake()
	f.killErr = fmt.Errorf("slot 'w1' previous session is still running (window rota:w1); not spawning a second one")
	_, err := envWith(f).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "t"), Task: "T1"})
	if exitOf(err) != exitcode.ExitUnavailable || !strings.Contains(err.Error(), "not spawning a second one") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(strings.Join(f.calls, ","), "spawn") {
		t.Errorf("spawned a second session: %v", f.calls)
	}
	if got := slotField(t, dir, "w1", "handle"); got != "rota:w1" {
		t.Errorf("handle = %s; the old session is still there, so its handle stays", got)
	}
}

func TestDispatchSpawnFailureClearsTheDeadHandle(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	f := tmuxFake()
	f.spawnErr = fmt.Errorf("slot 'w1' session did not come up within 60s")
	_, err := envWith(f).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "t"), Task: "T1"})
	if exitOf(err) != exitcode.ExitUnavailable {
		t.Fatalf("err = %v", err)
	}
	if h, s := slotField(t, dir, "w1", "handle"), slotField(t, dir, "w1", "state"); h != "<null>" || s != "idle" {
		t.Errorf("handle=%s state=%s; the old session is dead, a poll must not chase it", h, s)
	}
}

func TestDispatchSameTaskRetryKeepsWork(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	f := tmuxFake()
	e := envWith(f)
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "t"), Task: "T1"}); err != nil {
		t.Fatal(err)
	}
	wip := filepath.Join(dir, ".worktrees", "w1", "wip.txt")
	os.WriteFile(wip, []byte("x"), 0o644)
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "t"), Task: "T1"}); err != nil {
		t.Fatalf("re-dispatching the task the slot holds must keep its WIP: %v", err)
	}
	if _, err := os.Stat(wip); err != nil {
		t.Error("WIP lost")
	}
}

func TestRelaySummaryAndSplitLines(t *testing.T) {
	if got := relaySummary("--- ORCHESTRATOR (round 1) ---\n\n  hello  \nx"); got != "hello" {
		t.Errorf("%q", got)
	}
	long := strings.Repeat("é", 300)
	if got := relaySummary(long); len([]rune(got)) != 200 {
		t.Errorf("summary has %d chars", len([]rune(got)))
	}
	if got := splitLines("a\r\nb\rc\x0bd\u2028e\n"); strings.Join(got, "|") != "a|b|c|d|e" {
		t.Errorf("%q", got)
	}
}

// ── real host adapters, fake binaries ───────────────────────────────────────

// fakeBinRunner runs test/fakes/herdr and test/fakes/tmux by absolute path
// with their state dirs, and ps for the pid checks. It refuses anything else,
// so even a bug here cannot reach a real herdr or tmux.
func fakeBinRunner(t *testing.T, herdrDir, tmuxDir string) host.Runner {
	t.Helper()
	fakes, err := filepath.Abs("../../test/fakes")
	if err != nil {
		t.Fatal(err)
	}
	return func(ctx context.Context, name string, args []string) (host.Result, error) {
		var cmd *exec.Cmd
		switch name {
		case "herdr", "tmux":
			cmd = exec.CommandContext(ctx, filepath.Join(fakes, name), args...)
			cmd.Env = append(os.Environ(), "FAKE_HERDR="+herdrDir, "FAKE_TMUX="+tmuxDir)
		case "ps":
			cmd = exec.CommandContext(ctx, "ps", args...)
		default:
			t.Fatalf("fakeBinRunner: refusing to run %q", name)
		}
		var out, errb strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &errb
		err := cmd.Run()
		r := host.Result{Stdout: out.String(), Stderr: errb.String()}
		if ee, ok := err.(*exec.ExitError); ok {
			r.ExitCode = ee.ExitCode()
			return r, nil
		}
		return r, err
	}
}

func hostDeps(run host.Runner, env map[string]string) host.Deps {
	return host.Deps{
		Run: run, Getenv: func(k string) string { return env[k] }, Sleep: func(time.Duration) {},
		LookPath: func(n string) (string, error) { return "/fake/" + n, nil }, KillWait: 2,
	}
}

// Contract: an exit-4 refusal carries {blockedBy, changed}. The resume-flag
// refusal changes nothing; a slot holding work is refused before the kill.
func TestDispatchRefusalsCarryFailureData(t *testing.T) {
	data := func(err error) BlockData {
		we, ok := err.(*exitcode.Error)
		if !ok {
			t.Fatalf("err = %v", err)
		}
		bd, _ := we.Data.(BlockData)
		return bd
	}
	dir := newProject(t, `{"work":{"workerCommand":"claude -c"}}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	_, err := envWith(tmuxFake()).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "t"), Task: "T1"})
	if bd := data(err); bd != (BlockData{BlockedBy: "resume flag"}) {
		t.Errorf("resume flag: %+v", bd)
	}
	dir = newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	os.WriteFile(filepath.Join(dir, ".worktrees", "w1", "wip.txt"), []byte("x"), 0o644)
	_, err = envWith(tmuxFake()).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "t"), Task: "T2"})
	if bd := data(err); bd != (BlockData{BlockedBy: "reset guard"}) {
		t.Errorf("slot holds work: %+v", bd)
	}
}

// resubHost is a fakeHost that can submit a brief an earlier send left unsent.
type resubHost struct {
	*fakeHost
	pending bool
}

func (r *resubHost) SubmitPending(_ context.Context, slot, _, _ string) (bool, error) {
	r.calls = append(r.calls, "submit-pending "+slot)
	if !r.pending {
		return false, nil
	}
	r.pending = false
	return true, nil
}

// A stalled send marks the slot; the relay that resends submits what is on the
// prompt line instead of typing the brief again (#89).
func TestDispatchRelayResendSubmitsUnsentBrief(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	f := &resubHost{fakeHost: tmuxFake(), pending: true}
	e := envWith(f)
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "t"), Task: "T1"}); err != nil {
		t.Fatal(err)
	}
	if slotField(t, dir, "w1", "unsent") != "<null>" {
		t.Error("a clean send must not mark the slot")
	}
	f.sendErr = host.ErrNotSubmitted
	relay := DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "q"), Relay: true}
	if _, err := e.Dispatch(bg, dir, relay); exitOf(err) != exitcode.ExitRetry {
		t.Fatalf("stalled relay err = %v", err)
	}
	if slotField(t, dir, "w1", "unsent") != "true" {
		t.Fatal("a stalled send must mark the slot unsent")
	}
	f.calls, f.sendErr = nil, nil
	if _, err := e.Dispatch(bg, dir, relay); err != nil {
		t.Fatalf("resend: %v", err)
	}
	if strings.Join(f.calls, ",") != "submit-pending w1" {
		t.Errorf("resend calls = %v, want only submit-pending (no second typing)", f.calls)
	}
	if slotField(t, dir, "w1", "unsent") != "<null>" {
		t.Error("a submitted brief must clear the mark")
	}
	// Nothing pending: the relay sends as usual.
	f.calls = nil
	f.sendErr = host.ErrNotSubmitted
	e.Dispatch(bg, dir, relay)
	f.sendErr, f.calls = nil, nil
	if _, err := e.Dispatch(bg, dir, relay); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.calls, ",") != "submit-pending w1,send w1 w9:t7" {
		t.Errorf("calls = %v", f.calls)
	}
}

// #207: a task dispatch closes the shell pane an exited worker left behind
// before it kills the recorded tab, wherever a layout split moved that pane.
func TestDispatchSweepsLeftoverShellPanesBeforeKill(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	f := &sweepHost{tmuxFake()}
	if _, err := envWith(f).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "t"), Task: "T1"}); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(f.calls, ",")
	if i, j := strings.Index(got, "sweep w1"), strings.Index(got, "kill w1"); i < 0 || j < i {
		t.Errorf("want sweep before kill: %s", got)
	}
}
