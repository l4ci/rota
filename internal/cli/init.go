package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/initproj"
	"github.com/l4ci/rota/internal/jsonx"
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
		cfg, err := initConfig(dir)
		if err != nil {
			return Result{}, err
		}
		changed = changed || cfg.changed()
		cfg.report(data, &lines)
		if !*noBlocks {
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
		return Result{Data: data, Text: strings.Join(lines, "\n")}, nil
	}
}

// initConfigResult is what init's config step did: the required keys it
// filled and the version it stamped ("" when the stamp already matched or the
// binary has no release version).
type initConfigResult struct {
	filled  []string
	stamped string
}

func (r initConfigResult) changed() bool { return len(r.filled) > 0 || r.stamped != "" }

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
// nudge. Idempotent; an unreleased (dev) binary stamps nothing.
func initConfig(root string) (initConfigResult, error) {
	var r initConfigResult
	filled, err := config.Fill(root)
	if errors.Is(err, config.ErrCorrupt) {
		return r, &Error{Exit: ExitInternal, Message: err.Error()}
	}
	if err != nil {
		return r, err
	}
	r.filled = filled
	if v := installedVersionFn(); v != "" {
		if config.StampedVersion(config.Load(filepath.Join(root, ".rota", "config.json"))) != v {
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
	res := initproj.Check(dir, func() string { return versionDriftLine(dir) })
	data := knObj("initialized", res.Initialized, "missing", strSlice(res.Missing))
	if !res.Initialized {
		return Result{Data: data}, Failed("not initialized: %s missing", strings.Join(res.Missing, ", ")).WithHint("run: rota init")
	}
	for _, w := range res.Warnings {
		c.Warn("%s", w)
	}
	return Result{Data: data, Text: "initialized"}, nil
}
