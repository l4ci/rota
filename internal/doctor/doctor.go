// Package doctor is the logic behind `rota doctor`: one read-only preflight
// check per thing a parallel round depends on (git, the dispatch host, the
// forge CLI, the worker accounts, herdr's agent integration, the installed skills).
//
// Nothing here reaches os/exec or the real PATH directly: the caller injects
// Exec and Look, so tests need no real herdr, gh or tmux. A missing tool is a
// failed check, never an error.
package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/hook"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/proc"
	"github.com/l4ci/rota/internal/skills"
)

// Check statuses.
const (
	Pass = "pass"
	Fail = "fail"
	Skip = "skip"
	// Warn is a finding that does not fail the report: the round can run, but
	// the machine is about to make it fail (low disk).
	Warn = "warn"
)

// Check is one line of the report.
type Check struct {
	Name   string
	Status string
	Detail string
	Hint   string // set on every Fail
}

// Report is every check, in order.
type Report struct{ Checks []Check }

// OK is true when no check failed.
func (r Report) OK() bool {
	for _, c := range r.Checks {
		if c.Status == Fail {
			return false
		}
	}
	return true
}

// Account is one entry of work.accounts.
type Account struct{ Name, ConfigDir string }

// Disk is the space on one volume, in bytes.
type Disk struct {
	Path        string
	Free, Total uint64
}

// Result is what a finished command left behind.
type Result = proc.Result

// Exec runs bin (a path Look returned) in dir with extraEnv added to the
// process environment. A command that ran and exited non-zero is a Result
// with ExitCode set and a nil error; err means it could not run at all.
type Exec func(ctx context.Context, bin string, args []string, extraEnv []string, dir string) (Result, error)

// Input is everything Run needs from the outside.
type Input struct {
	Dir  string // working directory: where git runs
	Home string // expands a leading "~/" in a configDir

	// From the project config when .rota/ exists; zero values otherwise.
	Dispatch       string // work.dispatch
	IssuesProvider string // issues.provider
	Accounts       []Account
	SwitchOnUsage  bool // orchestrator.switchOnUsage (D4)
	// CodexHomes are the existing slot homes under <git-common-dir>/rota/codex/.
	CodexHomes []CodexHome
	// CodexTiers is whether any round.tiers.codex.<tier> is set.
	CodexTiers bool

	// Skills is the state of the installed skill sets (skills.Set.Status);
	// nil when nothing was read.
	Skills *skills.Report

	// LegacyDir is the directory that still holds the old state folder with no .rota/ (a
	// project `rota migrate hv` has not moved); "" otherwise.
	LegacyDir string

	// ProjectRoot is the directory holding .rota/, "" outside a rota project:
	// the orchestrator checks (statusline, stop-hook) skip without it.
	ProjectRoot string
	// ConfigDirs are the Claude config dirs whose settings.json counts for
	// those checks: each account's configDir, else the default user dir.
	ConfigDirs []string

	// Disk is the free space of the volume the round writes to; nil when it
	// could not be read (no check then). MinFreeDiskPercent is the
	// doctor.minFreeDiskPercent threshold; 0 turns the check off. Leftovers
	// names what rota left behind that would give space back (stale scratch
	// worktrees, leaked temp dirs): lines for the warning, nothing when clean.
	Disk               *Disk
	MinFreeDiskPercent int
	Leftovers          []string

	Exec Exec
	// Getenv reads the environment for host detection (HERDR_ENV, TMUX); nil
	// reads as empty.
	Getenv func(string) string
	// Look resolves a tool name to a path, or false when it is not found.
	Look func(name string) (string, bool)
}

// Run executes every check in the contract's order.
func Run(ctx context.Context, in Input) Report {
	d := &runner{in: in, ctx: ctx}
	checks := []Check{
		d.git(), d.host(), d.tracker(), d.accounts(), d.hook(), d.statusline(), d.stopHook(), d.switchCheck(), d.skills(), d.codex(),
	}
	if c, ok := d.disk(); ok {
		// Only a volume below the threshold adds a line: a healthy one stays
		// out of the report, like the legacy-state line below.
		checks = append(checks, c)
	}
	if in.LegacyDir != "" {
		// Only a project that still holds the old state folder gets this line.
		checks = append([]Check{fail("state", in.LegacyDir+" still uses the old .hv state folder", "run: rota migrate hv")}, checks...)
	}
	return Report{Checks: checks}
}

