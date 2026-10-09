package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/orchestrate"
	"github.com/l4ci/rota/internal/palette"
	"github.com/l4ci/rota/internal/worker"
)

// launchRig replaces the launcher's outside world: no herdr, tmux or agent is
// reached, and Exec records instead of replacing the test process.
type launchRig struct {
	env       map[string]string
	installed []string
	runs      []string
	execs     [][]string
	panes     string // `herdr pane list` reply; empty means the call is not faked
}

func useLaunchRig(d *Deps, env map[string]string, installed ...string) *launchRig {
	r := &launchRig{env: env, installed: installed}
	d.OrchestrateEnv = func() orchestrate.Env {
		look := func(n string) (string, error) {
			for _, i := range r.installed {
				if i == n {
					return "/fake/" + n, nil
				}
			}
			return "", errors.New("not found")
		}
		run := func(_ context.Context, name string, args []string) (host.Result, error) {
			r.runs = append(r.runs, name+" "+strings.Join(args, " "))
			if name == "herdr" && r.panes != "" {
				switch strings.Join(args, " ") {
				case "pane list":
					return host.Result{Stdout: r.panes}, nil
				case "tab list":
					return host.Result{Stdout: `{"result":{"tabs":[]}}`}, nil
				case "workspace list":
					return host.Result{Stdout: `{"result":{"workspaces":[]}}`}, nil
				}
			}
			if name == "herdr" && args[0] == "tab" {
				return host.Result{Stdout: `{"result":{"tab":{"tab_id":"w1:t2"},"root_pane":{"pane_id":"w1:p2"}}}`}, nil
			}
			return host.Result{}, nil
		}
		get := func(k string) string { return r.env[k] }
		return orchestrate.Env{Getenv: get, LookPath: look, Self: "/bin/rota",
			Host: func(kind string) host.Host { return host.New(kind, host.Deps{Run: run, Getenv: get, LookPath: look}) },
			Exec: func(path string, argv, _ []string) error {
				r.execs = append(r.execs, append([]string{path}, argv...))
				return nil
			}}
	}
	return r
}

func passingDoctor(t *testing.T, d *Deps) {
	t.Helper()
	d.DoctorPath = doctorFakes(t, map[string]string{"git": `case "$1" in remote) exit 2;; esac; exit 0`})
}

func TestOrchestrateDryRunPlansWithoutStarting(t *testing.T) {
	deps := testDeps()
	passingDoctor(t, deps)
	r := useLaunchRig(deps, nil)
	dir := trackerProject(t, "")
	code, env, errs := rotaRunWith(t, deps, "--json", "-C", dir, "orchestrate", "--dry-run")
	if code != 0 {
		t.Fatalf("exit %d: %v %s", code, env, errs)
	}
	d := umbData(env)
	if d["harness"] != "claude" || d["host"] != "solo" || d["mode"] != "inplace" || d["changed"] != false || d["dryRun"] != true {
		t.Errorf("data = %v", d)
	}
	if len(r.runs)+len(r.execs) != 0 {
		t.Errorf("dry run must start nothing: %v %v", r.runs, r.execs)
	}
}

func TestOrchestrateOpensATabInsideHerdr(t *testing.T) {
	deps := testDeps()
	passingDoctor(t, deps)
	r := useLaunchRig(deps, map[string]string{"HERDR_ENV": "1", "HERDR_WORKSPACE_ID": "w1"}, "herdr")
	dir := trackerProject(t, "")
	code, env, errs := rotaRunWith(t, deps, "--json", "-C", dir, "orchestrate")
	if code != 0 {
		t.Fatalf("exit %d: %v %s", code, env, errs)
	}
	if d := umbData(env); d["tab"] != "w1:t2" || d["changed"] != true || d["host"] != "herdr" {
		t.Errorf("data = %v", d)
	}
	if got := strings.Join(r.runs, "\n"); !strings.Contains(got, "herdr pane run w1:p2 /bin/rota keepalive run --first-prompt /rota-orchestrate -- claude") {
		t.Errorf("runs:\n%s", got)
	}
}

