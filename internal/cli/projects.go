package cli

import (
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/projects"
)

// projectsCommand is `rota projects` (#24): the machine-wide registry that
// `rota init` fills. Listing is read-only; `cleanup` prunes stale entries. Both
// run without a project root.
func projectsCommand() *Command {
	return &Command{Name: "projects", Summary: "list the rota projects registered on this machine", Verb: noFlags(runProjects), Subs: []*Command{
		{Name: "cleanup", Summary: "drop registry entries whose directory is gone or no longer holds .rota/", Verb: noFlags(runProjectsCleanup)},
	}}
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
