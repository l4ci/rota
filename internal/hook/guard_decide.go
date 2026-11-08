package hook

import (
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/rotatree"
)

// GitFunc runs `git <args>` in dir and returns trimmed stdout; a non-zero exit
// is an error.
type GitFunc func(dir string, args ...string) (string, error)

// GuardRequest is one PreToolUse call the guard has to decide on. Nothing in it
// reads the process: the caller supplies the decoded payload, the worker's slot, a
// fallback cwd and the git runner.
type GuardRequest struct {
	// Payload is the hook's decoded input.
	Payload Payload
	// Slot is the worker's slot name (ROTA_SLOT).
	Slot string
	// DefaultCwd is used when the payload carries no cwd.
	DefaultCwd string
	// Git runs git for the guard. Required: DecideGuard calls it unconditionally
	// and panics on nil.
	Git GitFunc
}

// DecideGuard returns "" when the call may proceed, else the one-line reason it
// may not. A non-Bash tool and an empty command all
// pass, so the guard never blocks a build step on its own malfunction.
func DecideGuard(r GuardRequest) string {
	tool, command := r.Payload.String("tool_name"), r.Payload.String("tool_input", "command")
	if (tool != "" && tool != "Bash") || command == "" {
		return ""
	}
	cwd := r.Payload.CwdOr(r.DefaultCwd)
	return Guard(command, GuardEnv{
		Cwd:      cwd,
		Worktree: slotWorktree(cwd, r.Slot, r.Git),
		Branch: func(dir string) (string, error) {
			return r.Git(dir, "branch", "--show-current")
		},
		Toplevel: func(dir string) (string, error) {
			return r.Git(dir, "rev-parse", "--show-toplevel")
		},
		Upstream: func(dir string) (string, error) {
			up, err := r.Git(dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
			if err != nil {
				return "", nil // no upstream is not an error
			}
			return up, nil
		},
	})
}

// slotWorktree is the slot's own worktree: the `.worktrees/<slot>` ancestor of
// cwd, else the entry of `git worktree list` with that name. "" when neither.
func slotWorktree(cwd, slot string, git GitFunc) string {
	for d := cwd; d != "" && d != filepath.Dir(d); d = filepath.Dir(d) {
		if filepath.Base(d) == slot && filepath.Base(filepath.Dir(d)) == rotatree.WorktreeRoot {
			return d
		}
	}
	out, err := git(cwd, "worktree", "list", "--porcelain")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok && filepath.Base(p) == slot {
			return p
		}
	}
	return ""
}
