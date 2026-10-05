package roundtick

import "testing"

func TestStateRoundTripAndStop(t *testing.T) {
	cd := t.TempDir()
	r := Result{Held: map[string]string{"ben": "verify-failed"}, Reported: map[string]bool{"blocked dana": true}}
	if err := SaveState(cd, 2, r); err != nil {
		t.Fatal(err)
	}
	s := LoadState(cd, 2)
	if s.Held["ben"] == "" || len(s.Reported) != 1 || s.Stopped {
		t.Fatalf("%+v", s)
	}
	if err := SetStopped(cd, 2, true); err != nil {
		t.Fatal(err)
	}
	// A tick that loaded before the stop must not undo it when it saves.
	if err := SaveState(cd, 2, r); err != nil {
		t.Fatal(err)
	}
	if !LoadState(cd, 2).Stopped {
		t.Fatal("SaveState dropped the stop")
	}
	if got := LoadState(cd, 3); got.Stopped || len(got.Held) != 0 {
		t.Fatalf("another round must start clean: %+v", got)
	}
	ClearState(cd)
	if got := LoadState(cd, 2); got.Stopped || len(got.Held) != 0 {
		t.Fatalf("%+v", got)
	}
}
