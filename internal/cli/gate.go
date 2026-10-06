package cli

import (
	"errors"
	"flag"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/escalation"
	"github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/jsonx"
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
	if conf.Given {
		var err error
		if root, err = c.Root(); err != nil {
			return Result{}, err
		}
	}
	verb := strings.TrimPrefix(c.Path, "rota ")
	err := gate.Clear(root, name, verb, target, conf, paths)
	var r *gate.Refused
	switch {
	case errors.As(err, &r):
		d := extra
		if d == nil {
			d = jsonx.NewObject()
		}
		d.Set("blockedBy", "manual gate")
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
	p, err := gate.LoadMergePolicy(root)
	if errors.Is(err, gate.ErrBadMergeMode) {
		return p, Usage("%v", err)
	}
	return p, err
}

// approvalThread is the forge thread a merge's approval request lives on (C5).
type approvalThread struct {
	Kind   string // pr | issue
	Number int
	Slot   string
	Title  string
}

// approvalReq is --approval and --escalate on a merge verb. Thread resolves
// lazily, only once the policy covers the merge; an error from it (exit 2:
// the merge has no thread) stops the verb. The zero value is neither flag.
type approvalReq struct {
	ID       string
	Escalate bool
	Thread   func() (approvalThread, error)
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
func approvalConfirm(c *Ctx, id string, th approvalThread, extra *jsonx.Object) (gate.Confirm, Result, error) {
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
	case e.Kind != th.Kind || e.Number != th.Number:
		return gate.Confirm{}, Result{}, Usage("escalation %s is on %s #%d, not on this merge's approval thread (%s #%d)", id, e.Kind, e.Number, th.Kind, th.Number)
	case e.Status != escalation.StatusAnswered:
		status := e.Derived(approvalNow(c))
		res, err := approvalFail(extra, fmt.Sprintf("escalation %s is %s; no approval yet", id, status),
			"blockedBy", "approval pending", "escalation", id, "status", status)
		return gate.Confirm{}, res, err
	}
	body := ""
	if e.Answer != nil {
		body = e.Answer.Body
	}
	if !escalation.Approves(body) {
		res, err := approvalFail(extra, fmt.Sprintf("escalation %s was answered without approving the merge", id),
			"blockedBy", "approval declined", "escalation", id, "answer", body)
		return gate.Confirm{}, res, err
	}
	return gate.Confirm{Given: true, Note: body, Escalation: id}, Result{}, nil
}

// approvalRequestBody is the escalation body: the policy, then the reply
// words from the allowlist.
func approvalRequestBody(p gate.MergePolicy, hit []string) string {
	var b strings.Builder
	if p.Mode == gate.MergePaths {
		b.WriteString("Merge policy `paths`: these changed files need approval:\n")
		for _, f := range hit {
			b.WriteString("- " + f + "\n")
		}
	} else {
		b.WriteString("Merge policy `all`: every merge needs approval.\n")
	}
	var words []string
	for _, w := range append(append([]string{}, escalation.ApprovalWords...), escalation.ApprovalPhrase) {
		words = append(words, "`"+w+"`")
	}
	last := len(words) - 1
	fmt.Fprintf(&b, "\nReply %s or %s to merge. Any other reply holds the merge.", strings.Join(words[:last], ", "), words[last])
	return b.String()
}

// approvalEscalate sends the approval request on the thread, or reuses the
// pending one, and returns the entry for the refusal data. A failure is a
// warning and nil: the refusal stands.
func approvalEscalate(c *Ctx, p gate.MergePolicy, hit []string, th approvalThread) any {
	root, err := c.Root()
	if err != nil {
		c.Warn("approval request not sent: %v", err)
		return nil
	}
	if e, ok := escalation.PendingOn(escalation.Load(root), th.Kind, th.Number); ok {
		return e.Object()
	}
	env := c.deps().escalationEnv()
	if env.Forge == nil {
		env.Forge = escalationForge(c)
	}
	res, err := escalation.Send(c.Context(), env, root, escalation.SendOpts{
		Number: th.Number, PR: th.Kind == "pr", Slot: th.Slot, Title: th.Title, Body: approvalRequestBody(p, hit),
	})
	for _, w := range res.Warnings {
		c.Warn("%s", w)
	}
	if err != nil {
		c.Warn("approval request not sent: %v", err)
		return nil
	}
	return res.Entry.Object()
}

// clearMerge applies the merge-approval gate: files lists the merge's changed
// paths and runs only when the policy reads them. A merge the policy does not
// cover passes without an audit line, whatever --approval or --escalate say.
// --approval swaps the confirmation for an answered escalation's; --escalate
// asks the human when the gate refuses.
func clearMerge(c *Ctx, p gate.MergePolicy, target string, conf gate.Confirm, req approvalReq, files func() ([]string, error), extra *jsonx.Object) error {
	var changed []string
	if p.NeedsFiles() {
		var err error
		if changed, err = files(); err != nil {
			return err
		}
	}
	covered, hit := p.Covers(changed)
	if !covered {
		return nil
	}
	var th approvalThread
	if req.ID != "" || req.Escalate {
		var err error
		if th, err = req.Thread(); err != nil {
			return err
		}
	}
	if req.ID != "" {
		var res Result
		var err error
		if conf, res, err = approvalConfirm(c, req.ID, th, extra); err != nil {
			return carryData(res, err)
		}
	}
	res, err := clearGate(c, gate.MergeApproval, target, conf, hit, extra)
	if err != nil && req.Escalate {
		if d, ok := res.Data.(*jsonx.Object); ok {
			if e := approvalEscalate(c, p, hit, th); e != nil {
				d.Set("escalation", e)
			}
		}
	}
	return carryData(res, err)
}

// carryData attaches the refusal envelope in res to err, so it travels with
// the error through a domain callback that can only return an error. The
// caller reads it back with refusalData.
func carryData(res Result, err error) error {
	var e *Error
	if err != nil && res.Data != nil && errors.As(err, &e) {
		e.Data = res.Data
	}
	return err
}

// refusalData is the envelope carryData attached to err, or nil.
func refusalData(err error) *jsonx.Object {
	d, _ := exitcode.DataOf[*jsonx.Object](err)
	return d
}

// gateRefusal is the result of a merge whose approval gate refused: d is the
// verb's own data, with the keys of the gate's envelope copied onto it. An
// error that is not an exit-coded refusal means listing the merge's files
// failed.
func gateRefusal(err error, d *jsonx.Object) (Result, error) {
	if !errors.As(err, new(*Error)) {
		return Result{}, Unavailable("%v", err)
	}
	if g := refusalData(err); g != nil {
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
