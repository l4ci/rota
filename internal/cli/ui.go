package cli

import (
	"errors"
	"io"
	"time"

	"github.com/l4ci/rota/internal/tui"
)

// --ui opens a verb's terminal view instead of printing its result. It is
// opt-in: nothing here looks at whether stdout is a terminal unless --ui was
// given, so a verb's output never changes because of where it runs.

// uiRefuse is --ui's gate, checked before the verb runs: a refused --ui
// changes nothing. Every refusal is a usage error naming the plain verb.
func uiRefuse(c *Ctx, cmd *Command) error {
	hint := "run: " + c.Path
	switch {
	case c.JSON:
		return Usage("--ui and --json do not mix").WithHint(hint)
	case cmd.View == nil:
		return Usage("no --ui view for this verb").WithHint(hint)
	case !c.deps().IsTerminal(c.Stdin) || !c.deps().IsTerminal(c.Stdout) || tui.Dumb():
		return errNeedsTerminal(c)
	}
	return nil
}

func errNeedsTerminal(c *Ctx) error {
	return Usage("--ui needs an interactive terminal").WithHint("run: " + c.Path)
}

// uiFinish turns the verb's successful result into its screen and runs it. A
// view that returns prints nothing: neither Text nor an envelope.
func uiFinish(c *Ctx, stdout io.Writer, cmd *Command, res Result) int {
	m, err := cmd.View(c, res)
	if err == nil {
		err = c.deps().RunView(c, m)
	}
	if err != nil {
		return fail(c, stdout, err)
	}
	return ExitOK
}

// runView is the real Deps.RunView: the screen on the process terminal. A
// terminal that cannot take raw mode is the same refusal as no terminal.
func runView(c *Ctx, m tui.Model) error {
	t, ok := tui.NewTerminal(c.Stdin, c.Stdout)
	if !ok {
		return errNeedsTerminal(c)
	}
	// A screen that refreshes itself says how often.
	if tk, ok := m.(interface{ TickEvery() time.Duration }); ok {
		t.Tick = tk.TickEvery()
	}
	if err := tui.Run(t, m); err != nil {
		if errors.Is(err, tui.ErrNoRaw) {
			return errNeedsTerminal(c)
		}
		return err
	}
	return nil
}
