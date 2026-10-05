package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/layout"
	"github.com/l4ci/rota/internal/orchestrate"
	"github.com/l4ci/rota/internal/projects"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/worker"
)

// `rota layout` (#180): arrange a round's herdr panes as one split view
// (split) or as tabs (tabs); bare, report which each project is in. The grid
// and the moves are internal/layout; this file finds the projects and the
// panes. Deps.LayoutHost is the seam tests replace: a real run moves the
// panes of the herdr it is inside.

func layoutCommand() *Command {
	return &Command{Name: "layout", Summary: "show how each round's herdr panes are arranged: split or tabs", Verb: layoutVerb(""), Subs: []*Command{
		{Name: "split", Summary: "fold the live workers into the orchestrator's tab as one grid (wide screen)", Verb: layoutVerb(layout.Split)},
		{Name: "tabs", Summary: "give every worker a tab of its own again (narrow screen)", Verb: layoutVerb(layout.Tabs)},
	}}
}

// defaultLayoutHost is herdr, when it is installed.
func defaultLayoutHost() (host.Layouter, error) {
	h := host.New("herdr", host.Deps{})
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
	h, herr := c.deps().LayoutHost()
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
			workers := liveWorkers(ctx, h, reg)
			st, ok := layout.Find(panes, root, orchestrate.Label, workers)
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
			if r.Moves > 0 {
				// Report where the panes are now, not where they were.
				if now, err := h.LayoutPanes(ctx); err == nil {
					if st, ok := layout.Find(now, root, orchestrate.Label, workers); ok {
						o.st = st
					}
				}
			}
		}
		outs = append(outs, o)
	}
	return layoutResult(c, mode, outs)
}

// layoutRoots are the projects to look at: the one named by --project, else
// every registered project plus the one the caller is in.
func layoutRoots(c *Ctx, project string) ([]string, error) {
	if project != "" {
		abs, err := filepath.Abs(project)
		if err != nil {
			return nil, Resolution("%v", err)
		}
		if st, err := os.Stat(filepath.Join(abs, ".rota")); err != nil || !st.IsDir() {
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

// liveWorkers are the slots whose agent has a pane, in registry order. A
// parked slot has no handle and a dead one no agent, so both are left out.
func liveWorkers(ctx context.Context, h host.Layouter, reg worker.Registry) []layout.Worker {
	var out []layout.Worker
	for _, s := range reg.Slots() {
		handle := s.PaneHandle()
		if handle == "" {
			continue
		}
		if pane := h.PaneOf(ctx, s.Name(), handle); pane != "" {
			out = append(out, layout.Worker{Slot: s.Name(), Pane: pane})
		}
	}
	return out
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
		lines = append(lines, head, "  orchestrator\t"+o.st.Orch.ID+"\t"+o.st.Orch.Tab)
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
