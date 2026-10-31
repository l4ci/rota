package skills

import (
	"os"
	"path/filepath"
)

const briefFile = "worker-contract.md"

// CheckoutBrief is the standing worker contract in a source checkout rooted
// at top, or "" when the checkout has none.
func CheckoutBrief(top string) string {
	return regularFile(filepath.Join(top, "skills", "references", briefFile))
}

// InstalledBrief is the contract under the first rota-installed skills root
// (the project's before the user's, Claude's before Codex's), or "". A root
// counts only when rota installed it (a manifest) and it carries the file: a
// stub install drops references, so it yields "" and the caller falls back to
// the cache.
func InstalledBrief(getenv func(string) string, top string) string {
	for _, r := range InstalledRoots(getenv, top) {
		if !HasManifest(r) {
			continue
		}
		return regularFile(filepath.Join(r, "rota-orchestrate", "references", briefFile))
	}
	return ""
}

// CachedBrief is the contract in the skills cache this binary extracts under
// cache (made when missing), or "" when cache is empty or extraction fails.
func CachedBrief(version, cache string) string {
	if cache == "" {
		return ""
	}
	set, err := Embedded()
	if err != nil {
		return ""
	}
	tree, err := Extract(set, version, cache)
	if err != nil {
		return ""
	}
	return regularFile(filepath.Join(tree, "references", briefFile))
}

func regularFile(p string) string {
	if fi, err := os.Stat(p); err != nil || fi.IsDir() {
		return ""
	}
	return p
}
