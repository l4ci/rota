package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/testledger"
)

// VerifyResult is one run of test.full. Callers only phrase the
// verdict; reading the key, running the commands and keeping the log live here.
type VerifyResult struct {
	NoCommands bool     // test.full is empty: nothing was run
	Verified   []string // commands that passed
	Failed     []string // commands that failed
	Log        string   // every command's output, as it ran
	// LogPath is the same log on disk, set only when a command failed. The
	// caller owns it: report it, or remove it.
	LogPath string
	// CI marks a run on the project's CI (see ci.go): Verified and Failed are
	// check names, Log lists the checks, and Ref and SHA are what was pushed.
	// NotRun is a listed check never starting (Missing names them), TimedOut
	// checks still pending at the deadline; neither is OK.
	CI               bool
	Ref, SHA         string
	NotRun, TimedOut bool
	Missing          []string
	// Cached marks a passing verdict reused from the train cache: nothing ran.
	Cached bool
	// Excluded lists the ledger entries that excused a failing command: its
	// failing tests all had an unexpired entry (see internal/testledger), so
	// the command counts as verified.
	Excluded []testledger.Entry
}

// OK is true when every configured command passed (or none was configured;
// check NoCommands to tell the two apart).
func (r VerifyResult) OK() bool { return len(r.Failed) == 0 && !r.NotRun && !r.TimedOut }

// Verify runs test.full, read from root's config, in dir. Each
// command goes through Env.Shell, whose default runs it in its own process
// group and kills the group on cancel. err is the log file failing to open.
func (e Env) Verify(ctx context.Context, root, dir string) (VerifyResult, error) {
	led, err := LoadLedger(root)
	if err != nil {
		return VerifyResult{}, err
	}
	return e.RunVerifyWith(ctx, verifyCommandsAt(root), dir, led)
}

// LoadLedger reads root's exclusion ledger; a malformed one is exit 2.
func LoadLedger(root string) (testledger.Ledger, error) {
	led, err := testledger.Load(root)
	var bad *testledger.MalformedError
	if errors.As(err, &bad) {
		return led, fail(exitcode.ExitUsage, bad.Error())
	}
	return led, err
}

// LedgerExpiry is the failure message for expired entries in led at now: each
// names its test, owner and receipt. "" when none has expired.
func LedgerExpiry(led testledger.Ledger, now time.Time) string {
	exp := led.Expired(now)
	if len(exp) == 0 {
		return ""
	}
	lines := make([]string, len(exp))
	for i, x := range exp {
		lines[i] = "  " + x.String()
	}
	return "expired test-ledger entries:\n" + strings.Join(lines, "\n")
}

// RunVerify is Verify with the commands already read. The gate reads them
// before it merges a branch into root, so the branch's own config cannot
// change what verifies it.
func (e Env) RunVerify(ctx context.Context, cmds []string, dir string) (VerifyResult, error) {
	return e.RunVerifyWith(ctx, cmds, dir, testledger.Ledger{})
}

// RunVerifyWith is RunVerify under an exclusion ledger: a failing command whose
// failing tests all have an unexpired entry counts as verified and is listed
// in Excluded.
func (e Env) RunVerifyWith(ctx context.Context, cmds []string, dir string, led testledger.Ledger) (VerifyResult, error) {
	e = e.withDefaults()
	return runVerifyCmds(ctx, e.Shell, cmds, dir, led, e.Now())
}

// runVerifyCmds runs cmds through shell, one after another, in dir.
func runVerifyCmds(ctx context.Context, shell func(ctx context.Context, dir, command string) (string, int), cmds []string, dir string, led testledger.Ledger, now time.Time) (VerifyResult, error) {
	var res VerifyResult
	if len(cmds) == 0 {
		res.NoCommands = true
		return res, nil
	}
	logf, err := os.CreateTemp("", "rota-gate-verify-")
	if err != nil {
		return res, err
	}
	logf.Close()
	var log strings.Builder
	for _, c := range cmds {
		out, code := shell(ctx, dir, c)
		section := "== " + c + "\n" + out
		log.WriteString(section)
		appendFile(logf.Name(), section)
		excused, covered := led.Excuses(out, now)
		switch {
		case code == 0:
			res.Verified = append(res.Verified, c)
		case covered:
			res.Verified = append(res.Verified, c)
			res.Excluded = append(res.Excluded, excused...)
		default:
			res.Failed = append(res.Failed, c)
		}
	}
	res.Log = log.String()
	if res.OK() {
		os.Remove(logf.Name())
	} else {
		res.LogPath = logf.Name()
	}
	return res, nil
}

// HasVerifyCommands reports whether Verify has anything to run, for a caller
// that must check preconditions before it does.
func HasVerifyCommands(root string) bool { return len(verifyCommandsAt(root)) > 0 }

// GateCommands is what the merge gate runs for test.full: test.full, or the
// deprecated refactor.verifyCommands while a config still holds only that.
func GateCommands(root string) []string { return verifyCommandsAt(root) }

// TierCommands is test.<tier> (fast, full or e2e) from root's config: the
// non-blank entries, trimmed.
func TierCommands(root, tier string) []string {
	return commandList(config.Load(rotatree.Config(root)), "test."+tier)
}

func commandList(cfg any, key string) []string {
	var cmds []string
	if v, ok := config.Lookup(cfg, key); ok {
		if list, ok := v.([]any); ok {
			for _, c := range list {
				if t := strings.TrimSpace(fmt.Sprint(c)); t != "" {
					cmds = append(cmds, t)
				}
			}
		}
	}
	return cmds
}

// verifyCommandsAt is test.full, the merge-gate tier and the only place Verify
// reads. While a config still holds refactor.verifyCommands and test.full is
// empty, those commands run instead, with one deprecation warning.
func verifyCommandsAt(root string) []string {
	cfg := config.Load(rotatree.Config(root))
	if cmds := commandList(cfg, config.TestFullKey); len(cmds) > 0 {
		return cmds
	}
	cmds := commandList(cfg, config.LegacyVerifyKey)
	if len(cmds) > 0 {
		legacyWarn.Do(func() {
			fmt.Fprintln(os.Stderr, "rota: refactor.verifyCommands is deprecated; run rota config fill to move it to test.full")
		})
	}
	return cmds
}

var legacyWarn sync.Once

// RunShell runs one command through the Env's shell (sh -c by default) in dir
// and returns its combined output and exit code.
func (e Env) RunShell(ctx context.Context, dir, command string) (string, int) {
	return e.withDefaults().Shell(ctx, dir, command)
}
