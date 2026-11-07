package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/stamp"
)

// cancelledTrain is the answer of a train whose context was cancelled before
// it finished (rota worker train cancel, SIGINT or SIGTERM): verdict cancelled,
// whatever stage it stopped in. A cancel is no finding about any member, so
// every culprit is cleared; the members that landed before it stay landed.
func cancelledTrain(res TrainResult) TrainResult {
	res = res.owned()
	res.Verdict, res.Culprit, res.Culprits, res.CulpritGate = GateCancelled, "", nil, GateResult{}
	for i := range res.Members {
		res.Members[i].Culprit = false
	}
	res.Stop = nil
	if len(res.Landed) == 0 {
		res.Err = "CANCELLED train — stopped before it finished; nothing landed"
		res.Hint = "re-run the train when you want it to land"
	} else {
		res.Err = fmt.Sprintf("CANCELLED train — stopped before it finished; landed %d of %d member(s): %s", len(res.Landed), len(res.Members), strings.Join(res.Landed, ", "))
		res.Hint = fmt.Sprintf("landed %d of %d member(s); re-run the train for the rest", len(res.Landed), len(res.Members))
	}
	if res.Log != "" {
		res.Hint += "; the verify log is kept at " + res.Log
	}
	return res
}

// membersOf is the member list of a train that stopped before it resolved its
// targets (cancelled while queued): each target, in the order given.
func membersOf(targets []string) []TrainMember {
	ms := make([]TrainMember, len(targets))
	for i, t := range targets {
		ms[i] = TrainMember{Target: t}
	}
	return ms
}

// TrainRun is a train process that holds a request id: the live marker a
// receipted train writes before it queues for the land lock, so `rota worker
// train cancel --request-id` and `round status` can find it.
type TrainRun struct {
	PID       int      `json:"pid"`
	Start     uint64   `json:"start,omitempty"`
	RequestID string   `json:"requestId"`
	Targets   []string `json:"targets"`
	Base      string   `json:"base"`
	Since     string   `json:"since"`
}

const trainRunPrefix = "train-run-"

// trainRunFile is the marker of request id under common. The id is hashed: it
// is caller text and must not shape a path.
func trainRunFile(common, id string) string {
	sum := sha256.Sum256([]byte(id))
	return rotastate.File(common, trainRunPrefix+hex.EncodeToString(sum[:6])+".json")
}

// claimTrainRun writes this process's marker for o.RequestID and returns the
// func that removes it. Two calls under one id never overlap: the receipt
// claim serialises them before the train starts.
func (e Env) claimTrainRun(ctx context.Context, root string, o TrainOpts) (release func(), err error) {
	common, err := rotastate.CommonDirVia(ctx, e.Git, root)
	if err != nil {
		return nil, err
	}
	me := os.Getpid()
	run := TrainRun{PID: me, RequestID: o.RequestID, Targets: slices.Clone(o.Targets), Base: o.Base, Since: stamp.Format(e.Now())}
	if _, st, ok := e.procs().Stat(me); ok {
		run.Start = st
	}
	path := trainRunFile(common, o.RequestID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := fsio.WriteJSONAtomic(path, run); err != nil {
		return nil, err
	}
	return func() { os.Remove(path) }, nil
}

// readTrainRun reads one marker; ok is false when it is missing, unreadable
// or names a process that is no longer the one that wrote it.
func (e Env) readTrainRun(path string) (TrainRun, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return TrainRun{}, false
	}
	var run TrainRun
	if json.Unmarshal(b, &run) != nil || run.PID <= 0 || !markerLive(e.procs(), run.PID, run.Start) {
		return TrainRun{}, false
	}
	return run, true
}

// FindTrainRun is the live train holding request id, ok false when none does.
func (e Env) FindTrainRun(ctx context.Context, root, id string) (TrainRun, bool, error) {
	e = e.WithDefaults()
	common, err := rotastate.CommonDirVia(ctx, e.Git, root)
	if err != nil {
		return TrainRun{}, false, err
	}
	run, ok := e.readTrainRun(trainRunFile(common, id))
	return run, ok && run.RequestID == id, nil
}

// TrainRuns lists the live trains of the repository, oldest first.
func (e Env) TrainRuns(ctx context.Context, root string) ([]TrainRun, error) {
	e = e.WithDefaults()
	common, err := rotastate.CommonDirVia(ctx, e.Git, root)
	if err != nil {
		return nil, err
	}
	paths, _ := filepath.Glob(rotastate.File(common, trainRunPrefix+"*.json"))
	var out []TrainRun
	for _, p := range paths {
		if run, ok := e.readTrainRun(p); ok {
			out = append(out, run)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Since < out[j].Since })
	return out, nil
}

// ErrTrainNotRunning is CancelTrain's answer for an id no live train holds.
var ErrTrainNotRunning = errors.New("no running train holds this request id")

// CancelTrain sends SIGTERM to the train holding request id, which takes the
// same path as an interrupt: it stops its tests, cleans up and settles its
// receipt as cancelled. It waits up to wait for the process to exit; an error
// that is not ErrTrainNotRunning means it did not stop in time or could not
// be signalled.
func (e Env) CancelTrain(ctx context.Context, root, id string, wait time.Duration) (TrainRun, error) {
	e = e.WithDefaults()
	run, ok, err := e.FindTrainRun(ctx, root, id)
	if err != nil {
		return TrainRun{}, err
	}
	if !ok {
		return TrainRun{}, ErrTrainNotRunning
	}
	if err := syscall.Kill(run.PID, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return run, nil // it exited between the read and the signal
		}
		return run, fmt.Errorf("signal train pid %d: %w", run.PID, err)
	}
	deadline := e.Now().Add(wait)
	for markerLive(e.procs(), run.PID, run.Start) {
		if !e.Now().Before(deadline) {
			return run, fmt.Errorf("train pid %d did not stop within %s; it may still be cleaning up", run.PID, wait)
		}
		select {
		case <-ctx.Done():
			return run, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return run, nil
}
