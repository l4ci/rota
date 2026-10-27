package cli

import (
	"errors"
	"flag"
	"fmt"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/labels"
	"github.com/l4ci/rota/internal/migrate"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
)

func labelsMigrateCommand() *Command {
	return &Command{Name: "labels", Summary: "replace the retired support label with a machine pin (preview unless --apply)", Verb: migrateLabels}
}
func migrateLabels(fs *flag.FlagSet) RunFunc {
	apply := fs.Bool("apply", false, "write the changes; without it only report them")
	return func(c *Ctx, args []string) (Result, error) {
		if err := knNoArgs(args); err != nil {
			return Result{}, err
		}
		root, issue, err := modeRoot(c)
		if err != nil {
			return Result{}, err
		}
		if !issue {
			return supportFileModeStop()
		}
		cfg := c.deps().Config(root)
		support := roundcfg.SupportName(cfg)
		if err := roundcfg.CheckMachineName(support, roundcfg.RoleMain); err != nil {
			return blockedRefusal(nil, blockedby.Config, err.Error(), "set round.support before migrating labels")
		}
		if support == "" && !*apply {
			return blockedRefusal(nil, blockedby.Config, "round.support must name the support machine before migrating labels", "")
		}
		from := labels.LegacySupport
		if v, ok := config.Lookup(cfg, "issues.labels.support"); ok {
			var valid bool
			from, valid = v.(string)
			if !valid || strings.TrimSpace(from) == "" {
				return blockedRefusal(nil, blockedby.Config, "issues.labels.support must be a non-empty string before migrating labels", "")
			}
		}
		tr, err := c.deps().NewTracker(c.Context(), root, cfg)
		if err != nil {
			return backlogFail(err)
		}
		changes := []migrate.Relabel{}
		var merr error
		if support == "" {
			// No destination is needed to retire an unused key. Prove that no open
			// issue still needs relabelling before removing it.
			issues, err := tr.List(c.Context(), tracker.ListFilter{State: "open", Labels: []string{from}})
			if err != nil {
				return backlogFail(err)
			}
			for _, issue := range issues {
				if issue.State == "open" && slices.Contains(issue.Labels, from) {
					return blockedRefusal(nil, blockedby.Config, "round.support must name the support machine before migrating labels", "open issues still carry "+from)
				}
			}
		} else {
			changes, merr = migrate.RelabelSupport(c.Context(), tr, from, roundcfg.MachineLabel(support), *apply)
		}
		rows := []any{}
		lines := []string{}
		changed := false
		for _, r := range changes {
			rows = append(rows, jsonObj("id", r.Key.String(), "changed", r.Changed))
			if r.Changed {
				changed = true
				verb := "would migrate"
				if *apply {
					verb = "migrated"
				}
				lines = append(lines, fmt.Sprintf("%s: %s", r.Key.String(), verb))
			}
		}
		if *apply && merr == nil {
			removed, e := config.Unset(root, "issues.labels.support", config.LayerProject)
			if e != nil {
				merr = e
			}
			changed = changed || removed
		}
		text := strings.Join(lines, "\n")
		if text == "" {
			text = "nothing to migrate"
		}
		data := jsonObj("applied", *apply, "issues", rows, "changed", changed && *apply)
		res := Result{Data: data, Text: text}
		if merr != nil {
			var conflict *migrate.LabelConflictError
			if errors.As(merr, &conflict) {
				data.Set("blockedBy", blockedby.Site)
				return res, Refused("%s", merr)
			}
			_, err := backlogFail(merr)
			return res, err
		}
		if changed && !*apply {
			c.Warn("preview only; pass --apply")
		}
		return res, nil
	}
}
