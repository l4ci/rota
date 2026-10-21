package cli

import (
	"flag"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/verdict"
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
	force := fs.Bool("force", false, "count the bounce although the PR's current review verdicts hold no blocking finding")
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
		issue := worker.NormID(args[0])
		if !*force {
			if refused := noBlockingFindings(c, root, issue, *slot, *head); refused != nil {
				d := jsonx.NewObject()
				d.Set("issue", issue)
				d.Set("blockedBy", "no blocking findings")
				d.Set("changed", false)
				return Result{Data: d}, refused
			}
		}
		n, capped, err := recordBounce(c.deps().workerEnv(), root, issue, *slot, *head, set.MaxBounces)
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
				Hint: round.CapHint(issue)}
		}
		return Result{Data: d, Text: fmt.Sprintf("bounce %d of %s for %s", n, maxText(set.MaxBounces), issue)}, nil
	}
}

// noBlockingFindings is the refusal of a bounce when the verdicts recorded at
// the PR's current head judged it and none of their findings blocks: a nit does
// not cost a bounce (#963). The branch is the named slot's, else the slot
// holding the issue. Without a branch, a head, or a review verdict at that head
// the bounce is not a verdict-driven one and counts as before. A FAIL record,
// or one with an item FAIL, always counts as blocking, findings or not. The
// store is read under the plain branch key, so an umbrella root (keys
// "<repo>:<branch>") finds no record and fails open: the bounce counts.
func noBlockingFindings(c *Ctx, root, issue, slot, head string) error {
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return nil
	}
	var branch string
	for _, s := range reg.Slots() {
		if (slot != "" && s.Name() == slot) || (slot == "" && s.Issue() == issue) {
			branch = s.Branch()
			break
		}
	}
	if branch == "" {
		return nil
	}
	if head == "" {
		out, err := reviewGit(c.Context(), root, "rev-parse", "--short", branch)
		if err != nil {
			return nil
		}
		head = strings.TrimSpace(out)
	}
	list := verdict.Load(root).Branches[verdict.BranchKey("", branch)]
	judged := false
	for _, kind := range worker.ReviewKinds(config.DepthFull, true) {
		r, ok := verdict.Latest(list, kind)
		if !ok || !verdict.AtHead(r, head) {
			continue
		}
		if r.Failing() {
			return nil
		}
		judged = true
	}
	if !judged {
		return nil
	}
	return &Error{Exit: ExitRefused, Message: fmt.Sprintf("%s has no blocking finding in its current review verdicts: a bounce would relay only nits", issue),
		Hint: "file the deferred findings with `rota verdict add --file-deferred`, or pass --force to bounce anyway"}
}

// recordBounce counts one bounce unless the item is already at max (0 = no
// cap); the cap is enforced by the registry write. capped reports the refusal;
// n is then the unchanged count. A slot that
// is not in the pool is refused before anything is written.
func recordBounce(env worker.Env, root, issue, slot, head string, max int) (n int, capped bool, err error) {
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return 0, false, err
	}
	if slot != "" && reg.Slot(slot) == nil {
		return 0, false, &Error{Exit: ExitRefused, Message: fmt.Sprintf("unknown slot %q: not in the worker pool", slot),
			Hint: "see `rota worker pool list` for the slot names"}
	}
	n, _, capped, err = worker.RecordBounceCapped(env, nil, root, issue, slot, head, max)
	return n, capped, err
}

func maxText(max int) string {
	if max == 0 {
		return "unlimited"
	}
	return fmt.Sprint(max)
}
