package cli

import (
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/escalation"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/worker"
)

// gateCommands is the `rota gate` group (B1, #54).
func gateCommands() *Command {
	return &Command{Name: "gate", Summary: "the manual-gate registry", Subs: []*Command{
		{Name: "list", Summary: "list every manual gate and the verbs that enforce it", Verb: noFlags(gateList)},
	}}
}

func gateList(c *Ctx, args []string) (Result, error) {
	if len(args) > 0 {
		return Result{}, Usage("unexpected argument %q", args[0])
	}
	rows := []any{}
	var lines []string
	for _, g := range gate.Registry {
		o := jsonx.NewObject()
		o.Set("name", g.Name)
		o.Set("enforced", g.Enforced())
		o.Set("verbs", strList(g.Verbs))
		o.Set("skills", strList(g.Skills))
		o.Set("creates", g.Creates)
		rows = append(rows, o)
		by := "skill only"
		if g.Enforced() {
			by = "rota " + strings.Join(g.Verbs, ", rota ")
		}
		lines = append(lines, g.Name+": "+by+" ("+g.Creates+")")
	}
	return Result{Data: gitObj("gates", rows), Text: strings.Join(lines, "\n")}, nil
}

// confirmFlags defines --confirm and --confirm-note on a gated verb's flag
// set. The returned func validates the pair after parsing (exit 2 when one
// comes without the other).
func confirmFlags(fs *flag.FlagSet) func() (gate.Confirm, error) {
	given := fs.Bool("confirm", false, "the human approved this step (manual gate)")
	note := fs.String("confirm-note", "", "the human's answer, quoted as given (required with --confirm)")
	return func() (gate.Confirm, error) {
		c := gate.Confirm{Given: *given, Note: *note}
		if err := c.Validate(); err != nil {
			return c, Usage("%v", err)
		}
		return c, nil
	}
}

// clearGate runs the gate check for a gated verb. On a refusal it returns the
// manual-gate failure data and exit 4; with --confirm it audits the approval
// under the project root, so the caller may act. extra adds fields to the
// failure data (worker gate's own shape).
func clearGate(c *Ctx, name, target string, conf gate.Confirm, paths []string, extra *jsonx.Object) (Result, error) {
	root := ""
	var cfg any
	if conf.Given {
		var err error
		if root, err = c.Root(); err != nil {
			return Result{}, err
		}
		cfg = c.deps().Config(root)
	}
	verb := strings.TrimPrefix(c.Path, "rota ")
	err := gate.Clear(root, cfg, c.deps().Now(), name, verb, target, conf, paths)
	var r *gate.Refused
	switch {
	case errors.As(err, &r):
		d := extra
		if d == nil {
			d = jsonx.NewObject()
		}
		d.Set("blockedBy", blockedby.ManualGate)
		d.Set("gate", r.Gate)
		if name == gate.MergeApproval {
			d.Set("paths", strList(r.Paths))
		}
		d.Set("changed", false)
		return Result{Data: d}, Refused("%s", r.Error()).WithHint(r.Hint())
	case err != nil:
		return Result{}, err
	}
	return Result{}, nil
}

// mergePolicy loads ship.mergeApproval for a merge verb; a bad value is exit 2.
// Without a .rota/ root there is no config, so the default `none` applies.
func mergePolicy(c *Ctx) (gate.MergePolicy, error) {
	root, err := c.Root()
	if err != nil {
		return gate.MergePolicy{Mode: gate.MergeNone}, nil
	}
	p, err := gate.LoadMergePolicy(c.deps().Config(root))
	if errors.Is(err, gate.ErrBadMergeMode) {
		return p, Usage("%v", err)
	}
	return p, err
}

// approvalReq is --approval and --escalate on a merge verb. Thread resolves
// lazily, only once the policy covers the merge; an error from it (exit 2:
// the merge has no thread) stops the verb. The zero value is neither flag.
type approvalReq struct {
	ID       string
	Escalate bool
	Thread   func() (worker.ApprovalThread, error)
}

