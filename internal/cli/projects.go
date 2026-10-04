package cli

import (
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/projects"
)

// projectsCommand is `rota projects` (#24): the machine-wide registry that
// `rota init` fills. Read-only, and it runs without a project root.
func projectsCommand() *Command {
	return &Command{Name: "projects", Summary: "list the rota projects registered on this machine", Verb: noFlags(runProjects)}
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
