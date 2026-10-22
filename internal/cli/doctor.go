package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/agents"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/doctor"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/initproj"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/legacystate"
	"github.com/l4ci/rota/internal/migrate"
	"github.com/l4ci/rota/internal/proc"
	"github.com/l4ci/rota/internal/projects"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/skills"
	"github.com/l4ci/rota/internal/stalebin"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/version"
	"github.com/l4ci/rota/internal/worker"
)

// doctorCommand is `rota doctor` (C6): a read-only preflight. It runs without
// .rota/ and reads the project config only when one is found.
func doctorCommand() *Command {
	return &Command{Name: "doctor", Summary: "preflight: git, host, forge, accounts, herdr hook, orchestrator hooks, skills, codex", Verb: noFlags(runDoctor), View: doctorView}
}

// doctorCallTimeout bounds each tool call, so a hung herdr cannot hang the verb.
const doctorCallTimeout = 20 * time.Second

func runDoctor(c *Ctx, args []string) (Result, error) {
	if err := noArgs(args); err != nil {
		return Result{}, err
	}
	rep := doctor.Run(c.Context(), doctorInput(c.Context(), c.deps()))
	checks := make([]any, 0, len(rep.Checks))
	var lines []string
	for _, ch := range rep.Checks {
		o := jsonx.NewObject()
		o.Set("name", ch.Name)
		o.Set("status", ch.Status)
		o.Set("detail", ch.Detail)
		line := ch.Status + "\t" + ch.Name + "\t" + ch.Detail
		if ch.Hint != "" {
			o.Set("hint", ch.Hint)
			line += "\n\thint: " + ch.Hint
		}
		checks = append(checks, o)
		lines = append(lines, line)
	}
	data := knObj("ok", rep.OK(), "checks", checks)
	res := Result{Data: data, Text: strings.Join(lines, "\n")}
	if !rep.OK() {
		return res, Failed("doctor: a check failed")
	}
	return res, nil
}

// doctorInput gathers the real environment: Deps.DoctorPath replaces PATH for
// tool lookup (a test hook, not part of the CLI).
func doctorInput(ctx context.Context, d *Deps) doctor.Input {
	in := doctor.Input{Exec: doctorExec(d.Proc), Look: doctorLook(d.LookPath, d.DoctorPath), Credentials: d.credentials()}
	in.Dir, _ = d.Getwd()
	in.Home, _ = d.UserHomeDir()
	if dir, src, warn, err := projects.Resolve(d.projectsEnv()); err == nil {
		in.ConfigDir, in.ConfigDirSource, in.ConfigDirWarning = dir, src, warn
	}
	in.StateDir, _ = projects.StateDir(d.projectsEnv())
	if f, ok := staleBinary(ctx, d.Git, in.Dir); ok {
		in.StaleBinary = &f
	}
	if abs, err := filepath.EvalSymlinks(in.Dir); err == nil {
		in.LegacyDir, _ = legacystate.LegacyState(abs)
	} else {
		in.LegacyDir, _ = legacystate.LegacyState(in.Dir)
	}
	root, _ := rotatree.Find(in.Dir)
	in.Skills = doctorSkills(d.Getenv, in.Home, root, d.Toplevel(ctx))
	if root == "" {
		in.RoundHost = worker.ResolveHost("", nil, d.Getenv, doctorLookErr(in.Look))
		doctorDiskInput(ctx, &in, nil, "", d.Git, d.Now(), d.DoctorDisk, d.Getenv)
		return in
	}
	cfg := config.Load(rotatree.Config(root))
	in.Dispatch = config.Dispatch(cfg)
	in.RegistrySolo = worker.IsSolo(root)
	in.RoundHost = worker.ResolveHost(root, cfg, d.Getenv, doctorLookErr(in.Look))
	in.SourceProblem = doctorSourceProblem(cfg)
	in.Tracker = tracker.SettingsFromConfig(cfg)
	in.Kinds = doctorKinds(cfg)
	for _, a := range config.Accounts(cfg) {
		if a.Name != "" {
			in.Accounts = append(in.Accounts, a)
		}
	}
	in.OrchestratorConfigDir = d.Getenv("CLAUDE_CONFIG_DIR")
	in.ProjectRoot = root
	if name := in.Tracker.Connection; name != "" {
		in.Connection = doctorConnection(ctx, d, name)
		if in.Connection.State == string(connections.StateOK) &&
			(in.Tracker.Source == config.SourceLinear && in.Tracker.LinearTeam != "" || in.Tracker.Source == config.SourceItsaplan && in.Tracker.ItsaplanProject != "") {
			in.TeamProblem = linearTeamProblem(ctx, d, cfg, root)
		}
	}
	in.Slots, in.CwdOf = doctorSlotBlocks(root), procCwd(ctx, d.Proc)
	in.NothingToVerify = d.workerEnv().NothingToVerify(root)
	in.AgentProblems = agents.Problems(root, cfg)
	// No .gitignore at all is not an older project's drift: init creates it.
	if raw, err := os.ReadFile(filepath.Join(root, ".gitignore")); err == nil {
		in.GitignoreMissing = initproj.MissingIgnoreLines(string(raw))
	}
	if issue, err := issueBackend(root); err == nil && issue {
		in.StaleMilestoneFiles, _ = migrate.StaleMilestoneFiles(root)
	}
	doctorDiskInput(ctx, &in, cfg, root, d.Git, d.Now(), d.DoctorDisk, d.Getenv)
	if on, err := config.SwitchOnUsage(cfg); err == nil {
		in.SwitchOnUsage = on
	}
	for _, a := range in.Accounts {
		if a.ConfigDir != "" {
			in.ConfigDirs = append(in.ConfigDirs, a.ConfigDir)
		}
	}
	if len(in.ConfigDirs) == 0 {
		if d := skills.ClaudeDir(d.Getenv, in.Home); d != "" {
			in.ConfigDirs = []string{d}
		}
	}
	return in
}

