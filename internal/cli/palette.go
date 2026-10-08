package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/layout"
	"github.com/l4ci/rota/internal/palette"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/tui"
	"github.com/l4ci/rota/internal/version"
	"github.com/l4ci/rota/internal/worker"
)

// The palette bare `rota` opens in a terminal (#181): the menu is
// internal/palette; this file is the entry table and the context line.
// Entries that are not Orchestrate or Setup call the same verbs a script
// would, in-process, so their output is the verb's own.

// paletteVerb is an entry that runs one rota verb and returns to the palette.
// A verb with a --ui view opens it inside the palette instead: nothing here
// names a screen, the entry follows the verb's own Command.View.
func paletteVerb(c *Ctx, root *Command, label, hint string, scope palette.Scope, verb ...string) palette.Entry {
	e := palette.Entry{Label: label, Hint: hint, Scope: scope, Run: func() error {
		run(Tree(), c.deps(), verb, c.Stdin, c.Stdout, c.Stderr)
		return nil
	}}
	if cmd := findVerb(root, verb); cmd != nil && cmd.View != nil {
		e.View = func() (tui.Model, error) { return paletteView(c, root, verb) }
	}
	return e
}

// findVerb is the command at a verb path, nil when there is none.
func findVerb(root *Command, verb []string) *Command {
	cmd := root
	for _, w := range verb {
		if cmd = cmd.sub(w); cmd == nil {
			return nil
		}
	}
	return cmd
}

// paletteView runs `<verb> --ui` in-process and keeps the screen instead of
// running it: the palette hosts the model, so the verb's own checks (terminal,
// --json) and its View are the ones a typed --ui would hit.
func paletteView(c *Ctx, root *Command, verb []string) (tui.Model, error) {
	var m tui.Model
	d := *c.deps()
	d.RunView = func(_ *Ctx, built tui.Model) error { m = built; return nil }
	var out, errb bytes.Buffer
	run(root, &d, append(append([]string(nil), verb...), "--ui"), c.Stdin, &out, &errb)
	if m == nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		if msg == "" {
			msg = "no view"
		}
		return nil, errors.New(strutil.FirstLine(msg))
	}
	return m, nil
}

// layoutToggle is the one "View: split ⇄ tabs" entry. It exists only while a
// herdr round has panes, since `rota layout` has nothing to move under tmux or
// without a round. The label marks the layout in force, read from the round
// registry each time, so it follows the switch it just made.
func layoutToggle(c *Ctx) (palette.Entry, bool) {
	root, err := c.Root()
	if err != nil {
		return palette.Entry{}, false
	}
	state := func() (cur string, live bool) {
		reg := worker.LoadRegistry(root)
		cur = layout.Tabs
		if reg.Layout() == layout.Split {
			cur = layout.Split
		}
		return cur, reg.Host() == "herdr" && hasHandle(reg)
	}
	if _, live := state(); !live {
		return palette.Entry{}, false
	}
	other := func(cur string) string {
		if cur == layout.Split {
			return layout.Tabs
		}
		return layout.Split
	}
	return palette.Entry{
		Label: "View: split ⇄ tabs", Scope: palette.InProject,
		Dynamic: func() (string, string) {
			cur, _ := state()
			label := "View: [split] ⇄ tabs"
			if cur == layout.Tabs {
				label = "View: split ⇄ [tabs]"
			}
			return label, "enter switches to " + other(cur)
		},
		Run: func() error {
			cur, _ := state()
			run(Tree(), c.deps(), []string{"layout", other(cur), "--project", root}, c.Stdin, c.Stdout, c.Stderr)
			return nil
		},
	}, true
}

// paletteEntries is the table; one line per action. out receives the result
// of the entry that ends the palette (Orchestrate, Setup).
func paletteEntries(c *Ctx, out *paletteOutcome) []palette.Entry {
	root := Tree()
	verb := func(label, hint string, scope palette.Scope, v ...string) palette.Entry {
		return paletteVerb(c, root, label, hint, scope, v...)
	}
	entries := []palette.Entry{
		{Label: "Orchestrate", Hint: "start or attach the round", Scope: palette.InProject, Default: true, Ends: true, Run: func() error {
			out.set(runOrchestrate(c, false))
			return nil
		}},
		verb("Round status", "slots, PRs, drift", palette.InProject, "round", "status"),
	}
	if e, ok := layoutToggle(c); ok {
		entries = append(entries, e)
	}
	return append(entries,
		verb("Doctor", "check git, forge, host and agent", palette.InProject, "doctor"),
		verb("Skills update", "refresh the installed skills", palette.Always, "skills", "update"),
		verb("Projects", "rota projects on this machine", palette.Always, "projects"),
		palette.Entry{Label: "Setup", Hint: "init this directory", Scope: palette.NoProject, Default: true, Ends: true, Run: func() error {
			out.set(c.deps().BareSetup(c, nil))
			return nil
		}},
		verb("Config", "view and change the config", palette.InProject, "config", "show"),
		palette.Entry{Label: "Quit", Hint: "q", Quit: true},
	)
}

// paletteOutcome is the Result of the entry that ended the palette.
type paletteOutcome struct {
	res Result
	err error
	ran bool
}

func (o *paletteOutcome) set(res Result, err error) { o.res, o.err, o.ran = res, err, true }

// runPalette opens the palette and returns what the ending entry returned;
// quitting returns an empty Result.
func runPalette(c *Ctx, inProject bool) (Result, error) {
	var out paletteOutcome
	cfg := palette.Config{
		Entries:   paletteEntries(c, &out),
		InProject: inProject,
		Header:    func() palette.Header { return paletteHeader(c, inProject) },
		In:        c.Stdin,
		Out:       c.Stdout,
	}
	err := c.deps().Palette(cfg)
	if out.ran {
		return out.res, out.err
	}
	return Result{}, err
}

// paletteHeader is the version and the context line. The round state is the
// library call behind `rota round status`, with the forge left out so the
// palette opens without a network round trip; anything that fails drops its
// part of the line instead of blocking the menu.
func paletteHeader(c *Ctx, inProject bool) palette.Header {
	h := palette.Header{Version: version.Get().Version}
	dir, _ := os.Getwd()
	root, err := c.Root()
	if err == nil {
		dir = root
	}
	h.Dir = tildePath(dir)
	if !inProject || err != nil {
		h.Host = host.Resolve("", "", os.Getenv, nil)
		return h
	}
	h.Host = worker.ResolveHost(root, nil, nil)
	ctx, cancel := context.WithTimeout(c.Context(), 3*time.Second)
	defer cancel()
	if rep, err := c.deps().RoundEnvLocal(ctx, root).Status(ctx, root); err == nil {
		active := 0
		for _, r := range rep.Rows {
			if r.Issue != "" {
				active++
			}
		}
		h.Round = roundSummary(active)
		if rep.Host != "" {
			h.Host = rep.Host
		}
	}
	return h
}

func roundSummary(active int) string {
	switch active {
	case 0:
		return "idle"
	case 1:
		return "1 active slot"
	}
	return fmt.Sprintf("%d active slots", active)
}

// tildePath shortens the home directory prefix to ~.
func tildePath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return "~" + p[len(home):]
	}
	return p
}
