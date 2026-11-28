package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/rotastatetest"
)

func TestLandLockHeld(t *testing.T) {
	env := Env{Config: diskConfig}.WithDefaults()

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
		err := env.withLandLock(bg, repo, nil, func(bool) error {
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
	env := Env{Config: diskConfig}.WithDefaults()
	repo := gittest.NewRepo(t, "main")
	if got := env.LandLockHolder(bg, repo); got != "" {
		t.Fatalf("holder with a free lock = %q, want empty", got)
	}
	var during string
	err := env.withLandLock(bg, repo, unscopedLandHolder("ben"), func(bool) error {
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

func TestLandLockHolderPublishesNewFile(t *testing.T) {
	env := Env{Config: diskConfig}.WithDefaults()
	repo := gittest.NewRepo(t, "main")
	common, err := rotastatetest.CommonDir(repo)
	if err != nil {
		t.Fatal(err)
	}
	holder := rotastate.File(common, "land") + LandHolderSuffix
	if err := os.MkdirAll(filepath.Dir(holder), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(holder, []byte("previous"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A reader with the old inode must keep seeing the complete old value.
	snapshot, err := os.Open(holder)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	err = env.withLandLock(bg, repo, unscopedLandHolder("next"), func(bool) error {
		var b [16]byte
		n, err := snapshot.Read(b[:])
		if err != nil || string(b[:n]) != "previous" {
			t.Errorf("old holder reader saw %q, %v; want previous", b[:n], err)
		}
		if got := env.LandLockHolder(bg, repo); got != "next" {
			t.Errorf("current holder = %q, want next", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// An interrupted gate or train (its context cancelled mid-run) frees the land
// lock and its landing-run marker, so the next run does not wait on a corpse.
func TestInterruptedLandingRunReleasesLock(t *testing.T) {
	env := Env{Config: diskConfig}.WithDefaults()
	repo := gittest.NewRepo(t, "main")
	ctx, cancel := context.WithCancel(bg)
	err := env.withLandingRun(ctx, repo, unscopedLandHolder("slot"), false, func(bool) error {
		cancel()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if held, err := env.LandLockHeld(bg, repo); err != nil || held {
		t.Fatalf("LandLockHeld after interrupt = %v, %v; want false, nil", held, err)
	}
	common, err := rotastatetest.CommonDir(repo)
	if err != nil {
		t.Fatal(err)
	}
	if paths, _ := filepath.Glob(filepath.Join(rotastate.Dir(common), "landing-run-*.pid")); len(paths) != 0 {
		t.Errorf("landing-run markers left behind: %v", paths)
	}
}
