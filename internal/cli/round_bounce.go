package cli

import (
	"flag"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

// roundBounce is the orchestrator's count of a review bounce (SKILL §7): the
// merge gate counts its own stale and provenance bounces, this counts the ones
// the orchestrator sends back by hand. It refuses once the item sits at
// round.maxBounces, so a further bounce is never the answer: the item goes to a
// higher tier or the human instead.
func roundBounce(fs *flag.FlagSet) RunFunc {
	head := fs.String("head", "", "PR head sha being bounced; the same head twice counts once")
	slot := fs.String("slot", "", "slot holding the bounced PR; names the ledger row of a best-of attempt")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) != 1 {
			return Result{}, Usage("round bounce takes one item ID")
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		set, err := roundcfg.Load(root)
		if err != nil {
			return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
		}
		issue := strings.TrimPrefix(args[0], "#")
		n, capped, err := recordBounce(root, issue, *slot, *head, set.MaxBounces)
		if err != nil {
			return Result{}, err
		}
		d := jsonx.NewObject()
		d.Set("issue", issue)
		d.Set("bounces", n)
		d.Set("max", set.MaxBounces)
		d.Set("changed", !capped)
		if capped {
			return Result{Data: d}, &Error{Exit: ExitRefused, Message: fmt.Sprintf("%s was already sent back %d time(s) (round.maxBounces is %d)", issue, n, set.MaxBounces),
				Hint: "no further bounce: `rota round transfer " + issue + " --to <slot> --tier heavy --tier-reason <why>`, or `--to human`"}
		}
		return Result{Data: d, Text: fmt.Sprintf("bounce %d of %s for %s", n, maxText(set.MaxBounces), issue)}, nil
	}
}

// recordBounce counts one bounce unless the item is already at max (0 = no
// cap). capped reports the refusal; n is then the unchanged count.
func recordBounce(root, issue, slot, head string, max int) (n int, capped bool, err error) {
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return 0, false, err
	}
	if n = reg.Bounces(issue); max > 0 && n >= max {
		return n, true, nil
	}
	n, err = worker.RecordBounce(root, issue, slot, head)
	return n, false, err
}

func maxText(max int) string {
	if max == 0 {
		return "unlimited"
	}
	return fmt.Sprint(max)
}
