// Package update is `rota update`: it finds how the rota binary was installed,
// reads the running version, asks GitHub for the latest release and says what
// the user would run to update. It never runs the update.
package update

import (
	"context"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/proc"
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
	if r.CurrentVersion != "" && r.LatestVersion != "" {
		switch c := Compare(r.CurrentVersion, r.LatestVersion); {
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

// detect: a binary under a Homebrew prefix is brew; a version that is empty,
// "dev", "(devel)" or carries a -dev suffix is a checkout build; anything else is a
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
	if v := e.Current; v == "" || v == "dev" || v == "(devel)" || strings.HasSuffix(v, "-dev") {
		return Dev
	}
	return Script
}

var digits = regexp.MustCompile(`[0-9]+`)

// runs is tuple(int(p) for p in re.findall(r"\d+", v)).
func runs(v string) []*big.Int {
	var out []*big.Int
	for _, m := range digits.FindAllString(v, -1) {
		n, _ := new(big.Int).SetString(m, 10)
		out = append(out, n)
	}
	return out
}

// Compare is cmp_semver: the first three numeric runs of each version, zero
// padded to three, compared as numbers. It returns -1, 0 or 1.
func Compare(a, b string) int {
	three := func(v string) []*big.Int {
		p := runs(v)
		if len(p) > 3 {
			p = p[:3]
		}
		for len(p) < 3 {
			p = append(p, new(big.Int))
		}
		return p
	}
	pa, pb := three(a), three(b)
	for i := range pa {
		if c := pa[i].Cmp(pb[i]); c != 0 {
			return c
		}
	}
	return 0
}
