package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/layout"
	"github.com/l4ci/rota/internal/projects"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/worker"
)

// `rota layout` (#180): arrange a round's herdr panes as one split view
// (split) or as tabs (tabs); bare, report which each project is in. The grid
// and the moves are internal/layout; this file finds the projects and the
// panes. Deps.Host is the seam tests replace: a real run moves the
// panes of the herdr it is inside.

func layoutCommand() *Command {
	return &Command{Name: "layout", Summary: "show how each round's herdr panes are arranged: split or tabs", Verb: layoutVerb(""), Subs: []*Command{
		{Name: "split", Summary: "fold the live workers into the orchestrator's tab as one grid (wide screen)", Verb: layoutVerb(layout.Split)},
		{Name: "tabs", Summary: "give every worker a tab of its own again (narrow screen)", Verb: layoutVerb(layout.Tabs)},
	}}
}

// layoutHost is herdr, when it is installed.
func layoutHost(d *Deps) (host.Layouter, error) {
	h := d.Host("herdr")
	if err := h.Require(); err != nil {
		return nil, err
	}
	l, ok := h.(host.Layouter)
	if !ok {
		return nil, fmt.Errorf("herdr host cannot arrange panes")
	}
	return l, nil
}

// layoutOut is one project's line of the report.
type layoutOut struct {
	root, name, before, after string
	moves                     int
	skipped                   string
	st                        layout.State
}

func layoutVerb(mode string) func(*flag.FlagSet) RunFunc {
	return func(fs *flag.FlagSet) RunFunc {
		project := fs.String("project", "", "arrange only the rota project in this directory (default: every project with a round open in herdr)")
		return func(c *Ctx, args []string) (Result, error) {
			if err := noArgs(args); err != nil {
				return Result{}, err
			}
			return runLayout(c, mode, *project)
		}
	}
}

func runLayout(c *Ctx, mode, project string) (Result, error) {
	roots, err := layoutRoots(c, project)
	if err != nil {
		return Result{}, err
	}
	h, herr := layoutHost(c.deps())
	ctx := c.Context()
	var outs []layoutOut
	var panes []host.LayoutPane
	if herr == nil {
		if panes, herr = h.LayoutPanes(ctx); herr != nil {
			h = nil
		}
	}
	if herr != nil {
		c.Warn("herdr: %v", herr)
	}
	for _, root := range roots {
		o := layoutOut{root: root, name: filepath.Base(root)}
		reg := worker.LoadRegistry(root)
		explicit := project != ""
		switch rh := reg.Host(); {
		case rh == host.Solo || rh == "tmux":
			// A stale registry of an old tmux round is not worth a line.
			if !explicit && !hasHandle(reg) {
				continue
			}
			o.skipped = "host is " + rh + ": layout needs herdr"
		case herr != nil:
			if !explicit {
				continue
			}
			o.skipped = herr.Error()
		default:
			workers := worker.LiveWorkers(ctx, h, reg)
			cli := cliPane(c, root, reg, panes)
			st, ok := layout.Find(panes, root, layout.Label, cli, workers)
			if !ok {
				if !explicit {
					continue
				}
				o.skipped = "no orchestrator pane open in herdr"
				break
			}
			o.st, o.before, o.after = st, st.Layout(), st.Layout()
			if mode == "" {
				break
			}
			var r layout.Result
			var err error
			if mode == layout.Split {
				r, err = layout.ToSplit(ctx, h, st)
			} else {
				r, err = layout.ToTabs(ctx, h, st)
			}
			if err != nil {
				return Result{}, Unavailable("%s: %v", o.name, err).WithHint("some panes may have moved; rota layout shows where each is")
			}
			o.after, o.moves = r.After, r.Moves
			if err := worker.Update(root, func(d *worker.Doc) {
				d.SetLayout(mode)
				if mode == layout.Split && st.CLI != nil {
					d.SetCLIPane(st.CLI.ID)
				}
			}); err != nil {
				c.Warn("%s: could not remember the layout: %v", o.name, err)
			}
			if r.Moves > 0 {
				// Report where the panes are now, not where they were.
				if now, err := h.LayoutPanes(ctx); err == nil {
					if st, ok := layout.Find(now, root, layout.Label, cli, workers); ok {
						o.st = st
					}
				}
			}
		}
		outs = append(outs, o)
	}
	return layoutResult(c, mode, outs)
}

