package cli

import (
	"context"
	"errors"
	"github.com/l4ci/rota/internal/issues"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/doctor"
	"github.com/l4ci/rota/internal/escalation"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/migrate"
	"github.com/l4ci/rota/internal/orchestrate"
	"github.com/l4ci/rota/internal/palette"
	"github.com/l4ci/rota/internal/proc"
	"github.com/l4ci/rota/internal/projects"
	"github.com/l4ci/rota/internal/reap"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/stale"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/tui"
	"github.com/l4ci/rota/internal/update"
	"github.com/l4ci/rota/internal/version"
	"github.com/l4ci/rota/internal/worker"
)

// Deps is everything a verb reaches outside its own memory: forges, hosts,
// the terminal, the clock of the meters. Ctx carries one, so a test builds
// its own with defaultDeps and swaps a field instead of writing a package
// variable. Fields read each other through the Deps, so a swapped
// TrackerOptions reaches the round environment too.
type Deps struct {
	// Git runs git for every verb that reads a repository; Proc runs any
	// other binary. A test swaps in a fake instead of reaching the machine.
	Git  git.Runner
	Proc proc.Runner

	// Getwd and UserHomeDir are the one working-directory and home reads;
	// Toplevel asks Git from the working directory. A test swaps them to run
	// in a temp dir with a fake git.
	Getwd       func() (string, error)
	UserHomeDir func() (string, error)

	// Config returns the merged project config of root; the worker accounts
	// read it through here. A test swaps in an in-memory config.
	Config func(root string) any

	// Now is the one clock. Today is the day date-stamped output and age
	// checks use; it is Now unless a test pins the day alone. HolderPID names
	// the process that holds a round lease, 0 meaning find it from the process
	// table. defaultDeps reads the ROTA_TEST_* overrides once into these three;
	// ClockErr is a malformed override, raised by the verbs that read Today.
	Now       func() time.Time
	Today     func() time.Time
	HolderPID func() int
	ClockErr  error

	// Getenv is the one environment reader; the clock overrides, the worker
	// env, the accounts and every cli env read go through it. A test swaps it.
	// defaultDeps also reads the remaining ROTA_TEST_* hooks once, at this
	// edge, into PollFixture, PollStatus, DoctorPath and DoctorDisk.
	Getenv func(string) string
	// Config is the one read of the project config the domain envs (worker,
	// escalation, knowledge, gate) take: the merged .rota/config.json by
	// default, a fixed value in a test.
	Config  func(root string) any
	Environ func() []string
	// RecordMistakes makes an internal error or panic write a record to the
	// bug log (#1175). A test leaves it off so a failing fixture does not write
	// into the enclosing checkout's .rota/.
	RecordMistakes bool
	// LookPath is the one PATH lookup the host decision reads, beside Getenv.
	LookPath func(string) (string, error)

	// PollFixture, when set, makes `worker poll` classify that file as a static
	// pane (no host, no registry writes); PollStatus is the agent status it
	// reports. DoctorPath replaces PATH for doctor's tool lookup when set.
	// DoctorDisk reads the free space of the volume holding a directory.
	PollFixture string
	// PollKind is the harness kind the fixture is read as (ROTA_TEST_POLL_KIND).
	PollKind   string
	PollStatus string
	DoctorPath string
	DoctorDisk func(dir string) *doctor.Disk

	// TrackerOptions apply to every forge CLI the verbs build; a test swaps
	// in a fake executor here.
	TrackerOptions []tracker.Option
	// IssuesForge, when set, stands in for the forge `rota issues` verbs build;
	// a test uses it for a key-based tracker.
	IssuesForge func(ctx context.Context, provider, dir string) (issues.Forge, error)

	// ReadCache is shared by every forge adapter this invocation builds, so
	// the same read is made once. Nil turns it off.
	ReadCache *tracker.ReadCache

	NewTracker     func(ctx context.Context, root string, cfg any) (backlog.Tracker, error)
	MigrateTracker func(ctx context.Context, root string, cfg any) (migrate.Tracker, error)
	MigrateSleep   func(d time.Duration)

	RoundEnv func(ctx context.Context, root string) round.Env
	// RoundEnvLocal is RoundEnv without a forge, for a caller that reads only
	// the host and worktree sources and must not build one.
	RoundEnvLocal func(ctx context.Context, root string) round.Env
	ReapEnv       func(ctx context.Context, root string) (round.Env, reap.HostOps)
	EscalationEnv func() escalation.Env
	// LeaseEnv is the one route to the round lease's process and clock reads;
	// the real machine by default, a fake in tests.
	LeaseEnv       func() roundlease.Env
	OrchestrateEnv func() orchestrate.Env

	WorkerEnv      func() worker.Env
	WorkerAccounts func() *worker.Accounts

	// Host is the one place a verb gets a pane host: it builds the host for a
	// kind ("herdr", "tmux"). The env hooks above leave their host fields nil
	// and are filled from it, so a test swaps this one field to fake every host.
	Host func(kind host.Kind) host.Host

	UpdateEnv        func() update.Env
	InstalledVersion func() string
	SeedBase         func(root string) error
	SetupIsTTY       func(in io.Reader) bool
	IsTerminal       func(f any) bool
	BareSetup        RunFunc
	// Keychain is the OS keychain `rota connect` stores secrets in; nil means
	// none on this machine. ConnTesters make the authenticated read of a
	// connection kind; a kind with none reports unsupported. Tests swap both.
	Keychain    func() connections.Store
	ConnTesters map[string]connections.Tester
	// Palette is the menu bare `rota` opens; a test swaps in a fake terminal.
	Palette func(palette.Config) error
	// RunView runs a --ui screen on the process terminal; a test swaps in a fake.
	RunView func(c *Ctx, m tui.Model) error
}

