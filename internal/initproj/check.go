package initproj

import (
	"os"
	"path/filepath"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/rotatree"
)

// CoreFiles are the files `rota init check` requires under .rota/: the seeded
// files marked required in seedFiles.
var CoreFiles = func() []string {
	var names []string
	for _, f := range seedFiles {
		if f.required {
			names = append(names, f.name)
		}
	}
	return names
}()

// CheckResult is the answer of Check. Missing holds every absent path
// (`.rota` alone when the directory is missing), where hv-preflight named only
// the first. Warnings are advisory and only reported for an initialized project.
type CheckResult struct {
	Initialized bool
	Missing     []string
	Warnings    []string
}

// Check is hv-preflight without its helper-mirror half, which 5.0 drops. It
// acts on root, with no walk-up. cfg is the loaded project config. drift is the version-drift line (what `rota
// version --drift` reports), or nil.
func Check(root string, cfg any, drift func() string) CheckResult {
	res := CheckResult{Missing: []string{}, Warnings: []string{}}
	rota := rotatree.Dir(root)
	if fi, err := os.Stat(rota); err != nil || !fi.IsDir() {
		res.Missing = []string{rotatree.DirName}
		return res
	}
	for _, f := range CoreFiles {
		if !isFile(filepath.Join(rota, f)) {
			res.Missing = append(res.Missing, ".rota/"+f)
		}
	}
	if len(res.Missing) > 0 {
		return res
	}
	res.Initialized = true

	// The umbrella flag is informational; the registry is the truth
	// (DECISIONS.md, "Persistence-trio scoping under umbrella mode"). A flag
	// with no registered repos is a warning, never a failure.
	if jsonx.Truthy(umbrellaFlag(cfg)) && !repos.Umbrella(root) {
		if _, err := os.Stat(filepath.Join(rota, "repos.json")); err != nil {
			res.Warnings = append(res.Warnings, "umbrella.enabled=true but .rota/repos.json missing — run `rota init umbrella` from the umbrella root to register, or set umbrella.enabled=false")
		} else {
			res.Warnings = append(res.Warnings, "umbrella.enabled=true but no sub-repos in .rota/repos.json — run `rota init umbrella` from the umbrella root to register, or set umbrella.enabled=false")
		}
	}
	if drift != nil {
		if line := drift(); line != "" {
			res.Warnings = append(res.Warnings, line)
		}
	}
	return res
}

func umbrellaFlag(cfg any) any {
	v, _ := config.Lookup(cfg, "umbrella.enabled")
	return v
}