// staleBinary asks whether the running rota is behind the rota source checkout
// dir is in; it finds nothing anywhere else.
func staleBinary(ctx context.Context, run git.Runner, dir string) (stalebin.Finding, bool) {
	return stalebin.Check(ctx, run, dir, version.Get().Commit)
}

// doctorSkills reads the skill roots in both scopes; nil when the embedded set
// or the roots cannot be resolved (the check then skips).
func doctorSkills(getenv func(string) string, home, root, top string) *skills.Report {
	set, err := skills.Embedded()
	if err != nil {
		return nil
	}
	dirs, _ := skillsClaudeDirs(getenv, home, root)
	roots, err := skills.RootsFor("", "all", home, dirs, top)
	if err != nil {
		return nil
	}
	pluginRoots, pluginErrs := skillsPluginRoots(getenv, dirs, "", "all", top)
	roots = append(roots, pluginRoots...)
	rep, err := set.Status(roots, version.Get().Version)
	if err != nil {
		return nil
	}
	rep.Warnings = errStrings(pluginErrs)
	return &rep
}

// doctorLook finds a tool through lookPath: on pathOverride when set, else on
// PATH. A name with a slash is a path and skips the search. lookPath owns the
// executable-file rule.
func doctorLook(lookPath func(string) (string, error), pathOverride string) func(string) (string, bool) {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	return func(name string) (string, bool) {
		if pathOverride == "" || strings.Contains(name, "/") {
			p, err := lookPath(name)
			return p, err == nil
		}
		for _, dir := range filepath.SplitList(pathOverride) {
			if dir == "" {
				continue
			}
			if p, err := lookPath(filepath.Join(dir, name)); err == nil {
				return p, true
			}
		}
		return "", false
	}
}

// doctorExec adapts a process runner to doctor.Exec, bounding each call.
func doctorExec(run proc.Runner) doctor.Exec {
	return func(ctx context.Context, bin string, args, extraEnv []string, dir string) (doctor.Result, error) {
		return run(ctx, proc.Cmd{Name: bin, Args: args, Env: extraEnv, Dir: dir, Timeout: doctorCallTimeout})
	}
}

