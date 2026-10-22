package round

import (
	"os"
	"testing"

	"github.com/l4ci/rota/internal/roundlease"
)

func TestHoldsLeaseSeam(t *testing.T) {
	le := fakeLease("h", 100, 200)
	cd := t.TempDir()
	if _, _, held, err := HoldsLease(le, cd, 100, noEnv); err != nil || held {
		t.Fatalf("no lease: held=%v err=%v", held, err)
	}
	if _, _, _, err := le.Acquire(cd, cd, le.Discover(100, noEnv), 3); err != nil {
		t.Fatal(err)
	}
	l, st, held, err := HoldsLease(le, cd, 100, noEnv)
	if err != nil || !held || st != roundlease.Live || l.Round != 3 {
		t.Fatalf("holder: lease=%+v st=%v held=%v err=%v", l, st, held, err)
	}
	if _, _, held, err := HoldsLease(le, cd, 200, noEnv); err != nil || held {
		t.Fatalf("other live process: held=%v err=%v", held, err)
	}
	if err := os.WriteFile(roundlease.Path(cd), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, st, held, err := HoldsLease(le, cd, 100, noEnv); err != nil || held || st != roundlease.Stale {
		t.Fatalf("garbled lease: st=%v held=%v err=%v", st, held, err)
	}
	bad := t.TempDir()
	if err := os.MkdirAll(roundlease.Path(bad), 0o755); err != nil { // a directory where the file belongs: ReadFile fails
		t.Fatal(err)
	}
	if _, _, held, err := HoldsLease(le, bad, 100, noEnv); err == nil || held {
		t.Fatalf("lookup error: held=%v err=%v", held, err)
	}
}
