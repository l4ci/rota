package worker

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/flakeledger"
	"github.com/l4ci/rota/internal/testledger"
)

// Unknown output stays red. Even an assertion summary must not hide a
// process/infrastructure failure in another package of the same command.
var retryInfrastructure = regexp.MustCompile(`(?im)signal:|\bkilled\b|\[build failed\]|\[setup failed\]|^panic:|fatal error:|test timed out|context deadline exceeded|resource temporarily unavailable|cannot allocate memory|no space left on device|permission denied|command not found|executable file not found|fork/exec|connection refused|network is unreachable|i/o timeout`)

// cleanVerifyTree identifies the committed tree and refuses tracked edits.
// Untracked test artifacts are allowed, as in every scratch verification.
func (e Env) cleanVerifyTree(ctx context.Context, dir string) string {
	tree, code := e.runGit(ctx, dir, "rev-parse", "HEAD^{tree}")
	if code != 0 || tree == "" {
		return ""
	}
	status, code := e.runGit(ctx, dir, "status", "--porcelain", "--untracked-files=no")
	if code != 0 || status != "" {
		return ""
	}
	return tree
}

// runGateVerify opts gate/train local tiers into retry. General test runs,
// proof recording, CI and behaviour checks retain their existing semantics.
// Gate wrappers report their own isolated sub-check recoveries as markers;
// those use the same ledger entries and never trigger a second wrapper retry.
func (e Env) runGateVerify(ctx context.Context, cmds []string, dir string, led testledger.Ledger) (VerifyResult, error) {
	e = e.WithDefaults()
	now := e.Now()
	var flakes []flakeledger.Entry
	shell := func(ctx context.Context, dir, cmd string) (string, int) {
		tree := e.cleanVerifyTree(ctx, dir)
		out, code := e.Shell(ctx, dir, cmd)
		subchecks, parseErr := gateSubchecks(out, tree, e.Now())
		if parseErr != nil {
			return out + "\n" + parseErr.Error() + "\n", 1
		}
		if len(subchecks) > 0 {
			if ctx.Err() != nil || tree == "" || e.cleanVerifyTree(ctx, dir) != tree {
				return out, 1
			}
			flakes = append(flakes, subchecks...)
			return out, code // the wrapper already retried; never rerun it here
		}
		tests, assertion := testledger.FailingTests(out)
		_, excluded := led.Excuses(out, now)
		if code != 1 || ctx.Err() != nil || !assertion || excluded || retryInfrastructure.MatchString(out) || tree == "" || e.cleanVerifyTree(ctx, dir) != tree {
			return out, code
		}
		started := e.Now()
		again, retryCode := e.Shell(ctx, dir, cmd)
		elapsed := max(0, e.Now().Sub(started).Seconds())
		combined := out + "\n== RETRY " + cmd + "\n" + again
		if retryCode == 0 && ctx.Err() == nil && e.cleanVerifyTree(ctx, dir) == tree {
			flakes = append(flakes, flakeledger.Entry{Check: cmd, Tests: tests, Tree: tree, TS: e.Now().UTC(), First: flakeledger.Run{Output: out, ExitCode: code}, Retry: flakeledger.Run{Output: again, ExitCode: retryCode, WallSeconds: elapsed}})
			return combined, 0
		}
		// A changed tree or cancellation cannot turn the original red green.
		if retryCode == 0 {
			retryCode = 1
		}
		return combined, retryCode
	}
	r, err := runVerifyCmds(ctx, shell, cmds, dir, led, now)
	r.Flaky = flakes
	return r, err
}

// runE2E is the one entry point for the test.e2e tier: gate, train, the
// post-landing check and freeze recovery all verify e2e through it, so the
// retry policy has a single owner. Callers record vr.Flaky themselves.
func (e Env) runE2E(ctx context.Context, cmds []string, dir string, led testledger.Ledger) (VerifyResult, error) {
	return e.runGateVerify(ctx, cmds, dir, led)
}

func (e Env) recordFlakes(root string, round *RoundMemo, prs []string, vr VerifyResult) error {
	for _, f := range vr.Flaky {
		f.PRs = append([]string{}, prs...)
		f.Round = round.Round(e, root)
		if err := flakeledger.Append(root, f); err != nil {
			return fmt.Errorf("flake ledger not written: %w", err)
		}
	}
	return nil
}

func flakeNote(f flakeledger.Entry) string {
	return fmt.Sprintf("FLAKY %s — tests: %s; rerun: %.3fs", f.Check, strings.Join(f.Tests, ", "), f.Retry.WallSeconds)
}
