package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/escalation"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/round"
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
	autopilot := fs.Bool("autopilot", false, "run a round tick on every wake and wake the orchestrator only for something new (needs autonomy.level auto)")
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
		if worker.IsSolo(root) {
			return Result{}, &Error{Exit: ExitUnavailable, Message: "a solo round has no panes to watch",
				Hint: "solo workers report through rota round report; call rota round wait"}
		}
		set, err := c.deps().roundSettings(root, roundConfigUsage)
		if err != nil {
			return Result{}, err
		}
		if *autopilot && !set.Autopilot {
			return Result{}, Refused("the autopilot runs only under autonomy.level auto").WithHint("rota config set autonomy.level auto")
		}
		cd, err := rotastate.CommonDirVia(c.Context(), c.deps().Git, root)
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
				res, err := wenv.Wait(ctx, root, worker.WaitOpts{Timeout: d + secs(*settle) + 2*time.Second, Settle: secs(*settle), Lines: *lines, UnverifiableAfter: unverifiableAfter(c, root)})
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
		env.Seen, env.Mark = watchSeen(cd)
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
// escalations (as `round escalate check` does), reads each slot's PR state
// from `round status` and looks for review input on each done slot's PR (under
// round.reviewLoop auto it relays that input to the worker instead). A forge that cannot be reached leaves the entries out,
// which reads as no change.
func watchForge(ctx context.Context, c *Ctx, root string) map[string]string {
	out := map[string]string{}
	c.deps().freshReads()
	env := c.deps().escalationEnv()
	if env.Forge == nil {
		env.Forge = escalationForge(c)
	}
	if res, err := escalation.Check(ctx, env, root, nil); err == nil {
		for _, r := range res.Reports {
			out[roundwatch.EscalationStatusKey(r.Entry.ID)] = r.Status
		}
	}
	renv := c.deps().RoundEnv(ctx, root)
	merged := map[string]bool{}
	if rep, err := renv.Status(ctx, root); err == nil { // a bad round.* key is noted once by the config load below
		for _, r := range rep.Rows {
			if r.PR != "" && r.PRState != "" {
				out[roundwatch.PRStateKey(r.Name)] = r.PRState
				merged[r.Name] = r.PRState == "merged"
			}
		}
		if err := markMergedDone(root, merged); err != nil && !c.mergedMarkNoted {
			c.mergedMarkNoted = true
			c.Warn("merged-PR mark skipped: registry write failed: %v", err)
		}
	}
	set, err := c.deps().roundSettings(root, roundConfigUsage)
	if err != nil {
		if !c.reviewSkipNoted {
			c.reviewSkipNoted = true
			c.Warn("review poll skipped: round config does not load: %v", err)
		}
		return out
	}
	// Tolerant: the watch poll cannot refuse; a corrupt registry reads as no slots.
	for _, s := range worker.LoadRegistryTolerant(root).Slots() {
		if s.PR() == "" || !worker.IsDone(s.State()) {
			continue
		}
		if o := reviewStep(c, root, set, s.Name()); o.Pending {
			out[roundwatch.ReviewKey(s.Name())] = o.Detail
		}
	}
	for k, v := range supportSnapshot(ctx, renv, root) {
		out[k] = v
	}
	return out
}

// markMergedDone records on each done slot whether the forge shows its PR
// merged, so the prompt digest and the Stop hook stop asking for a slot that
// waits on nothing (#1843). A slot absent from merged is left as it is.
func markMergedDone(root string, merged map[string]bool) error {
	var first error
	for _, s := range worker.LoadRegistryTolerant(root).Slots() {
		m, ok := merged[s.Name()]
		if !ok || !worker.IsDone(s.State()) || s.PRMerged() == m {
			continue
		}
		if _, err := worker.UpdateSlot(root, s.Name(), func(s *worker.Slot) {
			if worker.IsDone(s.State()) {
				s.SetPRMerged(m)
			}
		}); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func watchResult(c *Ctx, root, cd string, lenv roundlease.Env, res roundwatch.Result, tick *roundtick.Result, stopped string) Result {
	roundNumber := 0
	if l, _, err := lenv.Read(cd); err == nil {
		roundNumber = l.Round
	}
	d := jsonx.NewObject()
	d.Set("reason", res.Reason)
	d.Set("waited", res.Waited.Seconds())
	d.Set("round", roundNumber)
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
		if id, kind, to, ok := roundwatch.SupportChange(ch); ok {
			o.Set("issue", id)
			o.Set("kind", kind)
			changes = append(changes, o)
			text = append(text, fmt.Sprintf("support: %s %s %s", id, kind, to))
			continue
		}
		changes = append(changes, o)
		if ch.Key == roundwatch.GateKey && ch.To == "" {
			text = append(text, fmt.Sprintf("gate cleared (was %s): assign now", ch.From))
			continue
		}
		text = append(text, fmt.Sprintf("change\t%s\t%s -> %s", ch.Key, dash(ch.From), dash(ch.To)))
	}
	d.Set("changes", changes)
	digest := roundwatch.ExitDigest(root, roundNumber)
	d.Set("digest", digest)
	if tick != nil {
		// Status and explicit ticks keep the full drift list. The watch
		// reports a missing PR once, then again only when it becomes news.
		visible := *tick
		visible.NeedsYou = nil
		for _, it := range tick.NeedsYou {
			if it.Kind == "drift" && strings.HasPrefix(it.Target, round.BranchNoPR+" ") && !slices.Contains(tick.New, it) {
				continue
			}
			visible.NeedsYou = append(visible.NeedsYou, it)
		}
		d.Set("autopilot", tickData(visible))
		if l := tickLines(visible); l != "nothing to do" {
			text = append(text, l)
		}
	}
	if n := res.Slot; n != nil && tick == nil { // a running autopilot assigns by itself
		if an := assignNudgeFor(c, root, n.Slot); an != nil {
			d.Set("assignNudge", an.data())
			text = append(text, an.Text())
		}
	}
	text = addTrainNudge(c, root, d, text)
	if stopped != "" {
		d.Set("autopilotStopped", stopped)
		text = append(text, "autopilot\tstopped\t"+stopped)
	}
	f, stale, serr := staleBinaryOncePerRound(c, cd, roundNumber)
	if stale {
		d.Set("staleBinary", staleData(f))
		text = append(text, "warning\t"+f.Detail()+"; rebuild: "+f.Rebuild)
	}
	if serr != nil {
		d.Set("staleBinaryError", serr.Error())
		text = append(text, "warning\tstale-binary check: "+serr.Error())
	}
	if len(text) == 0 || res.Reason == roundwatch.ReasonHeartbeat {
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

// watchSeenFile keeps what the support entries said when a watch last
// returned them (roundwatch.Env.Seen); it lives beside the watch marker.
const watchSeenFile = "round-watch-seen.json"

// watchSeen reads and extends the seen record of the support entries under
// the common dir cd. Mark merges, never drops: an entry that vanishes for a
// poll and comes back unchanged is not news.
func watchSeen(cd string) (seen func() map[string]string, mark func(map[string]string)) {
	path := rotastate.File(cd, watchSeenFile)
	load := func() map[string]string {
		out := map[string]string{}
		if b, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(b, &out)
		}
		return out
	}
	return load, func(m map[string]string) {
		cur := load()
		for k, v := range m {
			cur[k] = v
		}
		// A failed write only repeats a wake.
		if b, err := json.Marshal(cur); err == nil && os.MkdirAll(filepath.Dir(path), 0o755) == nil {
			_ = fsio.WriteFileAtomic(path, append(b, '\n'))
		}
	}
}
