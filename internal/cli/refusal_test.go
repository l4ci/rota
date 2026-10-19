package cli

import (
	"errors"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/ship"
	"github.com/l4ci/rota/internal/verdict"
)

// The legacy* builders are the refusal literals as they stood before the
// envelope moved into refusal(): the wire shape skills parse. They are copied,
// not called, so the test compares against the old shape and not the new code.

func legacyRecordedFail(vb *ship.VerdictBlockedError, d *jsonx.Object) (Result, error) {
	d.Set("blockedBy", "verdict")
	d.Set("kind", vb.Record.Kind)
	d.Set("sha", vb.Record.Sha)
	d.Set("stale", vb.Stale)
	d.Set("changed", false)
	return Result{Data: d}, Refused("%s", vb.Error()).WithHint("see: rota verdict show " + vb.Branch)
}

func legacyShipVerdict(vb *ship.VerdictBlockedError) (Result, error) {
	d := gitObj("blockedBy", "verdict", "kind", vb.Record.Kind, "verdict", vb.Record.Verdict, "sha", vb.Record.Sha, "stale", vb.Stale, "changed", false)
	return Result{Data: d}, Refused("%s", vb.Error()).WithHint("see: rota verdict show " + vb.Branch)
}

func legacyNoVerify(msg, hint string, d *jsonx.Object) (Result, error) {
	d.Set("blockedBy", "no-verify")
	d.Set("key", "test.full")
	d.Set("changed", false)
	return Result{Data: d}, Refused("%s", msg).WithHint(hint)
}

func legacyBlocked(by, msg, hint string, d *jsonx.Object) (Result, error) {
	d.Set("blockedBy", by)
	d.Set("changed", false)
	return Result{Data: d}, Refused("%s", msg).WithHint(hint)
}

func legacyShipBlocked(by, msg string) (Result, error) {
	return Result{Data: gitObj("blockedBy", by, "changed", false)}, Refused("%s", msg)
}

// wire renders a result and its error the way the CLI prints them.
func wire(t *testing.T, res Result, err error) string {
	t.Helper()
	b, merr := jsonx.MarshalCompact(res.Data)
	if merr != nil {
		t.Fatal(merr)
	}
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("want *Error, got %v", err)
	}
	return string(b) + "|" + e.Error() + "|" + e.Hint
}

func gateBase() *jsonx.Object {
	d := jsonx.NewObject()
	d.Set("slot", "nia")
	d.Set("verdict", "verdict-blocked")
	d.Set("sha", "full")
	return d
}

func TestRefusalEnvelopeMatchesLegacyShape(t *testing.T) {
	vb := &ship.VerdictBlockedError{Branch: "b", Record: verdict.Record{Kind: "quality", Verdict: "FAIL", Sha: "abc"}, Stale: true}
	cases := []struct {
		name        string
		legacy, now func() (Result, error)
	}{
		{"gate recorded fail",
			func() (Result, error) { return legacyRecordedFail(vb, gateBase()) },
			func() (Result, error) { return verdictBlocked(vb, gateBase(), false) }},
		{"ship recorded fail",
			func() (Result, error) { return legacyShipVerdict(vb) },
			func() (Result, error) { return verdictBlocked(vb, nil, true) }},
		{"no-verify",
			func() (Result, error) { return legacyNoVerify("m", "h", gateBase()) },
			func() (Result, error) { return noVerifyRefusal("m", "h", gateBase()) }},
		{"other reason",
			func() (Result, error) { return legacyBlocked("closes", "m", "h", gateBase()) },
			func() (Result, error) { return blockedRefusal(gateBase(), "closes", "m", "h") }},
		{"ship blocked",
			func() (Result, error) { return legacyShipBlocked("refused", "m") },
			func() (Result, error) { return shipBlocked("refused", "%s", "m") }},
	}
	for _, c := range cases {
		lr, le := c.legacy()
		nr, ne := c.now()
		if want, got := wire(t, lr, le), wire(t, nr, ne); want != got {
			t.Errorf("%s:\n legacy %s\n now    %s", c.name, want, got)
		}
	}
}
