package worker

import (
	"context"
	"errors"
	"github.com/l4ci/rota/internal/pidlive"
	"github.com/l4ci/rota/internal/roundlease"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/cfgdir"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/proc"
	"github.com/l4ci/rota/internal/tracker"
)

// Remote is the git remote the worker verbs fetch from and push to (git.remote,
// origin when unset), read from root's config.
func (e Env) Remote(root string) (string, error) {
	return config.GitRemote(e.WithDefaults().Config(root))
}

// git runs git and trims one trailing newline from stdout, like $(...).
func (e Env) git(dir string, args ...string) (string, int) {
	return e.runGit(e.Ctx, dir, args...)
}

// runGit is the one git adapter of package worker: git on ctx, a failure to
// start as exit 127, stdout trimmed of one trailing newline.
func (e Env) runGit(ctx context.Context, dir string, args ...string) (string, int) {
	out, code, _ := e.runGitErr(ctx, dir, args...)
	return out, code
}

// runGitErr is runGit with the typed start failure kept: err is git.ErrNoGit
// or the context's error when git did not run (code is then 127), nil when it
// ran, whatever its exit status. Callers that must tell "no git" or a
// cancelled run from a git exit status use this one.
func (e Env) runGitErr(ctx context.Context, dir string, args ...string) (string, int, error) {
	res, err := e.Git(ctx, dir, args...)
	if err != nil {
		return "", 127, err
	}
	return strings.TrimRight(res.Stdout, "\n"), res.ExitCode, nil
}

// Env is what the worker operations touch outside their own memory. Every
// field has a production default; tests replace them, and NewHost above all:
// this round runs inside herdr and tmux, so no test may reach the real ones.
type Env struct {
	// Ctx bounds every git call and is cancelled on SIGINT/SIGTERM by the
	// CLI. Nil means context.Background().
	Ctx context.Context
	Git git.Runner
	// NewHost returns the host for a kind; nil means host.New with the real runners.
	NewHost func(kind host.Kind) host.Host
	// Sleep defaults to time.Sleep (poll's settle).
	Sleep func(time.Duration)
	// Stderr defaults to os.Stderr (round wait's one-line fallback notice).
	Stderr io.Writer
	// Now defaults to time.Now (relay timestamps, ledger rows).
	Now func() time.Time
	// Home defaults to os.UserHomeDir (the ~/ of an account's configDir).
	Home func() (string, error)
	// Getwd defaults to os.Getwd (the directory a handoff hint names).
	Getwd func() (string, error)
	// Lease reads the round lease the ledger stamps its rows with; nil means
	// roundlease.DefaultEnv.
	Lease func() roundlease.Env
	// Getenv defaults to os.Getenv (ROTA_GATE_SHA_WAIT).
	Getenv func(string) string
	// Procs reads the process table the gate and landing markers are checked
	// against; nil means this machine's.
	Procs pidlive.Source
	// Forge returns the forge a provider ("github" or "gitlab") runs in dir.
	// Every forge call goes through internal/tracker, never an exec of gh or
	// glab from here. Tests inject a fake Forge.
	Forge func(provider, dir string, s tracker.Settings) (Forge, error)
	// Issues returns the issue tracker issues.source names when it is not the
	// forge's own (jira, linear, or github on a gitlab forge). Nil builds it with
	// tracker.NewIssueTracker (no tracker is nil, nil); tests inject a fake.
	Issues func(dir string, s tracker.Settings) (Issues, error)
	// IssueComments returns the issue tracker issues.source names, for the
	// issue bounce store (round.support set) and writer trust. Nil builds it
	// with tracker.NewIssueTracker when needed; tests inject a fake.
	IssueComments func(dir string, s tracker.Settings) (IssueComments, error)
	// Shell runs one verification command through `sh -c` in dir and returns
	// its combined output and exit code.
	Shell func(ctx context.Context, dir, command string) (output string, code int)
	// ShellEnv is Shell with the whole environment replaced by environ: the
	// behaviour run's steps see nothing of the gate's own environment.
	ShellEnv func(ctx context.Context, dir, command string, environ []string) (output string, code int)
	// Run runs one tool with extra environment (CODEX_HOME) and returns what
	// it left behind; a command that ran and failed is a Result with ExitCode
	// set, an error means it could not run at all. Every codex and herdr call
	// of the codex preflight goes through it, so tests need no real binary.
	Run func(ctx context.Context, name string, args, env []string) (host.Result, error)
	// Executable is the running rota binary, which a codex worker's prompt-check
	// hook calls back; nil means os.Executable.
	Executable func() (string, error)
	// LookPath reports whether a binary is installed; nil means exec.LookPath.
	LookPath func(string) (string, error)
	// Config returns the merged project config every worker verb reads (dispatch,
	// pool init, preflight, account meters, the operator command, the gate); nil
	// loads .rota/config.json. It is the one disk read of config in this package:
	// tests hand it an in-memory config. Harness adapters receive a typed
	// harness.ConfigFor view at their call boundary.
	Config func(root string) any
	// Accounts reads the usage meters; poll and wait use it to record the
	// account's headroom when a pane reports done. Nil records none.
	Accounts *Accounts
	// GateInput loads what the gate and the train verify with: config, verify
	// and e2e commands, test ledger. Defaults to reading root's .rota.
	GateInput func(root string) (gateInput, error)
	// Registry reads the slot registry for the passive readers (the watch
	// snapshot, the digest, the stop hook); nil means LoadRegistryTolerant.
	// Tests hand it an in-memory registry (ParseRegistry).
	Registry func(root string) Registry
}

