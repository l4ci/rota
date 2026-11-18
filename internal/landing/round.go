package landing

import "sync"

// RoundMemo reads the round lease once for a verb: the verb makes one and hands
// it to every ledger write it triggers. A nil memo reads the lease each time.
type RoundMemo struct {
	once sync.Once
	n    int
}

// Round is the lease's round, read on the first call only.
func (m *RoundMemo) Round(env interface{ LedgerRound(string) int }, root string) int {
	if m == nil {
		return env.LedgerRound(root)
	}
	m.once.Do(func() { m.n = env.LedgerRound(root) })
	return m.n
}
