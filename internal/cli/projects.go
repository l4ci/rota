package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/projects"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/tui"
	"github.com/l4ci/rota/internal/worker"
)

// projectsCommand is `rota projects` (#24): the machine-wide registry that
// `rota init` fills. Listing is read-only; `cleanup` prunes stale entries. Both
// run without a project root.
func projectsCommand() *Command {
	return &Command{Name: "projects", Summary: "list the rota projects registered on this machine", Verb: noFlags(runProjects), Subs: []*Command{
		{Name: "cleanup", Summary: "drop registry entries whose directory is gone or no longer holds .rota/", Verb: noFlags(runProjectsCleanup)},
		{Name: "remove", Summary: "drop one registry entry by directory; the directory itself stays", Verb: noFlags(runProjectsRemove)},
	}, View: projectsView}
}

// runProjectsCleanup deletes at once, no preview: the entries are a cache of
// `rota init` runs and the next init puts a live one back.
func runProjectsCleanup(c *Ctx, args []string) (Result, error) {
	if err := knNoArgs(args); err != nil {
		return Result{}, err
	}
	gone, err := projects.Cleanup()
	if err != nil {
		return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
	}
	rows := []any{}
	var lines []string
	for _, p := range gone {
		rows = append(rows, knObj("name", p.Name, "path", p.Path, "lastSeen", p.LastSeen))
		lines = append(lines, fmt.Sprintf("removed\t%s\t%s", p.Name, p.Path))
	}
	if len(gone) == 0 {
		lines = append(lines, "noop: no stale projects")
	}
	data := jsonx.NewObject()
	data.Set("removed", rows)
	data.Set("changed", len(gone) > 0)
	return Result{Data: data, Text: strings.Join(lines, "\n")}, nil
}

// runProjectsRemove drops the one entry for <dir>. Like cleanup it deletes at
// once and answers `changed`; a directory that is not registered is a noop.
func runProjectsRemove(c *Ctx, args []string) (Result, error) {
	if len(args) != 1 {
		return Result{}, Usage("projects remove takes one directory").WithHint("run: rota projects remove <dir>")
	}
	gone, err := projects.Remove(args[0])
	if err != nil {
		return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
	}
	rows := []any{}
	var lines []string
	for _, p := range gone {
		rows = append(rows, knObj("name", p.Name, "path", p.Path, "lastSeen", p.LastSeen))
		lines = append(lines, fmt.Sprintf("removed\t%s\t%s", p.Name, p.Path))
	}
	if len(gone) == 0 {
		lines = append(lines, "noop: not registered: "+args[0])
	}
	data := jsonx.NewObject()
	data.Set("removed", rows)
	data.Set("changed", len(gone) > 0)
	return Result{Data: data, Text: strings.Join(lines, "\n")}, nil
}

func runProjects(c *Ctx, args []string) (Result, error) {
	if err := knNoArgs(args); err != nil {
		return Result{}, err
	}
	ps, err := projects.List()
	if err != nil {
		return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
	}
	rows := []any{}
	var lines []string
	for _, p := range ps {
		rows = append(rows, knObj("name", p.Name, "path", p.Path, "lastSeen", p.LastSeen, "missing", p.Missing))
		line := fmt.Sprintf("%s\t%s", p.Name, p.Path)
		if p.Missing {
			line += "\t(missing)"
		}
		lines = append(lines, line)
	}
	if len(ps) == 0 {
		lines = append(lines, "no projects registered: run rota init in a project")
	}
	dir, _ := projects.Dir()
	data := jsonx.NewObject()
	data.Set("dir", dir)
	data.Set("projects", rows)
	return Result{Data: data, Text: strings.Join(lines, "\n")}, nil
}

// projectsView is `rota projects --ui`: the registry from the verb's own
// Data, a status, last round and lease per project, and the screen's actions
// calling the same verbs in process. A reload re-runs the verb.
func projectsView(c *Ctx, res Result) (tui.Model, error) {
	lease := c.deps().LeaseEnv()
	load := func() ([]projects.Row, error) {
		res, err := runProjects(c, nil)
		if err != nil {
			return nil, err
		}
		return projectRows(res.Data, lease), nil
	}
	run := func(args ...string) error {
		if code := run(Tree(), c.deps(), args, c.Stdin, c.Stdout, c.Stderr); code != ExitOK {
			return fmt.Errorf("rota %s exited %d", strings.Join(args, " "), code)
		}
		return nil
	}
	return projects.NewScreen(projectRows(res.Data, lease), load, run), nil
}

// projectRows turns `rota projects` Data into screen rows, adding what the
// directory itself says: no .rota/, the last round number, the round lease.
func projectRows(data any, lease roundlease.Env) []projects.Row {
	d, _ := data.(*jsonx.Object)
	if d == nil {
		return nil
	}
	raw, _ := d.Get("projects")
	items, _ := raw.([]any)
	var out []projects.Row
	for _, it := range items {
		o, ok := it.(*jsonx.Object)
		if !ok {
			continue
		}
		missing, _ := o.Get("missing")
		r := projects.Row{Name: jsonx.Str(o, "name"), Path: jsonx.Str(o, "path"), LastSeen: jsonx.Str(o, "lastSeen")}
		r.Status, r.Round, r.Lease = projectState(r.Path, missing == true, lease)
		out = append(out, r)
	}
	return out
}

// projectState is the status, last round and lease of one registered path.
// Only a project that still holds .rota/ has rounds to report.
func projectState(path string, missing bool, lease roundlease.Env) (status, round, held string) {
	switch {
	case missing:
		return "missing directory", "-", "-"
	}
	if _, err := os.Stat(rotatree.Dir(path)); err != nil {
		return "no .rota/", "-", "-"
	}
	round = "none"
	// Tolerant: a projects listing shows "none" for a registry it cannot read.
	if n, ok := worker.LoadRegistryTolerant(path).Round(); ok && n > 0 {
		round = fmt.Sprint(n)
	}
	held = "none"
	if common, err := rotastate.CommonDir(path); err == nil {
		if l, st, err := lease.Read(common); err == nil && st != roundlease.None {
			switch st {
			case roundlease.Stale:
				held = "stale (holder gone)"
			case roundlease.Foreign:
				held = "held on " + l.Host
			default:
				held = fmt.Sprintf("held by pid %d", l.PID)
			}
			if round == "none" && l.Round > 0 {
				round = fmt.Sprint(l.Round)
			}
		}
	}
	return "ok", round, held
}
