package cli

import (
	"errors"
	"flag"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/initproj"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/status"
)

func repoAdd(fs *flag.FlagSet) RunFunc {
	name := fs.String("name", "", "registered `name` (default: the directory's name)")
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 1, 1, "repo add takes one <path>"); err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		r, changed, err := repos.Add(root, args[0], *name)
		switch {
		case errors.Is(err, repos.ErrConflict):
			return Result{}, Refused("%v", err)
		case errors.Is(err, repos.ErrNotRepo), errors.Is(err, repos.ErrOutside):
			return Result{}, Resolution("%v", err)
		case err != nil:
			return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
		}
		if changed {
			if _, err := initproj.Seed(root, r.Name, r.Rel); err != nil {
				return Result{}, err
			}
		}
		text := "already registered: " + r.Name
		if changed {
			text = "registered: " + r.Name + " " + r.Rel
		}
		return Result{Data: jsonObj("name", r.Name, "path", r.Path, "changed", changed), Text: text}, nil
	}
}

func repoRm(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		if err := argCount(c, args, 1, 1, "repo rm takes one <name>"); err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		name := args[0]
		if missing := status.Missing(repos.Load(root), []string{name}); len(missing) > 0 {
			return Result{}, Resolution("%v: %s", repos.ErrUnknown, name)
		}
		// Look for what still points at the repo while its tracker is
		// reachable: once the entry is gone an umbrella cannot read it.
		// Both are warnings, not refusals.
		var open, streams []string
		if ids, err := openItemsOf(c, root, name); err != nil {
			c.Warn("could not check open items of %s: %v", name, err)
		} else if len(ids) > 0 {
			open = ids
			c.Warn("%d open item(s) still name repo %s: %s", len(ids), name, strings.Join(ids, ", "))
		}
		for _, e := range status.Entries(root) {
			if e.Repo == name {
				streams = append(streams, e.Branch)
			}
		}
		if len(streams) > 0 {
			c.Warn("active stream(s) still in repo %s: %s", name, strings.Join(streams, ", "))
		}
		r, err := repos.Remove(root, name)
		if err != nil {
			return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
		}
		return Result{
			Data: jsonObj("name", r.Name, "path", r.Path, "changed", true,
				"openItems", anySlice(open), "activeStreams", anySlice(streams)),
			Text: "unregistered: " + name + " (" + filepath.ToSlash(r.Rel) + " left untouched)",
		}, nil
	}
}

// openItemsOf lists the open items whose Repos field names repo. The backlog is
// read unscoped and filtered here, so a global --repo does not narrow it.
func openItemsOf(c *Ctx, root, repo string) ([]string, error) {
	saved := c.Repo
	c.Repo = ""
	defer func() { c.Repo = saved }()
	be, err := openBacklog(c, root, false, "")
	if err != nil {
		return nil, err
	}
	rows, _, ok, err := backlog.OpenRows(be)
	if err != nil || !ok {
		return nil, err
	}
	var ids []string
	for _, r := range rows {
		for _, n := range status.ParseReposCSV(r.Fields.Repos) {
			if n == repo {
				ids = append(ids, r.ID)
			}
		}
	}
	return ids, nil
}
