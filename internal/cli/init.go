package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/agents"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/initproj"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/projects"
	"github.com/l4ci/rota/internal/rotatree"
)

// The `rota init` group (A9): `init` seeds .rota/, `init check` is the preflight.
// They act on the working directory after -C, with no walk-up, because they run
// before a project root exists.

func initCommands() *Command {
	return &Command{Name: "init", Summary: "seed .rota/ in this directory", Verb: initVerb, Subs: []*Command{
		{Name: "check", Summary: "is .rota/ initialized here", Verb: noFlags(initCheck)},
		{Name: "umbrella", Summary: "register the git repos below this directory as an umbrella", Verb: initUmbrella},
	}}
}

// initDir is the directory init acts on: the working directory, symlinks resolved.
func initDir() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", Resolution("cannot read the working directory: %v", err)
	}
	return dir, nil
}

func initErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, initproj.ErrSeed):
		return &Error{Exit: ExitInternal, Message: strings.TrimPrefix(err.Error(), initproj.ErrSeed.Error()+": ")}
	}
	return knErr(err)
}

func initVerb(fs *flag.FlagSet) RunFunc {
	noBlocks := fs.Bool("no-blocks", false, "seed .rota/ only; skip AGENTS.md and the managed blocks")
	// Removed by F6a; kept as flags so they exit 2 with a hint, not "unknown flag".
	codex := fs.Bool("codex", false, "removed: use rota skills install --scope project --agent codex")
	skillsDir := fs.String("skills-dir", "", "removed: use rota skills install --scope project --agent codex")
	return func(c *Ctx, args []string) (Result, error) {
		if err := knNoArgs(args); err != nil {
			return Result{}, err
		}
		if *codex || *skillsDir != "" {
			return Result{}, Usage("--codex and --skills-dir were removed").WithHint("run: rota skills install --scope project --agent codex")
		}
		return runInit(c, *noBlocks)
	}
}

// runInit is `rota init` on the working directory: seed, fill and stamp the
// config, run the managed blocks. `rota setup` runs it too, so whatever init
// gains (the project registry, #24) reaches both.
func runInit(c *Ctx, noBlocks bool) (Result, error) {
	dir, err := initDir()
	if err != nil {
		return Result{}, err
	}
	res, err := initproj.Init(dir)
	if err != nil {
		return Result{}, initErr(err)
	}
	var warnings []string
	changed := res.Changed()
	data := knObj("root", dir, "created", strSlice(res.Created))
	var lines []string
	for _, p := range res.Created {
		lines = append(lines, "created: "+p)
	}
	cfg, err := initConfig(c, dir)
	if err != nil {
		return Result{}, err
	}
	warnings = append(warnings, cfg.warnings...)
	changed = changed || cfg.changed()
	cfg.report(data, &lines)
	if !noBlocks {
		b := initproj.Blocks(dir, func() (bool, error) { return initMilestoneIndex(c) })
		changed = changed || b.Changed()
		warnings = append(warnings, b.Warnings...)
		entries := []any{}
		for _, e := range b.Blocks {
			entries = append(entries, knObj("key", e.Key, "status", e.Status, "changed", e.Changed))
			lines = append(lines, fmt.Sprintf("block %s: %s", e.Key, e.Status))
		}
		data.Set("blocks", entries)
		data.Set("instructions", initInstructionsData(b))
	}
	if !noBlocks {
		// Ruling (#405): init writes the agent files, best effort. A foreign
		// file or a bad role config must not fail the seed; it warns.
		es, err := agents.Write(dir)
		if err != nil {
			warnings = append(warnings, "agent files not written: "+err.Error())
		}
		for _, e := range es {
			if err == nil && e.Status != agents.StatusCurrent {
				changed = true
				lines = append(lines, "agent: "+e.Path)
			}
		}
	}
	// The machine-wide registry is not the project's state: a failure to write
	// it warns, and neither it nor a first registration counts as `changed`.
	added, err := projects.Register(dir, c.deps().Now())
	if err != nil {
		warnings = append(warnings, "could not register the project in the global registry: "+err.Error())
	}
	data.Set("projectRegistered", added)
	for _, w := range warnings {
		c.Warn("%s", w)
	}
	if len(warnings) > 0 {
		data.Set("warnings", strSlice(warnings))
	}
	data.Set("changed", changed)
	if !changed {
		lines = append(lines, "noop: already initialized")
	}
	noVerify := c.deps().workerEnv().NothingToVerify(dir)
	data.Set("noVerify", noVerify)
	if noVerify {
		lines = append(lines, noVerifyWarning)
	}
	return Result{Data: data, Text: strings.Join(lines, "\n")}, nil
}

