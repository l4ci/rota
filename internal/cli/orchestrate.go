package cli

import (
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/orchestrate"
)

// `rota orchestrate` (#19) and bare `rota`: launch the orchestrator session.
// The decisions are internal/orchestrate; this file is the verb, the routing
// of bare `rota`, and the seams tests replace.

func orchestrateCommand() *Command {
	return &Command{Name: "orchestrate", Summary: "launch the orchestrator: an agent under keepalive that starts /rota-orchestrate", Verb: orchestrateVerb}
}

// orchestrateEnv is what the launcher touches outside its own memory. Tests
// replace it: a real launch opens tabs in the herdr or tmux a round runs in.
var orchestrateEnv = func() orchestrate.Env {
	self, err := os.Executable()
	if err != nil {
		self = "rota"
	}
	return orchestrate.Env{
		Getenv:   os.Getenv,
		LookPath: exec.LookPath,
		Host:     func(kind string) host.Host { return host.New(kind, host.Deps{}) },
		Exec:     syscall.Exec,
		Env:      os.Environ(),
		Self:     self,
	}
}

// bareSetup is what bare `rota` runs in a directory with no .rota/: the
// interactive setup of #25 (`rota setup`) with its defaults.
var bareSetup RunFunc = func(c *Ctx, args []string) (Result, error) {
	return setupVerb(flag.NewFlagSet("setup", flag.ContinueOnError))(c, args)
}

// isTerminal: bare `rota` opens an interactive session, so it needs a person.
var isTerminal = func(f any) bool {
	file, ok := f.(*os.File)
	if !ok {
		return false
	}
	fi, err := file.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// bareRota is the root's own verb. With no .rota/ it runs the setup; in an
// initialized project it launches the orchestrator. Anything that cannot
// drive a terminal (--json, a pipe) gets the usage error it always had, never
// a session nobody sees.
func bareRota(*flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) > 0 {
			return Result{}, Usage("unknown command %q", args[0]).WithHint("run: rota --help")
		}
		if c.JSON || !isTerminal(c.Stdin) || !isTerminal(c.Stdout) {
			return Result{}, Usage("missing command").WithHint("run: rota --help")
		}
		if _, err := c.Root(); err != nil {
			return bareSetup(c, nil)
		}
		return runOrchestrate(c, false)
	}
}

func orchestrateVerb(fs *flag.FlagSet) RunFunc {
	dry := fs.Bool("dry-run", false, "run doctor and print what would start; start nothing")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		return runOrchestrate(c, *dry)
	}
}

func runOrchestrate(c *Ctx, dry bool) (Result, error) {
	root, err := c.Root()
	if err != nil {
		return Result{}, err
	}
	// Fail fast: a round that starts on a broken host fails late and obscurely.
	if res, err := runDoctor(c, nil); err != nil {
		return res, Failed("doctor reports a failure: no session started").WithHint("fix each fail above (the hint says how), then run rota orchestrate again")
	}
	cfg := config.Load(filepath.Join(root, ".rota", "config.json"))
	env := orchestrateEnv()
	plan, err := env.Resolve(root, cfg)
	if err != nil {
		return orchestrateErr(err)
	}
	d := jsonx.NewObject()
	d.Set("harness", plan.Harness)
	d.Set("host", plan.Host)
	d.Set("mode", plan.Mode)
	d.Set("cwd", plan.Cwd)
	setIf(d, "session", plan.Session)
	d.Set("command", strs(plan.Supervisor))
	text := plan.Harness + " in " + plan.Host + " (" + plan.Mode + "): " + strings.Join(plan.Supervisor, " ")
	if dry {
		d.Set("dryRun", true)
		d.Set("changed", false)
		return Result{Data: d, Text: text}, nil
	}
	got, err := env.Launch(c.Context(), plan)
	if err != nil {
		return orchestrateErr(err)
	}
	// Reached only by ModeTab: the other modes replace this process.
	setIf(d, "tab", got.Handle)
	d.Set("changed", true)
	return Result{Data: d, Text: "orchestrator started in " + plan.Host + " tab " + got.Handle}, nil
}

func orchestrateErr(err error) (Result, error) {
	var oe *orchestrate.Error
	if !errors.As(err, &oe) {
		return Result{}, err
	}
	switch oe.Code {
	case "refused":
		d := jsonx.NewObject()
		d.Set("blockedBy", oe.Msg)
		d.Set("changed", false)
		return Result{Data: d}, Refused("%s", oe.Msg).WithHint(oe.Hint)
	case "unavailable":
		return Result{}, Unavailable("%s", oe.Msg).WithHint(oe.Hint)
	}
	return Result{}, &Error{Exit: ExitInternal, Message: oe.Msg, Hint: oe.Hint}
}
