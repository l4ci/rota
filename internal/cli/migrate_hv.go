package cli

// `rota migrate hv` (#236) and the hard stop that sends an un-migrated project
// to it. This file names the old state folder on purpose; it is on the
// legacy-name allowlist of test/grep-gate.sh.

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/migrate"
	"github.com/l4ci/rota/internal/skills"
	"github.com/l4ci/rota/internal/version"
)

func hvMigrateCommand() *Command {
	return &Command{Name: "hv", Summary: "move a project from .hv/ and the hv names to .rota/ and rota (preview unless --apply)", Verb: migrateHv}
}

// migrateHv is `rota migrate hv` (#236). It runs without .rota/: the project it
// migrates still holds .hv/.
func migrateHv(fs *flag.FlagSet) RunFunc {
	apply := fs.Bool("apply", false, "write the changes; without it only report them")
	verbose := fs.Bool("verbose", false, "include a diff for every rewritten file")
	skip := fs.Bool("skip-skills", false, "leave the skill installs alone (CI, shared machines)")
	return func(c *Ctx, args []string) (Result, error) {
		if err := knNoArgs(args); err != nil {
			return Result{}, err
		}
		cwd, _ := os.Getwd()
		home := os.Getenv("HOME")
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		o := migrate.HvOptions{Apply: *apply, Verbose: *verbose, SkipSkills: *skip, Cwd: cwd,
			Home: home, ClaudeDir: skills.ClaudeDir(home), Version: version.Get().Version, InstalledVersion: c.deps().InstalledVersion}
		if !*skip {
			if set, err := skills.Embedded(); err == nil {
				o.Skills = set
			}
		}
		here, _ := migrate.FindState(cwd)
		o.Milestone = func(dir string) func() (bool, error) {
			if dir != here {
				return nil
			}
			return func() (bool, error) { return initMilestoneIndex(c) }
		}
		rep, err := migrate.RunHv(o)
		if err != nil {
			var ref *migrate.Refusal
			switch {
			case errors.As(err, &ref):
				return Result{Data: knObj("blockedBy", ref.Blocked, "changed", false)}, Refused("%s", ref.Message)
			case errors.Is(err, migrate.ErrNoState):
				return Result{}, Resolution("%s", err.Error())
			case errors.Is(err, migrate.ErrGit):
				return Result{}, Unavailable("%s", strings.TrimPrefix(err.Error(), "git: "))
			}
			return Result{}, knErr(err)
		}
		if !*apply && !rep.Noop {
			c.Warn("preview only; pass --apply")
		}
		return migrateHvResult(rep), nil
	}
}

func migrateHvResult(r *migrate.HvReport) Result {
	projects := []any{}
	for _, p := range r.Projects {
		o := knObj("scope", p.Scope, "dir", p.Dir, "move", p.Move, "files", strSlice(p.Files), "blocks", p.Blocks, "stamp", p.Stamp)
		if p.Backup != "" {
			o.Set("backup", p.Backup)
		}
		projects = append(projects, o)
	}
	skillRoots := []any{}
	for _, s := range r.Skills {
		skillRoots = append(skillRoots, knObj("root", s.Path, "agent", s.Agent, "scope", s.Scope,
			"removed", s.Removed, "kept", strSlice(s.Kept), "reinstalled", s.Reinstalled))
	}
	d := knObj("applied", r.Applied, "noop", r.Noop, "changed", r.Changed, "projects", projects,
		"skillRoots", skillRoots, "settings", strSlice(r.Settings), "workerBranches", strSlice(r.WorkerBranches),
		"manualReview", strSlice(r.ManualReview))
	if r.VersionStamp != "" {
		d.Set("versionStamp", r.VersionStamp)
	}
	if len(r.Diffs) > 0 {
		d.Set("diffs", strSlice(r.Diffs))
	}

	var b strings.Builder
	mode := "dry-run"
	if r.Applied {
		mode = "apply"
	}
	fmt.Fprintf(&b, "rota migrate hv (%s)\n\n", mode)
	if r.Noop {
		b.WriteString("noop: already migrated (no .hv/, no hv markers, no hv skills install, no hv hooks).\n")
		return Result{Data: d, Text: b.String()}
	}
	for _, p := range r.Projects {
		label := p.Scope
		if label == "umbrella" {
			label = "project"
		}
		fmt.Fprintf(&b, "%s: %s\n", label, p.Dir)
		if p.Move {
			b.WriteString("  move .hv/ to .rota/\n")
		}
		for _, f := range p.Files {
			fmt.Fprintf(&b, "  rewrite %s\n", f)
		}
		if p.Blocks {
			b.WriteString("  regenerate the managed blocks\n")
		}
		if p.Stamp {
			b.WriteString("  stamp rota.version\n")
		}
		if p.Backup != "" {
			fmt.Fprintf(&b, "  backup at %s\n", p.Backup)
		}
	}
	if len(r.Skills) > 0 {
		b.WriteString("skills (hv install replaced by rota-*):\n")
		for _, s := range r.Skills {
			fmt.Fprintf(&b, "  %s (%s, %s)", s.Path, s.Agent, s.Scope)
			if r.Applied {
				fmt.Fprintf(&b, ": removed %d, kept %d, reinstalled %v", s.Removed, len(s.Kept), s.Reinstalled)
			}
			b.WriteString("\n")
		}
	}
	if len(r.Settings) > 0 {
		b.WriteString("settings (hv hooks and statusline become rota's):\n")
		for _, s := range r.Settings {
			fmt.Fprintf(&b, "  %s\n", s)
		}
	}
	if len(r.WorkerBranches) > 0 {
		b.WriteString("hv-worker branches (left as they are):\n")
		for _, w := range r.WorkerBranches {
			fmt.Fprintf(&b, "  %s\n", w)
		}
	}
	if len(r.ManualReview) > 0 {
		b.WriteString("manual review:\n")
		for _, m := range r.ManualReview {
			fmt.Fprintf(&b, "  %s\n", m)
		}
	}
	if len(r.Diffs) > 0 {
		b.WriteString("\n-- per-file diffs --\n")
		for _, df := range r.Diffs {
			b.WriteString(df + "\n")
		}
	}
	if r.Applied {
		b.WriteString("\napplied. Review with git status, then commit: git add -A .hv .rota <the other changed files>\n")
	} else {
		b.WriteString("\nRun with --apply to write the changes above.\n")
	}
	return Result{Data: d, Text: b.String()}
}

// legacyStateExempt are the verbs that run on a project still holding .hv/:
// the migration itself and doctor (which reports it), plus the verbs that
// never read project state (version, update, skills) or run from hooks that
// must not fail a Claude session (hook, statusline).
var legacyStateExempt = []string{"rota migrate hv", "rota doctor", "rota version", "rota update", "rota skills", "rota hook", "rota statusline"}

// legacyStateStop is the one hard stop for an un-migrated project (#236):
// rota looks for .rota/ only, so with .hv/ at the nearest state directory
// every verb but the exempt ones stops and says how to migrate.
func legacyStateStop(path string) error {
	for _, e := range legacyStateExempt {
		if path == e || strings.HasPrefix(path, e+" ") {
			return nil
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	if abs, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = abs
	}
	if dir, legacy := migrate.LegacyState(cwd); legacy {
		return Resolution("this project still uses .hv/ (%s); run: rota migrate hv", dir)
	}
	return nil
}