// initConfigResult is what init's config step did: the required keys it
// filled and the version it stamped ("" when the stamp already matched or the
// binary has no release version).
type initConfigResult struct {
	filled   []string
	stamped  string
	warnings []string
}

func (r initConfigResult) changed() bool {
	return len(r.filled) > 0 || r.stamped != ""
}

func (r initConfigResult) report(data *jsonx.Object, lines *[]string) {
	data.Set("configFilled", strSlice(r.filled))
	data.Set("versionStamped", r.stamped)
	if len(r.filled) > 0 {
		*lines = append(*lines, "config filled: "+strings.Join(r.filled, ", "))
	}
	if r.stamped != "" {
		*lines = append(*lines, "stamped rota.version: "+r.stamped)
	}
}

// initConfig is the config half of the old init skill: fill every missing
// required key with its schema default (never touching a present key), then
// stamp rota.version with the binary's version, which clears the drift
// nudge. Idempotent; an unreleased (dev) binary stamps nothing. The global
// config is not copied in: a new project inherits it at runtime.
func initConfig(c *Ctx, root string) (initConfigResult, error) {
	var r initConfigResult
	filled, sm, rm, err := config.FillDetect(root, c.deps().originProvider(c.Context(), root))
	if err != nil {
		_, err = roundMigrationFail(err)
		return r, err
	}
	r.filled = filled
	if sm.Changed {
		r.warnings = append(r.warnings, "migrated backlog.backend and issues.provider to issues.source "+sm.Source)
	}
	if rm.Changed() {
		r.warnings = append(r.warnings, "moved the round-only work.* keys to round.* (rota migrate config)")
	}
	if err := initIssueSource(c, root); err != nil {
		return r, err
	}
	if v := c.deps().InstalledVersion(); v != "" {
		if config.StampedVersion(config.Load(rotatree.Config(root))) != v {
			if _, err := config.Set(root, config.VersionKey, v); err != nil {
				return r, err
			}
			r.stamped = v
		}
	}
	return r, nil
}

// initMilestoneIndex runs `milestone index`, which handles issue mode, now
// that init has created the .rota/ it finds the project root by.
func initMilestoneIndex(c *Ctx) (bool, error) {
	res, err := runMilestoneIndex(c, nil)
	if err != nil {
		return false, err
	}
	if o, ok := res.Data.(*jsonx.Object); ok {
		if v, ok := o.Get("changed"); ok {
			b, _ := v.(bool)
			return b, nil
		}
	}
	return false, nil
}

func initInstructionsData(b initproj.BlocksResult) []any {
	acts := []any{}
	for _, a := range b.Instructions {
		o := knObj("action", a.Action, "file", a.File)
		if len(a.Keys) > 0 {
			o.Set("keys", strSlice(a.Keys))
		}
		acts = append(acts, o)
	}
	return acts
}

func initCheck(c *Ctx, args []string) (Result, error) {
	if err := knNoArgs(args); err != nil {
		return Result{}, err
	}
	dir, err := initDir()
	if err != nil {
		return Result{}, err
	}
	res := initproj.Check(dir, func() string { return versionDriftLine(c, dir) })
	data := knObj("initialized", res.Initialized, "missing", strSlice(res.Missing))
	if !res.Initialized {
		return Result{Data: data}, Failed("not initialized: %s missing", strings.Join(res.Missing, ", ")).WithHint("run: rota init")
	}
	for _, w := range res.Warnings {
		c.Warn("%s", w)
	}
	return Result{Data: data, Text: "initialized"}, nil
}

// initIssueSource writes an explicit issues.source when the project has none:
// the origin's provider for a backlog already moved onto the tracker (it holds
// .rota/issue-map.json), else file. forge.provider stays auto, and jira and
// linear are never detected. A config that still carries the legacy keys, or
// already names a source, is left alone.
func initIssueSource(c *Ctx, root string) error {
	cfg := config.Load(rotatree.Config(root))
	if _, set := config.Lookup(cfg, "issues.source"); set || len(config.Legacy(root)) > 0 {
		return nil
	}
	source := config.SourceFile
	if _, err := os.Stat(filepath.Join(rotatree.Dir(root), "issue-map.json")); err == nil {
		if p := c.deps().originProvider(c.Context(), root)(); p != "" {
			source = p
		}
	}
	_, err := config.Set(root, "issues.source", source)
	return err
}
