package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/rotastate"
)

// A gate that queued behind the land lock reads the PR again: it merged while
// the gate waited, so the verdict is already-merged, not a refusal about a head
// that has landed.
func TestGateWaitedOnLockFindsPRMerged(t *testing.T) {
	w := newWorld(t, ghURL)
	env := w.env(false).withDefaults()
	type out struct {
		res GateResult
		err error
	}
	done := make(chan out, 1)
	err := env.withLandLock(bg, w.dir, func(bool) error {
		go func() {
			res, err := w.gate(false, GateOpts{})
			done <- out{res, err}
		}()
		time.Sleep(300 * time.Millisecond) // the gate is queued on the lock
		w.forge("state", "MERGED")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case o := <-done:
		if o.err != nil || o.res.Verdict != GateAlreadyMerged || o.res.Changed || !o.res.AlreadyMerged {
			t.Fatalf("verdict %q changed=%v err %v: %s", o.res.Verdict, o.res.Changed, o.err, o.res.Err)
		}
		if strings.Contains(o.res.Err, "origin") || o.res.Verdict == GateCheckBroke {
			t.Errorf("must not report a missing origin: %q", o.res.Err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("gate did not finish after the lock was released")
	}
}

// A gate that starts while another process is gating or queued on the same PR
// refuses before the lock, naming that process.
func TestGateRefusesWhileAnotherGateRunsTheSamePR(t *testing.T) {
	w := newWorld(t, ghURL)
	env := w.env(false).withDefaults()
	common, key, ok := env.gateRunKey(bg, w.dir, "w1")
	if !ok {
		t.Fatal("no gate run key")
	}
	other := os.Getppid() // a live process that is not this one
	os.MkdirAll(rotastate.Dir(common), 0o755)
	if err := os.WriteFile(rotastate.File(common, "gate-"+key)+".pid", []byte(strconv.Itoa(other)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var held bool
	lockErr := env.withLandLock(bg, w.dir, func(bool) error { // would block a gate that tried the lock
		held = true
		res, err := w.gate(false, GateOpts{})
		if err != nil || res.Verdict != GateRunning || res.Changed {
			t.Fatalf("verdict %q err %v: %s", res.Verdict, err, res.Err)
		}
		if !strings.Contains(res.Err, strconv.Itoa(other)) || strings.Contains(res.Err, "origin") {
			t.Errorf("message must name pid %d and not origin: %q", other, res.Err)
		}
		return nil
	})
	if lockErr != nil || !held {
		t.Fatalf("lock: %v", lockErr)
	}
}

// A stale marker (its process is gone) does not block.
func TestGateRunMarkerOfDeadProcessIsReplaced(t *testing.T) {
	common := t.TempDir()
	os.MkdirAll(rotastate.Dir(common), 0o755)
	if err := os.WriteFile(rotastate.File(common, "gate-pr1")+".pid", []byte("2147483646\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	other, release, err := claimGateRun(common, "pr1")
	if err != nil || other != 0 {
		t.Fatalf("claim: other=%d err=%v", other, err)
	}
	release()
}

// A marker whose pid is alive but belongs to a different process (pid reuse
// after a crash) does not block; the same process still does.
func TestGateRunMarkerOfReusedPidIsReplaced(t *testing.T) {
	ppid := os.Getppid()
	start, ok := procStartTicks(ppid)
	if !ok {
		t.Skip("no /proc start time on this platform")
	}
	for _, c := range []struct {
		name    string
		start   uint64
		blocked bool
	}{{"reused", start + 1, false}, {"same process", start, true}} {
		t.Run(c.name, func(t *testing.T) {
			common := t.TempDir()
			os.MkdirAll(rotastate.Dir(common), 0o755)
			body := fmt.Sprintf("%d %d\n", ppid, c.start)
			if err := os.WriteFile(rotastate.File(common, "gate-pr1")+".pid", []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			other, release, err := claimGateRun(common, "pr1")
			if err != nil {
				t.Fatal(err)
			}
			if c.blocked != (other == ppid) {
				t.Fatalf("other=%d, blocked want %v", other, c.blocked)
			}
			if release != nil {
				release()
			}
		})
	}
}

// Only exit 2 means origin is absent. A git that cannot run is reported as
// that, never as a missing remote.
func TestGateRemoteProbeFailureIsNotAMissingOrigin(t *testing.T) {
	w := newWorld(t, ghURL)
	env := w.env(false).withDefaults()
	env.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		if len(args) > 0 && args[0] == "remote" {
			return git.Result{}, errors.New("chdir: no such file or directory")
		}
		return git.Exec(ctx, dir, args...)
	}
	res, err := env.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
	if err != nil || res.Verdict != GateCheckBroke {
		t.Fatalf("verdict %q err %v", res.Verdict, err)
	}
	if strings.Contains(res.Err, "no 'origin' remote") || !strings.Contains(res.Err, "could not run git") {
		t.Errorf("want the real cause, got %q", res.Err)
	}
}