type runner struct {
	in  Input
	ctx context.Context
}

func pass(name, detail string) Check { return Check{Name: name, Status: Pass, Detail: detail} }
func skip(name, detail string) Check { return Check{Name: name, Status: Skip, Detail: detail} }
func fail(name, detail, hint string) Check {
	return Check{Name: name, Status: Fail, Detail: detail, Hint: hint}
}

// run runs a looked-up tool; ok is false when it could not run at all.
func (d *runner) run(bin string, args []string, env []string) (Result, bool) {
	r, err := d.in.Exec(d.ctx, bin, args, env, d.in.Dir)
	return r, err == nil
}

func (d *runner) git() Check {
	bin, ok := d.in.Look("git")
	if !ok {
		return fail("git", "git not found on PATH", "install git")
	}
	r, ran := d.run(bin, []string{"check-ignore", "-q", ".worktrees/x"}, nil)
	switch {
	case !ran:
		return fail("git", "git could not run", "install git")
	case r.ExitCode == 0:
		return pass("git", ".worktrees/ is gitignored")
	case r.ExitCode == 1:
		return fail("git", ".worktrees/ is not gitignored", "add .worktrees/ to .gitignore (rota init does this)")
	default:
		return fail("git", "not inside a git repository", "run: git init")
	}
}

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// roundHost is the host a round would run on: the same detection the round
// verbs use, so doctor checks what `round start` will drive.
func (d *runner) roundHost() string {
	getenv := d.in.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	return host.Resolve("", d.in.Dispatch, getenv, func(name string) (string, error) {
		if p, ok := d.in.Look(name); ok {
			return p, nil
		}
		return "", os.ErrNotExist
	})
}

func (d *runner) host() Check {
	switch d.roundHost() {
	case "herdr":
		bin, ok := d.in.Look("herdr")
		if !ok {
			return fail("host", "herdr not found on PATH", "install herdr 0.9.x (https://herdr.dev)")
		}
		r, ran := d.run(bin, []string{"--version"}, nil)
		out := strings.TrimSpace(r.Stdout + " " + r.Stderr)
		m := versionRe.FindStringSubmatch(out)
		if !ran || r.ExitCode != 0 || m == nil {
			return fail("host", "herdr version unreadable", "reinstall herdr 0.9.x (https://herdr.dev)")
		}
		v := m[0]
		if m[1] != "0" || m[2] != "9" {
			return fail("host", fmt.Sprintf("herdr %s, need 0.9.x", v), "install herdr 0.9.x (https://herdr.dev)")
		}
		return pass("host", "herdr "+v)
	case "tmux":
		if _, ok := d.in.Look("tmux"); !ok {
			return fail("host", "tmux not found on PATH", "install tmux")
		}
		return pass("host", "tmux on PATH")
	default:
		name := d.in.Dispatch
		if name == "" {
			name = "subagent"
		}
		return skip("host", "work.dispatch is "+name+", not in herdr or tmux: solo, no terminal host needed")
	}
}

var forgeHost = regexp.MustCompile(`(?i)github|gitlab`)

// provider is "github", "gitlab" or "": the origin host decides, and
// issues.provider is only the fallback (as in `rota issues provider`).
func (d *runner) provider() string {
	if bin, ok := d.in.Look("git"); ok {
		if r, ran := d.run(bin, []string{"remote", "get-url", "origin"}, nil); ran && r.ExitCode == 0 {
			if m := forgeHost.FindString(r.Stdout); m != "" {
				return strings.ToLower(m)
			}
		}
	}
	if p := d.in.IssuesProvider; p == "github" || p == "gitlab" {
		return p
	}
	return ""
}

