package round

import (
	"time"

	"github.com/l4ci/rota/internal/worker"
)

// wire (on assignFixture and moveFx) rebuilds Env.Worker from a changed
// worker.Env; stubWorker below overrides single Worker methods on top of
// whatever Worker an Env already has. Neither reaches into an adapter.

// moveFx counterpart of assignFixture.wire.
func (f *moveFx) wire(mod func(*worker.Env)) {
	mod(&f.ws)
	f.env.Worker = wk(f.ws)
}

// stubWorker is a Worker that answers the clock, environment reader and
// config itself when a test set them, and delegates everything else.
type stubWorker struct {
	Worker
	now    func() time.Time
	getenv func(string) string
	config func(string) any
}

func (s stubWorker) Now() time.Time {
	if s.now != nil {
		return s.now()
	}
	return s.Worker.Now()
}

func (s stubWorker) Getenv(key string) string {
	if s.getenv != nil {
		return s.getenv(key)
	}
	return s.Worker.Getenv(key)
}

func (s stubWorker) Config(root string) any {
	if s.config != nil {
		return s.config(root)
	}
	return s.Worker.Config(root)
}

// WithAccounts keeps the stub around the Worker the accounts were set on.
func (s stubWorker) WithAccounts(a *worker.Accounts) Worker {
	s.Worker = s.Worker.WithAccounts(a)
	return s
}

// stubbed is e's Worker as a stubWorker, wrapping it when it is not one yet.
func stubbed(e *Env) stubWorker {
	if e.Worker == nil {
		e.Worker = wk(worker.Env{})
	}
	if s, ok := e.Worker.(stubWorker); ok {
		return s
	}
	return stubWorker{Worker: e.Worker}
}

func withNow(e *Env, now func() time.Time) { s := stubbed(e); s.now = now; e.Worker = s }

func withGetenv(e *Env, getenv func(string) string) {
	s := stubbed(e)
	s.getenv = getenv
	e.Worker = s
}

func withConfig(e *Env, config func(string) any) {
	s := stubbed(e)
	s.config = config
	e.Worker = s
}
