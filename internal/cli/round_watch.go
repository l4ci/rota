package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"os"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/escalation"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/roundtick"
	"github.com/l4ci/rota/internal/roundwatch"
	"github.com/l4ci/rota/internal/worker"
)

// roundWatch is `rota round watch`: `round wait` for a session that must stay
// free. It is meant to run in the background, one at a time; the harness wakes
// the orchestrator when it exits, and the orchestrator re-arms it.
func roundWatch(fs *flag.FlagSet) RunFunc {
	heartbeat := fs.Float64("heartbeat", 600, "seconds after which the watch returns even if nothing changed")
	poll := fs.Float64("poll", 30, "seconds between registry checks")
	forgePoll := fs.Float64("forge-poll", 120, "seconds between forge checks (escalation answers, PR states); 0 never asks the forge")
	settle := fs.Float64("settle", 5, "seconds between the two pane captures of one classification")
	lines := fs.Int("lines", 60, "pane lines to classify")
	autopilot := fs.Bool("autopilot", false, "run a round tick on every wake and wake the orchestrator only for something new (needs round.autopilot)")
	base := fs.String("base", "", "with --autopilot: the cycle branch PRs merge into (default each PR's recorded base)")
	pid := fs.Int("holder-pid", 0, "with --autopilot: orchestrator pid for the lease, when its ancestry cannot be read")
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
		set, err := roundcfg.Load(root)
		if err != nil {
			return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
		}
		if *autopilot && !set.Autopilot {
			return Result{}, Refused("round.autopilot is off").WithHint("rota config set round.autopilot true")
		}
		cd, err := rotastate.CommonDir(root)
		if err != nil {
			return Result{}, Resolution("%v", err)
		}
		lenv := c.deps().LeaseEnv()
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
		wenv := workerEnvCtx(c, ctx)
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
				var we *exitcode.Error
				if errors.As(err, &we) && we.Exit == exitcode.ExitResolution {
					return nil, roundwatch.ErrNothingToWatch
				}
				if err != nil {
					return nil, err
				}
				if res.TimedOut {
					return nil, nil
				}
				return &roundwatch.SlotNews{Slot: res.Slot, State: strings.ToLower(res.State), Evidence: res.Evidence, Source: res.Source}, nil
			},
			Local: func() map[string]string { return roundwatch.LocalSnapshot(root) },
			Forge: func(ctx context.Context) map[string]string { return watchForge(ctx, c, root) },
		}
		opts := roundwatch.Opts{Heartbeat: secs(*heartbeat), Poll: secs(*poll), ForgeEvery: secs(*forgePoll)}
		if !*autopilot {
			res, err := roundwatch.Run(ctx, env, opts)
			if err != nil {
				return Result{}, err
			}
			return watchResult(c, root, cd, lenv, res, nil, ""), nil
		}
		return autopilotWatch(c, ctx, env, opts, set, root, cd, lenv, *base, *pid, secs(*poll))
	}
}

func secs(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }

// watchForge asks the forge what the registry cannot know: it records answered
// escalations (as `round escalate check` does) and reads each slot's PR state
// from `round status`. A forge that cannot be reached leaves the entries out,
// which reads as no change.
func watchForge(ctx context.Context, c *Ctx, root string) map[string]string {
	out := map[string]string{}
	env := c.deps().escalationEnv()
	if env.Forge == nil {
		env.Forge = escalationForge(c)
	}
	if res, err := escalation.Check(ctx, env, root, nil); err == nil {
		for _, r := range res.Reports {
			out[roundwatch.EscalationStatusKey(r.Entry.ID)] = r.Status
		}
	}
	if rep, err := c.deps().RoundEnv(ctx, root).Status(ctx, root); err == nil {
		for _, r := range rep.Rows {
			if r.PR != "" && r.PRState != "" {
				out[roundwatch.PRStateKey(r.Name)] = r.PRState
			}
		}
	}
	return out
}

func watchResult(c *Ctx, root, cd string, lenv roundlease.Env, res roundwatch.Result, tick *roundtick.Result, stopped string) Result {
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
	if tick != nil {
		d.Set("autopilot", tickData(*tick))
		if l := tickLines(*tick); l != "nothing to do" {
			text = append(text, l)
		}
	}
	if stopped != "" {
		d.Set("autopilotStopped", stopped)
		text = append(text, "autopilot\tstopped\t"+stopped)
	}
	if f, ok := staleBinaryOncePerRound(c, cd, round); ok {
		d.Set("staleBinary", staleData(f))
		text = append(text, "warning\t"+f.Detail()+"; rebuild: "+f.Rebuild)
	}
	if len(text) == 0 {
		text = append(text, res.Reason+"\t"+digest)
	}
	return Result{Data: d, Text: strings.Join(text, "\n")}
}

// autopilotWatch is `round watch --autopilot`: tick first, then wait, tick on
// every wake, and return only for something the orchestrator has not seen, a
// stop (wind-down or a lost lease), or the heartbeat. The watch is the
// autopilot's heartbeat, so a round running it still has its watch armed.
func autopilotWatch(c *Ctx, ctx context.Context, env roundwatch.Env, opts roundwatch.Opts, set roundcfg.Settings, root, cd string, lenv roundlease.Env, base string, pid int, poll time.Duration) (Result, error) {
	c.ctx = ctx
	deadline := lenv.Now().Add(opts.Heartbeat)
	for {
		tr, terr := autopilotTick(c, root, set, base, pid)
		left := deadline.Sub(lenv.Now())
		var tp *roundtick.Result
		if terr == nil {
			tp = &tr
		}
		switch {
		case errors.Is(terr, errAutopilotStopped):
			return watchResult(c, root, cd, lenv, roundwatch.Result{Reason: "stopped"}, nil, "no round lease: the round wound down or the lease was lost"), nil
		case terr != nil:
			return watchResult(c, root, cd, lenv, roundwatch.Result{Reason: roundwatch.ReasonChange}, nil, "tick failed: "+terr.Error()), nil
		case ctx.Err() != nil:
			return watchResult(c, root, cd, lenv, roundwatch.Result{Reason: roundwatch.ReasonInterrupt}, tp, ""), nil
		case len(tr.New) > 0:
			return watchResult(c, root, cd, lenv, roundwatch.Result{Reason: roundwatch.ReasonChange}, tp, ""), nil
		case left <= 0:
			return watchResult(c, root, cd, lenv, roundwatch.Result{Reason: roundwatch.ReasonHeartbeat}, tp, ""), nil
		}
		o := opts
		o.Heartbeat = left
		res, err := roundwatch.Run(ctx, env, o)
		if err != nil {
			return Result{}, err
		}
		if res.Reason == roundwatch.ReasonHeartbeat || res.Reason == roundwatch.ReasonInterrupt {
			if t2, err := autopilotTick(c, root, set, base, pid); err == nil {
				tp = &t2
			}
			return watchResult(c, root, cd, lenv, res, tp, ""), nil
		}
		// A slot the orchestrator already knows about wakes Run at once: the
		// pause keeps the loop from spinning on it.
		env.Sleep(ctx, poll)
	}
}
