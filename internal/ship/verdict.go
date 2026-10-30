package ship

import "github.com/l4ci/rota/internal/verdict"

// The merge verdict rule lives in internal/verdict so the worker gate and
// train reach it without importing ship.
type (
	VerdictBlockedError = verdict.BlockedError
	VerdictCheck        = verdict.Check
)
