package cli

import (
	"context"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/doctor"
	"github.com/l4ci/rota/internal/escalation"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/migrate"
	"github.com/l4ci/rota/internal/orchestrate"
	"github.com/l4ci/rota/internal/palette"
	"github.com/l4ci/rota/internal/proc"
	"github.com/l4ci/rota/internal/reap"
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
	Getenv  func(string) string
	Environ func() []string

	// PollFixture, when set, makes `worker poll` classify that file as a static
	// pane (no host, no registry writes); PollStatus is the agent status it
	// reports. DoctorPath replaces PATH for doctor's tool lookup when set.
	// DoctorDisk reads the free space of the volume holding a directory.
	PollFixture string
	PollStatus  string
	DoctorPath  string
	DoctorDisk  func(dir string) *doctor.Disk

	// TrackerOptions apply to every forge CLI the verbs build; a test swaps
	// in a fake executor here.
	TrackerOptions []tracker.Option

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
	Host func(kind string) host.Host

	UpdateEnv        func() update.Env
	InstalledVersion func() string
	SeedBase         func(root string) error
	SetupIsTTY       func(in io.Reader) bool
	IsTerminal       func(f any) bool
	BareSetup        RunFunc
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
		WorkerEnv:        func() worker.Env { return worker.Env{} },
		EscalationEnv:    func() escalation.Env { return escalation.Env{} },
		LeaseEnv:         func() roundlease.Env { return roundlease.DefaultEnv() },
		Host:             func(kind string) host.Host { return host.New(kind, host.Deps{}) },
		InstalledVersion: installedVersion,
		Environ:          os.Environ,
		SeedBase:         seedProject,
		SetupIsTTY:       defaultSetupIsTTY,
		IsTerminal:       defaultIsTerminal,
		BareSetup:        defaultBareSetup,
		Palette:          palette.RunTerminal,
		RunView:          runView,
		ReadCache:        tracker.NewReadCache(),
	}
	d.Now, d.Today, d.HolderPID, d.ClockErr = envClock(d.Getenv)
	d.PollFixture, d.PollStatus = d.Getenv("ROTA_TEST_POLL_FIXTURE"), d.Getenv("ROTA_TEST_POLL_STATUS")
	d.DoctorPath = d.Getenv("ROTA_TEST_DOCTOR_PATH")
	d.DoctorDisk = doctorDisk(d.Getenv("ROTA_TEST_DOCTOR_DISK"))
	d.UpdateEnv = func() update.Env { return update.DefaultEnv(version.Get().Version, d.Getenv) }
	d.OrchestrateEnv = func() orchestrate.Env { return defaultOrchestrateEnv(d) }
	d.WorkerAccounts = func() *worker.Accounts { return &worker.Accounts{Env: worker.Env{Now: d.Now, Getenv: d.Getenv}} }
	d.NewTracker = func(ctx context.Context, root string, cfg any) (backlog.Tracker, error) {
		return d.forge(ctx, cfg, "", root)
	}
	d.MigrateTracker = func(ctx context.Context, root string, cfg any) (migrate.Tracker, error) {
		return d.forge(ctx, cfg, "", root)
	}
	d.RoundEnv = func(ctx context.Context, root string) round.Env { return defaultRoundEnv(ctx, root, d, true) }
	d.RoundEnvLocal = func(ctx context.Context, root string) round.Env { return defaultRoundEnv(ctx, root, d, false) }
	d.ReapEnv = func(ctx context.Context, root string) (round.Env, reap.HostOps) {
		return defaultReapEnv(ctx, root, d)
	}
	return d
}

// deps is the invocation's dependencies. A Ctx built by hand, without run,
// gets the real ones.
func (c *Ctx) deps() *Deps {
	if c.Deps == nil {
		c.Deps = defaultDeps()
	}
	return c.Deps
}

// workerEnv is the WorkerEnv hook with its host filled from Host.
func (d *Deps) workerEnv() worker.Env {
	e := d.WorkerEnv()
	if e.NewHost == nil {
		e.NewHost = func(kind string) host.Host { return d.Host(kind) }
	}
	if e.Now == nil {
		e.Now = d.Now
	}
	if e.Getenv == nil {
		e.Getenv = d.Getenv
	}
	if e.Accounts == nil && d.WorkerAccounts != nil {
		e.Accounts = d.WorkerAccounts()
	}
	return e
}

// escalationEnv is the EscalationEnv hook with its host (always herdr, the
// only one with notifications) filled from Host.
func (d *Deps) escalationEnv() escalation.Env {
	e := d.EscalationEnv()
	if e.Host == nil {
		e.Host = func() host.Host { return d.Host("herdr") }
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
		e.Host = func(kind string) host.Host { return d.Host(kind) }
	}
	return e
}

// forge is the one place a verb builds its forge adapter: settings from cfg,
// provider resolution and the TrackerOptions a test swaps the executor through.
func (d *Deps) forge(ctx context.Context, cfg any, provider, dir string) (tracker.Adapter, error) {
	return tracker.NewFromConfig(ctx, cfg, provider, dir, d.trackerOptions()...)
}

// freshReads drops the invocation's cached forge reads. A loop that reads the
// forge again inside one process (round watch, the autopilot) calls it at the
// start of each pass, so a pass sees what changed since the last one.
func (d *Deps) freshReads() {
	if d.ReadCache != nil {
		d.ReadCache.Clear()
	}
}

// trackerOptions is TrackerOptions plus the invocation's read cache.
func (d *Deps) trackerOptions() []tracker.Option {
	if d.ReadCache == nil {
		return d.TrackerOptions
	}
	return append(append([]tracker.Option(nil), d.TrackerOptions...), tracker.WithReadCache(d.ReadCache))
}

// forgeOrGitHub is forge falling back to github for an unrecognized origin.
func (d *Deps) forgeOrGitHub(ctx context.Context, cfg any, provider, dir string) (tracker.Adapter, error) {
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
