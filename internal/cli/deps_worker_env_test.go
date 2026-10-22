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
