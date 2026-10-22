package cli

import (
	"reflect"
	"testing"

	"github.com/l4ci/rota/internal/worker"
)

// A bare Deps, with no seam set, still yields a worker.Env whose every seam is
// non-nil once WithDefaults has run: workerEnv copies what Deps holds and
// WithDefaults is the one site that supplies a production value for the rest.
// Accounts is the exception on purpose: nil records no usage meters.
func TestBareDepsWorkerEnvFillsEverySeam(t *testing.T) {
	d := &Deps{WorkerEnv: func() worker.Env { return worker.Env{} }}
	v := reflect.ValueOf(d.workerEnv().WithDefaults())
	for i := 0; i < v.NumField(); i++ {
		f, name := v.Field(i), v.Type().Field(i).Name
		switch f.Kind() {
		case reflect.Func, reflect.Interface, reflect.Ptr:
			if name != "Accounts" && f.IsNil() {
				t.Errorf("seam %s left nil", name)
			}
		}
	}
}

// On the production Deps each worker.Env seam has exactly one owner: either
// Deps.workerEnv fills it (it reads the swappable Deps field) or it is left nil
// for worker.WithDefaults. A seam in both places, or in neither, fails here.
// Ctx is the CLI's to set (workerEnvCtx), so it belongs to neither.
func TestProductionDepsWorkerEnvSeamOwnership(t *testing.T) {
	withDefaultsOwned := map[string]bool{
		"Ctx": true, "Sleep": true, "Stderr": true, "Shell": true,
		"ShellEnv": true, "Run": true, "Executable": true, "GateInput": true,
	}
	v := reflect.ValueOf(defaultDeps().workerEnv())
	for i := 0; i < v.NumField(); i++ {
		f, name := v.Field(i), v.Type().Field(i).Name
		switch f.Kind() {
		case reflect.Func, reflect.Interface, reflect.Ptr:
			if withDefaultsOwned[name] && !f.IsNil() {
				t.Errorf("seam %s is filled by Deps.workerEnv and WithDefaults: two owners", name)
			}
			if !withDefaultsOwned[name] && f.IsNil() {
				t.Errorf("seam %s is left nil by Deps.workerEnv and not owned by WithDefaults", name)
			}
		}
	}
}
