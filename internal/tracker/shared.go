package tracker

import "context"

// firstN is the first n of list; n at or below zero keeps all of it.
func firstN(list []Issue, n int) []Issue {
	if n > 0 && len(list) > n {
		return list[:n]
	}
	return list
}

// uniq drops empty and repeated names, keeping first-seen order.
func uniq(names []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range names {
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// labelNames is the distinct, non-empty names of labels, and whether any are
// left: the guard every AddLabels and RemoveLabels shares, since writing an
// empty label set is a no-op.
func labelNames(labels []string) ([]string, bool) {
	names := uniq(labels)
	return names, len(names) > 0
}

// editLabels applies e through edit after the empty-label guard: nothing is
// written when neither add nor remove names a label.
func editLabels(ctx context.Context, edit func(context.Context, IssueKey, IssueEdit) error, key IssueKey, e IssueEdit) error {
	add, hasAdd := labelNames(e.AddLabels)
	remove, hasRemove := labelNames(e.RemoveLabels)
	if !hasAdd && !hasRemove {
		return nil
	}
	e.AddLabels, e.RemoveLabels = add, remove
	return edit(ctx, key, e)
}
