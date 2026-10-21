// Package doctor is the logic behind `rota doctor`: one read-only preflight
// check per thing a parallel round depends on (git, jq, the dispatch host, the
// forge CLI, the worker accounts, herdr's agent integration, the installed skills).
//
// Nothing here reaches os/exec or the real PATH directly: the caller injects
// Exec and Look, so tests need no real herdr, gh or tmux. A missing tool is a
// failed check, never an error.
package doctor

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/hook"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/proc"
	"github.com/l4ci/rota/internal/skills"
	"github.com/l4ci/rota/internal/stalebin"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
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

// ConnCheck is the outcome of the project's tracker connection check. State
// is a connections.State: ok, unknown, nosecret, failed or unsupported.
type ConnCheck struct {
	Name, State, Detail, Hint string
}

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
	Dispatch     string           // round.dispatch
	RegistryHost host.Kind        // the host `round start` recorded, "" when no round is in flight
	RoundHost    host.Kind        // the host the round's verbs drive (worker.ResolveHost), resolved with no project too
	Tracker      tracker.Settings // tracker.SettingsFromConfig over the project config
	// Tracker.Source is issues.source, "" outside a rota project (an unmigrated
	// legacy config reads as config.SourceIssues). SourceProblem is the reason
	// forge.provider or issues.source holds an invalid value ("" when valid);
	// the settings are then left zero. TeamProblem is why the Linear team or
	// It's a Plan project could not be read through a working connection ("" when
	// it was, or was not tried).
	SourceProblem string
	TeamProblem   string
	Accounts      []config.Account
	// Credentials reads each account's login; the zero value reads files only.
	Credentials worker.Credentials
	// OrchestratorConfigDir is the orchestrator's CLAUDE_CONFIG_DIR, "" when unset.
	OrchestratorConfigDir string
	SwitchOnUsage         bool // orchestrator.switchOnUsage (D4)
	// Kinds is what each worker harness with a check of its own (Doctor) has
	// configured, by kind.
	Kinds map[string]KindInput

	// Connection is the project's issues.connection and what checking it
	// found; nil (no connection named) adds no line.
	Connection *ConnCheck

	// AgentProblems are the generated subagent files that are missing or have
	// drifted from config (agents.Problems); empty adds no line.
	AgentProblems []string

	// GitignoreMissing are the managed .gitignore lines the project lacks
	// (initproj.MissingIgnoreLines); empty adds no line.
	GitignoreMissing []string

	// Skills is the state of the installed skill sets (skills.Set.Status);
	// nil when nothing was read.
	Skills *skills.Report

	// StaleMilestoneFiles are the .rota/milestones/*.md names left over while
	// milestones live on the tracker; empty adds no line.
	StaleMilestoneFiles []string

	// StateDir is the rota state dir (the project registry lives there); ""
	// adds no line.
	StateDir string

	// LegacyDir is the directory that still holds the old state folder with no .rota/ (a
	// project `rota migrate hv` has not moved); "" otherwise.
	LegacyDir string

	// ConfigDir is the global rota config dir in use and why ("override",
	// "xdg" or "default"); ConfigDirWarning says a relative ROTA_CONFIG_DIR
	// was ignored. Empty ConfigDir adds no line.
	ConfigDir, ConfigDirSource, ConfigDirWarning string

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
	// CacheDisk is the volume holding the Go build cache when it is not the
	// one Disk reads (the gate's builds grow it); nil adds no line.
	CacheDisk *Disk
	Leftovers []string

	// StaleBinary is set when the installed rota is behind the rota source
	// checkout Dir is in (stalebin.Check); nil adds no line.
	StaleBinary *stalebin.Finding

	// NothingToVerify is set when test.full and test.e2e are both empty under a
	// local verify, so the merge gate would refuse; false adds no line.
	NothingToVerify bool

	// Slots are the live worker slots with a reserved port block; empty skips
	// the ports check. CwdOf answers a process's working directory ("" when
	// unknown); nil reads every directory as unknown.
	Slots []SlotBlock
	CwdOf func(pid int) string

	Exec Exec
	// Look resolves a tool name, or a path with a slash, to an executable path, or
	// false when it is not found. It is the only filesystem and PATH read for tools.
	Look func(name string) (string, bool)
}

