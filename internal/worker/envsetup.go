package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/l4ci/rota/internal/exitcode"
)

// envHashFile is where a slot's last successful work.envSetup is remembered:
// inside the worktree's own git dir, so it is per slot, never tracked, and
// leaves with the worktree on reap.
const envHashFile = "rota-env-setup"

// lockfiles are the dependency lockfiles whose content decides whether
// work.envSetup runs again. requirements*.txt is a glob.
var lockfiles = []string{
	"package-lock.json", "pnpm-lock.yaml", "yarn.lock", "uv.lock", "poetry.lock", "go.sum",
}

// envSetupHash is a digest of the setup command and the lockfiles at the
// worktree root. The command is part of it so editing work.envSetup re-runs
// it. A project with no lockfile still hashes (to a constant), so its setup
// runs once per slot.
func envSetupHash(dir, command string) string {
	names := append([]string(nil), lockfiles...)
	if m, err := filepath.Glob(filepath.Join(dir, "requirements*.txt")); err == nil {
		for _, p := range m {
			names = append(names, filepath.Base(p))
		}
	}
	sort.Strings(names)
	h := sha256.New()
	fmt.Fprintf(h, "cmd\x00%s\x00", command)
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			continue
		}
		fmt.Fprintf(h, "%s\x00%d\x00", n, len(b))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// envSetup runs work.envSetup in a slot worktree unless the stored hash
// already matches, and stores the hash only after a zero exit. ran reports
// whether the command ran. A red command is an error naming slot and command.
func (e Env) envSetup(ctx context.Context, slot, dir, command string, portBase int) (ran bool, err error) {
	gitDir, code := e.git(dir, "rev-parse", "--absolute-git-dir")
	if code != 0 || gitDir == "" {
		return false, fail(exitcode.ExitUnavailable, fmt.Sprintf("slot %s: cannot find the git dir of %s to store the env setup hash", slot, dir))
	}
	stamp := filepath.Join(gitDir, envHashFile)
	want := envSetupHash(dir, command)
	if have, err := os.ReadFile(stamp); err == nil && strings.TrimSpace(string(have)) == want {
		return false, nil
	}
	// A stale stamp must not survive a red rerun.
	os.Remove(stamp)
	out, code := e.Shell(ctx, dir, exportPrefix(slot, portBase)+command)
	if code != 0 {
		msg := fmt.Sprintf("slot %s: work.envSetup failed (exit %d): %s", slot, code, command)
		if t := strings.TrimSpace(out); t != "" {
			msg += "\n" + t
		}
		return true, fail(exitcode.ExitFailed, msg)
	}
	if err := os.WriteFile(stamp, []byte(want+"\n"), 0o644); err != nil {
		return true, fail(exitcode.ExitUnavailable, fmt.Sprintf("slot %s: cannot store the env setup hash: %v", slot, err))
	}
	return true, nil
}
