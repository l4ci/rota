package cli

import (
	"flag"
	"fmt"
	"path/filepath"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/version"
)

// Tree is the rota command tree. Groups and verbs are added here as they are
// ported (A4-A8); their flags and --json shapes come from the verb contract (#46).
func Tree() *Command {
	root := &Command{
		Name:    "rota",
		Summary: "rota command line",
		Verb:    bareRota,
		Subs: []*Command{
			{Name: "version", Summary: "print the rota version", Verb: versionVerb},
			knowledgeCommands(),
			glossaryCommands(),
			blockCommand(),
			instructionsCommands(),
			decisionsCommands(),
			mapCommands(),
			qaCommands(),
			migrateCommands(),
			initCommands(),
			setupCommand(),
			projectsCommand(),
			skillsCommands(),
		},
	}
	root.Subs = append(root.Subs, a6Commands()...)
	root.Subs = append(root.Subs, a4Commands()...)
	root.Subs = append(root.Subs, workerCommands())
	root.Subs = append(root.Subs, roundCommands())
	root.Subs = append(root.Subs, trackerCommands(), gitCommands())
	root.Subs = append(root.Subs, reviewCommands(), shipCommands(), releaseCommands())
	root.Subs = append(root.Subs, verdictCommands(), gateCommands())
	root.Subs = append(root.Subs, doctorCommand(), orchestrateCommand(), reapCommand())
	root.Subs = append(root.Subs, statuslineCommands(), hookCommands(), keepaliveCommands(), limitCommands())
	return root
}

// noFlags is the Verb for a verb with no flags of its own.
func noFlags(run RunFunc) func(*flag.FlagSet) RunFunc {
	return func(*flag.FlagSet) RunFunc { return run }
}

func versionVerb(fs *flag.FlagSet) RunFunc {
	drift := fs.Bool("drift", false, "compare the version stamped in .rota/config.json with this binary")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) > 0 {
			return Result{}, Usage("version takes no arguments")
		}
		if *drift {
			return runVersionDrift(c)
		}
		return runVersion(c)
	}
}

// runVersionDrift is hv-version-check --json: rota.version of the merged config
// (the pre-rename stamp on a project not yet migrated) against the running
// binary. stamped or installed empty is "unknown".
// The old helper exited 0 without .rota/; 5.0 exits 3 (the root walk-up).
func runVersionDrift(c *Ctx) (Result, error) {
	root, err := c.Root()
	if err != nil {
		return Result{}, err
	}
	stamped, installed, status := versionDrift(root)
	data := jsonx.NewObject()
	data.Set("version", installed)
	data.Set("stamped", stamped)
	data.Set("installed", installed)
	data.Set("status", status)
	data.Set("drift", status == "drift")
	return Result{Data: data, Text: driftLine(stamped, installed, status)}, nil
}

// versionDrift compares the stamped version (rota.version, else the legacy
// the pre-rename stamp) of root's merged config with the running binary. Either
// side empty is "unknown".
func versionDrift(root string) (stamped, installed, status string) {
	cfg := config.Load(filepath.Join(root, ".rota", "config.json"))
	stamped = config.StampedVersion(cfg)
	installed = installedVersionFn()
	status = "unknown"
	switch {
	case stamped == "" || installed == "":
	case stamped == installed:
		status = "match"
	default:
		status = "drift"
	}
	return
}

func driftLine(stamped, installed, status string) string {
	if status != "drift" {
		return ""
	}
	return fmt.Sprintf("rota drift: project at %s, binary at %s: run rota init to refresh", stamped, installed)
}

// versionDriftLine is the drift nudge for root, or "".
func versionDriftLine(root string) string {
	return driftLine(versionDrift(root))
}

func runVersion(*Ctx) (Result, error) {
	info := version.Get()
	data := jsonx.NewObject()
	data.Set("version", info.Version)
	data.Set("commit", info.Commit)
	data.Set("date", info.Date)
	data.Set("goVersion", info.GoVersion)
	text := "rota " + info.Version
	switch {
	case info.Commit != "" && info.Date != "":
		text += fmt.Sprintf(" (%s, %s)", info.Commit, info.Date)
	case info.Commit != "":
		text += fmt.Sprintf(" (%s)", info.Commit)
	}
	return Result{Data: data, Text: text}, nil
}