// defaultDeps is the real machine: git, the forge CLIs, tmux or herdr.
func defaultDeps() *Deps {
	d := &Deps{
		Git:              git.Exec,
		Proc:             proc.Run,
		Getenv:           os.Getenv,
		Config:           func(root string) any { return config.Load(rotatree.Config(root)) },
		Getwd:            os.Getwd,
		UserHomeDir:      os.UserHomeDir,
		LookPath:         exec.LookPath,
		WorkerEnv:        func() worker.Env { return worker.Env{} },
		EscalationEnv:    func() escalation.Env { return escalation.Env{} },
		LeaseEnv:         func() roundlease.Env { return roundlease.DefaultEnv() },
		Host:             func(kind host.Kind) host.Host { return host.New(kind, host.Deps{}) },
		InstalledVersion: installedVersion,
		Environ:          os.Environ,
		SeedBase:         seedProject,
		SetupIsTTY:       defaultSetupIsTTY,
		IsTerminal:       defaultIsTerminal,
		BareSetup:        defaultBareSetup,
		Palette:          palette.RunTerminal,
		RunView:          runView,
		ReadCache:        tracker.NewReadCache(),
		Config:           func(root string) any { return config.Load(rotatree.Config(root)) },
		RecordMistakes:   true,
	}
	d.Keychain = func() connections.Store { return connections.OSKeychain(d.Proc, runtime.GOOS) }
	d.ConnTesters = map[string]connections.Tester{connections.KindJira: tracker.JiraTester{}, connections.KindLinear: tracker.LinearTester{}, connections.KindItsaplan: tracker.ItsaplanTester{}}
	d.Now, d.Today, d.HolderPID, d.ClockErr = envClock(d.Getenv)
	d.PollFixture, d.PollStatus = d.Getenv("ROTA_TEST_POLL_FIXTURE"), d.Getenv("ROTA_TEST_POLL_STATUS")
	d.PollKind = d.Getenv("ROTA_TEST_POLL_KIND")
	d.DoctorPath = d.Getenv("ROTA_TEST_DOCTOR_PATH")
	d.DoctorDisk = doctorDisk(d.Getenv("ROTA_TEST_DOCTOR_DISK"))
	d.UpdateEnv = func() update.Env { return update.DefaultEnv(version.Get().Version, d.Getenv, d.Proc) }
	d.OrchestrateEnv = func() orchestrate.Env { return defaultOrchestrateEnv(d) }
	d.WorkerAccounts = func() *worker.Accounts {
		env := worker.Env{Now: d.Now, Getenv: d.Getenv, Home: d.UserHomeDir, Config: d.Config}
		return &worker.Accounts{Env: env, Login: d.credentials()}
	}
	d.NewTracker = func(ctx context.Context, root string, cfg any) (backlog.Tracker, error) {
		return d.issueTracker(ctx, cfg, root)
	}
	// migrate issues onto a forge runs while issues.source is still file, so the
	// tracker it fills is not named yet: it is the host forge.provider or the
	// origin names (the legacy "issues, provider auto" resolution). It's a Plan
	// is no forge, so it cannot be told from the origin: that project sets
	// issues.source itsaplan (and its connection) first, and the run fills it.
	d.MigrateTracker = func(ctx context.Context, root string, cfg any) (migrate.Tracker, error) {
		s := tracker.SettingsFromConfig(cfg)
		if s.Source == config.SourceItsaplan {
			return tracker.NewIssueTracker(ctx, s, config.SourceItsaplan, root, d.trackerOptions()...)
		}
		return tracker.NewIssueTracker(ctx, s, config.SourceIssues, root, d.trackerOptions()...)
	}
	d.RoundEnv = func(ctx context.Context, root string) round.Env { return defaultRoundEnv(ctx, root, d, true) }
	d.RoundEnvLocal = func(ctx context.Context, root string) round.Env { return defaultRoundEnv(ctx, root, d, false) }
	d.ReapEnv = func(ctx context.Context, root string) (round.Env, reap.HostOps) {
		return defaultReapEnv(ctx, root, d)
	}
	return d
}

