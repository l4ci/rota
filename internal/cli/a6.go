package cli

import (
	"errors"
	"flag"
	"os"
	"strconv"

	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/debugctr"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/spike"
	"github.com/l4ci/rota/internal/verdict"
)

// The A6 verbs (milestone, plan, design, spike, proof, debug) live in
// domain packages that return *artifact.Error; this file is their glue.
// The packages cannot register themselves because cli imports them.

func a6Commands() []*Command {
	return append(append(append(docsCommands(), proofCommands()...), milestoneCommands()...), []*Command{
		{Name: "debug", Summary: "Iron Law fix-attempt counter", Subs: []*Command{
			{Name: "counter", Summary: "per-branch debug session counter", Subs: []*Command{
				{Name: "init", Summary: "start the counter for a bug", Verb: noFlags(runCounterInit)},
				{Name: "record-attempt", Summary: "record a pending fix attempt", Verb: counterRecordAttempt},
				{Name: "fail", Summary: "mark the last attempt failed", Verb: noFlags(runCounterFail)},
				{Name: "pass", Summary: "mark the last attempt passed", Verb: noFlags(runCounterPass)},
				{Name: "show", Summary: "print the counter state", Verb: noFlags(runCounterShow)},
				{Name: "summary", Summary: "Iron Law halt note", Verb: noFlags(runCounterSummary)},
				{Name: "clear", Summary: "delete the counter", Verb: noFlags(runCounterClear)},
				{Name: "inc-cycle", Summary: "count a hypothesis cycle", Verb: noFlags(runCounterIncCycle)},
			}},
			{Name: "verdict", Summary: "record whether a fix held and route on the item's failed-fix count", Verb: debugVerdict},
			{Name: "reset", Summary: "start an item's failed-fix count again", Verb: debugReset},
		}},
		{Name: "spike", Summary: "throwaway feasibility spikes", Subs: []*Command{
			{Name: "add", Summary: "create spike/<name> and its file", Repo: true, Verb: spikeAdd},
			{Name: "finish", Summary: "mark a spike done", Verb: noFlags(runSpikeFinish)},
			{Name: "list", Summary: "list spikes", Verb: noFlags(runSpikeList)},
			{Name: "show", Summary: "print a spike file", Verb: noFlags(runSpikeShow)},
		}},
	}...)
}

func asArtifact(err error) *artifact.Error {
	var ae *artifact.Error
	if errors.As(err, &ae) {
		return ae
	}
	return nil
}

// fromArtifact maps a domain error onto the exit table.
func fromArtifact(err error) error {
	var ae *artifact.Error
	if errors.As(err, &ae) {
		return &Error{Exit: ae.Exit, Message: ae.Message, Hint: ae.Hint}
	}
	return err
}

func oneArg(args []string, what string) (string, error) {
	switch len(args) {
	case 0:
		return "", Usage("missing %s", what)
	case 1:
		return args[0], nil
	}
	return "", Usage("expected one %s, got %d arguments", what, len(args))
}

func noArgs(args []string) error {
	if len(args) > 0 {
		return Usage("takes no arguments")
	}
	return nil
}

// ---- debug counter

func openCounter(c *Ctx) (*debugctr.Counter, error) {
	root, err := c.Root()
	if err != nil {
		return nil, err
	}
	ctr, err := debugctr.Open(root)
	return ctr, fromArtifact(err)
}

// ironLaw refuses (exit 4) when bug has 3 or more failed fixes since its
// last reset (B3). The count is the verdict store's, so a new branch or a
// cleared session file does not start it again.
func ironLaw(c *Ctx, bug string) (Result, error) {
	root, err := c.Root()
	if err != nil {
		return Result{}, err
	}
	failed := verdict.FailedFixes(verdict.Load(root).Items[bug])
	if failed < verdict.IronLaw {
		return Result{}, nil
	}
	d := jsonx.NewObject()
	d.Set("blockedBy", "iron law")
	d.Set("bugId", bug)
	d.Set("failedFixes", failed)
	d.Set("changed", false)
	return Result{Data: d}, Refused("%s has %d failed fixes; the Iron Law halts the session", bug, failed).
		WithHint("rota debug reset " + bug)
}

func runCounterInit(c *Ctx, args []string) (Result, error) {
	bug, err := oneArg(args, "bugId")
	if err != nil {
		return Result{}, err
	}
	if res, err := ironLaw(c, bug); err != nil {
		return res, err
	}
	ctr, err := openCounter(c)
	if err != nil {
		return Result{}, err
	}
	changed, err := ctr.Init(bug)
	if err != nil {
		return Result{}, fromArtifact(err)
	}
	d := jsonx.NewObject()
	d.Set("session", ctr.Session)
	d.Set("bugId", bug)
	d.Set("changed", changed)
	return Result{Data: d, Text: ctr.Session}, nil
}

