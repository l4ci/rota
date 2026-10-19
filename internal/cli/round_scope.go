package cli

import (
	"flag"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/round"
)

// The verb `rota round scope`: record the scope an explorer inferred for an
// item that declares none. The declared `## Touches` is never edited.
func roundScope(fs *flag.FlagSet) RunFunc {
	bodyFile := fs.String("body-file", "", "inferred entries, one bullet each: symbols, endpoints, config keys, tests (- for stdin; at most 10)")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) != 1 {
			return Result{}, Usage("round scope takes one item ID")
		}
		if *bodyFile == "" {
			return Result{}, Usage("--body-file is required")
		}
		raw, err := readInputFile(c, *bodyFile)
		if err != nil {
			return Result{}, readInputErr("body-file", *bodyFile, err)
		}
		var entries []string
		for _, l := range strings.Split(decodeText(raw), "\n") {
			if l = strings.TrimSpace(l); strings.HasPrefix(l, "- ") || strings.HasPrefix(l, "* ") {
				entries = append(entries, l[2:])
			}
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		be, err := openBacklog(c, root, false, "")
		if err != nil {
			return backlogFail(err)
		}
		it, err := be.Get(args[0])
		if err != nil {
			return backlogFail(err)
		}
		norm, err := round.RecordInferred(be, it.ID, entries)
		if err != nil {
			return Result{}, Usage("%v", err)
		}
		_, src := round.ItemScopeSource(be, it.ID)
		d := jsonObj("id", it.ID, "scopes", strs(norm), "source", src, "changed", true)
		return Result{Data: d, Text: fmt.Sprintf("recorded %d inferred scope entries on %s (effective source: %s)", len(norm), it.ID, src)}, nil
	}
}
