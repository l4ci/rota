package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/config"
)

// VerifyResult is one run of refactor.verifyCommands. Callers only phrase the
// verdict; reading the key, running the commands and keeping the log live here.
type VerifyResult struct {
	NoCommands bool     // refactor.verifyCommands is empty: nothing was run
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

// Verify runs refactor.verifyCommands, read from root's config, in dir. Each
// command goes through Env.Shell, whose default runs it in its own process
// group and kills the group on cancel. err is the log file failing to open.
func (e Env) Verify(ctx context.Context, root, dir string) (VerifyResult, error) {
	return e.RunVerify(ctx, verifyCommandsAt(root), dir)
}

// RunVerify is Verify with the commands already read. The gate reads them
// before it merges a branch into root, so the branch's own config cannot
// change what verifies it.
func (e Env) RunVerify(ctx context.Context, cmds []string, dir string) (VerifyResult, error) {
	e = e.withDefaults()
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
		out, code := e.Shell(ctx, dir, c)
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

// verifyCommandsAt is refactor.verifyCommands, the one place the key is read:
// the non-blank entries, trimmed.
func verifyCommandsAt(root string) []string {
	var cmds []string
	cfg := config.Load(filepath.Join(root, ".rota", "config.json"))
	if v, ok := config.Lookup(cfg, "refactor.verifyCommands"); ok {
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
