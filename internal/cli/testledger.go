package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/testledger"
)

// ledgerEntries is the data shape of ledger entries in a verb's result.
func ledgerEntries(list []testledger.Entry) []any {
	out := []any{}
	for _, e := range list {
		o := jsonx.NewObject()
		o.Set("test", e.Test)
		o.Set("owner", e.Owner)
		o.Set("receipt", e.Receipt)
		o.Set("expires", e.Raw)
		out = append(out, o)
	}
	return out
}

// setLedgerData adds the ledger keys a gate or train result carries, only
// when there is something to report.
func setLedgerData(d *jsonx.Object, excluded, expired []testledger.Entry) {
	if len(excluded) > 0 {
		d.Set("excluded", ledgerEntries(excluded))
	}
	if len(expired) > 0 {
		d.Set("expired", ledgerEntries(expired))
	}
}

// testLedgerCheck is `rota test ledger check`: the gate's own reading of
// .rota/test-ledger.json, without running anything.
func testLedgerCheck(c *Ctx, args []string) (Result, error) {
	if len(args) > 0 {
		return Result{}, Usage("unexpected argument %q", args[0])
	}
	root, err := c.Root()
	if err != nil {
		return Result{}, err
	}
	led, err := testledger.Load(root)
	d := jsonx.NewObject()
	var bad *testledger.MalformedError
	if errors.As(err, &bad) {
		m := jsonx.NewObject()
		m.Set("index", bad.Index)
		m.Set("test", bad.Test)
		m.Set("reason", bad.Reason)
		d.Set("verdict", "malformed")
		d.Set("entries", 0)
		d.Set("expired", []any{})
		d.Set("malformed", []any{m})
		return Result{Data: d, Text: bad.Error()}, Failed("%s", bad.Error())
	}
	if err != nil {
		return Result{}, err
	}
	now := time.Now()
	expired := led.Expired(now)
	d.Set("entries", len(led.Entries))
	d.Set("expired", ledgerEntries(expired))
	d.Set("malformed", []any{})
	if len(expired) == 0 {
		d.Set("verdict", "ok")
		return Result{Data: d, Text: fmt.Sprintf("test ledger ok: %d entr(ies), none expired", len(led.Entries))}, nil
	}
	d.Set("verdict", "expired")
	lines := make([]string, len(expired))
	for i, e := range expired {
		lines[i] = e.String()
	}
	msg := "expired test-ledger entries: " + strings.Join(lines, "; ")
	return Result{Data: d, Text: msg}, Failed("%s", msg)
}
