package worker

// recordBounceVia counts one bounce through the BounceStore production uses and
// returns the new count.
func recordBounceVia(e Env, root, issue, slot, head string) (int, error) {
	n, _, _, err := recordBounceCappedVia(e, nil, root, issue, slot, head, 0)
	return n, err
}

// recordBounceCappedVia is recordBounceVia with a cap (0 = none) and a round memo.
func recordBounceCappedVia(e Env, m *RoundMemo, root, issue, slot, head string, limit int) (n int, counted, capped bool, err error) {
	st, err := e.BounceStore(root)
	if err != nil {
		return 0, false, false, err
	}
	r, err := st.Record(m, issue, slot, head, limit)
	return r.N, r.Counted, r.Capped, err
}