// Run executes every check in the contract's order.
func Run(ctx context.Context, in Input) Report {
	d := &runner{in: in, ctx: ctx}
	checks := []Check{
		d.git(), d.jq(), d.host(), d.tracker(), d.accounts(), d.hook(), d.statusline(), d.stopHook(), d.guardHook(), d.switchCheck(), d.skills(),
	}
	checks = append(checks, d.harnessChecks()...)
	if c, ok := d.connection(); ok {
		checks = append(checks, c)
	}
	if c, ok := d.disk(); ok {
		// Only a volume below the threshold adds a line: a healthy one stays
		// out of the report, like the legacy-state line below.
		checks = append(checks, c)
	}
	if c, ok := d.cacheDisk(); ok {
		checks = append(checks, c)
	}
	if in.ConfigDir != "" {
		checks = append(checks, d.configDir())
	}
	if len(in.AgentProblems) > 0 {
		checks = append(checks, Check{Name: "agents", Status: Warn, Detail: strings.Join(in.AgentProblems, "; "), Hint: "run: rota agents write"})
	}
	if len(in.GitignoreMissing) > 0 {
		checks = append(checks, Check{Name: "gitignore", Status: Warn, Detail: "missing from .gitignore: " + strings.Join(in.GitignoreMissing, ", "), Hint: "run: rota init"})
	}
	if len(in.StaleMilestoneFiles) > 0 {
		checks = append(checks, Check{Name: "milestones", Status: Warn, Detail: "milestones live on the tracker, but .rota/milestones still holds " + strings.Join(in.StaleMilestoneFiles, ", ") + ": no verb reads them", Hint: "run: rota migrate milestone-files --apply"})
	}
	if c, ok := d.ports(); ok {
		checks = append(checks, c)
	}
	if f := in.StaleBinary; f != nil {
		// Only a binary behind its own checkout adds a line.
		checks = append(checks, Check{Name: "binary", Status: Warn, Detail: f.Detail(), Hint: "rebuild: " + f.Rebuild})
	}
	if in.NothingToVerify {
		// Only an unconfigured project adds a line; it warns, never fails.
		checks = append(checks, Check{Name: "verify", Status: Warn, Detail: "test.full and test.e2e are both empty: the merge gate will refuse until test.full is set", Hint: "run: rota config set test.full '[...]'"})
	}
	if in.StateDir != "" {
		checks = append(checks, pass("state dir", in.StateDir))
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

var configDirWhy = map[string]string{
	"override": "ROTA_CONFIG_DIR override",
	"xdg":      "from XDG_CONFIG_HOME",
	"default":  "default ~/.config/rota",
}

func (d *runner) configDir() Check {
	detail := d.in.ConfigDir + " (" + configDirWhy[d.in.ConfigDirSource] + ")"
	if w := d.in.ConfigDirWarning; w != "" {
		return Check{Name: "config dir", Status: Warn, Detail: detail + "; " + w, Hint: "set ROTA_CONFIG_DIR to an absolute path, or unset it"}
	}
	return pass("config dir", detail)
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

// connection is the project's tracker connection: it must exist on this
// machine, hold a secret and pass its test read. Only a project that names one
// gets the line. A kind with no test read yet warns instead of failing: the
// connection is fine, this build cannot prove it.
func (d *runner) connection() (Check, bool) {
	c := d.in.Connection
	if c == nil {
		return Check{}, false
	}
	switch c.State {
	case "ok":
		return pass("connection", c.Detail), true
	case "unsupported":
		return Check{Name: "connection", Status: Warn, Detail: c.Detail, Hint: c.Hint}, true
	}
	return fail("connection", c.Detail, c.Hint), true
}

// jq is a skill dependency, not a round one: the skills read fields out of
// `rota … --json` with it, and a missing jq fails them halfway through a write.
func (d *runner) jq() Check {
	if _, ok := d.in.Look("jq"); !ok {
		return fail("jq", "jq not found on PATH", "install jq (skills read rota --json output with it)")
	}
	return pass("jq", "jq on PATH")
}

// roundHost is the host a round runs on, resolved by worker.ResolveHost when
// the input was gathered, so doctor checks what the round verbs will drive.
func (d *runner) roundHost() host.Kind { return d.in.RoundHost }

func (d *runner) host() Check {
	switch d.roundHost() {
	case host.Herdr:
		want := host.SupportedHerdr + ".x"
		bin, ok := d.in.Look(host.Herdr.Binary())
		if !ok {
			return fail("host", "herdr not found on PATH", "install herdr "+want+" (https://herdr.dev)")
		}
		r, ran := d.run(bin, []string{"--version"}, nil)
		out := strings.TrimSpace(r.Stdout + " " + r.Stderr)
		v, err := host.HerdrVersion(out)
		if !ran || r.ExitCode != 0 || v == "" {
			return fail("host", "herdr version unreadable", "reinstall herdr "+want+" (https://herdr.dev)")
		}
		if err != nil {
			return fail("host", fmt.Sprintf("herdr %s, need %s", v, want), "install herdr "+want+" (https://herdr.dev)")
		}
		return pass("host", "herdr "+v)
	case host.Tmux:
		if _, ok := d.in.Look(host.Tmux.Binary()); !ok {
			return fail("host", "tmux not found on PATH", "install tmux")
		}
		return pass("host", "tmux on PATH")
	default:
		if d.in.RegistryHost == host.Solo {
			return skip("host", "the recorded round is solo: workers are subagents, no terminal host needed")
		}
		name := d.in.Dispatch
		if name == "" {
			name = "subagent"
		}
		return skip("host", "round.dispatch is "+name+", not in herdr or tmux: solo, no terminal host needed")
	}
}

// trackerExec adapts the injected Exec and Look to the tracker's, so the
// tracker owners (OriginURL, CLI) run through doctor's test seam.
func (d *runner) trackerExec() tracker.Exec {
	return func(ctx context.Context, dir, name string, args []string, _ []byte) ([]byte, []byte, int, error) {
		bin, ok := d.in.Look(name)
		if !ok {
			return nil, nil, 0, exec.ErrNotFound
		}
		r, err := d.in.Exec(ctx, bin, args, nil, dir)
		if err != nil {
			return nil, nil, 0, err
		}
		return []byte(r.Stdout), []byte(r.Stderr), r.ExitCode, nil
	}
}

// resolveForge is the forge doctor resolves the way the verbs do
// (tracker.ResolveProvider): forge.provider when it names github, gitlab or
// local, else the origin host; no origin remote at all is local. "" when the
// origin names no forge.
func (d *runner) resolveForge() string {
	p, err := tracker.ResolveProvider(d.ctx, "", d.in.Tracker.Provider, d.in.Dir, d.trackerExec())
	if err != nil {
		return ""
	}
	return p
}

// forgeLine says which forge doctor resolved and how, and what issues.source
// is: "forge github (from origin), issues file". An empty source (outside a
// rota project) leaves the issues half out.
func (d *runner) forgeLine(f string) string {
	how := "from origin"
	switch {
	case f == "":
		how = "origin names neither github nor gitlab"
	case d.in.Tracker.Provider == "github" || d.in.Tracker.Provider == "gitlab" || d.in.Tracker.Provider == "local":
		how = "forge.provider"
	case f == "local":
		how = "no origin remote"
	}
	shown := f
	if shown == "" {
		shown = "unknown"
	}
	line := "forge " + shown + " (" + how + ")"
	switch src := d.in.Tracker.Source; src {
	case "":
	case config.SourceIssues:
		line += ", issues on the tracker (legacy backlog.backend, run: rota migrate source --apply)"
	default:
		line += ", issues " + src
	}
	return line
}

// tracker checks the forge and the issue source together: it reports the
// detected forge (no remote with forge.provider auto is local) and the issue
// source, fails when issues.source names github or gitlab but the forge is
// something else (the issues and the code would live on different hosts), and
// otherwise checks that the forge CLI is installed and authenticated.
func (d *runner) tracker() Check {
	if d.in.SourceProblem != "" {
		return fail("tracker", d.in.SourceProblem, "run: rota config set forge.provider auto, and rota config set issues.source file|github|gitlab")
	}
	if d.in.Tracker.Source == config.SourceJira {
		switch {
		case d.in.Tracker.JiraProject == "":
			return fail("tracker", "issues.source is jira but issues.jira.project is not set", "run: rota config set issues.jira.project <KEY>")
		case d.in.Tracker.Connection == "":
			return fail("tracker", "issues.source is jira but issues.connection is not set", "run: rota connect add <name> --kind jira ..., then rota config set issues.connection <name>")
		}
	}
	if d.in.Tracker.Source == config.SourceLinear {
		switch {
		case d.in.Tracker.LinearTeam == "":
			return fail("tracker", "issues.source is linear but issues.linear.team is not set", "run: rota config set issues.linear.team <KEY>")
		case d.in.Tracker.Connection == "":
			return fail("tracker", "issues.source is linear but issues.connection is not set", "run: rota connect add <name> --kind linear --token-stdin, then rota config set issues.connection <name>")
		case d.in.TeamProblem != "":
			return fail("tracker", d.in.TeamProblem, "check issues.linear.team against the team's key in Linear, and the API key's access")
		}
	}
	if d.in.Tracker.Source == config.SourceItsaplan {
		switch {
		case d.in.Tracker.ItsaplanProject == "":
			return fail("tracker", "issues.source is itsaplan but issues.itsaplan.project is not set", "run: rota config set issues.itsaplan.project <KEY>")
		case d.in.Tracker.Connection == "":
			return fail("tracker", "issues.source is itsaplan but issues.connection is not set", "run: rota connect add <name> --kind itsaplan --site <url> --token-stdin, then rota config set issues.connection <name>")
		case d.in.TeamProblem != "":
			return fail("tracker", d.in.TeamProblem, "check issues.itsaplan.project against the project's key in It's a Plan, and the API key's access")
		}
	}
	f := d.resolveForge()
	line := d.forgeLine(f)
	if src := d.in.Tracker.Source; (src == config.SourceGitHub || src == config.SourceGitLab) && f != src {
		shown := f
		if shown == "" {
			shown = "unknown"
		}
		return fail("tracker", "issues.source is "+src+" but the forge is "+shown+": "+line,
			"set issues.source "+strutil.OrDefault(f, "file")+", or forge.provider "+src+" if detection is wrong")
	}
	switch f {
	case "local":
		return skip("tracker", "no pull requests: "+line)
	case "":
		return skip("tracker", "no origin remote naming github or gitlab (set forge.provider): "+line)
	}
	cli := tracker.CLIName(f)
	if _, ok := d.in.Look(cli); !ok {
		return fail("tracker", cli+" not found on PATH", "install "+cli)
	}
	c := &tracker.CLI{Provider: f, Dir: d.in.Dir, Exec: d.trackerExec(), LookPath: func(n string) (string, error) {
		if bin, ok := d.in.Look(n); ok {
			return bin, nil
		}
		return "", exec.ErrNotFound
	}}
	if err := c.CheckAuth(d.ctx); err != nil {
		return fail("tracker", cli+" is not authenticated", cli+" auth login")
	}
	return pass("tracker", cli+" authenticated ("+line+")")
}

func (d *runner) expand(dir string) string { return worker.ExpandConfigDirIn(dir, d.in.Home) }

func (d *runner) accounts() Check {
	if w := worker.ConfigDirMismatch(d.in.OrchestratorConfigDir, d.in.Accounts, d.in.Home); w != "" {
		return Check{Name: "accounts", Status: Warn, Detail: w, Hint: "add that configDir to round.accounts in .rota/config.local.json, or start the orchestrator under a listed one"}
	}
	if len(d.in.Accounts) == 0 {
		return skip("accounts", "no accounts in round.accounts")
	}
	var bad []string
	var hint string
	for _, a := range d.in.Accounts {
		var dir, why string
		if a.ConfigDir == "" {
			why = "no configDir"
			if hint == "" {
				hint = "set configDir for account " + a.Name + " in round.accounts"
			}
		} else if dir = worker.ExpandConfigDirIn(a.ConfigDir, d.in.Home); !fsio.IsDir(dir) {
			why = "configDir " + dir + " does not exist"
		} else if p := d.in.Credentials.Problem(dir); p != "" {
			why = p + " in " + dir
		}
		if why != "" {
			bad = append(bad, a.Name+": "+why)
			if hint == "" {
				hint = "CLAUDE_CONFIG_DIR=" + a.ConfigDir + " claude /login"
				if strings.Contains(why, "Keychain") {
					hint += " (on macOS the login is stored in the Keychain, not a file)"
				}
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
	if d.roundHost() != host.Herdr {
		return skip("hook", "host is not herdr")
	}
	if len(d.in.Accounts) == 0 {
		return skip("hook", "no accounts in round.accounts")
	}
	bin, ok := d.in.Look(host.Herdr.Binary())
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
		state := harness.ParseIntegration(r.Stdout, harness.ClaudeIntegration)
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
	guardHookHint  = "rota hook install --scope project (or --scope user)"
)

// projectFiles reads the project's two settings files, in precedence order.
func (d *runner) projectFiles() []hookFile {
	return []hookFile{
		d.read(hook.SettingsPath(hook.ScopeProjectLocal, d.in.ProjectRoot)),
		d.read(hook.SettingsPath(hook.ScopeProject, d.in.ProjectRoot)),
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
	return d.read(hook.SettingsPath(hook.ScopeUser, d.expand(dir)))
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
		return fail(name, fmt.Sprintf("%d account with a configDir in round.accounts, need 2 to move between", len(with)),
			"add a second account to round.accounts")
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

// guardHook checks the worker guard (#702). It must sit in project or user
// scope: project-local is untracked, so a worker worktree never reads it.
func (d *runner) guardHook() Check {
	const name = "guard-hook"
	if d.in.ProjectRoot == "" {
		return skip(name, "not inside a rota project")
	}
	if in, bad := d.optIn(); !in {
		return notInstalled(name, bad)
	}
	files := d.projectFiles()[1:] // settings.json; settings.local.json never reaches a worktree
	for _, dir := range d.in.ConfigDirs {
		files = append(files, d.userFile(dir))
	}
	var unreadable []string
	cmd := ""
	for _, f := range files {
		if f.err != nil {
			unreadable = append(unreadable, f.path)
		}
		if c, ok := f.events[hook.EventGuard]; ok && cmd == "" {
			cmd = c
		}
	}
	if cmd == "" {
		detail := "no # rota-hook PreToolUse entry in project or user scope (project-local does not reach worker worktrees)"
		if len(unreadable) > 0 {
			detail += " (cannot read " + strings.Join(unreadable, ", ") + ")"
		}
		return fail(name, detail, guardHookHint)
	}
	first := strings.Fields(cmd)[0]
	if _, ok := d.resolve(first); !ok {
		return fail(name, "PreToolUse hook runs "+first+", which is not found", guardHookHint)
	}
	for _, k := range harness.Kinds() {
		if plan := harness.Resolve(k).Doctor(); plan.Unguarded && d.in.Kinds[k].Tiers {
			return Check{Name: name, Status: Warn, Detail: "PreToolUse guard covers Claude workers only; " + plan.Title + " workers are not guarded"}
		}
	}
	return pass(name, "PreToolUse guard runs rota")
}

// resolve finds the executable a hook command starts with; a path with a slash
// gets its ~ and $ expanded before Look sees it.
func (d *runner) resolve(word string) (string, bool) {
	if strings.Contains(word, "/") {
		word = d.expand(word)
	}
	return d.in.Look(word)
}

// skills compares the installed skill sets with the binary's. It skips until
// something is installed (the repo's opt-in rule) and fails only on a broken
// install: another digest, or missing or edited files.
func (d *runner) skills() Check {
	c := d.skillsCheck()
	rep := d.in.Skills
	if rep == nil || len(rep.Warnings) == 0 {
		return c
	}
	// A plugin read problem never fails the check on its own.
	c.Detail += "; " + strings.Join(rep.Warnings, "; ")
	if c.Status == Pass {
		c.Status, c.Hint = Warn, "make the plugin state file readable, or check the Claude Code plugins directory"
	}
	return c
}

func (d *runner) skillsCheck() Check {
	const name = "skills"
	const installHint = "run: rota skills install"
	const pluginHint = "run /rota:rota-install in Claude Code (binary to the plugin's version) or claude plugin update rota@rota"
	var have []skills.RootStatus
	if rep := d.in.Skills; rep != nil {
		for _, r := range rep.Roots {
			if r.Installed {
				have = append(have, r)
			}
		}
	}
	if len(have) == 0 {
		if rep := d.in.Skills; rep != nil {
			for _, r := range rep.Roots {
				if r.Plugin {
					return skip(name, "rota plugin recorded but its skills are not on disk: claude plugin update rota@rota")
				}
			}
		}
		return skip(name, "not installed (opt-in): "+installHint)
	}
	rep := d.in.Skills
	var problems []string
	hint := "run: rota skills update"
	var manifestBad, pluginBad bool
	for _, r := range have {
		if r.Plugin {
			if !r.Current {
				pluginBad = true
				problems = append(problems, fmt.Sprintf("%s (plugin): skills %s, rota %s", r.Path, versionOrDigest(r.Version, r.Digest), versionOrDigest(rep.Version, rep.Digest)))
			}
			if len(r.Missing) > 0 {
				pluginBad = true
				problems = append(problems, fmt.Sprintf("%s (plugin): %d missing (%s)", r.Path, len(r.Missing), first(r.Missing)))
			}
			continue
		}
		if !r.Current {
			manifestBad = true
			if len(r.Changed) > 0 {
				problems = append(problems, fmt.Sprintf("%s: stub install stale, changed: %s", r.Path, strings.Join(r.Changed, ", ")))
			} else {
				problems = append(problems, fmt.Sprintf("%s: skills %s, rota %s", r.Path, versionOrDigest(r.Version, r.Digest), versionOrDigest(rep.Version, rep.Digest)))
			}
		}
		if len(r.Missing) > 0 {
			manifestBad = true
			problems = append(problems, fmt.Sprintf("%s: %d missing (%s)", r.Path, len(r.Missing), first(r.Missing)))
		}
		if len(r.Edited) > 0 {
			manifestBad = true
			problems = append(problems, fmt.Sprintf("%s: %d edited (%s)", r.Path, len(r.Edited), first(r.Edited)))
			hint = "run: rota skills update --overwrite"
		}
	}
	if len(problems) > 0 {
		var hints []string
		if manifestBad {
			hints = append(hints, hint)
		}
		if pluginBad {
			hints = append(hints, pluginHint)
		}
		return fail(name, strings.Join(problems, "; "), strings.Join(hints, "; "))
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

// cacheDisk warns when the volume holding the Go build cache is under the same
// threshold: a full cache volume fails a gate with "no space left on device".
func (d *runner) cacheDisk() (Check, bool) {
	disk, min := d.in.CacheDisk, d.in.MinFreeDiskPercent
	if disk == nil || disk.Total == 0 || min <= 0 {
		return Check{}, false
	}
	pct := float64(disk.Free) / float64(disk.Total) * 100
	if pct >= float64(min) {
		return Check{}, false
	}
	return Check{Name: "go-cache-disk", Status: Warn,
		Detail: fmt.Sprintf("%s (the Go build cache) has %s free (%.0f%%), under the %d%% threshold", disk.Path, HumanBytes(disk.Free), pct, min),
		Hint:   "run: go clean -cache; set doctor.minFreeDiskPercent to change the threshold"}, true
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
