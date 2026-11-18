package skills

import (
	"os"
	"path/filepath"
)

const briefFile = "worker-contract.md"

// briefRel is the contract's path under a skills tree: the extracted cache
// tree and a checkout's skills/ dir carry it, an installed root nests it under
// rota-orchestrate.
const briefRel = "references/" + briefFile

// CheckoutBrief is the standing worker contract in a source checkout rooted
// at top, or "" when the checkout has none.
func CheckoutBrief(top string) string {
	return regularFile(filepath.Join(top, "skills", filepath.FromSlash(briefRel)))
}

// InstalledBrief is the contract under the first rota-installed skills root
// (the project's before the user's, Claude's before Codex's), or "". The first
// root with a manifest decides: a root without one is skipped, but a manifested
// stub root (it drops references) yields "" and hides any later full root, so
// the caller falls back to the cache.
func InstalledBrief(home string, claudeDirs []string, top string) string {
	for _, r := range InstalledRoots(home, claudeDirs, top) {
		if !HasManifest(r) {
			continue
		}
		return regularFile(filepath.Join(r, "rota-orchestrate", filepath.FromSlash(briefRel)))
	}
	return ""
}

// ExtractedBrief extracts this binary's skills into the cache under cache (a
// disk write: it makes the tree when missing) and returns the contract in it,
// or "" when cache is empty or extraction fails.
func ExtractedBrief(version, cache string) string {
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
	return regularFile(filepath.Join(tree, filepath.FromSlash(briefRel)))
}

func regularFile(p string) string {
	if fi, err := os.Stat(p); err != nil || fi.IsDir() {
		return ""
	}
	return p
}