// Toplevel is the toplevel of the git work tree around the working directory,
// "" outside one or when git cannot run.
func (d *Deps) Toplevel(ctx context.Context) string {
	dir, err := d.Getwd()
	if err != nil {
		return ""
	}
	res, err := d.Git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil || res.ExitCode != 0 {
		return ""
	}
	return strings.TrimSpace(res.Stdout)
}

// deps is the invocation's dependencies. A Ctx built by hand, without run,
// gets the real ones.
func (c *Ctx) deps() *Deps {
	if c.Deps == nil {
		c.Deps = defaultDeps()
	}
	return c.Deps
}

// Repo is the git handle for dir, running through Deps.Git, so every cli
// repository read follows the one seam a test swaps.
func (d *Deps) Repo(dir string) git.Repo {
	return git.Repo{Dir: dir, Runner: d.Git}
}

// projectsEnv is the environment the global dir lookups read: Deps.Getenv and
// Deps.UserHomeDir, so nothing under internal/projects reads the process.
func (d *Deps) projectsEnv() projects.Env {
	return projects.Env{Getenv: d.Getenv, Home: d.UserHomeDir}
}

// resolveHost is worker.ResolveHost over Deps' own getenv and lookPath, so the
// decision and the handle from Host come from the same injected sources.
func (d *Deps) resolveHost(root string) host.Kind {
	return worker.ResolveHost(root, config.Load(rotatree.Config(root)), d.Getenv, d.LookPath)
}

// resolvePaneHost is worker.ResolvePaneHost over Deps' own getenv and lookPath.
func (d *Deps) resolvePaneHost(root string, cfg any) host.Kind {
	return worker.ResolvePaneHost(root, cfg, d.Getenv, d.LookPath)
}

// credentials is worker.SystemCredentials with the home read off Deps.
func (d *Deps) credentials() worker.Credentials {
	c := worker.SystemCredentials(d.Proc)
	c.Home = d.UserHomeDir
	return c
}

// homeOf reads a home seam; "" when it has none or fails.
func homeOf(f func() (string, error)) string {
	if f == nil {
		return ""
	}
	h, _ := f()
	return h
}