func (d *runner) tracker() Check {
	p := d.provider()
	if p == "" {
		return skip("tracker", "no origin remote naming github or gitlab")
	}
	cli, login := "gh", "gh auth login"
	if p == "gitlab" {
		cli, login = "glab", "glab auth login"
	}
	bin, ok := d.in.Look(cli)
	if !ok {
		return fail("tracker", cli+" not found on PATH", "install "+cli)
	}
	r, ran := d.run(bin, []string{"auth", "status"}, nil)
	if !ran || r.ExitCode != 0 {
		return fail("tracker", cli+" is not authenticated", login)
	}
	return pass("tracker", cli+" authenticated ("+p+")")
}

func (d *runner) expand(dir string) string {
	if strings.HasPrefix(dir, "~/") && d.in.Home != "" {
		return filepath.Join(d.in.Home, dir[2:])
	}
	return dir
}

func (d *runner) accounts() Check {
	if len(d.in.Accounts) == 0 {
		return skip("accounts", "no accounts in work.accounts")
	}
	var bad []string
	var hint string
	for _, a := range d.in.Accounts {
		dir := d.expand(a.ConfigDir)
		var why string
		if dir == "" {
			why = "no configDir"
			if hint == "" {
				hint = "set configDir for account " + a.Name + " in work.accounts"
			}
		} else if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			why = "configDir " + dir + " does not exist"
		} else if fi, err := os.Stat(filepath.Join(dir, ".credentials.json")); err != nil || !fi.Mode().IsRegular() {
			why = "no credentials file in " + dir
		}
		if why != "" {
			bad = append(bad, a.Name+": "+why)
			if hint == "" {
				hint = "CLAUDE_CONFIG_DIR=" + a.ConfigDir + " claude /login"
			}
		}
	}
	if len(bad) > 0 {
		return fail("accounts", strings.Join(bad, "; "), hint)
	}
	return pass("accounts", fmt.Sprintf("%d accounts with credentials", len(d.in.Accounts)))
}

const hookHint = "herdr integration install claude"

func (d *runner) hook() Check {
	if d.roundHost() != "herdr" {
		return skip("hook", "host is not herdr")
	}
	if len(d.in.Accounts) == 0 {
		return skip("hook", "no accounts in work.accounts")
	}
	bin, ok := d.in.Look("herdr")
	if !ok {
		return skip("hook", "herdr not on PATH (see host)")
	}
	var parts []string
	failed := false
	for _, a := range d.in.Accounts {
		r, ran := d.run(bin, []string{"integration", "status"}, []string{"CLAUDE_CONFIG_DIR=" + d.expand(a.ConfigDir)})
		if !ran || r.ExitCode != 0 {
			failed = true
			parts = append(parts, a.Name+": herdr integration status failed")
			continue
		}
		state := harness.ParseIntegration(r.Stdout, "claude")
		if state != "current" {
			failed = true
		}
		parts = append(parts, a.Name+": "+state)
	}
	if failed {
		return fail("hook", strings.Join(parts, "; "), hookHint)
	}
	return pass("hook", strings.Join(parts, "; "))
}

const (
	statuslineHint = "rota hook install --wrap-statusline"
	stopHookHint   = "rota hook install"
)

// projectFiles reads the project's two settings files, in precedence order.
func (d *runner) projectFiles() []hookFile {
	return []hookFile{
		d.read(filepath.Join(d.in.ProjectRoot, ".claude", "settings.local.json")),
		d.read(filepath.Join(d.in.ProjectRoot, ".claude", "settings.json")),
	}
}

type hookFile struct {
	path   string
	events map[string]string
	slCmd  string
	hasSL  bool
	exists bool
	err    error
}

func (d *runner) read(path string) hookFile {
	f := hookFile{path: path}
	o, err := hook.ReadSettings(path)
	if err != nil {
		f.err, f.exists = err, true
		return f
	}
	if o == nil {
		return f
	}
	f.exists = true
	f.events = hook.MarkedEvents(o)
	_, f.slCmd, f.hasSL = hook.StatusLine(o)
	return f
}

func (d *runner) userFile(dir string) hookFile {
	return d.read(filepath.Join(d.expand(dir), "settings.json"))
}

