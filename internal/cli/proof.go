package cli

import (
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/plan"
	"github.com/l4ci/rota/internal/proof"
	"github.com/l4ci/rota/internal/worker"
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
		row, changed, err := proof.Add(st, root, id, c.deps().Today(), proof.AddOpts{Check: *check, Result: *result, Evidence: *evidence, Sha: *sha})
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
		expanded, err := expandFiles(c, root, *base, cmdArgs)
		if err != nil {
			return Result{}, err
		}
		command := expanded[0]
		ctx, stop := workerContext()
		defer stop()
		run, err := runProofCommand(c, ctx, root, root, command, "proof record")
		if err != nil {
			return Result{}, err
		}
		code := run.code
		result := "PASS"
		if code != 0 {
			result = "FAIL"
		}
		row, changed, err := proof.Add(st, root, id, c.deps().Today(), proof.AddOpts{Check: command, Result: result, Evidence: run.evidence()})
		if err != nil {
			return failAny(err)
		}
		d := typedData(w.ID, w.Type, nil)
		d.Set("check", row.Check)
		d.Set("result", row.Result)
		d.Set("sha", row.Sha)
		d.Set("evidence", row.Evidence)
		d.Set("exitCode", code)
		d.Set("niced", run.niced)
		d.Set("changed", changed)
		res := Result{Data: d, Text: row.Result + ": " + row.Check}
		if code != 0 {
			return res, Failed("%s (exit %d)", command, code)
		}
		return res, nil
	}
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

// proofRun is one measured proof command: what it printed, how it ended and
// whether it ran at lowered priority.
type proofRun struct {
	out      string
	code     int
	niced    bool
	targeted bool
}

// evidence is the proof row's evidence: the exit code and an output hash, then
// the marks (niced, targeted) of how the command ran, so the row says so.
func (r proofRun) evidence() string {
	ev := fmt.Sprintf("exit=%d output-sha256=%x", r.code, sha256.Sum256([]byte(r.out)))
	if r.niced {
		ev += " niced=true"
	}
	if r.targeted {
		ev += " targeted=true"
	}
	return ev
}

// runProofCommand runs a worker's proof command in dir holding a test slot, at
// lowered priority (nice -n 10, ionice -c3 when present) so the orchestrator's
// gate wins the scheduler. The command's descendants inherit the priority, and
// ROTA_TEST_LOCK_HELD keeps a `rota test run` inside it from queueing behind
// the slot its parent holds.
func runProofCommand(c *Ctx, ctx context.Context, root, dir, command, verb string) (proofRun, error) {
	run := proofRun{}
	wrapped, niced := worker.NiceCommand(command, c.deps().lookPath())
	run.niced = niced
	err := withTestLock(c, ctx, root, c.deps().workerEnv().GateConfig(root), verb, func() error {
		run.out, run.code = workerEnvCtx(c, ctx).RunShell(ctx, dir, "export "+testLockHeldEnv+"=1; "+wrapped)
		return nil
	})
	return run, err
}
