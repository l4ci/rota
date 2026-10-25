package worker

import (
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

func TestLandLockHeld(t *testing.T) {
	env := Env{}.WithDefaults()

	t.Run("free lock reads as not held", func(t *testing.T) {
		repo := gittest.NewRepo(t, "main")
		held, err := env.LandLockHeld(bg, repo)
		if err != nil || held {
			t.Fatalf("LandLockHeld = %v, %v; want false, nil", held, err)
		}
	})

	t.Run("held lock reads as held and release frees it", func(t *testing.T) {
		repo := gittest.NewRepo(t, "main")
		var during bool
		var duringErr error
		err := env.withLandLock(bg, repo, "", func(bool) error {
			during, duringErr = env.LandLockHeld(bg, repo)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if duringErr != nil || !during {
			t.Fatalf("LandLockHeld under the lock = %v, %v; want true, nil", during, duringErr)
		}
		held, err := env.LandLockHeld(bg, repo)
		if err != nil || held {
			t.Fatalf("LandLockHeld after release = %v, %v; want false, nil", held, err)
		}
	})

	t.Run("non-git root passes the error through as not held", func(t *testing.T) {
		held, err := env.LandLockHeld(bg, gittest.TempDir(t))
		if err == nil {
			t.Fatal("LandLockHeld on a non-git root returned nil error")
		}
		if held {
			t.Fatal("LandLockHeld on error returned held=true")
		}
	})
}

func TestLandLockHolder(t *testing.T) {
	env := Env{}.WithDefaults()
	repo := gittest.NewRepo(t, "main")
	if got := env.LandLockHolder(bg, repo); got != "" {
		t.Fatalf("holder with a free lock = %q, want empty", got)
	}
	var during string
	err := env.withLandLock(bg, repo, "ben", func(bool) error {
		during = env.LandLockHolder(bg, repo)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if during != "ben" {
		t.Errorf("holder under the lock = %q, want ben", during)
	}
	if got := env.LandLockHolder(bg, repo); got != "" {
		t.Errorf("holder after release = %q, want empty", got)
	}
}
