package cli

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/agents"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/doctor"
	"github.com/l4ci/rota/internal/extknowledge"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/harness"
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

// doctorInput gathers the environment through the invocation dependencies.
func doctorInput(ctx context.Context, d *Deps) doctor.Input {
	in := doctor.Input{Exec: doctorExec(d.Proc), Credentials: d.credentials(), Look: func(name string) (string, bool) {
		path, err := d.LookPath(name)
		return path, err == nil
	}}
	in.Dir, _ = d.Getwd()
	in.Home, _ = d.UserHomeDir()
	if dir, src, warn, err := projects.Resolve(d.projectsEnv()); err == nil {
		in.ConfigDir, in.ConfigDirSource, in.ConfigDirWarning = dir, src, warn
	}
	in.StateDir, _ = projects.StateDir(d.projectsEnv())
	in.ExternalKnowledge = doctorExternalKnowledge(ctx, d, in.Dir, in.Home)
	if f, ok := staleBinary(ctx, d.Git, in.Dir); ok {
		in.StaleBinary = &f
	}
	if abs, err := filepath.EvalSymlinks(in.Dir); err == nil {
		in.LegacyDir, _ = legacystate.LegacyState(abs)
	} else {
		in.LegacyDir, _ = legacystate.LegacyState(in.Dir)
	}
	root, _ := rotatree.Find(in.Dir)
	in.Skills = doctorSkills(d, d.Getenv, in.Home, root, d.Toplevel(ctx))
	if root == "" {
		in.RoundHost = worker.ResolveHost("", worker.HostConfig{}, d.Getenv, d.LookPath)
		doctorDiskInput(ctx, &in, nil, "", d.Git, d.Now(), d.DiskUsage, d.Getenv)
		return in
	}
	cfg := d.Config(root)
	in.Dispatch = config.Dispatch(cfg)
	in.RegistrySolo = worker.IsSolo(root)
	in.RoundHost = worker.ResolveHost(root, worker.ParseHostConfig(cfg), d.Getenv, d.LookPath)
	if err := tracker.ValidateConfig(cfg); err != nil {
		in.SourceProblem = err.Error()
	}
	in.Tracker = tracker.SettingsFromConfig(cfg)
	in.Kinds = doctorKinds(cfg, in.Home)
	for _, a := range config.AccountsOf(cfg, config.KindClaude) {
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
	in.Slots, in.CwdOf = doctorSlotBlocks(root), d.procs().Cwd
	issue, issueErr := issueBackend(d, root)
	if issueErr != nil {
		in.StaleClaimWarnings = []string{issueErr.Error()}
	} else if issue {
		scan := doctor.RemoteScanner{
			NewTracker: func(ctx context.Context, dir string) (doctor.Tracker, error) { return d.NewTracker(ctx, dir, cfg) },
			NewForge:   func(ctx context.Context) (tracker.Forge, error) { return d.forge(ctx, cfg, "", root) },
			RoundEnv:   func(ctx context.Context) round.Env { return d.RoundEnvLocal(ctx, root) },
			Cwd:        in.Dir, Today: d.Today,
		}.Scan(ctx, root, cfg)
		in.StaleClaims, in.StaleClaimWarnings, in.Labels = scan.StaleClaims, scan.Warnings, scan.Labels
	}
	in.NothingToVerify = d.workerEnv().NothingToVerify(root)
	if slots, err := d.workerEnv().GateConfig(root).TestSlots(); err == nil {
		in.TestSlots = slots
		in.SmokeShards, in.SmokeShardsSource = worker.SmokeShards(d.Getenv("ROTA_SMOKE_SHARDS"), runtime.NumCPU())
	}
	// A support machine writes only the gitignored .codex files, so the tracked
	// ones are not its drift and `rota agents write` could not fix them.
	var keep func(string) bool
	if role, err := roundcfg.MachineRole(cfg); err == nil && role == roundcfg.RoleSupport {
		keep = agents.Untracked
	}
	in.AgentProblems = agents.ProblemsOnly(root, cfg, keep)
	if raw, err := os.ReadFile(filepath.Join(root, ".gitignore")); err == nil {
		text := string(raw)
		in.Gitignore = &text
	}
	if issueErr == nil && issue {
		in.StaleMilestoneFiles, _ = migrate.StaleMilestoneFiles(root)
	}
	doctorDiskInput(ctx, &in, cfg, root, d.Git, d.Now(), d.DiskUsage, d.Getenv)
	if on, err := config.SwitchOnUsage(cfg); err == nil {
		in.SwitchOnUsage = on
	}
	in.DefaultConfigDir = skills.ClaudeDir(d.Getenv, in.Home)
	return in
}

// staleBinary asks whether the running rota is behind the rota source checkout
// dir is in; it finds nothing anywhere else.
func staleBinary(ctx context.Context, run git.Runner, dir string) (stalebin.Finding, bool) {
	return stalebin.Check(ctx, run, dir, version.Get().Commit)
}

// doctorSkills reads the skill roots in both scopes; nil when the embedded set
// or the roots cannot be resolved (the check then skips).
func doctorSkills(d *Deps, getenv func(string) string, home, root, top string) *skills.Report {
	set, err := skills.Embedded()
	if err != nil {
		return nil
	}
	dirs, _ := skillsClaudeDirs(d, getenv, home, root)
	roots, err := skills.Roots("", "all", home, dirs, top)
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

// doctorExec adapts a process runner to doctor.Exec, bounding each call.
func doctorExec(run proc.Runner) doctor.Exec {
	return func(ctx context.Context, bin string, args, extraEnv []string, dir string) (doctor.Result, error) {
		return run(ctx, proc.Cmd{Name: bin, Args: args, Env: extraEnv, Dir: dir, Timeout: doctorCallTimeout})
	}
}

// doctorKinds is what each worker harness with a doctor check has configured:
// its accounts as homes (else the default home) and whether a tier map is set.
func doctorKinds(cfg any, home string) map[string]doctor.KindInput {
	out := map[string]doctor.KindInput{}
	for _, k := range harness.Kinds() {
		h := harness.ResolveDiagnoser(k)
		if h.Doctor().Check == "" {
			continue
		}
		accts := harness.ResolveAccounts(k).Accounts(harness.ConfigFor(cfg, k)).In(home)
		in := doctor.KindInput{AccountsKey: accts.Key}
		for _, a := range accts.Entries {
			if a.Name != "" {
				in.Homes = append(in.Homes, doctor.Home{Slot: a.Name, Dir: a.Dir})
			}
		}
		if len(in.Homes) == 0 {
			in.Homes = []doctor.Home{{}}
		}
		in.Tiers, in.Models = roundcfg.TierSet(cfg, k)
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

// doctorExternalKnowledge probes the clone knowledge.external points at.
func doctorExternalKnowledge(ctx context.Context, d *Deps, cwd, home string) doctor.ExternalKnowledge {
	path, _ := externalSettings(d, cwd)
	if path == "" {
		return doctor.ExternalKnowledge{}
	}
	path = config.ExpandDir(path, home)
	st := extknowledge.Clone{Dir: path, Env: extknowledge.Env{Git: d.Git}}.State(ctx)
	k := doctor.ExternalKnowledge{Path: path, Exists: st.Exists, IsGit: st.IsGit, HasRemote: st.HasRemote, HasPolicy: st.HasPolicy}
	if st.Exists {
		// Disk only: no fetch, so a stale clone is measured as it stands.
		k.Malformed = (extknowledge.Clone{Dir: path}).Malformed()
		if stats, err := (extknowledge.Clone{Dir: path}).Stats(); err == nil {
			k.Over = externalLimits(d, cwd).Over(stats)
			for _, st := range stats {
				if st.Stray {
					k.Stray = append(k.Stray, st.File)
				}
			}
		}
	}
	return k
}
