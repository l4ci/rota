package cli

import (
	"flag"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/plan"
	"github.com/l4ci/rota/internal/proof"
)

// Glue for the proof group and plan uncertain, which read items through
// internal/backlog. File mode only for now; issue mode exits 71.

func proofCommands() []*Command {
	return []*Command{
		{Name: "proof", Summary: "verification proof rows on items", Subs: []*Command{
			{Name: "add", Summary: "append a proof row", Verb: proofAdd},
			{Name: "show", Summary: "list an item's proof rows", Verb: proofShow},
		}},
	}
}

// backlogMode is the project root and whether backlog.backend is "issues".
// An invalid backlog.backend is an internal error (exit 70, as the old
// helpers exited 1 on it).
func backlogMode(c *Ctx) (root string, issue bool, err error) {
	root, err = c.Root()
	if err != nil {
		return "", false, err
	}
	name, err := config.Backend(config.Load(root + "/.rota/config.json"))
	if err != nil {
		return "", false, &Error{Exit: ExitInternal, Message: err.Error()}
	}
	return root, name == "issues", nil
}

func proofData(id string, changed any) *jsonx.Object {
	d := jsonx.NewObject()
	d.Set("id", id)
	d.Set("type", id[:1])
	if changed != nil {
		d.Set("changed", changed)
	}
	return d
}

func proofAdd(fs *flag.FlagSet) RunFunc {
	check := fs.String("check", "", "what was checked")
	result := fs.String("result", "", "PASS or FAIL")
	evidence := fs.String("evidence", "", "path or text proving it")
	sha := fs.String("sha", "", "commit the check ran against (default: HEAD)")
	return func(c *Ctx, args []string) (Result, error) {
		id, err := oneArg(args, "item ID")
		if err != nil {
			return Result{}, err
		}
		if strings.TrimSpace(*check) == "" || strings.TrimSpace(*evidence) == "" {
			return Result{}, Usage("--check and --evidence are required")
		}
		if *result != "PASS" && *result != "FAIL" {
			return Result{}, Usage("--result must be exactly PASS or FAIL")
		}
		root, issue, err := backlogMode(c)
		if err != nil {
			return Result{}, err
		}
		if issue {
			return proofAddIssue(c, root, id, proof.AddOpts{Check: *check, Result: *result, Evidence: *evidence, Sha: *sha})
		}
		row, changed, err := proof.Add(root, id, proof.AddOpts{Check: *check, Result: *result, Evidence: *evidence, Sha: *sha})
		if err != nil {
			return Result{}, err
		}
		d := proofData(id, nil)
		d.Set("check", row.Check)
		d.Set("result", row.Result)
		d.Set("sha", row.Sha)
		d.Set("evidence", row.Evidence)
		d.Set("changed", changed)
		return Result{Data: d, Text: id}, nil
	}
}

func proofShow(fs *flag.FlagSet) RunFunc {
	count := fs.Bool("count", false, "text mode: print only the number of rows")
	return func(c *Ctx, args []string) (Result, error) {
		id, err := oneArg(args, "item ID")
		if err != nil {
			return Result{}, err
		}
		root, issue, err := backlogMode(c)
		if err != nil {
			return Result{}, err
		}
		if issue {
			return proofShowIssue(c, id, *count)
		}
		rows, lines, err := proof.Show(root, id)
		if err != nil {
			return Result{}, err
		}
		return proofResult(id, id[:1], rows, lines, *count), nil
	}
}

// proofResult is proof show's answer: the rows as JSON, or their lines (the
// count alone with --count) as text.
func proofResult(id, typ string, rows []proof.Row, lines []string, countOnly bool) Result {
	out := make([]any, len(rows))
	for i, r := range rows {
		o := jsonx.NewObject()
		o.Set("date", r.Date)
		o.Set("check", r.Check)
		o.Set("result", r.Result)
		o.Set("sha", r.Sha)
		o.Set("evidence", r.Evidence)
		out[i] = o
	}
	d := typedData(id, typ, nil)
	d.Set("count", len(rows))
	d.Set("rows", out)
	text := strings.Join(lines, "\n")
	if countOnly {
		text = strconv.Itoa(len(rows))
	}
	return Result{Data: d, Text: text}
}

func runPlanUncertain(c *Ctx, args []string) (Result, error) {
	id, err := oneArg(args, "item ID")
	if err != nil {
		return Result{}, err
	}
	root, issue, err := backlogMode(c)
	if err != nil {
		return Result{}, err
	}
	if issue {
		return uncertainIssue(c, id)
	}
	typ, reasons, err := plan.Uncertain(root, id)
	if err != nil {
		return Result{}, err
	}
	return uncertainResult(id, typ, reasons)
}

func uncertainResult(id, typ string, reasons []string) (Result, error) {
	rs := make([]any, len(reasons))
	for i, r := range reasons {
		rs[i] = r
	}
	d := jsonx.NewObject()
	d.Set("id", id)
	d.Set("type", typ)
	d.Set("uncertain", len(reasons) > 0)
	d.Set("reasons", rs)
	if len(reasons) == 0 {
		return Result{Data: d}, Failed("%s is certain", id)
	}
	return Result{Data: d, Text: strings.Join(reasons, "\n")}, nil
}
