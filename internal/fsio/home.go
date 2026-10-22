package fsio

import (
	"path/filepath"
	"strings"
)

// ExpandHome replaces a leading "~" or "~/" in p with home, cleaned. An empty
// home, or a p that does not start with either form (so "~bob" too), comes
// back unchanged.
func ExpandHome(p, home string) string {
	if home == "" || (p != "~" && !strings.HasPrefix(p, "~/")) {
		return p
	}
	return filepath.Join(home, p[1:])
}
