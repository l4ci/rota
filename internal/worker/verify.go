package worker

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/rotatree"
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
}

// OK is true when every configured command passed (or none was configured;
// check NoCommands to tell the two apart).
func (r VerifyResult) OK() bool { return len(r.Failed) == 0 }

// Verify runs test.full, read from root's config, in dir. Each
// command goes through Env.Shell, whose default runs it in its own process
// group and kills the group on cancel. err is the log file failing to open.
func (e Env) Verify(ctx context.Context, root, dir string) (VerifyResult, error) {
	return e.RunVerify(ctx, verifyCommandsAt(root), dir)
}

// RunVerify is Verify with the commands already read. The gate reads them
// before it merges a branch into root, so the branch's own config cannot
// change what verifies it.
func (e Env) RunVerify(ctx context.Context, cmds []string, dir string) (VerifyResult, error) {
	return runVerifyCmds(ctx, e.withDefaults().Shell, cmds, dir)
}

// runVerifyCmds runs cmds through shell, one after another, in dir.
func runVerifyCmds(ctx context.Context, shell func(ctx context.Context, dir, command string) (string, int), cmds []string, dir string) (VerifyResult, error) {
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
		if code == 0 {
			res.Verified = append(res.Verified, c)
		} else {
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
