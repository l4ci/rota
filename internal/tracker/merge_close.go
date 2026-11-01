package tracker

import "context"

// closeAfterMerge leaves an already-done issue alone and otherwise closes it
// with the merged PR's note. Each adapter supplies its native done check.
func closeAfterMerge(ctx context.Context, key IssueKey, pr string, close func(context.Context, IssueKey, string, string) error, done func() (bool, error)) error {
	closed, err := done()
	if err != nil || closed {
		return err
	}
	return close(ctx, key, CloseCompleted, "Closed by rota: "+pr+" merged.")
}
