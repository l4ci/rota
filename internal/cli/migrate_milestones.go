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

func milestoneFilesMigrateCommand() *Command {
	return &Command{Name: "milestone-files", Summary: "archive .rota/milestones files left over from file mode (tracker only; preview unless --apply)", Verb: migrateMilestoneFiles}
}

// migrateMilestoneFiles is `rota migrate milestone-files`: with milestones on
// the tracker no verb reads .rota/milestones/*.md, so it moves them to
// .rota/archive/milestones. Preview unless --apply; refused in file mode,
// where those files are the store.
func migrateMilestoneFiles(fs *flag.FlagSet) RunFunc {
	apply := fs.Bool("apply", false, "move the files; without it only report them")
	return func(c *Ctx, args []string) (Result, error) {
		if err := knNoArgs(args); err != nil {
			return Result{}, err
		}
		root, issue, err := modeRoot(c)
		if err != nil {
			return Result{}, err
		}
		if !issue {
			return blockedRefusal(nil, "backend", "milestones live in .rota/milestones (issues.source is file): nothing is stale", "")
		}
		names, err := migrate.ArchiveMilestoneFiles(root, *apply)
		if err != nil {
			return Result{}, err
		}
		text := "nothing to archive"
		if len(names) > 0 {
			verb := "would archive "
			if *apply {
				verb = "archived "
			} else {
				c.Warn("preview only; pass --apply")
			}
			lines := make([]string, len(names))
			for i, n := range names {
				lines[i] = verb + n
			}
			text = strings.Join(lines, "\n")
		}
		return Result{Data: jsonObj("applied", *apply, "files", names, "changed", *apply && len(names) > 0), Text: text}, nil
	}
}
