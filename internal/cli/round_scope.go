package cli

import (
	"flag"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/round"
)

// scopeToken closes a scout reply that holds a scope; any other last line
// (too-vague., no-match., a hedge) is refused rather than recorded.
const scopeToken = "scoped."

// The verb `rota round scope`: record the scope an explorer inferred for an
// item that declares none. The declared `## Touches` is never edited.
func roundScope(fs *flag.FlagSet) RunFunc {
	bodyFile := fs.String("body-file", "", "inferred entries, one bullet each: symbols, endpoints, config keys, tests (- for stdin; at most 10), then a last line `scoped.`")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) != 1 {
			return Result{}, Usage("round scope takes one item ID")
		}
		if *bodyFile == "" {
			return Result{}, Usage("--body-file is required")
		}
		raw, err := readBodyInput(c, "body-file", *bodyFile, ExitResolution)
		if err != nil {
			return Result{}, err
		}
		var entries []string
		last := ""
		for _, l := range strings.Split(decodeText(raw), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				last = l
			}
			if strings.HasPrefix(l, "- ") || strings.HasPrefix(l, "* ") {
				entries = append(entries, l[2:])
			}
		}
		if last != scopeToken {
			return Result{}, Refused("the reply must end with %q on its last line (got %q): too-vague. and no-match. are not scope", scopeToken, last).
				WithHint("a scout ends scoped., too-vague. or no-match.; record only scoped.")
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
