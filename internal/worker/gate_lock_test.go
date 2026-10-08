package worker

import (
	"testing"
	"time"
)

// Lock ownership is an explicit gate option, not inferred from Train: a gate
// that does not say it already holds the land lock takes it, even when it is
// a train's landing step.
func TestGateTakesLandLockUnlessCallerHoldsIt(t *testing.T) {
	w := newWorld(t, ghURL)
	env := w.env(false).withDefaults()
	done := make(chan error, 1)
	early := false
	err := env.withLandLock(bg, w.dir, func() error {
		go func() {
			_, err := w.gate(false, GateOpts{Train: true, NoVerify: true})
			done <- err
		}()
		select {
		case <-done:
			early = true
			t.Error("gate finished while another holder had the land lock: it never took the lock")
		case <-time.After(500 * time.Millisecond):
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if early {
		return
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("gate did not finish after the lock was released")
	}
}

func TestGateHoldsLandLockSkipsTheLock(t *testing.T) {
	w := newWorld(t, ghURL)
	env := w.env(false).withDefaults()
	done := make(chan error, 1)
	err := env.withLandLock(bg, w.dir, func() error {
		go func() {
			_, err := w.gate(false, GateOpts{Train: true, HoldsLandLock: true, NoVerify: true})
			done <- err
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("gate with HoldsLandLock blocked on the lock")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
