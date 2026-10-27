package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/l4ci/rota/internal/flakeledger"
	"github.com/l4ci/rota/internal/jsonx"
)

func testFlakes(c *Ctx, args []string) (Result, error) {
	if err := noArgs(args); err != nil {
		return Result{}, err
	}
	root, err := c.Root()
	if err != nil {
		return Result{}, err
	}
	entries, err := flakeledger.Load(root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Result{}, Unavailable("cannot read flake ledger: %v", err)
	}
	ranked := flakeledger.Rank(entries)
	rows := make([]any, 0, len(ranked))
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	if len(ranked) > 0 {
		fmt.Fprintln(tw, "COUNT\tCHECK\tTEST\tLAST SEEN")
	}
	for _, r := range ranked {
		when := r.LastSeen.UTC().Format(time.RFC3339)
		o := jsonx.NewObject()
		o.Set("check", r.Check)
		o.Set("test", r.Test)
		o.Set("count", r.Count)
		o.Set("lastSeen", when)
		rows = append(rows, o)
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", r.Count, r.Check, r.Test, when)
	}
	tw.Flush()
	if len(ranked) == 0 {
		b.WriteString("no recorded flakes")
	}
	d := jsonx.NewObject()
	d.Set("flakes", rows)
	d.Set("records", len(entries))
	return Result{Data: d, Text: b.String()}, nil
}