func counterRecordAttempt(fs *flag.FlagSet) RunFunc {
	hyp := fs.String("hypothesis", "", "what the fix assumes")
	commit := fs.String("commit", "", "commit holding the attempt")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		if *hyp == "" || *commit == "" {
			return Result{}, Usage("--hypothesis and --commit are required")
		}
		ctr, err := openCounter(c)
		if err != nil {
			return Result{}, err
		}
		st, err := ctr.State() // exit 3 without a session file
		if err != nil {
			return Result{}, fromArtifact(err)
		}
		bug, _ := st.Get("bug_id")
		if id, _ := bug.(string); id != "" {
			if res, err := ironLaw(c, id); err != nil {
				return res, err
			}
		}
		n, err := ctr.RecordAttempt(*hyp, *commit)
		if err != nil {
			return Result{}, fromArtifact(err)
		}
		d := jsonx.NewObject()
		d.Set("attempt", n)
		d.Set("changed", true)
		return Result{Data: d, Text: strconv.Itoa(n)}, nil
	}
}

// counterRefusal maps a close-attempt error. A refusal (no attempt, or the
// last one is not pending) carries {"blockedBy": "attempt", "changed": false}.
func counterRefusal(err error) (Result, error) {
	var ae *artifact.Error
	if errors.As(err, &ae) && ae.Exit == artifact.ExitRefused {
		d := jsonx.NewObject()
		d.Set("blockedBy", "attempt")
		d.Set("changed", false)
		return Result{Data: d}, fromArtifact(err)
	}
	return Result{}, fromArtifact(err)
}

func runCounterFail(c *Ctx, args []string) (Result, error) {
	if err := noArgs(args); err != nil {
		return Result{}, err
	}
	ctr, err := openCounter(c)
	if err != nil {
		return Result{}, err
	}
	n, err := ctr.Fail()
	if err != nil {
		return counterRefusal(err)
	}
	d := jsonx.NewObject()
	d.Set("failedFixes", n)
	d.Set("changed", true)
	return Result{Data: d, Text: strconv.Itoa(n)}, nil
}

func runCounterPass(c *Ctx, args []string) (Result, error) {
	if err := noArgs(args); err != nil {
		return Result{}, err
	}
	ctr, err := openCounter(c)
	if err != nil {
		return Result{}, err
	}
	n, err := ctr.Pass()
	if err != nil {
		return counterRefusal(err)
	}
	d := jsonx.NewObject()
	d.Set("attempt", n)
	d.Set("changed", true)
	return Result{Data: d}, nil
}

func runCounterIncCycle(c *Ctx, args []string) (Result, error) {
	if err := noArgs(args); err != nil {
		return Result{}, err
	}
	ctr, err := openCounter(c)
	if err != nil {
		return Result{}, err
	}
	n, err := ctr.IncCycle()
	if err != nil {
		return Result{}, fromArtifact(err)
	}
	d := jsonx.NewObject()
	d.Set("hypothesisCycles", n)
	d.Set("changed", true)
	return Result{Data: d, Text: strconv.Itoa(n)}, nil
}

func runCounterClear(c *Ctx, args []string) (Result, error) {
	if err := noArgs(args); err != nil {
		return Result{}, err
	}
	ctr, err := openCounter(c)
	if err != nil {
		return Result{}, err
	}
	changed, err := ctr.Clear()
	if err != nil {
		return Result{}, err
	}
	d := jsonx.NewObject()
	d.Set("changed", changed)
	return Result{Data: d}, nil
}

// camel renames the state file's snake_case keys for --json.
var counterKeys = [][2]string{
	{"session", "session"}, {"bug_id", "bugId"}, {"started_at", "startedAt"},
	{"failed_fixes", "failedFixes"}, {"hypothesis_cycles", "hypothesisCycles"},
}

