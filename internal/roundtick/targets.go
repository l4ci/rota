package roundtick

import (
	"context"

	"github.com/l4ci/rota/internal/postgate"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/worker"
)

// GateRun is the gate result and the bookkeeping that followed it. Refused
// distinguishes an expected refusal from a run that died on an error.
type GateRun struct {
	Result  worker.GateResult
	Err     error
	Refused bool
	BookErr error
}

// TrainRun is the train result and any failed bookkeeping steps.
type TrainRun struct {
	Result  worker.TrainResult
	Err     error
	Refused bool
	Book    []postgate.Failure
}

// gateOutcome interprets a gate run for one target. An empty Verdict on a
// failure reads as "gate-error".
type gateOutcome struct {
	Landed  bool
	Verdict string
	Detail  string
}

// trainOutcome interprets a train run. Landed names the members that merged,
// Culprit the member that broke the train, if the train names one.
type trainOutcome struct {
	Landed  []string
	Culprit string
	Verdict string
	Detail  string
	// Done is true when the whole train landed.
	Done bool
}

func (e Env) targetBase(target string) string {
	if e.BaseOverride != "" {
		return e.BaseOverride
	}
	if e.Target != nil {
		if t, err := e.Target(target); err == nil && t.Base != "" {
			return t.Base
		}
	}
	// The gate refuses a corrupt registry itself; this only picks its base.
	return e.Base
}

func (e Env) bounceCount(target string, err error) {
	if e.Say != nil {
		e.Say("BOUNCE-COUNT " + target + " — " + err.Error())
	}
}

func (e Env) gateTarget(ctx context.Context, target, base string) gateOutcome {
	run := e.RunGate(ctx, target, base)
	if run.Err == nil && !run.Refused {
		if run.BookErr != nil {
			e.bounceCount(target, run.BookErr)
		}
		if run.Result.OK() {
			return gateOutcome{Landed: true}
		}
		return gateOutcome{Verdict: run.Result.Verdict, Detail: strutil.FirstLine(run.Result.Err)}
	}
	return gateOutcome{Verdict: failedVerdict(run.Result.Verdict, run.Err, run.Refused), Detail: failureDetail(run.Err, run.Result.Err)}
}

func (e Env) trainTargets(ctx context.Context, targets []string, base string) trainOutcome {
	run := e.RunTrain(ctx, targets, base)
	r := run.Result
	if run.Err == nil && !run.Refused {
		for _, f := range run.Book {
			e.bounceCount(f.Target, f.Err)
		}
		if r.OK() {
			return trainOutcome{Done: true}
		}
	}
	out := trainOutcome{Verdict: failedVerdict(r.Verdict, run.Err, run.Refused), Detail: failureDetail(run.Err, r.Err)}
	if run.Err != nil && !run.Refused {
		return out // died on an error: nothing of the result is reported
	}
	out.Culprit = r.Culprit
	for _, m := range r.Members {
		if m.Landed {
			out.Landed = append(out.Landed, m.Target)
		}
	}
	return out
}

// failedVerdict keeps a refusal's verdict, but drops a result from a run
// that died on an unexpected error.
func failedVerdict(verdict string, err error, refused bool) string {
	if err != nil && !refused {
		return ""
	}
	return verdict
}

func failureDetail(err error, msg string) string {
	if err != nil {
		return strutil.FirstLine(err.Error())
	}
	return strutil.FirstLine(msg)
}
