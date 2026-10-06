// Package legacystate probes for the hv-era state folder. It is the one live
// dependency on the old layout (doctor's preflight and the `migrate hv` verb);
// the rename itself lives in hvmigrate and can be deleted without touching it.
// The package names the old spelling on purpose; it is on the legacy-name
// allowlist of test/grep-gate.sh.
package legacystate

import (
	"os"
	"path/filepath"
)

const (
	legacyDir = ".hv"
	stateDir  = ".rota"
)

// LegacyState walks up from start (a physical path). It returns the nearest
// directory that holds .hv/ and no .rota/ next to it, when no .rota/ is found
// first: such a project has not been migrated.
func LegacyState(start string) (dir string, legacy bool) {
	d := start
	for {
		if isDir(filepath.Join(d, stateDir)) {
			return "", false
		}
		if isDir(filepath.Join(d, legacyDir)) {
			return d, true
		}
		p := filepath.Dir(d)
		if p == d {
			return "", false
		}
		d = p
	}
}

// FindState is the nearest directory holding .hv/ or .rota/.
func FindState(start string) (string, bool) {
	d := start
	for {
		if isDir(filepath.Join(d, stateDir)) || isDir(filepath.Join(d, legacyDir)) {
			return d, true
		}
		p := filepath.Dir(d)
		if p == d {
			return "", false
		}
		d = p
	}
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