// optIn reports whether anything `rota hook install` writes is present in any
// settings file in scope: a `# rota-hook` entry for any event, or a statusLine
// that runs `rota statusline dump`. The hooks are opt-in, so with none of that
// the checks skip; they fail only on a broken or partial install. Unreadable
// files are returned so a skip can say it could not look there.
func (d *runner) optIn() (in bool, unreadable []string) {
	files := d.projectFiles()
	for _, dir := range d.in.ConfigDirs {
		files = append(files, d.userFile(dir))
	}
	for _, f := range files {
		if f.err != nil {
			unreadable = append(unreadable, f.path)
		}
		if len(f.events) > 0 || (f.hasSL && strings.Contains(f.slCmd, hook.StatuslineCmd)) {
			in = true
		}
	}
	return in, unreadable
}

func notInstalled(name string, unreadable []string) Check {
	detail := "hooks not installed (opt-in)"
	if len(unreadable) > 0 {
		detail += "; cannot read " + strings.Join(unreadable, ", ")
	}
	return skip(name, detail+": rota hook install")
}

func (d *runner) statusline() Check {
	const name = "statusline"
	if d.in.ProjectRoot == "" {
		return skip(name, "not inside a rota project")
	}
	if in, bad := d.optIn(); !in {
		return notInstalled(name, bad)
	}
	proj := d.projectFiles()
	dirs := d.in.ConfigDirs
	if len(dirs) == 0 {
		dirs = []string{""}
	}
	anyFile := proj[0].exists || proj[1].exists
	var parts, bad []string
	for _, dir := range dirs {
		files := append([]hookFile{}, proj...)
		label := "project"
		if dir != "" {
			u := d.userFile(dir)
			files = append(files, u)
			label = dir
			anyFile = anyFile || u.exists
		}
		var eff *hookFile
		broken := ""
		for i := range files {
			if files[i].err != nil {
				broken = files[i].path
				break
			}
			if files[i].hasSL {
				eff = &files[i]
				break
			}
		}
		switch {
		case broken != "":
			bad = append(bad, label+": cannot read "+broken)
		case eff == nil:
			bad = append(bad, label+": no statusLine")
		case !strings.Contains(eff.slCmd, hook.StatuslineCmd):
			bad = append(bad, label+": statusLine does not run rota statusline dump ("+eff.path+")")
		default:
			parts = append(parts, label+": "+eff.path)
		}
	}
	if !anyFile {
		return skip(name, "no Claude settings file in scope")
	}
	if len(bad) > 0 {
		return fail(name, strings.Join(bad, "; "), statuslineHint)
	}
	return pass(name, "rota statusline dump runs ("+strings.Join(parts, "; ")+")")
}

// switchCheck is D4's: with orchestrator.switchOnUsage on, the Stop hook must
// be installed and a second account must exist to move to. It reads config and
// settings files only.
func (d *runner) switchCheck() Check {
	const name = "switch"
	if !d.in.SwitchOnUsage {
		return skip(name, "orchestrator.switchOnUsage is off")
	}
	if d.in.ProjectRoot == "" {
		return skip(name, "not inside a rota project")
	}
	var with []string
	for _, a := range d.in.Accounts {
		if a.ConfigDir != "" {
			with = append(with, a.Name)
		}
	}
	if len(with) < 2 {
		return fail(name, fmt.Sprintf("%d account with a configDir in work.accounts, need 2 to move between", len(with)),
			"add a second account to work.accounts")
	}
	if h := d.stopHook(); h.Status != Pass {
		return fail(name, "the Stop hook is not installed: "+h.Detail, stopHookHint)
	}
	return pass(name, fmt.Sprintf("%d accounts, Stop hook installed", len(with)))
}

