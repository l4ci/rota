package round

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/rotastate"
)

func TestClaimStaleWarningOncePerRound(t *testing.T) {
	cd := t.TempDir()
	for _, tc := range []struct {
		round int
		want  bool
	}{{7, true}, {7, false}, {8, true}, {8, false}, {7, true}} {
		got, err := ClaimStaleWarning(cd, tc.round)
		if err != nil || got != tc.want {
			t.Fatalf("round %d: first=%v err=%v, want first=%v", tc.round, got, err, tc.want)
		}
	}
}

func TestClaimStaleWarningSurfacesErrors(t *testing.T) {
	cd := t.TempDir()
	path := rotastate.File(cd, staleMarkerName)
	if err := os.MkdirAll(path, 0o755); err != nil { // a directory where the marker file belongs
		t.Fatal(err)
	}
	first, err := ClaimStaleWarning(cd, 3)
	if err == nil || !first {
		t.Fatalf("unreadable marker: first=%v err=%v, want first with an error", first, err)
	}

	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	first, err = ClaimStaleWarning(blocked, 3) // state dir cannot be created under a file
	if err == nil || !first {
		t.Fatalf("unwritable marker: first=%v err=%v, want first with an error", first, err)
	}
}
