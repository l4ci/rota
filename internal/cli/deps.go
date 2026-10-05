package cli

import (
	"context"
	"io"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/escalation"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/orchestrate"
	"github.com/l4ci/rota/internal/reap"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/tracker"
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
	// TrackerOptions apply to every forge CLI the verbs build; a test swaps
	// in a fake executor here.
	TrackerOptions []tracker.Option

	NewTracker     func(ctx context.Context, root string, cfg any) (backlog.Tracker, error)
	MigrateTracker func(ctx context.Context, root string, cfg any) (backlog.MigrateTracker, error)
	MigrateSleep   func(d time.Duration)

	RoundEnv       func(ctx context.Context, root string) round.Env
	ReapEnv        func(ctx context.Context, root string) (round.Env, reap.HostOps)
	EscalationEnv  func() escalation.Env
	WatchEnv       func() roundlease.Env
	OrchestrateEnv func() orchestrate.Env

	WorkerEnv      func() worker.Env
	WorkerAccounts func() *worker.Accounts
	LimitHost      func(kind string) host.Host

	UpdateEnv        func() update.Env
	InstalledVersion func() string
	SeedBase         func(root string) error
	SetupIsTTY       func(in io.Reader) bool
	IsTerminal       func(f any) bool
	BareSetup        RunFunc
}

// defaultDeps is the real machine: git, the forge CLIs, tmux or herdr.
func defaultDeps() *Deps {
	d := &Deps{
		WorkerEnv:        func() worker.Env { return worker.Env{} },
		WorkerAccounts:   func() *worker.Accounts { return &worker.Accounts{Now: hookNow} }, // ROTA_TEST_NOW fixes the meters' clock too
		EscalationEnv:    func() escalation.Env { return escalation.Env{} },
		WatchEnv:         func() roundlease.Env { return roundlease.DefaultEnv() },
		OrchestrateEnv:   defaultOrchestrateEnv,
		LimitHost:        func(kind string) host.Host { return host.New(kind, host.Deps{}) },
		UpdateEnv:        func() update.Env { return update.DefaultEnv(version.Get().Version) },
		InstalledVersion: installedVersion,
		SeedBase:         seedProject,
		SetupIsTTY:       defaultSetupIsTTY,
		IsTerminal:       defaultIsTerminal,
		BareSetup:        defaultBareSetup,
	}
	d.NewTracker = func(ctx context.Context, root string, cfg any) (backlog.Tracker, error) {
		return d.forge(ctx, cfg, "", root)
	}
	d.MigrateTracker = func(ctx context.Context, root string, cfg any) (backlog.MigrateTracker, error) {
		return d.forge(ctx, cfg, "", root)
	}
	d.RoundEnv = func(ctx context.Context, root string) round.Env { return defaultRoundEnv(ctx, root, d) }
	d.ReapEnv = func(ctx context.Context, root string) (round.Env, reap.HostOps) {
		return defaultReapEnv(ctx, root, d.RoundEnv)
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

// forge is the one place a verb builds its forge adapter: settings from cfg,
// provider resolution and the TrackerOptions a test swaps the executor through.
func (d *Deps) forge(ctx context.Context, cfg any, provider, dir string) (tracker.Adapter, error) {
	return tracker.NewFromConfig(ctx, cfg, provider, dir, d.TrackerOptions...)
}

// forgeOrGitHub is forge falling back to github for an unrecognized origin.
func (d *Deps) forgeOrGitHub(ctx context.Context, cfg any, provider, dir string) (tracker.Adapter, error) {
	return tracker.NewFromConfigOrGitHub(ctx, cfg, provider, dir, d.TrackerOptions...)
}
