package tracker

import "context"

// closeAfterMerge leaves an already-done issue alone and otherwise closes it
// with the merged PR's note. Each adapter supplies its native done check.
//
// Retries accept repeated comments. Close comments before it changes state, so
// a permanent state-change failure (no done transition in the Jira workflow, no
// completed or canceled column on It's a Plan) posts the same note again on
// every retry of this path. Deduplicating would need a comment read in each
// adapter; the note is idempotent text and the error still surfaces, so the
// duplicate is accepted.
func closeAfterMerge(ctx context.Context, key IssueKey, pr string, close func(context.Context, IssueKey, string, string) error, done func() (bool, error)) error {
	closed, err := done()
	if err != nil || closed {
		return err
	}
	return close(ctx, key, CloseCompleted, "Closed by rota: "+pr+" merged.")
}
