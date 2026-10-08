package round

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/escalation"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// ReviewForge is the forge side of ReviewRelay: the review poll's reads and the
// PR head the bounce is counted against.
type ReviewForge interface {
	worker.ReviewForge
	PRView(ctx context.Context, pr int) (tracker.PRInfo, error)
}

// ReviewOpts are the inputs of ReviewRelay.
type ReviewOpts struct {
	Slot  string
	Forge ReviewForge
	// Verdict is a FAIL verdict recorded for the slot's branch, nil for none.
	Verdict *worker.ReviewVerdict
	// MaxBounces is round.maxBounces; 0 turns the cap off.
	MaxBounces int
	// Escalate posts the cap escalation on PR number.
	Escalate func(ctx context.Context, number int, slot, title, body string) error
}

// Relayed is what ReviewRelay did for a slot.
type Relayed struct {
	Slot, PR string
	Items    int
	// Bounces is the item's bounce count after the relay (at the cap: the count
	// that hit it).
	Bounces int
	// Nothing is true when no review input was waiting.
	Nothing bool
	// Escalated is true when the cap was reached and the escalation was posted
	// now; an escalation that was already open is not posted twice.
	Escalated bool
}

// itemLimit truncates one relayed item; the rest is on the PR.
const itemLimit = 1500

// ReviewRelay hands a done slot's waiting review input back to its worker as a
// counted bounce: it reads what is new on the PR, and either relays it (a signed
// REVIEW relay through the dispatch path, which marks the slot busy and logs the
// relay for provenance) or, with the item at round.maxBounces, escalates on the PR
// and leaves the slot done. The cursor moves only after the relay went out, and
// the bounce is counted only then too, so a refused dispatch leaves both as they
// were. It never gates or merges: the orchestrator still owns the merge.
func (e Env) ReviewRelay(ctx context.Context, root string, o ReviewOpts) (Relayed, error) {
	reg := worker.LoadRegistry(root)
	s := reg.Slot(o.Slot)
	if s == nil {
		return Relayed{}, &exitcode.Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("slot '%s' is not in the pool", o.Slot)}
	}
	out := Relayed{Slot: o.Slot, PR: s.PR()}
	number, ok := worker.PRRefNumber(s.PR())
	if !ok {
		return out, &exitcode.Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("slot '%s' has no PR to read review input from", o.Slot)}
	}
	if st := strings.ToLower(s.State()); st != "done" {
		return out, &exitcode.Error{Exit: exitcode.ExitRefused, Message: fmt.Sprintf("slot '%s' is %s, not done: it has not handed its PR back yet", o.Slot, st),
			Data: worker.BlockData{BlockedBy: "state"}}
	}
	batch, err := worker.PendingReview(ctx, o.Forge, s, o.Verdict)
	if err != nil {
		return out, err
	}
	out.Items = len(batch.Items)
	if batch.Empty() {
		out.Nothing = true
		return out, nil
	}
	issue := s.HeldID()
	out.Bounces = reg.Bounces(issue)
	if o.MaxBounces > 0 && out.Bounces >= o.MaxBounces {
		return e.reviewCap(ctx, root, o, batch, number, issue, out)
	}
	pr, err := o.Forge.PRView(ctx, number)
	if err != nil {
		return out, err
	}
	body, err := os.CreateTemp("", "rota-review-relay-*")
	if err != nil {
		return out, err
	}
	defer os.Remove(body.Name())
	_, werr := body.WriteString(ReviewRelayText(batch))
	if cerr := body.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return out, werr
	}
	if _, err := e.Worker.Dispatch(ctx, root, worker.DispatchOpts{Slot: o.Slot, BodyFile: body.Name(), Relay: true}); err != nil {
		return out, err
	}
	if out.Bounces, err = worker.RecordBounce(root, issue, pr.HeadSHA); err != nil {
		return out, err
	}
	_, err = worker.UpdateSlot(root, o.Slot, func(s *worker.Slot) { s.SetReviewSeen(batch.Cursor) })
	return out, err
}

// reviewCap is the item at round.maxBounces: another resume is not the answer,
// so the slot stays done with its batch pending (the autopilot does not merge
// a PR with review input waiting) and the human is asked once on the PR.
func (e Env) reviewCap(ctx context.Context, root string, o ReviewOpts, batch worker.ReviewBatch, number int, issue string, out Relayed) (Relayed, error) {
	data := worker.BlockData{BlockedBy: "maxBounces"}
	msg := fmt.Sprintf("%s was already sent back %d time(s) (round.maxBounces is %d)", issue, out.Bounces, o.MaxBounces)
	for _, en := range escalation.Load(root) {
		if en.Slot == o.Slot && en.Number == number && en.Status == escalation.StatusPending {
			return out, &exitcode.Error{Exit: exitcode.ExitRefused, Message: msg + "; the escalation is still open", Data: data}
		}
	}
	title := fmt.Sprintf("Review input on %s, but %s is at the bounce cap", prLabel(out.PR), issue)
	body := fmt.Sprintf("%s was sent back %d time(s) (round.maxBounces is %d), so the review below was not relayed to %s.\n\n%s\n\nTo resume the worker, raise round.maxBounces or transfer %s to a heavier tier; to hand it over, `rota round transfer %s --to human`.",
		issue, out.Bounces, o.MaxBounces, o.Slot, ReviewRelayText(batch), issue, issue)
	if o.Escalate == nil {
		return out, &exitcode.Error{Exit: exitcode.ExitUnavailable, Message: "no way to escalate on the PR"}
	}
	if err := o.Escalate(ctx, number, o.Slot, title, body); err != nil {
		return out, err
	}
	out.Escalated = true
	return out, &exitcode.Error{Exit: exitcode.ExitRefused, Message: msg + "; escalated on the PR", Data: data}
}

func prLabel(pr string) string {
	if pr == "" {
		return "the PR"
	}
	return pr
}

// ReviewRelayText is the relay body: a REVIEW header line, one line per item,
// and the verdict. Each item is cut at itemLimit.
func ReviewRelayText(b worker.ReviewBatch) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "REVIEW %s (%d items)\n", b.PR, len(b.Items))
	for _, it := range b.Items {
		text := strings.TrimSpace(it.Body)
		if r := []rune(text); len(r) > itemLimit {
			text = string(r[:itemLimit]) + " [cut: read the rest on the PR]"
		}
		fmt.Fprintf(&sb, "- %s: %s\n", firstOf(it.Author, "reviewer"), text)
	}
	if b.Verdict != "" {
		fmt.Fprintf(&sb, "\nVerdict: %s\n", b.Verdict)
	}
	return sb.String()
}

func firstOf(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
