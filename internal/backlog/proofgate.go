package backlog

// The proof-before-done rule, written once. Completing an item as done and
// merging a PR that closes it both need a recorded proof row; the three
// callers (File.Complete, Issues.Complete, Issues.MergePRGated) differ only in
// where the rows are counted and what a miss does.

// hasProof reports whether count finds at least one proof row.
func hasProof(count func() (int, error)) (bool, error) {
	n, err := count()
	return n > 0, err
}

// requireProof is the refusal for a done close without proof: nil when a row
// exists, else a RefusedError wrapping ErrProofMissing.
func requireProof(id string, count func() (int, error)) error {
	ok, err := hasProof(count)
	if err != nil {
		return err
	}
	if !ok {
		return refused("proof missing", ErrProofMissing,
			"[%s] no proof recorded, pass --no-proof to override", id)
	}
	return nil
}
