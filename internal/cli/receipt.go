package cli

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/receipt"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/worker"
)

// receipted gives a state-writing verb --request-id and a receipt per call
// (#822); receiptedVerbs below is the one list of which verbs get it, and the
// protocol itself (claim, replay, record) is receipt.Store.Do. The same id with the same arguments replays the first answer
// and applies nothing; the same id with other arguments is refused; an id whose
// first call stopped part way runs again, and the saga's own skip checks resume
// it from the stage that failed. Without --request-id the verb mints one and
// puts it in the result, so the caller has an id to retry with.
//
// A call that wrote nothing (usage, resolution and refusal, exits 2 to 4, or a
// --check-only run) leaves no receipt: a retry must judge it afresh.
func receipted(verb string, inner func(*flag.FlagSet) RunFunc, inputs ...inputsFunc) func(*flag.FlagSet) RunFunc {
	return func(fs *flag.FlagSet) RunFunc {
		rid := fs.String("request-id", "", "idempotency key: a retry with the same id returns the first result and applies nothing (default: minted and printed)")
		run := inner(fs)
		return func(c *Ctx, args []string) (Result, error) {
			if checkOnly(fs) {
				return run(c, args)
			}
			root, err := c.Root()
			if err != nil {
				return run(c, args) // the verb reports the missing project itself
			}
			id, minted := *rid, *rid == ""
			if minted {
				id = mintRequestID()
			}
			fp := fingerprint(c, fs, args)
			for _, in := range inputs {
				fp += "\x00" + strings.Join(in(c, root, args), "\x00")
			}
			call := receipt.Call{ID: id, Verb: verb, Fingerprint: fp}
			var res Result
			var runErr error
			var ae *Error
			out, err := receipt.New(root).Do(call, func(prior receipt.Entry) (receipt.Entry, bool) {
				res, runErr = run(c, args)
				e := receipt.Entry{Status: receipt.Done}
				if runErr != nil {
					ae = asError(runErr)
					if ae.Exit != ExitFailed && ae.Exit < ExitUnavailable {
						return e, false // wrote nothing: no receipt
					}
					e.Status, e.Exit, e.Message = receipt.Failed, ae.Exit, ae.Message
					var se *round.StageError
					if errors.As(runErr, &se) {
						e.FailedStage, e.Residual = se.Stage, se.Residual
					}
				}
				if res.Data == nil && ae != nil {
					res.Data = jsonx.NewObject()
				}
				stamp(res.Data, id, prior.FailedStage, e.FailedStage, e.Residual)
				e.Text = res.Text
				if b, merr := jsonx.MarshalCompact(res.Data); res.Data != nil && merr == nil {
					e.Data = b
				}
				return e, true
			})
			var mm *receipt.Mismatch
			switch {
			case errors.As(err, &mm):
				d := jsonx.NewObject()
				d.Set("blockedBy", "request-id")
				d.Set("requestId", id)
				d.Set("changed", false)
				return Result{Data: d}, Refused("%s", err)
			case err != nil:
				return Result{}, err
			case out.Replayed:
				return replay(c, out.Entry)
			case runErr != nil && ae.Exit != ExitFailed && ae.Exit < ExitUnavailable:
				return res, runErr
			}
			if out.RecordErr != nil {
				c.Warn("receipt for %s not recorded: %v", id, out.RecordErr)
				return res, runErr
			}
			if ae != nil {
				cp := *ae
				cp.Hint = strings.TrimPrefix(cp.Hint+"; "+retryHint(id, out.Entry), "; ")
				return res, &cp
			}
			return res, runErr
		}
	}
}

// retryHint tells the caller what stopped and how to retry; an exit-5 failure
// carries no data envelope, so the hint is where the stage and residue show.
func retryHint(id string, e receipt.Entry) string {
	h := "retry with --request-id " + id
	if e.FailedStage != "" {
		h = "failed at stage " + e.FailedStage + "; " + h
		if len(e.Residual) > 0 {
			h = "left behind: " + strings.Join(e.Residual, ", ") + "; " + h
		}
	}
	return h
}

// stamp writes the request id into a result object, and for a retry the stage
// it resumed from or, for a failure, the stage that failed and what it left.
func stamp(data any, id, resumedFrom, failedStage string, residual []string) {
	set := func(k string, v any) {
		switch d := data.(type) {
		case *jsonx.Object:
			d.Set(k, v)
		case map[string]any:
			d[k] = v
		}
	}
	set("requestId", id)
	if resumedFrom != "" {
		set("resumedFrom", resumedFrom)
	}
	if failedStage != "" {
		set("failedStage", failedStage)
		set("residualResources", append([]string{}, residual...))
	}
}

// replay answers a retried call from its stored receipt.
func replay(c *Ctx, e receipt.Entry) (Result, error) {
	fmt.Fprintf(c.Stderr, "%s: request %s already applied; returning the first result\n", c.Path, e.ID)
	var data any
	if len(e.Data) > 0 {
		v, err := jsonx.Decode(e.Data)
		if err != nil {
			return Result{}, fmt.Errorf("receipt %s: %w", e.ID, err)
		}
		data = v
	}
	if o, ok := data.(*jsonx.Object); ok {
		if _, has := o.Get("changed"); has {
			o.Set("changed", false)
		}
		o.Set("replayed", true)
	}
	return Result{Data: data, Text: e.Text}, nil
}

