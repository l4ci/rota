package cli

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/plan"
	"github.com/l4ci/rota/internal/proof"
)

// Glue for the proof group and plan uncertain, which read items through
// internal/backlog. Both run on whichever store the backend gives them.

func proofCommands() []*Command {
	return []*Command{
		{Name: "proof", Summary: "verification proof rows on items", Subs: []*Command{
			{Name: "add", Summary: "append a proof row", Verb: proofAdd},
			{Name: "record", Summary: "run a command and record it as a proof row", Verb: proofRecord},
			{Name: "show", Summary: "list an item's proof rows", Verb: proofShow},
		}},
	}
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
		root, st, w, err := openProof(c, id)
		if err != nil {
			return Result{}, err
		}
		row, changed, err := proof.Add(st, root, id, proof.AddOpts{Check: *check, Result: *result, Evidence: *evidence, Sha: *sha})
		if err != nil {
			return failAny(err)
		}
		d := typedData(w.ID, w.Type, nil)
		d.Set("check", row.Check)
		d.Set("result", row.Result)
		d.Set("sha", row.Sha)
		d.Set("evidence", row.Evidence)
		d.Set("changed", changed)
		return Result{Data: d, Text: w.ID}, nil
	}
}

// proofRecord is `rota proof record <ID> [--base <ref>] -- <cmd>...`: it runs
// the command itself, so the row's check, result and evidence are measured,
// not typed.
func proofRecord(fs *flag.FlagSet) RunFunc {
	base := fs.String("base", "", "ref {files} is diffed against (default: the base branch)")
	return func(c *Ctx, args []string) (Result, error) {
		if c.dashAt != 1 {
			return Result{}, Usage("usage: rota proof record <ID> -- <command> [<arg>...]").
				WithHint("the command goes after --, e.g. rota proof record B07 -- go test ./...")
		}
		if len(args) < 2 || strings.TrimSpace(strings.Join(args[1:], "")) == "" {
			return Result{}, Usage("missing <command> after --")
		}
		id, cmdArgs := args[0], args[1:]
		root, st, w, err := openProof(c, id)
		if err != nil {
			return Result{}, err
		}
		// Resolve the item before running anything: an unknown one exits 3
		// without the command having run.
		if err := st.Update(id, func(content string) (string, bool, error) { return content, false, nil }); err != nil {
			return failAny(err)
		}
		list := ""
		if anyContains(cmdArgs, filesPlaceholder) {
			files, err := changedFiles(c, root, *base)
			if err != nil {
				return Result{}, err
			}
			quoted := make([]string, len(files))
			for i, f := range files {
				quoted[i] = shellQuote(f)
			}
			list = strings.Join(quoted, " ")
		}
		command := recordedCommand(cmdArgs, list)
		ctx, stop := workerContext()
		defer stop()
		out, code := workerEnvCtx(c, ctx).RunShell(ctx, root, command)
		result := "PASS"
		if code != 0 {
			result = "FAIL"
		}
		evidence := fmt.Sprintf("exit=%d output-sha256=%x", code, sha256.Sum256([]byte(out)))
		row, changed, err := proof.Add(st, root, id, proof.AddOpts{Check: command, Result: result, Evidence: evidence})
		if err != nil {
			return failAny(err)
		}
		d := typedData(w.ID, w.Type, nil)
		d.Set("check", row.Check)
		d.Set("result", row.Result)
		d.Set("sha", row.Sha)
		d.Set("evidence", row.Evidence)
		d.Set("exitCode", code)
		d.Set("changed", changed)
		res := Result{Data: d, Text: row.Result + ": " + row.Check}
		if code != 0 {
			return res, Failed("%s (exit %d)", command, code)
		}
		return res, nil
	}
}

// recordedCommand is the shell line that ran: one argument is a shell line
// already; several are quoted one by one. {files} expands to list, which is
// already quoted, and stays unquoted when it is a whole argument.
func recordedCommand(args []string, list string) string {
	if len(args) == 1 {
		return strings.ReplaceAll(args[0], filesPlaceholder, list)
	}
	parts := make([]string, 0, len(args))
	for _, a := range args {
		switch {
		case a == filesPlaceholder:
			if list != "" {
				parts = append(parts, list)
			}
		default:
			parts = append(parts, shellQuote(strings.ReplaceAll(a, filesPlaceholder, list)))
		}
	}
	return strings.Join(parts, " ")
}

func proofShow(fs *flag.FlagSet) RunFunc {
	count := fs.Bool("count", false, "text mode: print only the number of rows")
	return func(c *Ctx, args []string) (Result, error) {
		id, err := oneArg(args, "item ID")
		if err != nil {
			return Result{}, err
		}
		_, st, w, err := openProof(c, id)
		if err != nil {
			return Result{}, err
		}
		rows, lines, err := proof.Show(st, id)
		if err != nil {
			return failAny(err)
		}
		return proofResult(w.ID, w.Type, rows, lines, *count), nil
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
	src, err := openItems(c, id)
	if err != nil {
		return Result{}, err
	}
	itemID, typ, reasons, err := plan.Uncertain(src, id)
	if err != nil {
		return failAny(err)
	}
	return uncertainResult(itemID, typ, reasons)
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
