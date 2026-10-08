package cli

import (
	"errors"
	"flag"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/orchestrate"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/worker"
)

// `rota orchestrate` (#19) and bare `rota`: launch the orchestrator session.
// The decisions are internal/orchestrate; this file is the verb, the routing
// of bare `rota`, and the seams tests replace.

func orchestrateCommand() *Command {
	return &Command{Name: "orchestrate", Summary: "launch the orchestrator: an agent under keepalive that starts /rota-orchestrate", Verb: orchestrateVerb}
}

// defaultOrchestrateEnv is what the launcher touches outside its own memory.
// Tests replace Deps.OrchestrateEnv: a real launch opens tabs in the herdr or
// tmux a round runs in.
func defaultOrchestrateEnv() orchestrate.Env {
	self, err := os.Executable()
	if err != nil {
		self = "rota"
	}
	return orchestrate.Env{
		Getenv:   os.Getenv,
		LookPath: exec.LookPath,
		Exec:     syscall.Exec,
		Env:      os.Environ(),
		Self:     self,
	}
}

// defaultBareSetup is what bare `rota` runs in a directory with no .rota/: the
// interactive setup of #25 (`rota setup`) with its defaults.
func defaultBareSetup(c *Ctx, args []string) (Result, error) {
	return setupVerb(flag.NewFlagSet("setup", flag.ContinueOnError))(c, args)
}

// defaultIsTerminal: bare `rota` opens an interactive session, so it needs a person.
func defaultIsTerminal(f any) bool {
	file, ok := f.(*os.File)
	if !ok {
		return false
	}
	fi, err := file.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	// /dev/null is a character device too; redirecting to it is not a person.
	null, err := os.Stat(os.DevNull)
	return err != nil || !os.SameFile(fi, null)
}

// bareRota is the root's own verb: it opens the palette (#181). Enter on the
// preselected entry runs the setup with no .rota/ and launches the
// orchestrator in an initialized project. Anything that cannot
// drive a terminal (--json, a pipe) gets the usage error it always had, never
// a session nobody sees.
func bareRota(*flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) > 0 {
			return Result{}, Usage("unknown command %q", args[0]).WithHint("run: rota --help")
		}
		if c.JSON || !c.deps().IsTerminal(c.Stdin) || !c.deps().IsTerminal(c.Stdout) {
			return Result{}, Usage("missing command").WithHint("run: rota --help")
		}
		_, err := c.Root()
		return runPalette(c, err == nil)
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
	cfg := config.Load(rotatree.Config(root))
	env := c.deps().orchestrateEnv()
	if env.PickAccount == nil {
		env.PickAccount = func(root string) (string, bool) {
			return c.deps().WorkerAccounts().Pick(c.Context(), root, nil)
		}
	}
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
	setIf(d, "account", plan.Account)
	setIf(d, "configDir", plan.ConfigDir)
	d.Set("command", strs(plan.Supervisor))
	text := plan.Harness + " in " + plan.Host + " (" + plan.Mode + "): " + strings.Join(plan.Supervisor, " ")
	if plan.Account != "" {
		text += " [account " + plan.Account + "]"
	}
	if dry {
		d.Set("dryRun", true)
		d.Set("changed", false)
		return Result{Data: d, Text: text}, nil
	}
	recordLaunchingPane(c, env, root, plan)
	got, err := env.Launch(c.Context(), plan)
	if err != nil {
		return orchestrateErr(err)
	}
	// Reached only by ModeTab: the other modes replace this process.
	setIf(d, "tab", got.Handle)
	d.Set("changed", true)
	return Result{Data: d, Text: "orchestrator started in " + plan.Host + " tab " + got.Handle}, nil
}

// recordLaunchingPane remembers the plain herdr pane `rota orchestrate` runs in
// as the round's CLI pane, so a later `rota layout split` needs no run from it.
// It is recorded before the launch: the orchestrator's `round start` follows
// at once and keeps it. Outside herdr, or from an agent pane, nothing changes.
func recordLaunchingPane(c *Ctx, env orchestrate.Env, root string, plan orchestrate.Plan) {
	me := host.CurrentPane("herdr", env.Getenv)
	if me == "" || plan.Host != "herdr" || plan.Mode != orchestrate.ModeTab {
		return
	}
	l, ok := env.Host("herdr").(host.Layouter)
	if !ok {
		return
	}
	panes, err := l.LayoutPanes(c.Context())
	if err != nil {
		return
	}
	for _, p := range panes {
		if p.ID == me && p.Agent == "" {
			if err := worker.Update(root, func(d *worker.Doc) { d.SetCLIPane(me) }); err != nil {
				c.Warn("could not remember the launching pane: %v", err)
			}
			return
		}
	}
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
