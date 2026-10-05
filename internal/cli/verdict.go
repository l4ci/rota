package cli

import (
	"errors"
	"flag"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/rotatree"
	"os"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/debugctr"
	"github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/verdict"
)

// verdictCommands is the `rota verdict` group (B2, #55). `rota debug verdict`
// lives in the debug group (debug.go) and shares this file's glue.
func verdictCommands() *Command {
	return &Command{Name: "verdict", Summary: "record typed review, second-opinion and QA verdicts and route on them", Subs: []*Command{
		{Name: "add", Summary: "record a verdict for a branch", Repo: true, Verb: verdictAdd},
		{Name: "show", Summary: "latest verdict of each kind for a branch", Repo: true, Verb: noFlags(verdictShow)},
		{Name: "route", Summary: "next step for a consumer of a branch's verdict", Repo: true, Verb: verdictRoute},
	}}
}

// verdictBranch is the branch a verdict verb works on and where its
// records live.
type verdictBranch struct {
	root, key, repo, branch, head string
}

func resolveVerdictBranch(c *Ctx, args []string) (verdictBranch, error) {
	t, err := resolveBranch(c, args)
	if err != nil {
		return verdictBranch{}, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return verdictBranch{}, err
	}
	root, err := git.FindRoot(cwd, registeredRels)
	if err != nil {
		return verdictBranch{}, Resolution("no .rota/ directory here or in any parent").WithHint("run: rota init")
	}
	v := verdictBranch{root: root, repo: c.Repo, branch: t.Branch}
	if v.repo == "" && repos.Umbrella(root) {
		if r, err := repos.Which(t.Dir); err == nil {
			v.repo = r.Name
		}
	}
	v.key = verdict.BranchKey(v.repo, v.branch)
	out, err := reviewGit(c.Context(), t.Dir, "rev-parse", "--short", t.Branch)
	if err != nil {
		return verdictBranch{}, err
	}
	v.head = strings.TrimSpace(out)
	return v, nil
}

// verdictBody reads and validates an optional --body-file.
func verdictBody(c *Ctx, file string) (verdict.Body, error) {
	if file == "" {
		return verdict.Body{Findings: []verdict.Finding{}}, nil
	}
	text, err := readBody(c, file)
	if err != nil {
		return verdict.Body{}, err
	}
	b, err := verdict.ParseBody(text)
	return b, err
}

// sameVerdict is exit 2 when the body names a verdict other than --verdict.
func sameVerdict(b verdict.Body, v string) error {
	if b.Verdict != "" && b.Verdict != v {
		return Usage("the body's verdict %s does not match --verdict %s", b.Verdict, v)
	}
	return nil
}

// checkVerdict is exit 2 unless kind takes v.
func checkVerdict(kind, v string) error {
	if v == "" {
		return Usage("--verdict is required")
	}
	if ok, allowed := verdict.Takes(kind, v); !ok {
		return Usage("--verdict %s is not a %s verdict (expected %s)", v, kind, strings.Join(allowed, ", "))
	}
	return nil
}

func verdictAdd(fs *flag.FlagSet) RunFunc {
	kind := fs.String("kind", "", "review-spec, review-quality, second-opinion or qa")
	v := fs.String("verdict", "", "PASS, CONCERNS or FAIL (qa also INFRA-FAIL)")
	body := bodyFlag(fs)
	return func(c *Ctx, args []string) (Result, error) {
		if *kind == "" {
			return Result{}, Usage("--kind is required")
		}
		if !verdict.KnownKind(*kind) || *kind == verdict.DebugFix {
			return Result{}, Usage("--kind must be one of %s", strings.Join(verdict.BranchKinds, ", "))
		}
		if err := checkVerdict(*kind, *v); err != nil {
			return Result{}, err
		}
		b, err := verdictBody(c, *body)
		if err != nil {
			return Result{}, err
		}
		if err := sameVerdict(b, *v); err != nil {
			return Result{}, err
		}
		t, err := resolveVerdictBranch(c, args)
		if err != nil {
			return Result{}, err
		}
		r := verdict.NewRecord(*kind, *v, t.head, b)
		r.Branch, r.Repo = t.branch, t.repo
		if r, err = verdict.AddBranch(t.root, t.key, r); err != nil {
			return Result{}, err
		}
		d := r.Object()
		next := verdict.ProducerNext(r.Kind, r.Verdict)
		d.Set("next", next)
		d.Set("changed", true)
		text := r.Verdict
		if r.Combined != "" {
			text = r.Combined
		}
		return Result{Data: d, Text: text + " -> " + next}, nil
	}
}

