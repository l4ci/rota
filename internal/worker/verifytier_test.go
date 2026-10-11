package worker

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/testledger"
)

func TestTierOutcomeOfEmptyFullSaysNoVerify(t *testing.T) {
	out := tierOutcomeOf(tierFull, "s1", VerifyResult{NoCommands: true})
	if !out.NoCommands || !out.OK || len(out.Verified) != 0 {
		t.Fatalf("empty tier: %+v", out)
	}
	if len(out.Notes) != 2 || out.Notes[0] != noVerifyNote("s1") || !strings.HasPrefix(out.Notes[0], "NO-VERIFY s1 —") {
		t.Errorf("notes %q", out.Notes)
	}
}

func TestTierOutcomeOfLabelsFailuresByTier(t *testing.T) {
	vr := VerifyResult{Verified: []string{"a"}, Failed: []string{"b"}, Excluded: []testledger.Entry{{}}}
	for tier, want := range map[string]string{tierFull: "verify FAILED: b", tierE2E: "e2e FAILED: b"} {
		out := tierOutcomeOf(tier, "train", vr)
		if out.OK || out.NoCommands || !reflect.DeepEqual(out.Notes, []string{want}) {
			t.Errorf("%s: %+v", tier, out)
		}
		if !reflect.DeepEqual(out.Verified, vr.Verified) || len(out.Excluded) != 1 {
			t.Errorf("%s: verified/excluded lost: %+v", tier, out)
		}
	}
}

// verifyStageRun builds a trainRun that stops short of git: the verify stage
// sees a two-member scratch tree, a fake test.full and a counting test.e2e shell.
func verifyStageRun(full VerifyResult, e2e []string, ran *[]string) *trainRun {
	e := Env{Shell: func(_ context.Context, _, cmd string) (string, int) {
		*ran = append(*ran, cmd)
		return "", 0
	}}.withDefaults()
	in := gateInput{}
	in.cfg.E2E = e2e
	return &trainRun{
		e: e, ctx: context.Background(), cache: loadTrainCache(t0()), in: in, baseSHA: "base",
		heads: []string{"h1", "h2"}, tips: []string{"t0", "t1", "t2"}, scratch: "scratch",
		verify: func(string) (VerifyResult, error) { return full, nil },
		res:    TrainResult{Members: []TrainMember{{Target: "a"}, {Target: "b"}}},
	}
}

func t0() string { return "/nonexistent-rota-root" }

func TestVerifyStageRunsE2EAfterFullGreen(t *testing.T) {
	var ran []string
	tr := verifyStageRun(VerifyResult{Verified: []string{"go test"}}, []string{"e2e-cmd"}, &ran)
	done, err := tr.verifyStage()
	if done || err != nil {
		t.Fatalf("done=%v err=%v res=%+v", done, err, tr.res)
	}
	if tr.passing != 2 || !reflect.DeepEqual(tr.res.Verified, []string{"go test"}) || !reflect.DeepEqual(tr.res.E2EVerified, []string{"e2e-cmd"}) {
		t.Errorf("passing %d, res %+v", tr.passing, tr.res)
	}
	if !reflect.DeepEqual(ran, []string{"e2e-cmd"}) || len(tr.res.Notes) != 0 {
		t.Errorf("ran %v notes %v", ran, tr.res.Notes)
	}
}

func TestVerifyStageEmptyFullNotesNoVerifyAndStillRunsE2E(t *testing.T) {
	var ran []string
	tr := verifyStageRun(VerifyResult{NoCommands: true}, []string{"e2e-cmd"}, &ran)
	if done, err := tr.verifyStage(); done || err != nil {
		t.Fatalf("done=%v err=%v", done, err)
	}
	if len(tr.res.Notes) < 1 || tr.res.Notes[0] != noVerifyNote("train") || len(tr.res.Verified) != 0 {
		t.Errorf("notes %q verified %v", tr.res.Notes, tr.res.Verified)
	}
	if !reflect.DeepEqual(ran, []string{"e2e-cmd"}) {
		t.Errorf("e2e ran %v", ran)
	}
}
