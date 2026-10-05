package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/escalation"
	"github.com/l4ci/rota/internal/jsonx"
)

// escalationEnv is the seam tests replace to inject a clock and a fake host.
// The forge goes through trackerOptions like every other verb, so a test
// swaps the executor there.
func escalationForge(c *Ctx) func(ctx context.Context, root string) (escalation.Forge, error) {
	return func(ctx context.Context, root string) (escalation.Forge, error) {
		cfg := config.Load(filepath.Join(root, ".rota", "config.json"))
		return c.deps().forge(ctx, cfg, "", root)
	}
}

// fromEscalation maps an escalation.Error onto the exit table, keeping the
// failure data for exit 1 and 4.
func fromEscalation(err error) (Result, error) {
	var ee *escalation.Error
	if errors.As(err, &ee) {
		return Result{Data: ee.Data}, &Error{Exit: ee.Exit, Message: ee.Message}
	}
	return Result{}, err
}

// roundEscalate is the `rota round escalate` group.
func roundEscalate() *Command {
	return &Command{Name: "escalate", Summary: "ask the human on an issue or PR thread", Subs: []*Command{
		{Name: "send", Summary: "post a question on a thread and notify", Verb: roundEscalateSend},
		{Name: "check", Summary: "look for the human's answers", Verb: roundEscalateCheck},
	}}
}

func roundEscalateSend(fs *flag.FlagSet) RunFunc {
	pr := fs.Bool("pr", false, "the number is a PR/MR, not an issue")
	slot := fs.String("slot", "", "slot the question comes from")
	title := fs.String("title", "", "one-line question")
	bodyFile := fs.String("body-file", "", "question body: a path, or - for stdin")
	timeout := fs.Float64("timeout", 0, "seconds until the escalation counts as timed out; 0 means no deadline")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) != 1 {
			return Result{}, Usage("usage: rota round escalate send <number> [--pr] --title <text> --body-file <path|->")
		}
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 1 || strings.TrimLeft(args[0], "0123456789") != "" {
			return Result{}, Usage("<number> must be a positive integer, not %q", args[0])
		}
		if strings.TrimSpace(*title) == "" {
			return Result{}, Usage("--title is required")
		}
		if *bodyFile == "" {
			return Result{}, Usage("--body-file is required")
		}
		if *timeout < 0 {
			return Result{}, Usage("--timeout must not be negative")
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
		if strings.TrimSpace(string(raw)) == "" {
			return Result{}, Usage("the body is empty")
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		env := c.deps().EscalationEnv()
		if env.Forge == nil {
			env.Forge = escalationForge(c)
		}
		res, err := escalation.Send(c.Context(), env, root, escalation.SendOpts{
			Number: n, PR: *pr, Slot: *slot, Title: strings.TrimSpace(*title), Body: string(raw),
			Timeout: time.Duration(*timeout * float64(time.Second)),
		})
		for _, w := range res.Warnings {
			c.Warn("%s", w)
		}
		if err != nil {
			return fromEscalation(err)
		}
		d := jsonx.NewObject()
		d.Set("escalation", res.Entry.Object())
		d.Set("url", res.URL)
		d.Set("notified", res.Notified)
		d.Set("changed", true)
		text := fmt.Sprintf("%s\t%s", res.Entry.ID, res.URL)
		return Result{Data: d, Text: text}, nil
	}
}

func roundEscalateCheck(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		env := c.deps().EscalationEnv()
		if env.Forge == nil {
			env.Forge = escalationForge(c)
		}
		res, err := escalation.Check(c.Context(), env, root, args)
		for _, w := range res.Warnings {
			c.Warn("%s", w)
		}
		if err != nil {
			return fromEscalation(err)
		}
		rows, lines := escalationRows(res.Reports)
		var pending, answered, timedOut int
		for _, r := range res.Reports {
			switch r.Status {
			case escalation.StatusPending:
				pending++
			case escalation.StatusAnswered:
				answered++
			case escalation.StatusTimedOut:
				timedOut++
			}
		}
		d := jsonx.NewObject()
		d.Set("escalations", rows)
		d.Set("pending", pending)
		d.Set("answered", answered)
		d.Set("timedOut", timedOut)
		d.Set("changed", res.Changed)
		return Result{Data: d, Text: strings.Join(lines, "\n")}, nil
	}
}

// escalationRows renders reports as `check` shows them, which is also the
// `escalations` list of `round status` and `round reconcile`, plus one text
// line each.
func escalationRows(reports []escalation.Report) ([]any, []string) {
	rows := make([]any, 0, len(reports))
	var lines []string
	for _, r := range reports {
		e := r.Entry
		o := jsonx.NewObject()
		o.Set("id", e.ID)
		o.Set("kind", e.Kind)
		o.Set("number", e.Number)
		if e.Slot != "" {
			o.Set("slot", e.Slot)
		}
		o.Set("title", e.Title)
		o.Set("status", r.Status)
		o.Set("sentAt", e.SentAt)
		if e.Deadline != "" {
			o.Set("deadline", e.Deadline)
		}
		if e.Answer != nil {
			o.Set("answer", e.Answer.Object())
		}
		rows = append(rows, o)
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s #%d\t%s", e.ID, r.Status, e.Kind, e.Number, e.Title))
	}
	return rows, lines
}