func runCounterShow(c *Ctx, args []string) (Result, error) {
	if err := noArgs(args); err != nil {
		return Result{}, err
	}
	ctr, err := openCounter(c)
	if err != nil {
		return Result{}, err
	}
	raw, err := ctr.Raw()
	if err != nil {
		return Result{}, fromArtifact(err)
	}
	st, _ := ctr.State()
	d := jsonx.NewObject()
	for _, k := range counterKeys {
		if v, ok := st.Get(k[0]); ok {
			d.Set(k[1], v)
		}
	}
	atts := []any{}
	if v, _ := st.Get("attempts"); v != nil {
		l, _ := v.([]any)
		for _, a := range l {
			ao, ok := a.(*jsonx.Object)
			if !ok {
				continue
			}
			out := jsonx.NewObject()
			for _, k := range [][2]string{{"n", "n"}, {"started_at", "startedAt"}, {"hypothesis", "hypothesis"}, {"commit", "commit"}, {"outcome", "outcome"}, {"ended_at", "endedAt"}} {
				if v, ok := ao.Get(k[0]); ok {
					out.Set(k[1], v)
				}
			}
			atts = append(atts, out)
		}
	}
	d.Set("attempts", atts)
	return Result{Data: d, Text: raw}, nil
}

func runCounterSummary(c *Ctx, args []string) (Result, error) {
	if err := noArgs(args); err != nil {
		return Result{}, err
	}
	ctr, err := openCounter(c)
	if err != nil {
		return Result{}, err
	}
	st, err := ctr.State()
	if err != nil {
		return Result{}, fromArtifact(err)
	}
	md, bug, failed := ctr.Summary(st)
	d := jsonx.NewObject()
	d.Set("bugId", bug)
	d.Set("failedFixes", failed)
	if l, _ := st.Get("attempts"); l == nil || len(l.([]any)) == 0 {
		d.Set("markdown", "")
		return Result{Data: d}, Failed("no attempts recorded yet")
	}
	d.Set("markdown", md)
	return Result{Data: d, Text: md}, nil
}

// ---- spike

func spikeAdd(fs *flag.FlagSet) RunFunc {
	question := fs.String("question", "", "the question the spike answers")
	return func(c *Ctx, args []string) (Result, error) {
		name, err := oneArg(args, "spike name")
		if err != nil {
			return Result{}, err
		}
		if *question == "" {
			return Result{}, Usage("--question is required")
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		gitDir, err := c.RepoPath()
		if err != nil {
			return Result{}, err
		}
		if gitDir == "" {
			if gitDir, err = os.Getwd(); err != nil {
				return Result{}, err
			}
		}
		branch, err := spike.Add(root, gitDir, name, *question, c.Repo)
		if err != nil {
			return Result{Data: refusal(err)}, fromArtifact(err)
		}
		d := jsonx.NewObject()
		d.Set("name", name)
		d.Set("branch", branch)
		d.Set("changed", true)
		return Result{Data: d, Text: branch}, nil
	}
}

func runSpikeFinish(c *Ctx, args []string) (Result, error) {
	name, err := oneArg(args, "spike name")
	if err != nil {
		return Result{}, err
	}
	root, err := c.Root()
	if err != nil {
		return Result{}, err
	}
	changed, err := spike.Finish(root, name)
	if err != nil {
		return Result{}, fromArtifact(err)
	}
	d := jsonx.NewObject()
	d.Set("name", name)
	d.Set("status", "done")
	d.Set("changed", changed)
	return Result{Data: d, Text: "spike " + name + " done"}, nil
}

func runSpikeShow(c *Ctx, args []string) (Result, error) {
	name, err := oneArg(args, "spike name")
	if err != nil {
		return Result{}, err
	}
	root, err := c.Root()
	if err != nil {
		return Result{}, err
	}
	body, err := spike.Show(root, name)
	if err != nil {
		return Result{}, fromArtifact(err)
	}
	d := jsonx.NewObject()
	d.Set("name", name)
	d.Set("body", body)
	return Result{Data: d, Text: body}, nil
}

func runSpikeList(c *Ctx, args []string) (Result, error) {
	if err := noArgs(args); err != nil {
		return Result{}, err
	}
	root, err := c.Root()
	if err != nil {
		return Result{}, err
	}
	cwd, _ := os.Getwd()
	list, err := spike.List(root, cwd)
	if err != nil {
		return Result{}, err
	}
	rows := []any{}
	text := ""
	for _, e := range list {
		o := jsonx.NewObject()
		o.Set("name", e.Name)
		o.Set("branch", e.Branch)
		if e.Repo != "" {
			o.Set("repo", e.Repo)
		}
		o.Set("status", e.Status)
		o.Set("created", e.Created)
		o.Set("branchExists", e.BranchExists)
		rows = append(rows, o)
		text += e.Name + "\t" + e.Status + "\t" + e.Branch + "\n"
	}
	d := jsonx.NewObject()
	d.Set("spikes", rows)
	return Result{Data: d, Text: text}, nil
}
