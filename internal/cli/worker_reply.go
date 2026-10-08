package cli

import (
	"flag"
	"io"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/worker"
)

// workerReply is `rota worker reply <slot> --body-file <f>`: a worker answers a
// reviewer on its PR. It posts through the tracker, so GitHub and GitLab behave
// the same, and ends the comment with a rota marker, which is how the review poll
// tells the worker's own comments from review input when both share one forge
// login.
func workerReply(fs *flag.FlagSet) RunFunc {
	bodyFile := fs.String("body-file", "", "the reply: a path, or - for stdin")
	return func(c *Ctx, args []string) (Result, error) {
		slot, err := oneArg(args, "slot")
		if err != nil {
			return Result{}, err
		}
		if *bodyFile == "" {
			return Result{}, Usage("--body-file is required")
		}
		var raw []byte
		if *bodyFile == "-" {
			raw, err = io.ReadAll(c.Stdin)
		} else {
			raw, err = os.ReadFile(*bodyFile)
		}
		if err != nil {
			return Result{}, Usage("--body-file %s: %v", *bodyFile, unwrapPathErr(err))
		}
		text := strings.TrimSpace(string(raw))
		if text == "" {
			return Result{}, Usage("the reply is empty")
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		s := worker.LoadRegistry(root).Slot(slot)
		if s == nil {
			return Result{}, Resolution("slot '%s' is not in the pool", slot)
		}
		n, ok := worker.PRRefNumber(s.PR())
		if !ok {
			return Result{}, Resolution("slot '%s' has no PR to reply on", slot)
		}
		ctx := c.Context()
		fg, err := c.deps().forge(ctx, config.Load(rotatree.Config(root)), "", root)
		if err != nil {
			return Result{}, trackerErr(err)
		}
		id, err := fg.AddMRNote(ctx, n, text+"\n\n"+marker.Line("worker-reply", slot))
		if err != nil {
			return Result{}, trackerErr(err)
		}
		url, err := fg.CommentURL(ctx, true, n, id)
		if err != nil {
			url = "" // posted; the link is a nicety
		}
		d := jsonx.NewObject()
		d.Set("slot", slot)
		d.Set("pr", s.PR())
		d.Set("commentId", id)
		d.Set("url", url)
		d.Set("changed", true)
		out := url
		if out == "" {
			out = s.PR() + " (comment " + id + ")"
		}
		return Result{Data: d, Text: out}, nil
	}
}
