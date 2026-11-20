package worker

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/landing"
)

// fakeVerifyRun is a verifyRun over an in-memory cache and recording fakes: no
// train, scratch tree or ledger behind it.
func fakeVerifyRun(t *testing.T, members ...string) (*verifyRun, *[]string) {
	t.Helper()
	res := &landing.TrainResult{}
	var heads, tips = []string{}, []string{"t0"}
	for _, m := range members {
		res.Members = append(res.Members, landing.TrainMember{Target: m, Branch: m, PR: "#" + m})
		heads = append(heads, "h-"+m)
		tips = append(tips, "t-"+m)
	}
	var recorded []string
	v := &verifyRun{
		res: res, cache: loadTrainCache(t.TempDir()), ctx: context.Background(),
		baseSHA: "base", heads: heads, tips: tips, n: len(members), passing: len(members),
		phase: func(string, ...any) {},
		record: func(tier string, prs []string, _ VerifyResult) error {
			recorded = append(recorded, tier+":"+strings.Join(prs, ","))
			return nil
		},
	}
	return v, &recorded
}

func TestVerifyRunCachedHitsOnSameBaseAndHeadsOnly(t *testing.T) {
	v, recorded := fakeVerifyRun(t, "a", "b")
	runs := 0
	verify := v.cached("full", func() (VerifyResult, error) { runs++; return VerifyResult{Verified: []string{"go test"}}, nil })
	if _, err := verify(2); err != nil || runs != 1 {
		t.Fatalf("first run: runs=%d err=%v", runs, err)
	}
	r, _ := verify(2)
	if !r.Cached || runs != 1 || !slices.Equal(r.Verified, []string{"go test"}) || len(v.res.CacheHits) != 1 {
		t.Fatalf("same key must hit: %+v runs=%d hits=%v", r, runs, v.res.CacheHits)
	}
	verify(1) // a shorter prefix is another key
	if runs != 2 {
		t.Errorf("a different prefix must miss, runs=%d", runs)
	}
	v.heads[0] = "moved" // a changed head
	verify(2)
	if runs != 3 {
		t.Errorf("a changed head must miss, runs=%d", runs)
	}
	v.baseSHA = "moved" // a changed base
	verify(2)
	if runs != 4 {
		t.Errorf("a changed base must miss, runs=%d", runs)
	}
	if want := []string{"full:#a,#b", "full:#a", "full:#a,#b", "full:#a,#b"}; !slices.Equal(*recorded, want) {
		t.Errorf("recorded %v, want %v (cache hits record nothing)", *recorded, want)
	}
}

func TestVerifyRunCachedKeysAnIntegrationTrainByTip(t *testing.T) {
	v, _ := fakeVerifyRun(t, "a")
	v.tipOf = func(int) string { return "tip" }
	runs := 0
	verify := v.cached("full", func() (VerifyResult, error) { runs++; return VerifyResult{Verified: []string{"x"}}, nil })
	verify(1)
	v.heads[0] = "moved" // heads do not enter the key
	verify(1)
	if runs != 1 {
		t.Errorf("an integration key is the tip alone, runs=%d", runs)
	}
}

func TestVerifyRunCachedNeverCachesARedAndSplitsDiagnoseTime(t *testing.T) {
	v, _ := fakeVerifyRun(t, "a", "b")
	runs := 0
	red := v.cached("full", func() (VerifyResult, error) { runs++; return VerifyResult{Failed: []string{"go test"}}, nil })
	red(1)
	red(1)
	if runs != 2 {
		t.Errorf("a red must re-verify, runs=%d", runs)
	}
	if v.lastVerify != 0 {
		t.Error("a prefix run is a diagnosis, not the full-tree verify")
	}
	red(2)
	if v.lastVerify == 0 {
		t.Error("the full-tree run sets the diagnose estimate's unit")
	}
}

func TestVerifyRunCachedCancelledRedIsNoFinding(t *testing.T) {
	v, recorded := fakeVerifyRun(t, "a")
	ctx, cancel := context.WithCancel(context.Background())
	v.ctx = ctx
	verify := v.cached("full", func() (VerifyResult, error) {
		cancel()
		return VerifyResult{Failed: []string{"go test"}, LogPath: "/log"}, nil
	})
	_, err := verify(1)
	if !errors.Is(err, context.Canceled) || v.res.Log != "/log" || len(*recorded) != 0 {
		t.Fatalf("err=%v log=%q recorded=%v", err, v.res.Log, *recorded)
	}
}

