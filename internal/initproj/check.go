package initproj

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/repos"
)

// CoreFiles are the files `rota init check` requires under .rota/.
var CoreFiles = []string{"DECISIONS.md", "BACKLOG.md", "KNOWLEDGE.md", "MILESTONES.md", "counters.json", "config.json", "status.json"}

// CheckResult is the answer of Check. Missing holds every absent path
// (`.rota` alone when the directory is missing), where hv-preflight named only
// the first. Warnings are advisory and only reported for an initialized project.
type CheckResult struct {
	Initialized bool
	Missing     []string
	Warnings    []string
}

// Check is hv-preflight without its helper-mirror half, which 5.0 drops. It
// acts on root, with no walk-up. drift is the version-drift line (what `rota
// version --drift` reports), or nil.
func Check(root string, drift func() string) CheckResult {
	res := CheckResult{Missing: []string{}, Warnings: []string{}}
	rota := filepath.Join(root, ".rota")
	if fi, err := os.Stat(rota); err != nil || !fi.IsDir() {
		res.Missing = []string{".rota"}
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
	if truthy(umbrellaFlag(rota)) && !repos.Umbrella(root) {
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

func umbrellaFlag(rota string) any {
	v, _ := config.Lookup(config.Load(filepath.Join(rota, "config.json")), "umbrella.enabled")
	return v
}

// truthy is Python's truth value, which is what the old helper tested.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		f, err := x.Float64()
		return err != nil || f != 0
	case []any:
		return len(x) > 0
	case *jsonx.Object:
		return x.Len() > 0
	}
	return true
}
