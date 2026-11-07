package ship

import "github.com/l4ci/rota/internal/exitcode"

// Every ship verb reports a refusal as an error: a *Refusal for a guard the
// rules own, a *GateRefusedError for a gate the caller ran. Callers branch on
// the error type; no verb returns a refusal beside its error.

// Refusal is a guard that stopped a ship verb before it changed anything. By
// names the guard (the envelope's blockedBy).
type Refusal struct {
	By, Msg, Hint string
}

func (r *Refusal) Error() string { return r.Msg }

// GateRefusedError is a caller-run gate (verdict, merge approval) that refused.
// Ref carries the gate's own exit-coded error and envelope.
type GateRefusedError struct{ Ref *exitcode.Refusal }

func (e *GateRefusedError) Error() string { return e.Ref.Err.Error() }

func (e *GateRefusedError) Unwrap() error { return e.Ref.Err }

// NotFoundError is a cycle that cannot be resolved to a commit.
type NotFoundError struct{ Msg string }

func (e *NotFoundError) Error() string { return e.Msg }