// approvalFlags defines --approval and --escalate next to --confirm and
// --confirm-note. The returned func validates after parsing: the confirmation
// pair as confirmFlags does, and exit 2 when two of --approval, --escalate and
// --confirm come together.
func approvalFlags(fs *flag.FlagSet) func() (gate.Confirm, approvalReq, error) {
	confirm := confirmFlags(fs)
	id := fs.String("approval", "", "id of the answered escalation that approves this merge (C5)")
	esc := fs.Bool("escalate", false, "when the merge-approval gate refuses, ask the human on the approval thread (C5)")
	return func() (gate.Confirm, approvalReq, error) {
		conf, err := confirm()
		if err != nil {
			return conf, approvalReq{}, err
		}
		n := 0
		for _, set := range []bool{*id != "", *esc, conf.Given} {
			if set {
				n++
			}
		}
		if n > 1 {
			return conf, approvalReq{}, Usage("--approval, --escalate and --confirm are mutually exclusive")
		}
		return conf, approvalReq{ID: *id, Escalate: *esc}, nil
	}
}

// approvalNow is the clock for the derived escalation status.
func approvalNow(c *Ctx) time.Time {
	if env := c.deps().escalationEnv(); env.Now != nil {
		return env.Now()
	}
	return c.deps().Now()
}

// approvalFail builds the exit 4 refusal of a --approval check: extra's
// fields first (the caller's own shape), then the C5 fields, changed false.
func approvalFail(extra *jsonx.Object, msg string, fields ...any) (Result, error) {
	d := extra
	if d == nil {
		d = jsonx.NewObject()
	}
	for i := 0; i+1 < len(fields); i += 2 {
		d.Set(fields[i].(string), fields[i+1])
	}
	d.Set("changed", false)
	return Result{Data: d}, Refused("%s", msg)
}

// approvalConfirm turns --approval <id> into the confirmation the gate takes:
// the answered escalation on this merge's thread whose answer the allowlist
// approves. It reads the registry only.
func approvalConfirm(c *Ctx, id string, th worker.ApprovalThread, extra *jsonx.Object) (gate.Confirm, Result, error) {
	root, err := c.Root()
	if err != nil {
		return gate.Confirm{}, Result{}, err
	}
	var e *escalation.Entry
	list := escalation.Load(root)
	for i := range list {
		if list[i].ID == id {
			e = &list[i]
			break
		}
	}
	switch {
	case e == nil:
		return gate.Confirm{}, Result{}, Resolution("no escalation %s", id)
	case e.Kind != th.Kind || e.Ref() != th.Ref():
		return gate.Confirm{}, Result{}, Usage("escalation %s is on %s %s, not on this merge's approval thread (%s %s)", id, e.Kind, e.Ref(), th.Kind, th.Ref())
	case e.Status != escalation.StatusAnswered:
		status := e.Derived(approvalNow(c))
		res, err := approvalFail(extra, fmt.Sprintf("escalation %s is %s; no approval yet", id, status),
			"blockedBy", blockedby.ApprovalPending, "escalation", id, "status", status)
		return gate.Confirm{}, res, err
	}
	body := ""
	if e.Answer != nil {
		body = e.Answer.Body
	}
	if !escalation.Approves(body) {
		res, err := approvalFail(extra, fmt.Sprintf("escalation %s was answered without approving the merge", id),
			"blockedBy", blockedby.ApprovalDeclined, "escalation", id, "answer", body)
		return gate.Confirm{}, res, err
	}
	return gate.Confirm{Given: true, Note: body, Escalation: id}, Result{}, nil
}

// approvalForge is the escalation registry and forge behind a merge's approval
// request: the worker.ApprovalEscalator the cli wires.
type approvalForge struct{ c *Ctx }

func (f approvalForge) Pending(th worker.ApprovalThread) (any, bool, error) {
	root, err := f.c.Root()
	if err != nil {
		return nil, false, err
	}
	e, ok := escalation.PendingOn(escalation.Load(root), th.Kind, th.Ref())
	if !ok {
		return nil, false, nil
	}
	return e.Object(), true, nil
}

