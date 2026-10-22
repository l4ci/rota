package cli

import (
	"flag"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/tracker"
)

// The support split (#1598): main labels the issues the support machine may
// take. The verb is fenced on a support machine (supportFenced), so it runs on
// main only.
func roundSupport() *Command {
	return &Command{Name: "support", Summary: "split work with the support machine (round.support)", Subs: []*Command{
		{Name: "offer", Summary: "label issues for the support machine; --withdraw takes them back", Verb: roundSupportOffer},
	}}
}

func roundSupportOffer(fs *flag.FlagSet) RunFunc {
	withdraw := fs.Bool("withdraw", false, "remove the support label instead of adding it")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) == 0 {
			return Result{}, Usage("round support offer takes one or more item IDs")
		}
		root, issue, err := modeRoot(c)
		if err != nil {
			return Result{}, err
		}
		if !issue {
			return supportFileModeStop()
		}
		env, be, err := moveEnv(c, root)
		if err != nil {
			return Result{}, err
		}
		ids := make([]string, 0, len(args))
		for _, a := range args {
			id, _, err := resolveItem(be, a)
			if err != nil {
				return backlogFail(err)
			}
			ids = append(ids, id)
		}
		changed, err := env.OfferSupport(c.Context(), root, be, ids, *withdraw)
		if err != nil {
			return moveFailure(err, len(changed) > 0)
		}
		verb := "offered to the support machine"
		if *withdraw {
			verb = "withdrawn from the support machine"
		}
		refs := make([]string, 0, len(changed))
		for _, id := range changed {
			refs = append(refs, tracker.RefOf(id))
		}
		text := "nothing to change: every issue already is " + verb
		if len(refs) > 0 {
			text = fmt.Sprintf("%s %s", strings.Join(refs, ", "), verb)
		}
		return Result{Data: jsonObj("ids", append([]string{}, changed...), "withdraw", *withdraw, "changed", len(changed) > 0), Text: text}, nil
	}
}