func (d *runner) stopHook() Check {
	const name = "stop-hook"
	if d.in.ProjectRoot == "" {
		return skip(name, "not inside a rota project")
	}
	if in, bad := d.optIn(); !in {
		return notInstalled(name, bad)
	}
	files := d.projectFiles()
	for _, dir := range d.in.ConfigDirs {
		files = append(files, d.userFile(dir))
	}
	found := map[string]string{}
	var unreadable []string
	for _, f := range files {
		if f.err != nil {
			unreadable = append(unreadable, f.path)
		}
		for ev, c := range f.events {
			if _, ok := found[ev]; !ok {
				found[ev] = c
			}
		}
	}
	var missing []string
	for _, ev := range []string{"Stop", "SessionStart"} {
		if _, ok := found[ev]; !ok {
			missing = append(missing, ev)
		}
	}
	if len(missing) > 0 {
		detail := "no # rota-hook entry for " + strings.Join(missing, " and ")
		if len(unreadable) > 0 {
			detail += " (cannot read " + strings.Join(unreadable, ", ") + ")"
		}
		return fail(name, detail, stopHookHint)
	}
	for _, ev := range []string{"Stop", "SessionStart"} {
		first := strings.Fields(found[ev])[0]
		if _, ok := d.resolve(first); !ok {
			return fail(name, ev+" hook runs "+first+", which is not found", stopHookHint)
		}
	}
	return pass(name, "Stop and SessionStart hooks run rota")
}

// resolve finds the executable a hook command starts with.
func (d *runner) resolve(word string) (string, bool) {
	if strings.Contains(word, "/") {
		w := d.expand(word)
		fi, err := os.Stat(w)
		return w, err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
	}
	return d.in.Look(word)
}

// skills compares the installed skill sets with the binary's. It skips until
// something is installed (the repo's opt-in rule) and fails only on a broken
// install: another digest, or missing or edited files.
func (d *runner) skills() Check {
	const name = "skills"
	const installHint = "run: rota skills install"
	var have []skills.RootStatus
	if rep := d.in.Skills; rep != nil {
		for _, r := range rep.Roots {
			if r.Installed {
				have = append(have, r)
			}
		}
	}
	if len(have) == 0 {
		return skip(name, "not installed (opt-in): "+installHint)
	}
	rep := d.in.Skills
	var problems []string
	hint := "run: rota skills update"
	for _, r := range have {
		if !r.Current {
			problems = append(problems, fmt.Sprintf("%s: skills %s, rota %s", r.Path, versionOrDigest(r.Version, r.Digest), versionOrDigest(rep.Version, rep.Digest)))
		}
		if len(r.Missing) > 0 {
			problems = append(problems, fmt.Sprintf("%s: %d missing (%s)", r.Path, len(r.Missing), first(r.Missing)))
		}
		if len(r.Edited) > 0 {
			problems = append(problems, fmt.Sprintf("%s: %d edited (%s)", r.Path, len(r.Edited), first(r.Edited)))
			hint = "run: rota skills update --overwrite"
		}
	}
	if len(problems) > 0 {
		return fail(name, strings.Join(problems, "; "), hint)
	}
	return pass(name, fmt.Sprintf("%d roots match rota %s", len(have), versionOrDigest(rep.Version, rep.Digest)))
}

// versionOrDigest names a skill set: its version when it has one, else the
// start of its digest (a dev build has no version).
func versionOrDigest(ver, digest string) string {
	if ver = strings.TrimPrefix(strings.TrimSpace(ver), "v"); ver != "" && ver != "(devel)" && ver != "dev" {
		return ver
	}
	if len(digest) > 12 {
		digest = digest[:12]
	}
	return digest
}

func first(l []string) string {
	if len(l) > 1 {
		return l[0] + ", ..."
	}
	return l[0]
}

// disk warns when the free share of the volume is under the threshold, and
// names the rota leftovers that would give space back. It reports nothing when
// the space is fine, unreadable or the check is off.
func (d *runner) disk() (Check, bool) {
	disk, min := d.in.Disk, d.in.MinFreeDiskPercent
	if disk == nil || disk.Total == 0 || min <= 0 {
		return Check{}, false
	}
	pct := float64(disk.Free) / float64(disk.Total) * 100
	if pct >= float64(min) {
		return Check{}, false
	}
	detail := fmt.Sprintf("%s has %s free (%.0f%%), under the %d%% threshold", disk.Path, HumanBytes(disk.Free), pct, min)
	hint := "free space before a round; set doctor.minFreeDiskPercent to change the threshold"
	if len(d.in.Leftovers) > 0 {
		hint = "reclaimable rota leftovers: " + strings.Join(d.in.Leftovers, "; ")
	}
	return Check{Name: "disk", Status: Warn, Detail: detail, Hint: hint}, true
}

// HumanBytes is a size in the largest unit that keeps it above 1.
func HumanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