func TestVerifyRunBlameOnlyNamedCulpritOnAFailedVerdict(t *testing.T) {
	v, _ := fakeVerifyRun(t, "a", "b")
	v.res.Members[1].Culprit = true
	v.blame("full") // verdict not failed yet
	if len(v.cache.f.Culprits) != 0 {
		t.Fatalf("no blame without a failed verdict: %+v", v.cache.f.Culprits)
	}
	v.res.Verdict = landing.GateVerifyFailed
	v.blame("full")
	want := trainCacheCulprit{Head: "h-b", Key: trainKey("full", "base", []string{"h-a", "h-b"})}
	if got := v.cache.f.Culprits["b"]; got != want || len(v.cache.f.Culprits) != 1 {
		t.Errorf("culprits %+v, want b=%+v", v.cache.f.Culprits, want)
	}
}

func TestVerifyRunForgiveNotesTransientOnce(t *testing.T) {
	v, _ := fakeVerifyRun(t, "a", "b")
	v.cache.blame("b", "h-b", "k")
	v.cache.blame("a", "other-head", "k") // blamed at another head: stays
	v.forgive()
	if !slices.Equal(v.res.Transient, []string{"b"}) || len(v.res.Notes) != 1 || !strings.Contains(v.res.Notes[0], "TRANSIENT b") {
		t.Fatalf("%+v", v.res)
	}
	if _, kept := v.cache.f.Culprits["a"]; !kept {
		t.Error("a culprit at another head must stay")
	}
	v.forgive()
	if len(v.res.Transient) != 1 {
		t.Error("forgiving clears the record")
	}
}

// bisectFakes wires the collaborators and counts which ran.
type bisectFakes struct{ stopped, reran, bisected int }

func (f *bisectFakes) wire(v *verifyRun, passing int, culprit int) {
	v.stop = func(string, VerifyResult) { f.stopped++ }
	v.rerun = func(string, VerifyResult) (bool, error) { f.reran++; return false, nil }
	v.bisectTrain = func(string, func(int) (VerifyResult, error), VerifyResult) (int, bool, error) {
		f.bisected++
		if culprit >= 0 {
			v.res.Members[culprit].Culprit = true
			v.res.Culprit, v.res.Verdict = v.res.Members[culprit].Target, landing.GateVerifyFailed
		}
		return passing, false, nil
	}
}

func TestVerifyRunBisectRecordsOriginalOnceAndBlamesTheCulprit(t *testing.T) {
	v, _ := fakeVerifyRun(t, "a", "b", "c")
	var f bisectFakes
	f.wire(v, 1, 1)
	stop, err := v.bisect("full", nil, VerifyResult{Failed: []string{"go test"}}, "full")
	if err != nil || stop || v.passing != 1 || f.bisected != 1 || f.reran != 1 {
		t.Fatalf("stop=%v err=%v passing=%d %+v", stop, err, v.passing, f)
	}
	if v.res.Original == nil || v.res.Original.Tier != "full" || v.res.Original.Members != 3 {
		t.Errorf("original failure: %+v", v.res.Original)
	}
	if got := v.cache.f.Culprits["b"]; got.Head != "h-b" {
		t.Errorf("culprit not blamed: %+v", v.cache.f.Culprits)
	}
	first := v.res.Original
	f.wire(v, 1, -1)
	v.bisect("e2e", nil, VerifyResult{Failed: []string{"other"}}, "e2e")
	if v.res.Original != first {
		t.Error("the first failure stays the original evidence")
	}
}

func TestVerifyRunBisectOnFailStopSkipsDiagnosis(t *testing.T) {
	v, _ := fakeVerifyRun(t, "a", "b")
	var f bisectFakes
	f.wire(v, 0, 0)
	v.onFailStop = true
	stop, err := v.bisect("full", nil, VerifyResult{Failed: []string{"x"}}, "full")
	if err != nil || !stop || f.stopped != 1 || f.reran != 0 || f.bisected != 0 || len(v.cache.f.Culprits) != 0 || v.passing != 2 {
		t.Fatalf("stop=%v err=%v %+v passing=%d", stop, err, f, v.passing)
	}
}

func TestVerifyRunBisectFlakyRerunEndsBeforeTheBisect(t *testing.T) {
	v, _ := fakeVerifyRun(t, "a", "b")
	var f bisectFakes
	f.wire(v, 0, 0)
	v.rerun = func(string, VerifyResult) (bool, error) { return true, nil }
	stop, err := v.bisect("full", nil, VerifyResult{Failed: []string{"x"}}, "full")
	if err != nil || !stop || f.bisected != 0 || len(v.cache.f.Culprits) != 0 {
		t.Fatalf("stop=%v err=%v %+v", stop, err, f)
	}
}

func TestVerifyRunBisectCancelledBlamesNobody(t *testing.T) {
	v, _ := fakeVerifyRun(t, "a", "b")
	var f bisectFakes
	f.wire(v, 1, 1)
	ctx, cancel := context.WithCancel(context.Background())
	v.ctx = ctx
	cancel()
	v.bisect("full", nil, VerifyResult{Failed: []string{"x"}}, "full")
	if len(v.cache.f.Culprits) != 0 {
		t.Errorf("a cancelled bisect must not blame: %+v", v.cache.f.Culprits)
	}
}
