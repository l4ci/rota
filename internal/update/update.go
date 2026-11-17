// Package update is `rota update`: it finds how the rota binary was installed,
// reads the running version, asks GitHub for the latest release and says what
// the user would run to update. It never runs the update.
package update

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/proc"
	"github.com/l4ci/rota/internal/semver"
)

// Repo is the GitHub repository whose latest release rota update asks about.
const Repo = "l4ci/rota"

// Install types: how the running binary got where it is.
const (
	Brew    = "brew"    // under a Homebrew prefix
	Script  = "script"  // install.sh, or any other copy of a release binary
	Dev     = "dev"     // built from a checkout (no release version stamped)
	Unknown = "unknown" // the binary's path could not be resolved
)

// Result is the data of `rota update`. InstallRoot is the directory holding the
// running binary.
type Result struct {
	InstallType    string
	InstallRoot    string
	CurrentVersion string
	LatestVersion  string
	Status         string
	UpdateCommand  string
}

// Env is everything Check reads from the machine, so tests can pin it.
type Env struct {
	ExeDir  string // the directory of the running binary, symlinks resolved; "" when unknown
	Current string // the running binary's version
	Commit  string // the commit stamped at link time, as stamped (not shortened); "" when none
	// Latest returns the latest release version, or "" when it cannot be found.
	Latest func() string
	// SkillsNeedUpdate says whether a skill root needs `rota skills update`:
	// false only when every installed root is a current stub. Nil means yes.
	SkillsNeedUpdate func() bool
}

// DefaultEnv is the real machine. Latest runs `gh api repos/<repo>/releases/latest`
// through run, which only reads.
func DefaultEnv(current string, run proc.Runner) Env {
	var dir string
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		dir = filepath.Dir(exe)
	}
	return Env{ExeDir: dir, Current: current, Latest: func() string { return ghLatest(run) }}
}

// ghLatest asks gh through run. A missing gh, a non-zero exit or a timeout is "".
func ghLatest(run proc.Runner) string {
	res, err := run(context.Background(), proc.Cmd{
		Name:    "gh",
		Args:    []string{"api", "repos/" + Repo + "/releases/latest", "--jq", ".tag_name"},
		Timeout: 30 * time.Second,
	})
	if err != nil || res.ExitCode != 0 {
		return ""
	}
	tag := strings.TrimRight(res.Stdout, "\n")
	return strings.TrimPrefix(tag, "v")
}

// Check classifies the install, compares the running version with the latest
// release and names the command that updates it.
func Check(e Env) Result {
	kind := detect(e)
	r := Result{InstallType: kind, InstallRoot: e.ExeDir, CurrentVersion: e.Current}
	if e.Latest != nil {
		r.LatestVersion = e.Latest()
	}
	r.Status = "unknown"
	if r.LatestVersion != "" && stamped(r.CurrentVersion) {
		switch c := semver.CompareLenient(r.CurrentVersion, r.LatestVersion); {
		case c < 0:
			r.Status = "behind"
		case c > 0:
			r.Status = "ahead"
		default:
			r.Status = "current"
		}
	}
	r.UpdateCommand = command(kind)
	if e.SkillsNeedUpdate != nil && !e.SkillsNeedUpdate() {
		r.UpdateCommand = strings.TrimSuffix(r.UpdateCommand, refresh)
	}
	return r
}

// refresh is the step after a binary update that refreshes the skills the
// binary carries; it is dropped when every installed root is a current stub.
const refresh = " && rota skills update"

func command(kind string) string {
	switch kind {
	case Brew:
		return "brew update && brew upgrade rota" + refresh
	case Dev:
		return "git pull && go build -o <where rota lives> ./cmd/rota" + refresh
	}
	return "curl -fsSL https://raw.githubusercontent.com/" + Repo + "/main/install.sh | sh" + refresh
}

// stamped says whether v is a real version. An unstamped build ("", "dev",
// "(devel)") has nothing to compare, so its status stays unknown.
func stamped(v string) bool {
	return v != "" && v != "dev" && v != "(devel)"
}

// fullCommitLen is the width of the SHA the checkout rebuild (stalebin.Rebuild)
// stamps via `git rev-parse HEAD`. goreleaser's {{ .ShortCommit }} is git's
// auto-abbreviation (7 to 12 chars by repo size), so only a full SHA is a signal.
const fullCommitLen = 40

// detect: a binary under a Homebrew prefix is brew; a version that is empty,
// "dev", "(devel)" or carries a -dev suffix is a checkout build, and so is one
// stamped with a full-length commit (stalebin.Rebuild); anything else is a
// release binary somebody copied there, which install.sh also is.
func detect(e Env) string {
	if e.ExeDir == "" {
		return Unknown
	}
	for _, m := range []string{"/Cellar/", "/.linuxbrew/", "/opt/homebrew/"} {
		if strings.Contains(e.ExeDir+"/", m) {
			return Brew
		}
	}
	if v := e.Current; !stamped(v) || strings.HasSuffix(v, "-dev") {
		return Dev
	}
	if len(e.Commit) >= fullCommitLen {
		return Dev
	}
	return Script
}
