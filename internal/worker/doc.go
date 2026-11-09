// Package worker owns the standing worker slots of a parallel round and
// everything done to a slot's work.
//
// It holds the slot registry (.rota/workers.json and its list sections), the
// pool of slot worktrees, dispatch of briefs and relays, account selection,
// the merge gate, the merge train, done and its proof checks, and the review
// loop (review depth, inputs and results).
//
// It does not own round-level records (the lease, ledger and watch live in
// the round* packages) or the harness adapters for Claude, Codex and OpenCode
// (internal/harness); it calls them through their interfaces.
//
// The registry file stays byte-identical to what the retired shell helpers
// wrote.
package worker
