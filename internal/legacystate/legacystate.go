// Package legacystate probes for the hv-era state folder. It is the one live
// dependency on the old layout (doctor's preflight and the `migrate hv` verb);
// the rename itself lives in hvmigrate and can be deleted without touching it.
// The package names the old spelling on purpose; it is on the legacy-name
// allowlist of test/grep-gate.sh.
package legacystate

import (
	"path/filepath"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/rotatree"
)

const (
	legacyDir = ".hv"
)

// LegacyState walks up from start (a physical path). It returns the nearest
// directory that holds .hv/ and no .rota/ next to it, when no .rota/ is found
// first: such a project has not been migrated.
func LegacyState(start string) (dir string, legacy bool) {
	rotatree.Ascend(start, func(d string) bool {
		if rotatree.Exists(d) {
			return true
		}
		if fsio.IsDir(filepath.Join(d, legacyDir)) {
			dir, legacy = d, true
			return true
		}
		return false
	})
	return dir, legacy
}

// FindState is the nearest directory holding .hv/ or .rota/.
func FindState(start string) (dir string, ok bool) {
	rotatree.Ascend(start, func(d string) bool {
		if rotatree.Exists(d) || fsio.IsDir(filepath.Join(d, legacyDir)) {
			dir, ok = d, true
		}
		return ok
	})
	return dir, ok
}