// workerEnv is the WorkerEnv hook with its host filled from Host.
func (d *Deps) workerEnv() worker.Env {
	e := d.WorkerEnv()
	if e.Config == nil {
		e.Config = d.Config
	}
	if e.Git == nil {
		e.Git = d.Git
	}
	if e.NewHost == nil {
		e.NewHost = func(kind host.Kind) host.Host { return d.Host(kind) }
	}
	if e.Now == nil {
		e.Now = d.Now
	}
	if e.Getenv == nil {
		e.Getenv = d.Getenv
	}
	if e.Home == nil {
		e.Home = d.UserHomeDir
	}
	if e.Getwd == nil {
		e.Getwd = d.Getwd
	}
	if e.LookPath == nil {
		e.LookPath = d.LookPath
	}
	if e.Lease == nil {
		e.Lease = d.LeaseEnv
	}
	if e.Accounts == nil && d.WorkerAccounts != nil {
		e.Accounts = d.WorkerAccounts()
	}
	if e.Issues == nil {
		// the gate's issue source gets the TrackerOptions too (a jira or
		// linear test points its connection at a fake site).
		e.Issues = func(dir string, s tracker.Settings) (worker.Issues, error) {
			it, err := tracker.NewIssueTracker(context.Background(), s, "", dir, d.trackerOptions()...)
			if err != nil {
				return nil, err
			}
			return it, nil
		}
	}
	if e.Forge == nil {
		// and so does its forge, but only the executor swap: never the read
		// cache. The gate re-reads the forge in loops that need a fresh answer
		// each time (PRMergeable retries, CommitChecks polls, PRView after a
		// push), and nothing in a gate run calls freshReads.
		ctx := e.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		e.Forge = func(provider, dir string, s tracker.Settings) (worker.Forge, error) {
			return tracker.NewForge(ctx, s, provider, dir, d.trackerBase()...)
		}
	}
	return e
}

// baseRoundEnv is the round.Env every round verb starts from: the one Git, the
// lease and the worker env, all read off Deps so a swapped fake reaches them.
func (d *Deps) baseRoundEnv() round.Env {
	return round.Env{Git: d.Git, Lease: d.LeaseEnv(), Worker: d.workerEnv(), Login: d.credentials()}
}

// escalationEnv is the EscalationEnv hook with its host (always herdr, the
// only one with notifications) filled from Host.
func (d *Deps) escalationEnv() escalation.Env {
	e := d.EscalationEnv()
	if e.Config == nil {
		e.Config = d.Config
	}
	if e.Host == nil {
		e.Host = func() host.Host { return d.Host(host.Herdr) }
	}
	if e.Now == nil {
		e.Now = d.Now
	}
	return e
}

// orchestrateEnv is the OrchestrateEnv hook with its host filled from Host.
func (d *Deps) orchestrateEnv() orchestrate.Env {
	e := d.OrchestrateEnv()
	if e.Host == nil {
		e.Host = func(kind host.Kind) host.Host { return d.Host(kind) }
	}
	return e
}

// forge is the one place a verb builds its forge (pull requests, checks,
// releases): settings from cfg, forge.provider resolution (a local forge is
// tracker.ErrLocalForge) and the TrackerOptions a test swaps the executor
// through. The worker verbs build theirs in workerEnv, with the same
// TrackerOptions but no read cache.
func (d *Deps) forge(ctx context.Context, cfg any, provider, dir string) (tracker.Forge, error) {
	return tracker.NewForgeFromConfig(ctx, cfg, provider, dir, d.trackerOptions()...)
}

// issueTracker is the one place a verb builds its issue tracker: issues.source
// from cfg (file is tracker.ErrNoIssueTracker), with the same TrackerOptions.
func (d *Deps) issueTracker(ctx context.Context, cfg any, dir string) (tracker.IssueTracker, error) {
	return tracker.NewIssueTrackerFromConfig(ctx, cfg, dir, d.trackerOptions()...)
}

// issuesOrNone is issueTracker with the file source answered by
// tracker.NoIssues, for a verb that reads issues when there are any and works
// without. Any other failure is returned.
func (d *Deps) issuesOrNone(ctx context.Context, cfg any, dir string) (tracker.IssueTracker, error) {
	it, err := d.issueTracker(ctx, cfg, dir)
	if errors.Is(err, tracker.ErrNoIssueTracker) {
		return tracker.NoIssues{}, nil
	}
	return it, err
}

// bothSides is a forge and an issue tracker handed to a consumer that needs
// both (the round and the escalations read PRs and claim labels). The methods
// both sides have are routed by what they mean.
type bothSides struct {
	tracker.Forge
	tracker.IssueTracker
}

func (b bothSides) Provider() string                    { return b.Forge.Provider() }
func (b bothSides) CheckAuth(ctx context.Context) error { return b.Forge.CheckAuth(ctx) }