// defaultConfig is the Config of an Env that sets none: no config, the schema
// defaults. cli's Deps fills Env.Config; worker tests swap in a disk reader.
var defaultConfig = func(string) any { return nil }

// procs is the process table: the injected one, else this machine's.
func (e Env) procs() pidlive.Source {
	if e.Procs != nil {
		return e.Procs
	}
	return pidlive.Real{}
}

// WithDefaults is the one defaulting mechanism of Env: every nil seam gets its
// production value. Each verb entry calls it once and then reads the fields
// directly; code outside the package calls it to read the clock or environment.
// Idempotent, so a defaulted Env passes through unchanged.
func (e Env) WithDefaults() Env {
	if e.Ctx == nil {
		e.Ctx = context.Background()
	}
	if e.Git == nil {
		e.Git = git.Exec
	}
	if e.NewHost == nil {
		e.NewHost = func(kind host.Kind) host.Host { return host.New(kind, host.Deps{}) }
	}
	if e.Sleep == nil {
		e.Sleep = time.Sleep
	}
	if e.Stderr == nil {
		e.Stderr = os.Stderr
	}
	if e.Now == nil {
		e.Now = time.Now
	}
	if e.Home == nil {
		e.Home = os.UserHomeDir
	}
	if e.Getwd == nil {
		e.Getwd = os.Getwd
	}
	if e.Getenv == nil {
		e.Getenv = os.Getenv
	}
	e.Procs = e.procs()
	if e.Lease == nil {
		e.Lease = roundlease.DefaultEnv
	}
	if e.Forge == nil {
		e.Forge = func(provider, dir string, s tracker.Settings) (Forge, error) {
			return tracker.NewForge(e.Ctx, s, provider, dir, machineOption())
		}
	}
	if e.Config == nil {
		e.Config = defaultConfig
	}
	if e.Issues == nil {
		e.Issues = func(dir string, s tracker.Settings) (Issues, error) {
			it, err := tracker.NewIssueTracker(e.Ctx, s, "", dir, machineOption())
			if errors.Is(err, tracker.ErrNoIssueTracker) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			return it, nil
		}
	}
	if e.Shell == nil {
		e.Shell = execShell
	}
	if e.ShellEnv == nil {
		e.ShellEnv = execShellEnv
	}
	if e.Run == nil {
		e.Run = execRun
	}
	if e.Executable == nil {
		e.Executable = os.Executable
	}
	if e.LookPath == nil {
		e.LookPath = exec.LookPath
	}
	if e.GateInput == nil {
		e.GateInput = e.loadGateInput
	}
	if e.Registry == nil {
		e.Registry = LoadRegistryTolerant
	}
	return e
}

// machineOption points a default-built tracker at this machine's connections,
// as cli's trackerBase does for the production Env.
func machineOption() tracker.Option {
	return tracker.WithMachine(cfgdir.OSEnv(), func() connections.Store {
		return connections.OSKeychain(proc.Run, runtime.GOOS)
	})
}

// execRun is the production Env.Run. It bounds the call like the host runner.
func execRun(ctx context.Context, name string, args, env []string) (host.Result, error) {
	return proc.Run(ctx, proc.Cmd{Name: name, Args: args, Env: env, Timeout: host.CallTimeout})
}

// killTree is the cancel of a verification command: its process group and
// every descendant, so a test server in its own session does not outlive a
// cancelled or timed-out gate or train (#2751).
func killTree(pid int) error { return pidlive.KillTree(pidlive.Real{}, pid) }

// execShell is the production Env.Shell. A verification command may run as
// long as the caller's context allows, so it sets no timeout of its own.
func execShell(ctx context.Context, dir, command string) (string, int) {
	res, err := proc.Run(ctx, proc.Cmd{
		Name: "sh", Args: []string{"-c", command}, Dir: dir,
		Timeout: proc.NoTimeout, Combined: true, Group: true, Kill: killTree,
	})
	if err != nil {
		return err.Error(), 127
	}
	return res.Stdout, res.ExitCode
}

// execShellEnv is the production Env.ShellEnv: execShell under exactly
// environ. An empty environ means an empty environment, not the caller's. The
// output is capped at behaviourMaxOutput bytes; a capped run ends in
// outputTruncatedMarker.
func execShellEnv(ctx context.Context, dir, command string, environ []string) (string, int) {
	if environ == nil {
		environ = []string{}
	}
	res, err := proc.Run(ctx, proc.Cmd{
		Name: "sh", Args: []string{"-c", command}, Dir: dir, Environ: environ,
		Timeout: proc.NoTimeout, Combined: true, Group: true, Kill: killTree, MaxOutput: behaviourMaxOutput,
	})
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		// keep what the step wrote before it was stopped: it shows where it hung
		out := res.Stdout
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		return out + "[timed out: " + err.Error() + "]\n", 124
	}
	if err != nil {
		return err.Error(), 127
	}
	if res.Truncated {
		return strings.TrimSuffix(res.Stdout, "\n") + "\n" + outputTruncatedMarker + "\n", res.ExitCode
	}
	return res.Stdout, res.ExitCode
}

// behaviourMaxOutput caps the output a Try it step keeps.
const behaviourMaxOutput = 4 << 20

// outputTruncatedMarker is the last line of a step's output that hit the cap.
const outputTruncatedMarker = "[output truncated at 4 MiB]"
