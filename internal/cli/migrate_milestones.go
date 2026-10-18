package cli

import (
	"flag"
	"strings"

	"github.com/l4ci/rota/internal/migrate"
)

func milestonesMigrateCommand() *Command {
	return &Command{Name: "milestones", Summary: "rename ## Acceptance criteria to ## Acceptance with checkboxes on every milestone body (preview unless --apply)", Verb: migrateMilestones}
}

// migrateMilestones is `rota migrate milestones`: it runs every milestone
// body of the project's store (files or tracker) through
// migrate.MigrateMilestoneBody. Idempotent; preview unless --apply.
func migrateMilestones(fs *flag.FlagSet) RunFunc {
	apply := fs.Bool("apply", false, "write the changes; without it only report them")
	return func(c *Ctx, args []string) (Result, error) {
		if err := knNoArgs(args); err != nil {
			return Result{}, err
		}
		st, err := openMilestones(c)
		if err != nil {
			return Result{}, err
		}
		changes, err := migrate.RunMilestones(st, *apply)
		if err != nil {
			return milestoneFail(err, "")
		}
		rows := []any{}
		var lines []string
		changedAny := false
		for _, ch := range changes {
			rows = append(rows, jsonObj("id", ch.ID, "changed", ch.Changed))
			switch {
			case !ch.Changed:
				lines = append(lines, ch.ID+": up to date")
			case *apply:
				changedAny = true
				lines = append(lines, ch.ID+": migrated")
			default:
				changedAny = true
				lines = append(lines, ch.ID+": would migrate")
			}
		}
		text := strings.Join(lines, "\n")
		if !changedAny {
			text = "nothing to migrate"
		} else if !*apply {
			c.Warn("preview only; pass --apply")
		}
		return Result{Data: jsonObj("applied", *apply, "milestones", rows, "changed", changedAny && *apply), Text: text}, nil
	}
}