// forgeIssues is what forgeAndIssues hands back: bothSides or trustedSides.
type forgeIssues interface {
	tracker.Forge
	tracker.IssueTracker
}

// trustedSides is bothSides whose issue side says who wrote an issue, so the
// round can tell whether an issue's Try it steps may go into a brief (#899).
// Embedding hides the issue side's own methods; these two hand them through.
type trustedSides struct {
	bothSides
	trust worker.IssueTrust
}

func (t trustedSides) IssueAuthorship(ctx context.Context, key tracker.IssueKey) (tracker.Authorship, error) {
	return t.trust.IssueAuthorship(ctx, key)
}

func (t trustedSides) CanPush(ctx context.Context, login string) (bool, error) {
	return t.trust.CanPush(ctx, login)
}

// forgeAndIssues builds both seams for a consumer that needs them. A forge
// failure (a local forge included) fails it; the file source's issue side is
// tracker.NoIssues. The result is a worker.IssueTrust exactly when the issue
// side is.
func (d *Deps) forgeAndIssues(ctx context.Context, cfg any, dir string) (forgeIssues, error) {
	f, err := d.forge(ctx, cfg, "", dir)
	if err != nil {
		return nil, err
	}
	it, err := d.issuesOrNone(ctx, cfg, dir)
	if err != nil {
		return nil, err
	}
	both := bothSides{f, it}
	if t, ok := it.(worker.IssueTrust); ok {
		return trustedSides{both, t}, nil
	}
	return both, nil
}

// trackerExec is the executor TrackerOptions install (nil: the real one), for a
// verb that reads the origin through tracker.ResolveProvider.
func (d *Deps) trackerExec() tracker.Exec {
	cl := &tracker.CLI{}
	for _, o := range d.trackerOptions() {
		o(cl)
	}
	return cl.Exec
}

// freshReads drops the invocation's cached forge reads. A loop that reads the
// forge again inside one process (round watch, the autopilot) calls it at the
// start of each pass, so a pass sees what changed since the last one.
func (d *Deps) freshReads() {
	if d.ReadCache != nil {
		d.ReadCache.Clear()
	}
}

// trackerBase is TrackerOptions plus the machine the default connection lookup
// reads: Deps' own env and keychain. A test's WithConnections still wins.
func (d *Deps) trackerBase() []tracker.Option {
	return append([]tracker.Option{tracker.WithMachine(d.projectsEnv(), d.Keychain)}, d.TrackerOptions...)
}

// trackerOptions is trackerBase plus the invocation's read cache.
func (d *Deps) trackerOptions() []tracker.Option {
	if d.ReadCache == nil {
		return d.trackerBase()
	}
	return append(d.trackerBase(), tracker.WithReadCache(d.ReadCache))
}

// forgeOrGitHub is forge falling back to github for an unrecognized origin.
func (d *Deps) forgeOrGitHub(ctx context.Context, cfg any, provider, dir string) (tracker.Forge, error) {
	return tracker.NewFromConfigOrGitHub(ctx, cfg, provider, dir, d.trackerOptions()...)
}

// envClock reads the test overrides of the clock once, at the edge:
// ROTA_TEST_NOW (RFC 3339) fixes Now, ROTA_TEST_TODAY (YYYY-MM-DD) fixes the
// day alone and wins over Now there, ROTA_TEST_HOLDER_PID stands in for the
// nearest non-shell ancestor, which a smoke test cannot arrange. Not part of
// the CLI.
func envClock(getenv func(string) string) (now, today func() time.Time, holder func() int, err error) {
	now = time.Now
	if v := getenv("ROTA_TEST_NOW"); v != "" {
		if t, perr := time.Parse(time.RFC3339, v); perr == nil {
			now = func() time.Time { return t }
		}
	}
	today = now
	if v := getenv("ROTA_TEST_TODAY"); v != "" {
		if t, ok := stale.ParseDate(v); ok {
			today = func() time.Time { return t }
		} else {
			err = Usage("ROTA_TEST_TODAY must be YYYY-MM-DD, got %q", v)
		}
	}
	pid, _ := strconv.Atoi(getenv("ROTA_TEST_HOLDER_PID"))
	return now, today, func() int { return pid }, err
}