func TestOrchestrateRecordsItsPlainPaneAsTheCLIPane(t *testing.T) {
	for _, tc := range []struct{ name, pane, agent, want string }{
		{"plain pane", "w1:p1", "", "w1:p1"},
		{"agent pane", "w1:p1", "claude", ""},
		{"not in herdr", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps := testDeps()
			passingDoctor(t, deps)
			r := useLaunchRig(deps, map[string]string{"HERDR_ENV": "1", "HERDR_WORKSPACE_ID": "w1", "HERDR_PANE_ID": tc.pane}, "herdr")
			r.panes = `{"result":{"panes":[{"pane_id":"w1:p1","tab_id":"w1:t1","workspace_id":"w1","agent":"` + tc.agent + `"}]}}`
			dir := trackerProject(t, "")
			if code, env, errs := rotaRunWith(t, deps, "--json", "-C", dir, "orchestrate"); code != 0 {
				t.Fatalf("exit %d: %v %s", code, env, errs)
			}
			if got := worker.LoadRegistry(dir).CLIPane(); got != tc.want {
				t.Errorf("cliPane = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOrchestrateHarnessComesFromConfig(t *testing.T) {
	deps := testDeps()
	passingDoctor(t, deps)
	useLaunchRig(deps, nil)
	dir := trackerProject(t, `{"orchestrator":{"harness":"codex"}}`)
	code, env, _ := rotaRunWith(t, deps, "--json", "-C", dir, "orchestrate", "--dry-run")
	cmd, _ := umbData(env)["command"].([]any)
	if code != 0 || len(cmd) < 3 || cmd[len(cmd)-1] != "codex" || cmd[4] != "$rota-orchestrate" {
		t.Errorf("exit %d, command %v", code, cmd)
	}
	dir = trackerProject(t, `{"orchestrator":{"harness":"emacs"}}`)
	if code, _, errs := rotaRunWith(t, deps, "-C", dir, "orchestrate", "--dry-run"); code != ExitInternal || !strings.Contains(errs, "claude, codex") {
		t.Errorf("unknown harness: exit %d %s", code, errs)
	}
}

func TestOrchestrateStopsOnADoctorFailureBeforeAnySession(t *testing.T) {
	deps := testDeps()
	deps.DoctorPath = doctorFakes(t, map[string]string{"herdr": `echo "herdr 0.8.2"`}) // too old; no git on PATH
	r := useLaunchRig(deps, map[string]string{"HERDR_ENV": "1", "HERDR_WORKSPACE_ID": "w1"}, "herdr")
	dir := trackerProject(t, `{"work":{"dispatch":"herdr"}}`)
	code, out, errs := rotaInWith(t, deps, dir, "orchestrate")
	if code != ExitFailed || !strings.Contains(errs, "no session started") || !strings.Contains(out, "fail\thost") {
		t.Fatalf("exit %d: %s | %s", code, out, errs)
	}
	if len(r.runs)+len(r.execs) != 0 {
		t.Errorf("nothing may start: %v %v", r.runs, r.execs)
	}
}

func TestOrchestrateOutsideAMultiplexerAttachesARotaHerdrSession(t *testing.T) {
	deps := testDeps()
	deps.DoctorPath = doctorFakes(t, map[string]string{"git": `case "$1" in remote) exit 2;; esac; exit 0`, "herdr": `echo "herdr 0.9.3"`})
	r := useLaunchRig(deps, nil, "herdr", "tmux")
	dir := trackerProject(t, `{"work":{"dispatch":"herdr"}}`)
	// The fake herdr answers `status server`, so the session counts as running
	// and only the attach replaces this process.
	if code, out, errs := rotaInWith(t, deps, dir, "orchestrate"); code != 0 {
		t.Fatalf("exit %d: %s | %s", code, out, errs)
	}
	if len(r.execs) != 1 || r.execs[0][1] != "herdr" || r.execs[0][2] != "session" || r.execs[0][3] != "attach" {
		t.Errorf("execs = %q", r.execs)
	}
}

// bareRig fakes a terminal and the setup verb for bare `rota`.
func bareRig(d *Deps) *int {
	d.IsTerminal = func(any) bool { return true }
	d.Palette = palette.RunDefault // Enter on the preselected entry
	setups := 0
	d.BareSetup = func(*Ctx, []string) (Result, error) { setups++; return Result{Text: "setup ran"}, nil }
	return &setups
}

func bareIn(t *testing.T, d *Deps, dir string, args ...string) (int, string, string) {
	t.Helper()
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := mainWith(d, args, strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

func TestBareRotaWithoutRotaDirRunsSetup(t *testing.T) {
	deps := testDeps()
	setups := bareRig(deps)
	r := useLaunchRig(deps, nil)
	code, out, _ := bareIn(t, deps, t.TempDir())
	if code != 0 || *setups != 1 || !strings.Contains(out, "setup ran") {
		t.Errorf("exit %d, setups %d, out %q", code, *setups, out)
	}
	if len(r.execs) != 0 {
		t.Errorf("no orchestrator without a project: %v", r.execs)
	}
}

func TestBareRotaInAnInitializedProjectLaunchesTheOrchestrator(t *testing.T) {
	deps := testDeps()
	setups := bareRig(deps)
	passingDoctor(t, deps)
	r := useLaunchRig(deps, nil)
	code, _, errs := bareIn(t, deps, trackerProject(t, ""))
	if code != 0 || *setups != 0 {
		t.Fatalf("exit %d, setups %d: %s", code, *setups, errs)
	}
	if len(r.execs) != 1 || r.execs[0][2] != "keepalive" {
		t.Errorf("execs = %q", r.execs)
	}
}

func TestBareRotaNeverLaunchesWithoutATerminalOrWithJSON(t *testing.T) {
	deps := testDeps()
	setups := bareRig(deps)
	deps.IsTerminal = func(any) bool { return false }
	r := useLaunchRig(deps, nil)
	dir := trackerProject(t, "")
	if code, _, errs := bareIn(t, deps, dir); code != ExitUsage || !strings.Contains(errs, "missing command") {
		t.Errorf("pipe: exit %d %s", code, errs)
	}
	deps.IsTerminal = func(any) bool { return true }
	if code, _, errs := bareIn(t, deps, dir, "--json"); code != ExitUsage || !strings.Contains(errs, "missing command") {
		t.Errorf("--json: exit %d %s", code, errs)
	}
	if code, _, errs := bareIn(t, deps, dir, "bogus"); code != ExitUsage || !strings.Contains(errs, `unknown command "bogus"`) {
		t.Errorf("bogus: exit %d %s", code, errs)
	}
	if *setups != 0 || len(r.execs) != 0 {
		t.Errorf("setups %d, execs %v", *setups, r.execs)
	}
}

func TestBareRotaTakesGlobalFlagsBeforeNothing(t *testing.T) {
	deps := testDeps()
	bareRig(deps)
	passingDoctor(t, deps)
	r := useLaunchRig(deps, nil)
	if code, _, errs := bareIn(t, deps, t.TempDir(), "-C", trackerProject(t, "")); code != 0 || len(r.execs) != 1 {
		t.Errorf("exit %d execs %v: %s", code, r.execs, errs)
	}
}
