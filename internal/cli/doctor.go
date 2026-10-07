package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/doctor"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/legacystate"
	"github.com/l4ci/rota/internal/proc"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/skills"
	"github.com/l4ci/rota/internal/stalebin"
	"github.com/l4ci/rota/internal/version"
)

// doctorCommand is `rota doctor` (C6): a read-only preflight. It runs without
// .rota/ and reads the project config only when one is found.
func doctorCommand() *Command {
	return &Command{Name: "doctor", Summary: "preflight: git, host, forge, accounts, herdr hook, orchestrator hooks, skills, codex", Verb: noFlags(runDoctor)}
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

// doctorInput gathers the real environment: ROTA_TEST_DOCTOR_PATH replaces PATH
// for tool lookup (a test hook, not part of the CLI).
func doctorInput(ctx context.Context, d *Deps) doctor.Input {
	in := doctor.Input{Exec: doctorExec(d.Proc), Getenv: os.Getenv, Look: doctorLook(os.Getenv("ROTA_TEST_DOCTOR_PATH"))}
	in.Dir, _ = os.Getwd()
	in.Home, _ = os.UserHomeDir()
	if f, ok := staleBinary(ctx, d.Git, in.Dir); ok {
		in.StaleBinary = &f
	}
	if abs, err := filepath.EvalSymlinks(in.Dir); err == nil {
		in.LegacyDir, _ = legacystate.LegacyState(abs)
	} else {
		in.LegacyDir, _ = legacystate.LegacyState(in.Dir)
	}
	root := ""
	for d := in.Dir; d != ""; {
		if rotatree.Exists(d) {
			root = d
			break
		}
		if p := filepath.Dir(d); p != d {
			d = p
		} else {
			break
		}
	}
	in.Skills = doctorSkills(in.Home, root)
	if root == "" {
		doctorDiskInput(ctx, &in, nil, "", d.Git, d.Now())
		return in
	}
	cfg := config.Load(rotatree.Config(root))
	str := func(key string) string {
		v, _ := config.Lookup(cfg, key)
		s, _ := v.(string)
		return s
	}
	in.Dispatch, in.IssuesProvider = config.Dispatch(cfg), str("issues.provider")
	in.CodexHomes = codexHomes(cfg)
	for _, t := range []string{"light", "standard", "heavy"} {
		if strings.TrimSpace(str("round.tiers.codex."+t)) != "" {
			in.CodexTiers = true
		}
	}
	for _, a := range config.Accounts(cfg) {
		if a.Name != "" {
			in.Accounts = append(in.Accounts, a)
		}
	}
	in.ProjectRoot = root
	doctorDiskInput(ctx, &in, cfg, root, d.Git, d.Now())
	if on, err := config.SwitchOnUsage(cfg); err == nil {
		in.SwitchOnUsage = on
	}
	for _, a := range in.Accounts {
		if a.ConfigDir != "" {
			in.ConfigDirs = append(in.ConfigDirs, a.ConfigDir)
		}
	}
	if len(in.ConfigDirs) == 0 {
		if d := skills.ClaudeDir(os.Getenv, in.Home); d != "" {
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
func doctorSkills(home, root string) *skills.Report {
	set, err := skills.Embedded()
	if err != nil {
		return nil
	}
	dirs, _ := skillsClaudeDirs(home, root)
	roots, err := skills.RootsFor("", "all", home, dirs, gitToplevel())
	if err != nil {
		return nil
	}
	rep, err := set.Status(roots, version.Get().Version)
	if err != nil {
		return nil
	}
	return &rep
}

// doctorLook finds a tool on pathOverride when set, else on PATH.
func doctorLook(pathOverride string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		if pathOverride == "" {
			p, err := exec.LookPath(name)
			return p, err == nil
		}
		for _, dir := range filepath.SplitList(pathOverride) {
			p := filepath.Join(dir, name)
			if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0 {
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

// codexHomes lists the Codex homes a worker can run under: the accounts of
// work.codexAccounts, else the default Codex home (an empty Dir).
func codexHomes(cfg any) []doctor.CodexHome {
	var homes []doctor.CodexHome
	for _, a := range config.CodexAccounts(cfg) {
		if a.Name != "" {
			homes = append(homes, doctor.CodexHome{Slot: a.Name, Dir: a.CodexHome})
		}
	}
	if len(homes) == 0 {
		homes = []doctor.CodexHome{{}}
	}
	return homes
}

func staleData(f stalebin.Finding) *jsonx.Object {
	return knObj("commit", f.Commit, "head", f.Head, "behind", f.Behind, "rebuild", f.Rebuild)
}

// staleBinaryOncePerRound is the round heartbeat's copy of doctor's binary
// warning: it reports the finding the first time it is asked in a round and
// stays quiet after, so a watch that wakes every ten minutes does not repeat it.
// The round it last spoke in lives in a state file next to the lease.
func staleBinaryOncePerRound(c *Ctx, cd string, round int) (stalebin.Finding, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return stalebin.Finding{}, false
	}
	f, ok := staleBinary(c.Context(), c.deps().Git, dir)
	if !ok {
		return stalebin.Finding{}, false
	}
	path := rotastate.File(cd, "stale-binary-round")
	if b, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(b)) == strconv.Itoa(round) {
		return stalebin.Finding{}, false
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = fsio.WriteFileAtomic(path, []byte(strconv.Itoa(round)+"\n"))
	return f, true
}
