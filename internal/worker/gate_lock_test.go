package worker

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Lock ownership is an explicit gate option, not inferred from LandVerified: a gate
// that does not say it already holds the land lock takes it, even when it is
// a train's landing step.
func TestGateTakesLandLockUnlessCallerHoldsIt(t *testing.T) {
	w := newWorld(t, ghURL)
	env := w.env(false).WithDefaults()
	done := make(chan error, 1)
	early := false
	err := env.withLandLock(bg, w.dir, func(bool) error {
		go func() {
			res, err := w.gate(false, GateOpts{LandVerified: true})
			if err == nil && res.Verdict != GatePass {
				err = fmt.Errorf("verdict %q: %s", res.Verdict, res.Err)
			}
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
	case err := <-done:
		if err != nil {
			t.Fatalf("gate after the lock was released: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("gate did not finish after the lock was released")
	}
}

func TestGateHoldsLandLockSkipsTheLock(t *testing.T) {
	w := newWorld(t, ghURL)
	env := w.env(false).WithDefaults()
	done := make(chan error, 1)
	err := env.withLandLock(bg, w.dir, func(bool) error {
		go func() {
			res, err := w.gate(false, GateOpts{LandVerified: true, HoldsLandLock: true})
			if err == nil && res.Verdict != GatePass {
				err = fmt.Errorf("verdict %q: %s", res.Verdict, res.Err)
			}
			done <- err
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("gate with HoldsLandLock: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("gate with HoldsLandLock blocked on the lock")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// LandVerified is what a train's landing gate says. It is not NoVerify plus
// HoldsLandLock: it also skips the freshness check (the train merged every PR
// in order) and the NO-VERIFY note (the train reports its own).
func TestGateLandVerifiedLandsWithoutFreshnessOrNoVerifyNote(t *testing.T) {
	w := newWorld(t, ghURL)
	advanceMainOn(w, "more.txt") // the branch is behind the base, as after an earlier train member landed
	res, err := w.gate(false, GateOpts{LandVerified: true, HoldsLandLock: true})
	if err != nil || res.Verdict != GatePass {
		t.Fatalf("verdict %q err %v: %s", res.Verdict, err, res.Err)
	}
	for _, n := range res.Notes {
		if strings.HasPrefix(n, "STALE-MERGE") || strings.HasPrefix(n, "NO-VERIFY") {
			t.Errorf("a landing gate must not note %q", n)
		}
	}
	if !res.VerifySkipped {
		t.Error("a landing gate runs no verify tier")
	}
}

// A landing gate does not refuse a branch the base moved past: the train merged
// every member in order already. Without LandVerified the same branch is stale.
func TestGateLandVerifiedSkipsTheFreshnessRefusal(t *testing.T) {
	w := newWorld(t, ghURL)
	advanceMainOn(w, "work.txt") // conflicts with the branch's work.txt
	res, err := w.gate(false, GateOpts{CheckOnly: true})
	if err != nil || res.Verdict != GateStale {
		t.Fatalf("control: want stale, got %q err %v", res.Verdict, err)
	}
	res, err = w.gate(false, GateOpts{CheckOnly: true, LandVerified: true})
	if res.Verdict == GateStale {
		t.Errorf("a landing gate must not run the freshness check: %q (%v)", res.Err, err)
	}
}