func checkOnly(fs *flag.FlagSet) bool {
	f := fs.Lookup("check-only")
	return f != nil && f.Value.String() == "true"
}

// inputsFunc names what a verb judges beyond its arguments (the heads of the
// branches a gate looks at), so a changed input is a different call.
type inputsFunc func(c *Ctx, root string, args []string) []string

// fingerprint is the call's identity: the repo, the positional arguments and
// every flag the caller set, except the request id itself. A *-file flag counts
// by its content, not its path: stdin ("-") is read once here and handed back to
// the verb, so "the same arguments" means the same inputs.
func fingerprint(c *Ctx, fs *flag.FlagSet, args []string) string {
	var parts []string
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "request-id" {
			return
		}
		v := f.Value.String()
		if strings.HasSuffix(f.Name, "-file") && v != "" {
			var b []byte
			var err error
			if v == "-" {
				if c.Stdin != nil {
					b, err = io.ReadAll(c.Stdin)
					c.Stdin = bytes.NewReader(b)
				}
			} else {
				b, err = os.ReadFile(v)
			}
			if err != nil {
				v += "#unreadable"
			} else {
				sum := sha256.Sum256(b)
				v += "#" + hex.EncodeToString(sum[:])
			}
		}
		parts = append(parts, "--"+f.Name+"="+v)
	})
	sort.Strings(parts)
	return strings.Join(append(append([]string{"repo=" + c.Repo}, parts...), args...), "\x00")
}

func mintRequestID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return "req-" + hex.EncodeToString(b)
}

// receiptedVerbs is the one place that decides which verbs keep a receipt: a
// verb that writes .rota state or the tracker is listed, with the inputsFunc
// naming what it judges beyond its arguments. Tree wraps each listed verb;
// TestReceiptedVerbsExist keeps a rename from dropping coverage silently.
//
// Left out on purpose: round bounce counts a review head once (see
// worker.recordBounce), which is a semantic dedupe a retry id cannot express;
// round escalate check only reads the thread; round status, reconcile
// without --apply and the other readers write nothing.
var receiptedVerbs = []struct {
	verb   string
	inputs []inputsFunc
}{
	{verb: "round assign"},
	{verb: "round transfer"},
	{verb: "round reclaim"},
	{verb: "round pick"},
	{verb: "round return"},
	{verb: "round report"},
	{verb: "round tick"},
	{verb: "round escalate send"},
	{verb: "worker gate", inputs: []inputsFunc{gateHeads}},
	{verb: "worker train", inputs: []inputsFunc{gateHeads}},
}

// applyReceipts wraps every verb in receiptedVerbs. A verb missing from the
// tree is a programming error, so it panics rather than leave a verb bare.
func applyReceipts(root *Command) {
	for _, r := range receiptedVerbs {
		cmd := findVerb(root, strings.Fields(r.verb))
		if cmd == nil || cmd.Verb == nil {
			panic("receiptedVerbs names " + r.verb + ", which is not a verb")
		}
		cmd.Verb = receipted(r.verb, cmd.Verb, r.inputs...)
	}
}

// gateHeads is the inputsFunc of gate and train: the head of the branch each
// argument resolves to, by the gate's own rule (Registry.GateTargetAny), so a
// queued PR, an adopted slot and a bare external PR get a head like a slot does.
// A target with a branch reads local refs, so a fingerprint does not reach the
// forge; a bare external PR knows no branch, so its head comes from the forge.
// A push moves the head, and the same request id is then a different call.
func gateHeads(c *Ctx, root string, args []string) []string {
	reg := worker.LoadRegistryTolerant(root)
	var out []string
	for _, a := range args {
		head := ""
		if t, err := reg.GateTargetAny(a); err == nil {
			head = targetHead(c, root, t)
		}
		if head == "" {
			head = "?"
		}
		out = append(out, "head:"+a+"="+head)
	}
	return out
}

// targetHead is the head of a resolved gate target's branch, "" when none is
// known: no branch and no forge answer (a failed read must not fail the call).
func targetHead(c *Ctx, root string, t worker.GateTarget) string {
	if t.Branch != "" {
		head := ""
		for _, ref := range []string{"refs/heads/", "refs/remotes/origin/"} {
			if r, err := c.deps().Git(c.Context(), root, "rev-parse", "--verify", "-q", ref+t.Branch); err == nil && r.ExitCode == 0 {
				head += strings.TrimSpace(r.Stdout) + ","
			}
		}
		return head
	}
	n, ok := worker.PRRefNumber(t.PR)
	if !t.External || !ok {
		return ""
	}
	fg, err := c.deps().forge(c.Context(), config.Load(rotatree.Config(root)), "", root)
	if err != nil {
		return ""
	}
	info, err := fg.PRView(c.Context(), n)
	if err != nil {
		return ""
	}
	return info.HeadSHA
}