func verdictShow(c *Ctx, args []string) (Result, error) {
	t, err := resolveVerdictBranch(c, args)
	if err != nil {
		return Result{}, err
	}
	list := verdict.Load(t.root).Branches[t.key]
	records := []any{}
	var lines []string
	for _, kind := range verdict.BranchKinds {
		r, ok := verdict.Latest(list, kind)
		if !ok {
			continue
		}
		o := r.Object()
		o.Set("stale", r.Sha != t.head)
		records = append(records, o)
		lines = append(lines, kind+" "+r.Verdict+" @ "+r.Sha)
	}
	d := jsonx.NewObject()
	d.Set("branch", t.branch)
	if t.repo != "" {
		d.Set("repo", t.repo)
	}
	d.Set("head", t.head)
	d.Set("records", records)
	return Result{Data: d, Text: strings.Join(lines, "\n")}, nil
}

func verdictRoute(fs *flag.FlagSet) RunFunc {
	consumer := fs.String("for", "", strings.Join(verdict.Consumers, ", "))
	return func(c *Ctx, args []string) (Result, error) {
		known := false
		for _, k := range verdict.Consumers {
			known = known || k == *consumer
		}
		if !known {
			return Result{}, Usage("--for must be one of %s", strings.Join(verdict.Consumers, ", "))
		}
		t, err := resolveVerdictBranch(c, args)
		if err != nil {
			return Result{}, err
		}
		r, ok := verdict.ForConsumer(*consumer, verdict.Load(t.root).Branches[t.key])
		if !ok {
			return Result{}, Resolution("no verdict recorded for %s that %s reads", t.branch, *consumer).
				WithHint("record one with: rota verdict add")
		}
		cfg := config.Load(rotatree.Config(t.root))
		s := verdict.Settings{
			QAGate: config.String(cfg, "qa.gate"),
			Runner: config.String(cfg, "ship.secondOpinionRunner"),
		}
		next := verdict.Route(*consumer, r.Verdict, s)
		d := jsonx.NewObject()
		d.Set("for", *consumer)
		d.Set("kind", r.Kind)
		d.Set("verdict", r.Verdict)
		d.Set("sha", r.Sha)
		d.Set("stale", r.Sha != t.head)
		d.Set("advisory", verdict.Advisory(*consumer, s))
		d.Set("next", next)
		return Result{Data: d, Text: next}, nil
	}
}

// ---- debug verdict

func debugVerdict(fs *flag.FlagSet) RunFunc {
	v := fs.String("verdict", "", "PASS when the fix held, FAIL when it did not")
	body := bodyFlag(fs)
	return func(c *Ctx, args []string) (Result, error) {
		bug, err := oneArg(args, "bugId")
		if err != nil {
			return Result{}, err
		}
		if bug = strings.TrimSpace(bug); bug == "" {
			return Result{}, Usage("bugId must not be empty")
		}
		if err := checkVerdict(verdict.DebugFix, *v); err != nil {
			return Result{}, err
		}
		b, err := verdictBody(c, *body)
		if err != nil {
			return Result{}, err
		}
		if err := sameVerdict(b, *v); err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		ctr, err := openCounter(c) // exit 5 outside git, as the counter
		if err != nil {
			return Result{}, err
		}
		out, err := reviewGit(c.Context(), root, "rev-parse", "--short", "HEAD")
		if err != nil {
			return Result{}, err
		}
		r := verdict.NewRecord(verdict.DebugFix, *v, strings.TrimSpace(out), b)
		failed, err := verdict.AddItem(root, bug, r)
		if err != nil {
			return Result{}, err
		}
		attempt, err := closeCounterAttempt(ctr, *v)
		if err != nil {
			return Result{}, err
		}
		next := verdict.DebugNext(*v, failed)
		d := jsonx.NewObject()
		d.Set("bugId", bug)
		o := r.Object()
		for _, k := range o.Keys() {
			val, _ := o.Get(k)
			d.Set(k, val)
		}
		d.Set("failedFixes", failed)
		d.Set("next", next)
		if attempt > 0 {
			d.Set("attempt", attempt)
		}
		d.Set("changed", true)
		return Result{Data: d, Text: next + " (" + strconv.Itoa(failed) + " failed)"}, nil
	}
}

