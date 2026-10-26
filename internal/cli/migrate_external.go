package cli

import (
	"flag"
	"fmt"
	"strings"
)

func externalKnowledgeMigrateCommand() *Command {
	return &Command{Name: "external-knowledge", Summary: "move trailing (seen on …) suffixes of external knowledge bullets into seen: stamps (preview unless --apply)", Verb: migrateExternalKnowledge}
}

// migrateExternalKnowledge is `rota migrate external-knowledge` (#2110): every
// bullet of the knowledge.external clone that ends in "(seen on Godot 4.6)"
// gets the tool versions in its stamp as seen:godot@4.6 and loses the suffix.
// A preview reads the clone as `knowledge query` does; --apply needs a clean,
// current clone and lands one commit by the repo's policy.
func migrateExternalKnowledge(fs *flag.FlagSet) RunFunc {
	apply := fs.Bool("apply", false, "write the changes; without it only report them")
	return func(c *Ctx, args []string) (Result, error) {
		if err := knNoArgs(args); err != nil {
			return Result{}, err
		}
		cl, err := externalClone(c)
		if err != nil {
			return Result{}, err
		}
		rep, err := cl.MigrateSeen(c.Context(), *apply)
		if err != nil {
			return Result{}, extErr(err)
		}
		changes, review := []any{}, []any{}
		var lines []string
		for _, ch := range rep.Changes {
			changes = append(changes, knObj("topic", ch.Topic, "title", ch.Title, "seen", ch.Seen, "inferred", ch.Inferred))
			l := fmt.Sprintf("%s :: %s -> seen:%s", ch.Topic, ch.Title, ch.Seen)
			if ch.Inferred {
				l += " (inferred from the topic)"
			}
			lines = append(lines, l)
		}
		var manual []string
		for _, m := range rep.ManualReview {
			review = append(review, knObj("topic", m.Topic, "title", m.Title, "text", m.Text))
			manual = append(manual, fmt.Sprintf("%s :: %s: %s", m.Topic, m.Title, m.Text))
		}
		data := knObj("changed", rep.Changed, "planned", len(rep.Changes) > 0, "applied", *apply, "count", len(rep.Changes),
			"changes", changes, "manualReview", review, "stranded", rep.Stranded)
		if rep.Changed {
			data.Set("write", rep.Write.Policy.Write)
			data.Set("pushed", rep.Write.Pushed)
			if rep.Write.PR != "" {
				data.Set("pr", rep.Write.PR)
				data.Set("branch", rep.Write.Branch)
			}
		}
		var text strings.Builder
		switch {
		case rep.Changed:
			fmt.Fprintf(&text, "moved %d (seen on …) suffix(es) into seen: stamps%s\n%s", len(rep.Changes), extLanded(rep.Write), strings.Join(lines, "\n"))
		case len(rep.Changes) > 0:
			c.Warn("preview only; pass --apply")
			fmt.Fprintf(&text, "would move %d (seen on …) suffix(es) into seen: stamps:\n%s", len(rep.Changes), strings.Join(lines, "\n"))
		default:
			text.WriteString("nothing to migrate: no bullet ends in a (seen on …) suffix")
		}
		if rep.Stranded {
			fmt.Fprintf(&text, "\nthe clone has commits its remote lacks (an earlier apply that did not push): run `git -C %s push`", cl.Dir)
		}
		if len(manual) > 0 {
			fmt.Fprintf(&text, "\nmanual review (left as written):\n%s", strings.Join(manual, "\n"))
		}
		return Result{Data: data, Text: text.String()}, nil
	}
}