// cliPane is the pane that launched root's round: the one `rota layout` runs
// from when that is a plain shell (no agent) of this project, else the one a
// split recorded earlier. "" when neither is known.
func cliPane(c *Ctx, root string, reg worker.Registry, panes []host.LayoutPane) string {
	if me := os.Getenv("HERDR_PANE_ID"); me != "" {
		if mine, err := c.Root(); err == nil && repos.Realpath(mine) == root {
			for _, p := range panes {
				if p.ID == me && p.Agent == "" {
					return me
				}
			}
		}
	}
	return reg.CLIPane()
}

// layoutRoots are the projects to look at: the one named by --project, else
// every registered project plus the one the caller is in.
func layoutRoots(c *Ctx, project string) ([]string, error) {
	if project != "" {
		abs, err := filepath.Abs(project)
		if err != nil {
			return nil, Resolution("%v", err)
		}
		if !rotatree.Exists(abs) {
			return nil, Resolution("%s is not a rota project (no .rota/)", project)
		}
		return []string{repos.Realpath(abs)}, nil
	}
	var roots []string
	seen := map[string]bool{}
	add := func(p string) {
		if p = repos.Realpath(p); !seen[p] {
			seen[p] = true
			roots = append(roots, p)
		}
	}
	if root, err := c.Root(); err == nil {
		add(root)
	}
	ps, err := projects.List()
	if err != nil {
		return nil, &Error{Exit: ExitInternal, Message: err.Error()}
	}
	for _, p := range ps {
		if !p.Missing {
			add(p.Path)
		}
	}
	return roots, nil
}

func hasHandle(reg worker.Registry) bool {
	for _, s := range reg.Slots() {
		if s.PaneHandle() != "" {
			return true
		}
	}
	return false
}

func layoutResult(c *Ctx, mode string, outs []layoutOut) (Result, error) {
	rows := []any{}
	var lines []string
	arranged := 0
	var skipped []string
	for _, o := range outs {
		row := knObj("project", o.root, "name", o.name)
		if o.skipped != "" {
			row.Set("skipped", o.skipped)
			rows = append(rows, row)
			lines = append(lines, fmt.Sprintf("%s\tskipped\t%s", o.name, o.skipped))
			skipped = append(skipped, o.name+": "+o.skipped)
			continue
		}
		arranged++
		row.Set("layout", o.after)
		if mode != "" {
			row.Set("before", o.before)
			row.Set("moves", o.moves)
			row.Set("changed", o.moves > 0)
		}
		row.Set("orchestrator", knObj("pane", o.st.Orch.ID, "tab", o.st.Orch.Tab))
		if o.st.CLI != nil {
			row.Set("cli", knObj("pane", o.st.CLI.ID, "tab", o.st.CLI.Tab))
		}
		ws := []any{}
		for _, w := range o.st.Workers {
			ws = append(ws, knObj("slot", w.Slot, "pane", w.Pane, "tab", w.Tab))
		}
		row.Set("workers", ws)
		fo := []any{}
		for _, p := range o.st.Foreign {
			fo = append(fo, knObj("pane", p.ID))
			c.Warn("%s: pane %s in the orchestrator tab is not rota's; left where it is", o.name, p.ID)
		}
		row.Set("foreign", fo)
		rows = append(rows, row)

		head := o.name + "\t" + o.after
		if mode != "" {
			head = o.name + "\t" + layout.Result{After: o.after, Moves: o.moves}.Describe()
		}
		lines = append(lines, head)
		if o.st.CLI != nil {
			lines = append(lines, "  cli\t"+o.st.CLI.ID+"\t"+o.st.CLI.Tab)
		}
		lines = append(lines, "  orchestrator\t"+o.st.Orch.ID+"\t"+o.st.Orch.Tab)
		for _, w := range o.st.Workers {
			lines = append(lines, "  "+w.Slot+"\t"+w.Pane+"\t"+w.Tab)
		}
	}
	data := jsonx.NewObject()
	data.Set("projects", rows)
	if len(outs) == 0 {
		lines = append(lines, "no rota project has a round open in herdr")
	}
	res := Result{Data: data, Text: strings.Join(lines, "\n")}
	if mode != "" && arranged == 0 {
		if len(skipped) > 0 {
			return res, Unavailable("nothing arranged: %s", strings.Join(skipped, "; "))
		}
		return res, Resolution("no rota project has a round open in herdr").WithHint("run it from a pane of the herdr that holds the orchestrator, or pass --project <dir>")
	}
	return res, nil
}