// doctorKinds is what each worker harness with a doctor check has configured:
// its accounts as homes (else the default home) and whether a tier map is set.
func doctorKinds(cfg any) map[string]doctor.KindInput {
	out := map[string]doctor.KindInput{}
	for _, k := range harness.Kinds() {
		h := harness.Resolve(k)
		if h.Doctor().Check == "" {
			continue
		}
		accts := h.Accounts(cfg)
		in := doctor.KindInput{AccountsKey: accts.Key}
		for _, a := range accts.Entries {
			if a.Name != "" {
				in.Homes = append(in.Homes, doctor.Home{Slot: a.Name, Dir: a.Dir})
			}
		}
		if len(in.Homes) == 0 {
			in.Homes = []doctor.Home{{}}
		}
		in.Tiers = harness.TierMapSet(cfg, k)
		if !in.Tiers {
			for _, tier := range harness.Tiers {
				if m, _ := roundcfg.Effective(cfg, k, tier); m != "" {
					in.Models = append(in.Models, tier+"="+m)
				}
			}
		}
		out[k] = in
	}
	return out
}

func staleData(f stalebin.Finding) *jsonx.Object {
	return knObj("commit", f.Commit, "head", f.Head, "behind", f.Behind, "rebuild", f.Rebuild)
}

// staleBinaryOncePerRound is the round heartbeat's copy of doctor's binary
// warning. The once-per-round rule and its marker belong to round
// (round.ClaimStaleWarning); a marker error comes back as err beside the finding.
func staleBinaryOncePerRound(c *Ctx, cd string, n int) (f stalebin.Finding, ok bool, err error) {
	dir, err := c.deps().Getwd()
	if err != nil {
		return stalebin.Finding{}, false, err
	}
	f, ok = staleBinary(c.Context(), c.deps().Git, dir)
	if !ok {
		return stalebin.Finding{}, false, nil
	}
	first, err := round.ClaimStaleWarning(cd, n)
	return f, first, err
}

// doctorSlotBlocks lists the live slots (a session or a task) that hold a
// port block, each with the width it was allocated with.
func doctorSlotBlocks(root string) []doctor.SlotBlock {
	var out []doctor.SlotBlock
	// Tolerant: a diagnostic listing; a corrupt registry reads as no slots.
	for _, s := range worker.LoadRegistryTolerant(root).Slots() {
		if s.PortBase() > 0 && (s.PaneHandle() != "" || s.Task() != "") {
			out = append(out, doctor.SlotBlock{Name: s.Name(), Worktree: s.Worktree(), Base: s.PortBase(), Size: s.PortBlock()})
		}
	}
	return out
}

// procCwd is a process's working directory: /proc on Linux, lsof where /proc
// is absent (macOS); "" when neither can say (someone else's process).
func procCwd(ctx context.Context, run proc.Runner) func(pid int) string {
	return func(pid int) string { return procCwdOf(ctx, run, pid) }
}

func procCwdOf(ctx context.Context, run proc.Runner, pid int) string {
	if p, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid)); err == nil {
		return p
	}
	res, err := run(ctx, proc.Cmd{Name: "lsof", Args: []string{"-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn"}})
	if err != nil {
		return ""
	}
	return doctor.ParseLsofCwd(res.Stdout)
}

// doctorSourceProblem is the reason forge.provider or issues.source holds an
// invalid value, "" when both are valid. The tracker check reports it instead
// of reading the invalid value as a default.
func doctorSourceProblem(cfg any) string {
	if _, err := config.ForgeProvider(cfg); err != nil {
		return err.Error()
	}
	if _, err := config.IssueSource(cfg); err != nil {
		return err.Error()
	}
	return ""
}

// doctorLookErr adapts doctor's Look to the lookPath worker.ResolveHost takes.
func doctorLookErr(look func(string) (string, bool)) func(string) (string, error) {
	return func(name string) (string, error) {
		if p, ok := look(name); ok {
			return p, nil
		}
		return "", os.ErrNotExist
	}
}
