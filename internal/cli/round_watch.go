package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/escalation"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/roundwatch"
	"github.com/l4ci/rota/internal/worker"
)

// watchEnv is the process side of `rota round watch`; tests replace it.
var watchEnv = func() roundlease.Env { return roundlease.DefaultEnv() }

// roundWatch is `rota round watch`: `round wait` for a session that must stay
// free. It is meant to run in the background, one at a time; the harness wakes
// the orchestrator when it exits, and the orchestrator re-arms it.
func roundWatch(fs *flag.FlagSet) RunFunc {
	heartbeat := fs.Float64("heartbeat", 600, "seconds after which the watch returns even if nothing changed")
	poll := fs.Float64("poll", 30, "seconds between registry checks")
	forgePoll := fs.Float64("forge-poll", 120, "seconds between forge checks (escalation answers, PR states); 0 never asks the forge")
	settle := fs.Float64("settle", 5, "seconds between the two pane captures of one classification")
	lines := fs.Int("lines", 60, "pane lines to classify")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		if *heartbeat <= 0 || *poll <= 0 {
			return Result{}, Usage("--heartbeat and --poll must be positive")
		}
		if *forgePoll < 0 || *settle < 0 {
			return Result{}, Usage("--forge-poll and --settle must not be negative")
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		if worker.RegistryHost(root) == host.Solo {
			return Result{}, &Error{Exit: ExitUnavailable, Message: "a solo round has no panes to watch",
				Hint: "solo workers report through rota round report; call rota round wait"}
		}
		cd, err := roundlease.CommonDir(root)
		if err != nil {
			return Result{}, Resolution("%v", err)
		}
		lenv := watchEnv()
		release, err := roundwatch.Arm(lenv, cd, os.Getpid(), secs(*heartbeat))
		if err != nil {
			var ae *roundwatch.ArmedError
			if errors.As(err, &ae) {
				return Result{}, Refused("%s", ae.Error()).WithHint("one watch is enough: leave it running")
			}
			return Result{}, err
		}
		defer release()

		ctx, stop := workerContext()
		defer stop()
		wenv := workerEnvCtx(ctx)
		env := roundwatch.Env{
			Now: lenv.Now,
			Sleep: func(ctx context.Context, d time.Duration) {
				select {
				case <-ctx.Done():
				case <-time.After(d):
				}
			},
			Wait: func(ctx context.Context, d time.Duration) (*roundwatch.SlotNews, error) {
				// The timeout covers one whole classification (its settle gap),
				// or a wait that ends mid-capture would never see a slot.
				res, err := wenv.Wait(ctx, root, worker.WaitOpts{Timeout: d + secs(*settle) + 2*time.Second, Settle: secs(*settle), Lines: *lines})
				var we *worker.Error
				if errors.As(err, &we) && we.Exit == worker.ExitResolution {
					return nil, roundwatch.ErrNothingToWatch
				}
				if err != nil {
					return nil, fromWorker(err)
				}
				if res.TimedOut {
					return nil, nil
				}
				return &roundwatch.SlotNews{Slot: res.Slot, State: strings.ToLower(res.State), Evidence: res.Evidence, Source: res.Source}, nil
			},
			Local: func() map[string]string { return roundwatch.LocalSnapshot(root) },
			Forge: func(ctx context.Context) map[string]string { return watchForge(ctx, root) },
		}
		res, err := roundwatch.Run(ctx, env, roundwatch.Opts{Heartbeat: secs(*heartbeat), Poll: secs(*poll), ForgeEvery: secs(*forgePoll)})
		if err != nil {
			return Result{}, err
		}
		return watchResult(root, cd, lenv, res), nil
	}
}

func secs(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }

// watchForge asks the forge what the registry cannot know: it records answered
// escalations (as `round escalate check` does) and reads each slot's PR state
// from `round status`. A forge that cannot be reached leaves the entries out,
// which reads as no change.
func watchForge(ctx context.Context, root string) map[string]string {
	out := map[string]string{}
	env := escalationEnv()
	if env.Forge == nil {
		env.Forge = escalationForge()
	}
	if res, err := escalation.Check(ctx, env, root, nil); err == nil {
		for _, r := range res.Reports {
			out[roundwatch.EscalationStatusKey(r.Entry.ID)] = r.Status
		}
	}
	if rep, err := roundEnv(ctx, root).Status(ctx, root); err == nil {
		for _, r := range rep.Rows {
			if r.PR != "" && r.PRState != "" {
				out[roundwatch.PRStateKey(r.Name)] = r.PRState
			}
		}
	}
	return out
}

func watchResult(root, cd string, lenv roundlease.Env, res roundwatch.Result) Result {
	round := 0
	if l, _, err := lenv.Read(cd); err == nil {
		round = l.Round
	}
	d := jsonx.NewObject()
	d.Set("reason", res.Reason)
	d.Set("waited", res.Waited.Seconds())
	d.Set("round", round)
	var text []string
	if n := res.Slot; n != nil {
		d.Set("slot", n.Slot)
		d.Set("state", n.State)
		d.Set("evidence", n.Evidence)
		d.Set("source", n.Source)
		text = append(text, fmt.Sprintf("slot\t%s\t%s\t%s", n.Slot, n.State, n.Evidence))
	}
	changes := make([]any, 0, len(res.Changes))
	for _, ch := range res.Changes {
		o := jsonx.NewObject()
		o.Set("key", ch.Key)
		o.Set("from", ch.From)
		o.Set("to", ch.To)
		changes = append(changes, o)
		text = append(text, fmt.Sprintf("change\t%s\t%s -> %s", ch.Key, dash(ch.From), dash(ch.To)))
	}
	d.Set("changes", changes)
	digest := roundwatch.Digest(root, round, false)
	d.Set("digest", digest)
	if len(text) == 0 {
		text = append(text, res.Reason+"\t"+digest)
	}
	return Result{Data: d, Text: strings.Join(text, "\n")}
}
