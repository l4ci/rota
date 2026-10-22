package cli

import (
	"fmt"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

// assignNudge is the next assignable candidate, printed when a done slot with
// a PR returns while candidates are left: #29 frees the slot the moment its PR
// is open, so the orchestrator assigns it before it gates that PR.
type assignNudge struct {
	Slot, ID, Title string
}

// Command is the assignment to run.
func (n assignNudge) Command() string { return "rota round assign " + n.ID }

// Text is the nudge line `round wait` and `round watch` print.
func (n assignNudge) Text() string {
	return fmt.Sprintf("nudge\tassign\tassign before you gate: %s is done, next candidate %s %s\t%s",
		n.Slot, n.ID, n.Title, n.Command())
}

func (n assignNudge) data() *jsonx.Object {
	o := jsonx.NewObject()
	o.Set("slot", n.Slot)
	o.Set("issue", n.ID)
	o.Set("title", n.Title)
	o.Set("command", n.Command())
	return o
}

// nextReady is the first candidate that is ready to assign, in backlog order.
func nextReady(cs []round.Candidate) (round.Candidate, bool) {
	for _, c := range cs {
		if c.Ready() {
			return c, true
		}
	}
	return round.Candidate{}, false
}

// assignNudgeFor is the nudge for a returned slot: nil unless the slot is done
// with a PR and a candidate is ready. Tolerant like the train nudge: advice
// never fails the wait, so an unreadable config or backlog reads as no candidate.
func assignNudgeFor(c *Ctx, root, slot string) *assignNudge {
	s := worker.LoadRegistryTolerant(root).Slot(slot)
	if s == nil || s.PR() == "" || !worker.IsDone(s.State()) {
		return nil
	}
	set, err := roundcfg.Load(root)
	if err != nil {
		return nil
	}
	be, err := openBacklog(c, root, false, "")
	if err != nil {
		return nil
	}
	cands, err := c.deps().RoundEnv(c.Context(), root).RoundCandidates(c.Context(), root, be, set, "")
	if err != nil {
		return nil
	}
	cd, ok := nextReady(cands)
	if !ok {
		return nil
	}
	return &assignNudge{Slot: slot, ID: cd.ID, Title: cd.Title}
}
