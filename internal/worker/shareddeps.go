package worker

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
)

// SharedDir is what round.sharedDirs did for one directory of one slot.
type SharedDir struct {
	Slot, Dir string
	// Action is linked, kept, unlinked or skipped.
	Action, Reason string
}

// guardLockfiles are the files whose content must match main's for a slot to
// share main's dependency directories: the envSetup lockfiles plus Cargo.lock
// and bun.lockb, which no setup hash reads.
func guardLockfiles(root, wt string) []string {
	names := append([]string(nil), lockfiles...)
	names = append(names, "Cargo.lock", "bun.lockb")
	for _, d := range []string{root, wt} {
		for _, p := range fsio.GlobDir(d, "requirements*.txt") {
			names = append(names, filepath.Base(p))
		}
	}
	return names
}

// lockfilesDiffer reports the first lockfile whose content in the slot is not
// main's. A file absent from both is equal.
func lockfilesDiffer(root, wt string) (string, bool) {
	for _, n := range guardLockfiles(root, wt) {
		a, _ := os.ReadFile(filepath.Join(root, n))
		b, _ := os.ReadFile(filepath.Join(wt, n))
		if !bytes.Equal(a, b) {
			return n, true
		}
	}
	return "", false
}

// safeRel cleans a configured directory and refuses one that leaves the repo.
func safeRel(dir string) (string, bool) {
	c := filepath.Clean(dir)
	if c == "." || filepath.IsAbs(c) || c == ".." || strings.HasPrefix(c, ".."+string(filepath.Separator)) {
		return "", false
	}
	return c, true
}

// shareDeps links each of dirs from the main checkout into the slot worktree.
// It is all or nothing: when any directory cannot be shared (a lockfile differs
// from main's, the directory is missing in main, the slot holds a real one),
// none is, and every link rota made is removed so the slot installs its own.
// shareAll reports that every directory is shared (linked or kept).
func (e Env) shareDeps(root, slot, wt string, dirs []string) (rows []SharedDir, shareAll bool) {
	type item struct{ rel, src, dst, reason string }
	var items []item
	lock, differ := lockfilesDiffer(root, wt)
	for _, d := range dirs {
		rel, ok := safeRel(d)
		if !ok {
			items = append(items, item{rel: d, reason: "not a path inside the repo"})
			continue
		}
		it := item{rel: rel, src: filepath.Join(root, rel), dst: filepath.Join(wt, rel)}
		switch {
		case differ:
			it.reason = lock + " differs from main's"
		case !fsio.IsDir(it.src):
			it.reason = "missing in the main checkout"
		default:
			if fi, err := os.Lstat(it.dst); err == nil && fi.Mode()&os.ModeSymlink == 0 {
				it.reason = "the slot has its own"
			}
		}
		items = append(items, it)
	}
	shareAll = len(items) > 0
	for _, it := range items {
		if it.reason != "" {
			shareAll = false
		}
	}
	for _, it := range items {
		row := SharedDir{Slot: slot, Dir: it.rel}
		ours := it.src != "" && isLinkTo(it.dst, it.src)
		switch {
		case !shareAll:
			row.Action, row.Reason = "skipped", it.reason
			if it.reason == "" {
				row.Reason = "another directory cannot be shared"
			}
			if ours {
				os.Remove(it.dst)
				row.Action = "unlinked"
			}
		case ours:
			row.Action = "kept"
		default:
			// A symlink pointing elsewhere or dangling is replaced.
			os.Remove(it.dst)
			if err := os.MkdirAll(filepath.Dir(it.dst), 0o777); err == nil {
				err = os.Symlink(it.src, it.dst)
				if err != nil {
					row.Action, row.Reason = "skipped", err.Error()
					break
				}
			}
			row.Action = "linked"
			e.excludeLink(wt, it.rel)
		}
		rows = append(rows, row)
	}
	return rows, shareAll
}

func isLinkTo(path, target string) bool {
	got, err := os.Readlink(path)
	return err == nil && got == target
}

// excludeLink keeps a linked directory out of `git status`: a gitignore line
// like node_modules/ matches a directory, not the symlink that replaces it.
func (e Env) excludeLink(wt, rel string) {
	p, code := e.git(wt, "rev-parse", "--git-path", "info/exclude")
	if code != 0 || p == "" {
		return
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(wt, p)
	}
	line := "/" + filepath.ToSlash(rel)
	have, _ := os.ReadFile(p)
	for _, l := range strings.Split(string(have), "\n") {
		if l == line {
			return
		}
	}
	os.MkdirAll(filepath.Dir(p), 0o777)
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	if len(have) > 0 && !bytes.HasSuffix(have, []byte("\n")) {
		f.WriteString("\n")
	}
	f.WriteString(line + "\n")
}

// slotDeps prepares a slot's dependencies: it shares round.sharedDirs when it
// can, and otherwise runs round.envSetup, which stays hash-skipped. It returns
// what the sharing did. With nothing configured to share it only runs
// envSetup when runSetup is set (pool init), never on a dispatch.
func (e Env) slotDeps(ctx context.Context, root, slot, wt string, cfg any, portBase, width int, runSetup ...bool) ([]SharedDir, error) {
	setup := strings.TrimSpace(config.String(cfg, config.RoundEnvSetupKey))
	dirs := config.SharedDirs(cfg)
	var rows []SharedDir
	shared := false
	if len(dirs) > 0 {
		rows, shared = e.shareDeps(root, slot, wt, dirs)
	}
	if setup != "" && !shared && (len(dirs) > 0 || (len(runSetup) > 0 && runSetup[0])) {
		if _, err := e.envSetup(ctx, slot, wt, setup, portBase, width); err != nil {
			return rows, err
		}
	}
	return rows, nil
}

// SharedList is the `shared` array of the JSON: one object per slot and
// directory, naming the action and, for a skip or unlink, why.
func SharedList(rows []SharedDir) []any {
	out := make([]any, 0, len(rows))
	for _, r := range rows {
		o := jsonx.NewObject()
		o.Set("slot", r.Slot)
		o.Set("dir", r.Dir)
		o.Set("action", r.Action)
		if r.Reason != "" {
			o.Set("reason", r.Reason)
		}
		out = append(out, o)
	}
	return out
}
