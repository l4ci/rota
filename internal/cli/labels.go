package cli

import (
	"flag"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/labels"
	"github.com/l4ci/rota/internal/tracker"
)

func labelsCommands() *Command {
	return &Command{Name: "labels", Summary: "compare tracker labels with the registry", Subs: []*Command{
		{Name: "sync", Summary: "report label drift; --apply creates and updates known labels", Repo: true, Verb: labelsSync},
	}}
}

func labelsSync(fs *flag.FlagSet) RunFunc {
	apply := fs.Bool("apply", false, "create missing labels and update descriptions and colors")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		root, dir, _, err := issuesScope(c)
		if err != nil {
			return Result{}, err
		}
		issue, err := issueBackend(c.deps(), root)
		if err != nil {
			return Result{}, err
		}
		if !issue {
			return Result{}, Resolution("labels sync requires an issue tracker; issues.source is file")
		}
		cfg := c.deps().Config(root)
		entries, err := config.ResolvedLabels(cfg)
		if err != nil {
			return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
		}
		tr, err := c.deps().NewTracker(c.Context(), dir, cfg)
		if err != nil {
			return Result{}, trackerErr(err)
		}
		if it, ok := tr.(tracker.IssueTracker); ok {
			if note := tracker.AssumedNote(it); note != "" {
				c.Warn("%s", note)
			}
		}
		lister, ok := tr.(tracker.LabelLister)
		if !ok {
			return Result{}, Unavailable("tracker does not support listing label definitions")
		}
		have, err := lister.ListLabels(c.Context())
		if err != nil {
			return Result{}, trackerErr(err)
		}
		// Labels on any open issue count, including issues outside rota's backlog.
		open, err := tr.List(c.Context(), tracker.ListFilter{State: "open"})
		if err != nil {
			return Result{}, trackerErr(err)
		}
		used := map[string]bool{}
		for _, issue := range open {
			for _, label := range issue.Labels {
				used[label] = true
			}
		}
		manager, writable := tr.(tracker.LabelManager)
		changes := labels.Diff(entries, have, used, writable)
		changed := false
		result := func() Result {
			rows := make([]any, 0, len(changes))
			var lines []string
			for _, change := range changes {
				row := knObj("kind", change.Kind, "name", change.Label.Name, "description", change.Label.Description, "color", change.Label.Color)
				if change.Current != nil {
					row.Set("current", knObj("name", change.Current.Name, "description", change.Current.Description, "color", change.Current.Color))
				}
				rows = append(rows, row)
				line := change.Kind + "\t" + change.Label.Name
				if change.Kind == labels.Missing || change.Kind == labels.Metadata {
					line += fmt.Sprintf("\tdescription=%q color=%s", change.Label.Description, change.Label.Color)
				}
				lines = append(lines, line)
			}
			if len(lines) == 0 {
				lines = append(lines, "labels match the registry")
			}
			if !writable {
				lines = append(lines, "tracker supports label names only; report only")
			} else if !*apply {
				lines = append(lines, "dry run; use --apply to create and update known labels")
			}
			return Result{Data: knObj("apply", *apply, "writable", writable, "changed", changed, "changes", rows), Text: strings.Join(lines, "\n")}
		}
		if *apply && writable {
			for _, change := range changes {
				if change.Kind != labels.Missing && change.Kind != labels.Metadata {
					continue
				}
				if err := manager.UpsertLabel(c.Context(), change.Label); err != nil {
					return result(), trackerErr(err)
				}
				changed = true
			}
		}
		return result(), nil
	}
}