// closeCounterAttempt ends the branch session's pending attempt, so
// `debug counter summary` still lists outcomes. No session file or no
// pending attempt is not an error: it returns 0.
func closeCounterAttempt(ctr *debugctr.Counter, v string) (int, error) {
	st, err := ctr.State()
	if err != nil {
		return 0, nil // no session file
	}
	attempts, _ := st.Get("attempts")
	list, _ := attempts.([]any)
	if len(list) == 0 {
		return 0, nil
	}
	if last, ok := list[len(list)-1].(*jsonx.Object); ok {
		if o, _ := last.Get("outcome"); o != "pending" {
			return 0, nil
		}
	}
	if v == verdict.Pass {
		n, err := ctr.Pass()
		return n, ignoreRefused(err)
	}
	if _, err := ctr.Fail(); err != nil {
		return 0, ignoreRefused(err)
	}
	return len(list), nil
}

// ignoreRefused drops a counter refusal: another call closed the attempt
// between the read and the write.
func ignoreRefused(err error) error {
	var ae *exitcode.Error
	if errors.As(err, &ae) && ae.Exit == exitcode.ExitRefused {
		return nil
	}
	return err
}

// ---- debug reset

func debugReset(fs *flag.FlagSet) RunFunc {
	reason := fs.String("reason", "", "why the count starts again")
	confirm := confirmFlags(fs)
	return func(c *Ctx, args []string) (Result, error) {
		bug, err := oneArg(args, "bugId")
		if err != nil {
			return Result{}, err
		}
		conf, err := confirm()
		if err != nil {
			return Result{}, err
		}
		if bug = strings.TrimSpace(bug); bug == "" {
			return Result{}, Usage("bugId must not be empty")
		}
		if strings.TrimSpace(*reason) == "" {
			return Result{}, Usage("--reason is required")
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		failed := verdict.FailedFixes(verdict.Load(root).Items[bug])
		if failed == 0 {
			d := jsonx.NewObject()
			d.Set("bugId", bug)
			d.Set("failedFixes", 0)
			d.Set("cleared", 0)
			d.Set("changed", false)
			return Result{Data: d, Text: "nothing to reset"}, nil
		}
		if res, err := clearGate(c, gate.DebugReset, bug, conf, nil, nil); err != nil {
			return res, err
		}
		sha := ""
		if out, err := reviewGit(c.Context(), root, "rev-parse", "--short", "HEAD"); err == nil {
			sha = strings.TrimSpace(out)
		}
		r := verdict.NewRecord(verdict.DebugReset, verdict.Reset, sha, verdict.Body{Summary: strings.TrimSpace(*reason)})
		cleared, err := verdict.ResetItem(root, bug, r)
		if err != nil {
			return Result{}, err
		}
		d := jsonx.NewObject()
		d.Set("bugId", bug)
		d.Set("failedFixes", 0)
		d.Set("cleared", cleared)
		d.Set("changed", cleared > 0)
		return Result{Data: d, Text: "reset " + bug + " (" + strconv.Itoa(cleared) + " cleared)"}, nil
	}
}
