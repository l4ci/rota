package cli

import (
	"fmt"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/receipt"
)

// receiptCommands is the `rota receipt` group (#959).
func receiptCommands() *Command {
	return &Command{Name: "receipt", Summary: "inspect the request-id receipts of the round verbs", Subs: []*Command{
		{Name: "show", Summary: "report whether a request id's call applied", Verb: noFlags(runReceiptShow)},
	}}
}

// States of a request id, as `receipt show` reports them.
const (
	receiptCompleted = "completed" // a done receipt: read the stored result
	receiptPending   = "pending"   // a failed receipt: replay with the same id
	receiptAbsent    = "absent"    // no receipt: inspect the state, then decide
)

// runReceiptShow answers "did my earlier call apply?" after a lost response, so
// the caller inspects instead of re-running a write that may have landed (#959).
// It reads the receipts file only; it never runs the verb and never writes.
func runReceiptShow(c *Ctx, args []string) (Result, error) {
	id, err := oneArg(args, "request id")
	if err != nil {
		return Result{}, err
	}
	root, err := c.Root()
	if err != nil {
		return Result{}, err
	}
	e, found, err := receipt.New(root).Show(id)
	if err != nil {
		return Result{}, err
	}
	d := jsonx.NewObject()
	d.Set("requestId", id)
	if !found {
		d.Set("state", receiptAbsent)
		return Result{Data: d, Text: fmt.Sprintf("absent: no receipt for %s. This is not proof that nothing happened: a call still running, a pruned receipt or a call that wrote nothing also leave none. Inspect the affected state before you re-run.", id)}, nil
	}
	d.Set("verb", e.Verb)
	d.Set("attempts", e.Attempts)
	d.Set("at", e.At)
	if e.Status == receipt.Done {
		d.Set("state", receiptCompleted)
		if len(e.Data) > 0 {
			v, err := jsonx.Decode(e.Data)
			if err != nil {
				return Result{}, fmt.Errorf("receipt %s: %w", id, err)
			}
			d.Set("result", v)
		}
		d.Set("text", e.Text)
		return Result{Data: d, Text: fmt.Sprintf("completed: %s applied (%s); the stored result is the answer, do not re-run\n%s", e.Verb, e.At, e.Text)}, nil
	}
	d.Set("state", receiptPending)
	d.Set("exit", e.Exit)
	d.Set("message", e.Message)
	if e.FailedStage != "" {
		d.Set("failedStage", e.FailedStage)
		d.Set("residualResources", append([]string{}, e.Residual...))
	}
	return Result{Data: d, Text: "pending: " + e.Verb + " stopped part way; " + retryHint(id, e) + " and the same arguments"}, nil
}
