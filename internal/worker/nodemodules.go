package worker

import (
	"os"
	"path/filepath"
	"strings"
)

// NodeModulesFix is the repair a reap names when node_modules points into a
// worktree it was about to remove.
const NodeModulesFix = "pnpm install --offline --frozen-lockfile"

// NodeModulesPointer reports the first thing in the main checkout's
// node_modules that resolves into the worktree wt, or "" when nothing does.
// It looks at symlinks one level down (and under @scope dirs), at pnpm's
// .pnpm/node_modules hoist dir, and at .modules.yaml's virtualStoreDir: a
// worker's pnpm install can rewrite these to point into its own worktree, and
// removing that worktree then breaks every other checkout sharing them.
func NodeModulesPointer(mainRoot, wt string) string {
	nm := filepath.Join(mainRoot, "node_modules")
	if st, err := os.Stat(nm); err != nil || !st.IsDir() {
		return ""
	}
	roots := []string{filepath.Clean(wt)}
	if real, err := filepath.EvalSymlinks(wt); err == nil && real != roots[0] {
		roots = append(roots, real)
	}
	inside := func(p string) bool {
		p = filepath.Clean(p)
		for _, r := range roots {
			if p == r || strings.HasPrefix(p, r+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}
	if data, err := os.ReadFile(filepath.Join(nm, ".modules.yaml")); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			v, ok := strings.CutPrefix(line, "virtualStoreDir:")
			if !ok {
				continue
			}
			v = strings.Trim(strings.TrimSpace(v), `"'`)
			if v != "" && !filepath.IsAbs(v) {
				v = filepath.Join(nm, v)
			}
			if v != "" && inside(v) {
				return filepath.Join(nm, ".modules.yaml") + ": virtualStoreDir " + v
			}
		}
	}
	var scan func(dir string, depth int) string
	scan = func(dir string, depth int) string {
		ents, err := os.ReadDir(dir)
		if err != nil {
			return ""
		}
		for _, e := range ents {
			p := filepath.Join(dir, e.Name())
			if e.Type()&os.ModeSymlink != 0 {
				t, err := os.Readlink(p)
				if err != nil {
					continue
				}
				if !filepath.IsAbs(t) {
					t = filepath.Join(dir, t)
				}
				if inside(t) {
					return p + " -> " + t
				}
			} else if e.IsDir() && depth > 0 && strings.HasPrefix(e.Name(), "@") {
				if hit := scan(p, depth-1); hit != "" {
					return hit
				}
			}
		}
		return ""
	}
	if hit := scan(nm, 1); hit != "" {
		return hit
	}
	return scan(filepath.Join(nm, ".pnpm", "node_modules"), 1)
}

// NodeModulesRefusal is the message every reap path shows for a pointer found by NodeModulesPointer.
func NodeModulesRefusal(wt, hit string) string {
	return "node_modules in the main checkout points into " + wt + " (" + hit + "); run `" + NodeModulesFix + "` in the main checkout, then reap again"
}
