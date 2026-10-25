package round

import (
	"context"
	"time"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/worker"
)

// Worker is every worker operation a round performs, listed once: the seam
// between round and internal/worker. Env.Worker holds one; FromWorkerEnv adapts
// the concrete worker.Env behind it, and a test fakes the few methods its verb
// reaches.
type Worker interface {
	// The clock, environment reader, home directory and project config a round
	// reads for itself. Config returns the merged project config in the untyped
	// form the config package reads everywhere (config.Label, config.Load).
	Now() time.Time
	Getenv(key string) string
	Home() (string, error)
	Config(root string) any

	// Accounts is the meter reader quota and the ledger read; nil means none.
	// WithAccounts returns the worker with it set.
	Accounts() *worker.Accounts
	WithAccounts(a *worker.Accounts) Worker

	// LedgerNote warns when the round ledger row is not written;
	// LedgerAppendErr returns the failure for a verb that must exit non-zero.
	LedgerNote(root string, e ledger.Entry)
	LedgerAppendErr(root string, e ledger.Entry) error

	GateConfig(root string) worker.GateConfig
	Verify(ctx context.Context, cfg worker.GateConfig, root, dir string) (worker.VerifyResult, error)
	LandLockHeld(ctx context.Context, root string) (bool, error)
	LandLockHolder(ctx context.Context, root string) string
	BounceStore(root string) (worker.BounceStore, error)

	PoolInit(ctx context.Context, root string, o worker.InitOpts, acc *worker.Accounts) (worker.InitResult, error)
	ModelAppliesTo(root, kind string) bool
	NeedsModel(root, kind string) bool
	Preflight(ctx context.Context, root, kind, slot, model string) (harness.Setup, error)
	Dispatch(ctx context.Context, root string, o worker.DispatchOpts) (worker.DispatchResult, error)

	ClearLabel(ctx context.Context, root, slot string)
	SweepSlot(ctx context.Context, root, slot string)
	KillSlot(ctx context.Context, root, slot string) error
	ResetTo(root, slot, task, newBranch string, checkOnly bool) (worker.ResetResult, error)
	ResetFrom(root, slot, task, newBranch, from string, checkOnly bool) (worker.ResetResult, error)
	ReleaseExternal(root, name string, prune bool) error
}

// envWorker is the one adapter of the concrete worker.Env to Worker. The
// embedded env supplies the operations; the methods below cover those that are
// fields, or free functions taking the env, on the worker side.
type envWorker struct{ worker.Env }

// FromWorkerEnv adapts w as the Worker a round runs against. It is the only
// place round holds the concrete worker.Env.
func FromWorkerEnv(w worker.Env) Worker { return &envWorker{w} }

func (w *envWorker) Now() time.Time             { return w.WithDefaults().Now() }
func (w *envWorker) Getenv(key string) string   { return w.WithDefaults().Getenv(key) }
func (w *envWorker) Home() (string, error)      { return w.WithDefaults().Home() }
func (w *envWorker) Config(root string) any     { return w.WithDefaults().Config(root) }
func (w *envWorker) Accounts() *worker.Accounts { return w.Env.Accounts }
func (w *envWorker) LedgerNote(root string, e ledger.Entry) {
	worker.LedgerNote(w.Env, root, e)
}
func (w *envWorker) LedgerAppendErr(root string, e ledger.Entry) error {
	return worker.LedgerAppendErr(w.Env, root, e)
}
func (w *envWorker) WithAccounts(a *worker.Accounts) Worker {
	c := *w
	c.Env.Accounts = a
	return &c
}
