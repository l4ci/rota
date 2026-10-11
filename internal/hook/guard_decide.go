package hook

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// GitFunc runs `git <args>` in dir and returns trimmed stdout; a non-zero exit
// is an error.
type GitFunc func(dir string, args ...string) (string, error)

// GuardRequest is one PreToolUse call the guard has to decide on. Nothing in it
// reads the process: the caller supplies the raw payload, the worker's slot, a
// fallback cwd and the git runner.
type GuardRequest struct {
	// Payload is the hook's JSON input.
	Payload []byte
	// Slot is the worker's slot name (ROTA_SLOT).
	Slot string
	// DefaultCwd is used when the payload carries no cwd.
	DefaultCwd string
	// Git runs git for the guard. Required: DecideGuard calls it unconditionally
	// and panics on nil.
	Git GitFunc
}

// DecideGuard returns "" when the call may proceed, else the one-line reason it
// may not. A payload it cannot read, a non-Bash tool and an empty command all
// pass, so the guard never blocks a build step on its own malfunction.
func DecideGuard(r GuardRequest) string {
	var p struct {
		Tool  string `json:"tool_name"`
		Input struct {
			Command string `json:"command"`
		} `json:"tool_input"`
		Cwd string `json:"cwd"`
	}
	if json.Unmarshal(r.Payload, &p) != nil || (p.Tool != "" && p.Tool != "Bash") || p.Input.Command == "" {
		return ""
	}
	cwd := p.Cwd
	if cwd == "" {
		cwd = r.DefaultCwd
	}
	return Guard(p.Input.Command, GuardEnv{
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
		if filepath.Base(d) == slot && filepath.Base(filepath.Dir(d)) == ".worktrees" {
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
