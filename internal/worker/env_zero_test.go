package worker

import (
	"context"
	"reflect"
	"testing"
)

// A hand-built zero Env must survive every verb entry: nil seams are defaulted
// once at entry (Env.WithDefaults), never dereferenced raw. The calls may fail
// on the empty dir; they may not panic.
func TestZeroEnvVerbEntriesDoNotPanic(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // a cancelled ctx keeps real git/shell/host calls from running long
	var e Env
	for name, call := range map[string]func(){
		"Gate":            func() { e.Gate(ctx, dir, GateOpts{}) },
		"Train":           func() { e.Train(ctx, dir, TrainOpts{}) },
		"Poll":            func() { e.Poll(ctx, dir, PollOpts{}) },
		"Wait":            func() { e.Wait(ctx, dir, WaitOpts{}) },
		"Dispatch":        func() { e.Dispatch(ctx, dir, DispatchOpts{}) },
		"KillSlot":        func() { e.KillSlot(ctx, dir, "w1") },
		"SweepSlot":       func() { e.SweepSlot(ctx, dir, "w1") },
		"SessionCheck":    func() { e.SessionCheck(ctx, dir) },
		"SessionEnsure":   func() { e.SessionEnsure(ctx, dir, SessionOpts{}) },
		"Preflight":       func() { e.Preflight(ctx, dir, "claude", "w1", "") },
		"Reset":           func() { e.Reset(dir, "w1", "t", true) },
		"ResetTo":         func() { e.ResetTo(dir, "w1", "t", "b", true) },
		"ClearLabel":      func() { e.ClearLabel(ctx, dir, "w1") },
		"PoolInit":        func() { e.PoolInit(ctx, dir, InitOpts{}, nil) },
		"Reap":            func() { e.Reap(dir, nil, true) },
		"ReleaseExternal": func() { e.ReleaseExternal(dir, "w1", false) },
		"Verify":          func() { e.Verify(ctx, GateConfig{}, dir, dir) },
		"RunVerify":       func() { e.RunVerify(ctx, []string{"true"}, dir) },
		"RunShell":        func() { e.RunShell(ctx, dir, "true") },
		"LedgerRound":     func() { LedgerRound(e, dir) },
		"Accounts":        func() { (&Accounts{}).Meters(ctx, dir) },
		"BounceRecord":    func() { recordBounceVia(e, dir, "1", "w1", "head") },
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s panicked on a zero Env: %v", name, r)
				}
			}()
			call()
		}()
	}
}

// Every seam is non-nil after WithDefaults, except Accounts (nil records no
// usage meters) and IssueComments (nil keeps the bounce store off the tracker),
// which are optional on purpose.
func TestWithDefaultsFillsEverySeam(t *testing.T) {
	v := reflect.ValueOf(Env{}.WithDefaults())
	for i := 0; i < v.NumField(); i++ {
		f, name := v.Field(i), v.Type().Field(i).Name
		switch f.Kind() {
		case reflect.Func, reflect.Interface, reflect.Ptr:
			if name != "Accounts" && name != "IssueComments" && f.IsNil() {
				t.Errorf("seam %s left nil", name)
			}
		}
	}
}
