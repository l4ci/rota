package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/palette"
	"github.com/l4ci/rota/internal/version"
	"github.com/l4ci/rota/internal/worker"
)

// The palette bare `rota` opens in a terminal (#181): the menu is
// internal/palette; this file is the entry table and the context line.
// Entries that are not Orchestrate or Setup call the same verbs a script
// would, in-process, so their output is the verb's own.

// paletteVerb is an entry that runs one rota verb and returns to the palette.
func paletteVerb(c *Ctx, label, hint string, scope palette.Scope, verb ...string) palette.Entry {
	return palette.Entry{Label: label, Hint: hint, Scope: scope, Run: func() error {
		run(Tree(), c.deps(), verb, c.Stdin, c.Stdout, c.Stderr)
		return nil
	}}
}

// paletteEntries is the table; one line per action. out receives the result
// of the entry that ends the palette (Orchestrate, Setup).
func paletteEntries(c *Ctx, out *paletteOutcome) []palette.Entry {
	return []palette.Entry{
		{Label: "Orchestrate", Hint: "start or attach the round", Scope: palette.InProject, Default: true, Ends: true, Run: func() error {
			out.set(runOrchestrate(c, false))
			return nil
		}},
		paletteVerb(c, "Round status", "slots, PRs, drift", palette.InProject, "round", "status"),
		paletteVerb(c, "Split view", "workers beside the orchestrator", palette.Always, "layout", "split"),
		paletteVerb(c, "Tab view", "one tab per worker", palette.Always, "layout", "tabs"),
		paletteVerb(c, "Doctor", "check git, forge, host and agent", palette.InProject, "doctor"),
		paletteVerb(c, "Skills update", "refresh the installed skills", palette.Always, "skills", "update"),
		paletteVerb(c, "Projects", "rota projects on this machine", palette.Always, "projects", "--ui"),
		{Label: "Setup", Hint: "init this directory", Scope: palette.NoProject, Default: true, Ends: true, Run: func() error {
			out.set(c.deps().BareSetup(c, nil))
			return nil
		}},
		paletteVerb(c, "Config", "view and change the config", palette.InProject, "config", "edit"),
		{Label: "Quit", Hint: "q", Quit: true},
	}
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
	env := c.deps().RoundEnv(ctx, root)
	env.Forge, env.ForgeErr = nil, ""
	if rep, err := env.Status(ctx, root); err == nil {
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
