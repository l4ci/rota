package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/rotastate"
)

// SlotsTmpDir is the entry of the project temp root that holds every slot's
// own temp dir.
const SlotsTmpDir = "slots"

// SlotTmpDir is the slot's own temp dir under the project temp root
// (<root>/slots/<slot>), the TMPDIR its session runs with so two workers on one
// machine never share a scratch file name (#3164). "" when slot is not a plain
// directory name.
func SlotTmpDir(tempRoot, slot string) string {
	if slot == "" || slot == "." || slot == ".." || filepath.Base(slot) != slot {
		return ""
	}
	return filepath.Join(tempRoot, SlotsTmpDir, slot)
}

// slotTmpEnv creates the slot's temp dir and returns the TMPDIR entry for its
// session. It returns nothing outside a rota project (no temp root), where the
// session keeps the harness's own temp dir.
func (e Env) slotTmpEnv(ctx context.Context, root, slot string) ([]string, error) {
	tr, ok, err := rotastate.TempRootVia(ctx, e.Git, root)
	if err != nil || !ok {
		return nil, err
	}
	dir := SlotTmpDir(tr, slot)
	if dir == "" {
		return nil, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return []string{"TMPDIR=" + dir}, nil
}

// SystemTmpEnviron is environ without a TMPDIR that points into a slot's temp
// dir (<tempRoot>/slots/...). A test process there would put its t.TempDir()
// fixtures inside the project's own git repo, where helpers that walk up find
// the real .rota and .git (#3317); without TMPDIR it falls back to the system
// temp dir, as the merge gate does. Any other TMPDIR is kept.
func SystemTmpEnviron(environ []string, tempRoot string) []string {
	slots := filepath.Join(tempRoot, SlotsTmpDir)
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		if v, ok := strings.CutPrefix(kv, "TMPDIR="); ok && (v == slots || strings.HasPrefix(v, slots+string(filepath.Separator))) {
			continue
		}
		out = append(out, kv)
	}
	return out
}
