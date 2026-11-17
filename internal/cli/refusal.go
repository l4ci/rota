package cli

import (
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/verdict"
	"github.com/l4ci/rota/internal/worker"
)

// blockedRefusal is the one place the exit-4 refusal envelope is built: d (a
// fresh object when nil) plus blockedBy, the extra key/value pairs in order and
// changed false, with the message and hint on the error.
func blockedRefusal(d *jsonx.Object, by, msg, hint string, extra ...any) (Result, error) {
	return Result{Data: blockedData(d, by, extra...)}, Refused("%s", msg).WithHint(hint)
}

// blockedRefusalErr is blockedRefusal for an error already built (its exit,
// message and hint stay as they are): only the data is the shared envelope.
func blockedRefusalErr(d *jsonx.Object, by string, err error, extra ...any) (Result, error) {
	return Result{Data: blockedData(d, by, extra...)}, err
}

// blockedData is the refusal envelope alone: d (a fresh object when nil) plus
// blockedBy, the extra pairs in order and changed false. For a refusal whose
// error is built elsewhere, or one that exits 1 and carries the same data.
func blockedData(d *jsonx.Object, by string, extra ...any) *jsonx.Object {
	if d == nil {
		d = jsonx.NewObject()
	}
	d.Set("blockedBy", by)
	for i := 0; i+1 < len(extra); i += 2 {
		d.Set(extra[i].(string), extra[i+1])
	}
	d.Set("changed", false)
	return d
}

// verdictBlocked is the refusal for a recorded FAIL verdict. withVerdict adds
// the record's verdict, which the ship paths report and the gate's d already
// carries as the gate verdict.
func verdictBlocked(vb *verdict.BlockedError, d *jsonx.Object, withVerdict bool) (Result, error) {
	extra := []any{"kind", vb.Record.Kind}
	if withVerdict {
		extra = append(extra, "verdict", vb.Record.Verdict)
	}
	extra = append(extra, "sha", vb.Record.Sha, "stale", vb.Stale)
	return blockedRefusal(d, worker.BlockedByVerdict, vb.Error(), "see: rota verdict show "+vb.Branch, extra...)
}
