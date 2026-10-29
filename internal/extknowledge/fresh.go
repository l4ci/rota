package extknowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/strutil"
)

func (c Clone) git(ctx context.Context, args ...string) (git.Result, error) {
	run := c.Git
	if run == nil {
		run = git.Exec
	}
	return run(ctx, c.Dir, args...)
}

// lit makes a repo path a literal git pathspec. A topic path comes from a
// shared base, so `*`, `?` and `[` in it must not match other files.
func lit(rel string) string { return ":(literal)" + rel }

func lits(rels []string) []string {
	out := make([]string, len(rels))
	for i, r := range rels {
		out[i] = lit(r)
	}
	return out
}

// out runs git and returns trimmed stdout; a non-zero exit is an error that
// carries git's first message line.
func (c Clone) out(ctx context.Context, args ...string) (string, error) {
	res, err := c.git(ctx, args...)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("git %s: %s", args[0], strutil.OrDefault(strutil.FirstNonEmpty(strutil.FirstLine(res.Stderr), strutil.FirstLine(res.Stdout)), "no output"))
	}
	return strings.TrimSpace(res.Stdout), nil
}

func (c Clone) now() time.Time {
	if c.Now == nil {
		return time.Now()
	}
	return c.Now()
}

// stampPath is the per-clone file that records the last successful fetch.
func (c Clone) stampPath() string { return c.statePath(".fetch") }

// statePath is a per-clone file in the state dir: the clone path's hash plus
// ext. Empty without a state dir.
func (c Clone) statePath(ext string) string {
	if c.StateDir == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(c.Dir))
	return filepath.Join(c.StateDir, "external-knowledge", hex.EncodeToString(sum[:6])+ext)
}

// fresh is whether the last fetch is within the TTL.
func (c Clone) fresh() bool {
	p := c.stampPath()
	if p == "" || c.TTL <= 0 {
		return false
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	secs, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return false
	}
	age := c.now().Sub(time.Unix(secs, 0))
	return age >= 0 && age < c.TTL
}

func (c Clone) stamp() {
	p := c.stampPath()
	if p == "" {
		return
	}
	// A stamp that cannot be written only costs one extra fetch.
	if os.MkdirAll(filepath.Dir(p), 0o755) == nil {
		_ = os.WriteFile(p, []byte(strconv.FormatInt(c.now().Unix(), 10)+"\n"), 0o644)
	}
}

func (c Clone) hasRemote(ctx context.Context) bool {
	out, err := c.out(ctx, "remote")
	return err == nil && out != ""
}

func (c Clone) dirty(ctx context.Context) (bool, error) {
	out, err := c.out(ctx, "status", "--porcelain", "--untracked-files=no")
	return out != "", err
}

// divergence is how many commits the clone has that its upstream lacks
// (ahead) and the reverse (behind). ok is false without an upstream.
func (c Clone) divergence(ctx context.Context) (ahead, behind int, ok bool) {
	out, err := c.out(ctx, "rev-list", "--left-right", "--count", "HEAD...@{u}")
	if err != nil {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(out, "%d %d", &ahead, &behind); err != nil {
		return 0, 0, false
	}
	return ahead, behind, true
}

// Freshen brings the clone up to date before a read: at most one fetch per
// TTL, then a fast-forward when the remote is ahead. It never resets: a dirty
// or diverged clone, or an unreachable remote, comes back as a one-line
// warning and the caller reads what is on disk.
func (c Clone) Freshen(ctx context.Context) string {
	if c.fresh() || !c.hasRemote(ctx) {
		return ""
	}
	if _, err := c.out(ctx, "fetch", "--quiet"); err != nil {
		return "reading stale copy: " + err.Error()
	}
	c.stamp()
	ahead, behind, ok := c.divergence(ctx)
	if !ok || behind == 0 {
		return ""
	}
	if ahead > 0 {
		return "clone has diverged from its remote; not touching it, reading as-is"
	}
	if d, err := c.dirty(ctx); err != nil || d {
		return "clone has uncommitted changes and the remote is ahead; not fast-forwarding, reading as-is"
	}
	if _, err := c.out(ctx, "merge", "--ff-only", "--quiet", "@{u}"); err != nil {
		return "fast-forward failed, reading as-is: " + err.Error()
	}
	return ""
}

// syncForWrite is Freshen's strict twin: a write needs a clean clone and a
// current base, so every problem is an error instead of a warning.
func (c Clone) syncForWrite(ctx context.Context) error {
	d, err := c.dirty(ctx)
	if err != nil {
		return err
	}
	if d {
		return fmt.Errorf("%w: commit or discard them in %s first", ErrDirty, c.Dir)
	}
	if !c.hasRemote(ctx) {
		return nil
	}
	if _, err := c.out(ctx, "fetch", "--quiet"); err != nil {
		return fmt.Errorf("%w: %v", ErrOffline, err)
	}
	c.stamp()
	ahead, behind, ok := c.divergence(ctx)
	if !ok || behind == 0 {
		return nil
	}
	if ahead > 0 {
		return fmt.Errorf("%w: reconcile %s by hand first", ErrDiverged, c.Dir)
	}
	_, err = c.out(ctx, "merge", "--ff-only", "--quiet", "@{u}")
	return err
}