func (f approvalForge) Send(th worker.ApprovalThread, body string) (any, []string, error) {
	root, err := f.c.Root()
	if err != nil {
		return nil, nil, err
	}
	env := f.c.deps().escalationEnv()
	if env.Forge == nil {
		env.Forge = escalationForge(f.c)
	}
	res, err := escalation.Send(f.c.Context(), env, root, escalation.SendOpts{
		Number: th.Number, Key: th.Key, PR: th.Kind == "pr", Slot: th.Slot, Title: th.Title, Body: body,
	})
	if err != nil {
		return nil, res.Warnings, err
	}
	return res.Entry.Object(), res.Warnings, nil
}

func (approvalForge) ReplyWords() []string {
	return append(append([]string{}, escalation.ApprovalWords...), escalation.ApprovalPhrase)
}

// approvalEscalation is the worker's escalation step wired to this verb.
func approvalEscalation(c *Ctx, p gate.MergePolicy) *worker.ApprovalEscalation {
	return &worker.ApprovalEscalation{Esc: approvalForge{c}, Policy: p, Warn: c.Warn}
}

// clearMerge applies the merge-approval gate: files lists the merge's changed
// paths and runs only when the policy reads them. A merge the policy does not
// cover passes without an audit line, whatever --approval or --escalate say.
// --approval swaps the confirmation for an answered escalation's; --escalate
// asks the human when the gate refuses. A refusal comes back as a value; the
// error is a step that could not run.
func clearMerge(c *Ctx, p gate.MergePolicy, target string, conf gate.Confirm, req approvalReq, files func() ([]string, error), extra *jsonx.Object) (*exitcode.Refusal, error) {
	var changed []string
	if p.NeedsFiles() {
		var err error
		if changed, err = files(); err != nil {
			return nil, err
		}
	}
	covered, hit := p.Covers(changed)
	if !covered {
		return nil, nil
	}
	var th worker.ApprovalThread
	if req.ID != "" || req.Escalate {
		var err error
		if th, err = req.Thread(); err != nil {
			return nil, err
		}
	}
	if req.ID != "" {
		var res Result
		var err error
		if conf, res, err = approvalConfirm(c, req.ID, th, extra); err != nil {
			return refusalOf(res, err)
		}
	}
	res, err := clearGate(c, gate.MergeApproval, target, conf, hit, extra)
	ref, err := refusalOf(res, err)
	if ref != nil && req.Escalate {
		approvalEscalation(c, p).Attach(ref, hit, th)
	}
	return ref, err
}

// refusalOf is the result of a gate step as a value: an exit-coded err is the
// gate refusing, with res's envelope; any other error is a step that could not
// run and passes through.
func refusalOf(res Result, err error) (*exitcode.Refusal, error) {
	var e *Error
	if err != nil && errors.As(err, &e) {
		return &exitcode.Refusal{Err: e, Data: res.Data}, nil
	}
	return nil, err
}

// refusalEnvelope is the envelope of ref, or nil.
func refusalEnvelope(ref *exitcode.Refusal) *jsonx.Object {
	if ref == nil {
		return nil
	}
	d, _ := ref.Data.(*jsonx.Object)
	return d
}

// gateRefusal is the result of a merge whose approval gate refused: d is the
// verb's own data, with the keys of the gate's envelope copied onto it. With no
// refusal, an error that is not exit-coded means listing the merge's files
// failed.
func gateRefusal(ref *exitcode.Refusal, err error, d *jsonx.Object) (Result, error) {
	if ref != nil {
		err = ref.Err
	} else if !errors.As(err, new(*Error)) {
		return Result{}, Unavailable("%v", err)
	}
	if g := refusalEnvelope(ref); g != nil {
		for _, k := range []string{"blockedBy", "gate", "paths", "changed"} {
			v, _ := g.Get(k)
			d.Set(k, v)
		}
		for _, k := range []string{"escalation", "status", "answer"} { // C5, only when set
			if v, ok := g.Get(k); ok {
				d.Set(k, v)
			}
		}
	}
	return Result{Data: d}, err
}
