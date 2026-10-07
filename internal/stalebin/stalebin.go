// Package stalebin spots an installed rota that has fallen behind the rota
// source checkout it is run from. Workers and the merge gate run the installed
// binary, not the source, so a verb fixed on main stays dormant until rebuilt.
package stalebin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/git"
)

const module = "github.com/l4ci/rota"

// Rebuild keeps Version equal to VERSION (doctor's skills check compares the
// two) and stamps Commit and Date; replace the installed binary with an atomic mv.
const Rebuild = `go build -ldflags "-X github.com/l4ci/rota/internal/version.Version=$(cat VERSION)` +
	` -X github.com/l4ci/rota/internal/version.Commit=$(git rev-parse HEAD)` +
	` -X github.com/l4ci/rota/internal/version.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)"` +
	` -o rota.new ./cmd/rota && mv rota.new ~/.local/bin/rota`

// Finding is a binary that is behind its checkout.
type Finding struct {
	Commit  string // the binary's commit
	Head    string // the checkout's HEAD, short
	Behind  int    // commits HEAD has beyond Commit
	Rebuild string
}

// Detail is the one-line warning.
func (f Finding) Detail() string {
	n := fmt.Sprintf("%d commits", f.Behind)
	if f.Behind == 1 {
		n = "1 commit"
	}
	return fmt.Sprintf("installed rota is built from %s, %s behind checkout HEAD %s, and cmd/ or internal/ Go code changed since", f.Commit, n, f.Head)
}

// Check compares the binary's commit with the checkout holding dir. It finds
// nothing outside a checkout of rota's own module, for a binary with no commit
// stamp or one this checkout does not know, and when no non-test Go file under
// cmd/ or internal/ changed since that commit.
func Check(ctx context.Context, run git.Runner, dir, commit string) (Finding, bool) {
	if commit == "" {
		return Finding{}, false
	}
	out := func(args ...string) (string, bool) {
		r, err := run(ctx, dir, args...)
		if err != nil || r.ExitCode != 0 {
			return "", false
		}
		return strings.TrimSpace(r.Stdout), true
	}
	top, ok := out("rev-parse", "--show-toplevel")
	if !ok || !ownModule(top) {
		return Finding{}, false
	}
	head, ok := out("rev-parse", "HEAD")
	if !ok || head == "" || strings.HasPrefix(head, commit) {
		return Finding{}, false
	}
	if _, ok := out("rev-parse", "--verify", "-q", commit+"^{commit}"); !ok {
		return Finding{}, false
	}
	files, ok := out("diff", "--name-only", commit, "HEAD", "--", "cmd", "internal")
	if !ok || !hasGoChange(files) {
		return Finding{}, false
	}
	behind := 0
	if s, ok := out("rev-list", "--count", commit+"..HEAD"); ok {
		behind, _ = strconv.Atoi(s)
	}
	short := head
	if len(short) > 7 {
		short = short[:7]
	}
	return Finding{Commit: commit, Head: short, Behind: behind, Rebuild: Rebuild}, true
}

func ownModule(top string) bool {
	b, err := os.ReadFile(filepath.Join(top, "go.mod"))
	if err != nil {
		return false
	}
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.Fields(l); len(f) == 2 && f[0] == "module" {
			return f[1] == module
		}
	}
	return false
}

// hasGoChange: a test file does not change the binary.
func hasGoChange(files string) bool {
	for _, f := range strings.Split(files, "\n") {
		if strings.HasSuffix(f, ".go") && !strings.HasSuffix(f, "_test.go") {
			return true
		}
	}
	return false
}
