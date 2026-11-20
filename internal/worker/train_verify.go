package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/l4ci/rota/internal/landing"
)

// verifyRun is the train's verify policy: the verdict cache, the blame record
// and the bisect hand-off, with the state they share held in fields instead of
// captured by closures. It reaches the process, the scratch tree and the
// ledgers only through the funcs below, so the cache, flake and bisect rules run
// in a test with fakes and no train fixture. verifyStage builds one per run.
type verifyRun struct {
	res         *landing.TrainResult
	cache       *trainCache
	ctx         context.Context
	baseSHA     string
	heads, tips []string
	// tipOf is set for an integration train: its verdict key is the verified
	// tip, since the last tip carries the base merge.
	tipOf func(k int) string
	n     int // members in the full train

	// Collaborators.
	phase func(format string, args ...any)
	// record writes the ledger rows of one verify run (verification, flakes).
	record func(tier string, prs []string, r VerifyResult) error
	// onFailStop ends a red tier without diagnosis; stop writes that answer.
	onFailStop bool
	stop       func(tier string, failed VerifyResult)
	// rerun re-runs a red tier once; done means it was a flake and res holds it.
	rerun func(tier string, failed VerifyResult) (done bool, err error)
	// bisectTrain walks prefixes with run and writes the verdict into res.
	bisectTrain func(tier string, run func(int) (VerifyResult, error), failed VerifyResult) (passing int, stop bool, err error)

	// State the policy writes.
	passing    int           // leading members that may still land
	lastVerify time.Duration // the last full-tree verify, the diagnose estimate's unit
}

// cached wraps run as a verify of the base plus its first k members: a hit in
// the cache answers it, a miss runs it, times it, records it and caches a pass.
func (v *verifyRun) cached(tier string, run func() (VerifyResult, error)) func(int) (VerifyResult, error) {
	return func(k int) (VerifyResult, error) {
		key := trainKey(tier, v.baseSHA, v.heads[:k])
		if v.tipOf != nil {
			key = trainKey(tier, v.tipOf(k), nil)
		}
		if hit, ok := v.cache.get(key); ok {
			v.res.CacheHits = append(v.res.CacheHits, fmt.Sprintf("%s: base + first %d member(s)", tier, k))
			return VerifyResult{Cached: true, Verified: hit.Verified}, nil
		}
		diagnosing := k != v.n
		verb := "verify"
		if diagnosing {
			verb = "diagnose"
		}
		v.phase("%s %s: base + %d member(s), tip %s", verb, tier, k, tipAt(v.tips, k))
		began := time.Now()
		r, err := run()
		if diagnosing {
			v.res.DiagnoseTime += time.Since(began)
		} else {
			v.res.VerifyTime += time.Since(began)
			v.lastVerify = time.Since(began)
		}
		if err == nil && v.ctx.Err() != nil && !r.OK() {
			// A command the cancel stopped is no finding: keep its log, run no
			// bisect, flake record or cache write on it.
			v.res.Log = r.LogPath
			return r, v.ctx.Err()
		}
		if err == nil {
			prs := []string{}
			for _, m := range v.res.Members[:k] {
				if m.PR != "" {
					prs = append(prs, m.PR)
				}
			}
			err = v.record(tier, prs, r)
			// Prefix verifies run inside bisect, outside the tier walk.
			if diagnosing {
				for _, f := range r.Flaky {
					v.res.Flaky = append(v.res.Flaky, f)
					v.res.Notes = append(v.res.Notes, flakeNote(f))
				}
			}
		}
		if err == nil {
			v.cache.put(key, r)
		}
		return r, err
	}
}

// blame remembers the member the bisect named, to recognise it as flaky later.
func (v *verifyRun) blame(tier string) {
	for i, m := range v.res.Members {
		if m.Culprit && v.res.Verdict == landing.GateVerifyFailed {
			v.cache.blame(m.Branch, v.heads[i], trainKey(tier, v.baseSHA, v.heads[:i+1]))
		}
	}
}

// forgive marks every member an earlier train blamed at this very head as
// transient: the failure did not reproduce on a different combination.
func (v *verifyRun) forgive() {
	for i, m := range v.res.Members {
		if v.cache.forgive(m.Branch, v.heads[i]) {
			v.res.Transient = append(v.res.Transient, m.Target)
			v.res.Notes = append(v.res.Notes, fmt.Sprintf("TRANSIENT %s — named culprit by an earlier train, but this train with it passes verification; treated as flaky, not blamed again", m.Target))
		}
	}
}

// bisect names the culprit of a red tier; it reports whether the train ends.
// run verifies a prefix of the train, key is the tier name the blame is cached
// under.
func (v *verifyRun) bisect(tier string, run func(int) (VerifyResult, error), failed VerifyResult, key string) (stop bool, err error) {
	if v.res.Original == nil {
		orig := failureOf(tier, failed, v.n, tipAt(v.tips, v.n))
		v.res.Original = &orig
	}
	if v.onFailStop {
		v.stop(tier, failed)
		return true, nil
	}
	// The failure is readable now, before the re-run and the diagnosis spend
	// minutes: an orchestrator can cancel instead of waiting (#3687).
	v.phase("verify failed: %s", failedSummary(failed))
	// A red the same tree does not show twice is a flake, not a culprit: re-run
	// before paying for a bisect (#3647).
	if done, err := v.rerun(tier, failed); done || err != nil {
		return done, err
	}
	v.phase("%s", diagnoseEstimate(v.n, v.lastVerify))
	v.passing, stop, err = v.bisectTrain(tier, run, failed)
	if v.ctx.Err() == nil {
		v.blame(key)
	}
	return stop, err
}
