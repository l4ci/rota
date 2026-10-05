package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/worker"
)

// The A7 verbs (worker pool, reset, account, ...) live in internal/worker;
// this file is their glue. Tests swap Deps.WorkerEnv and Deps.WorkerAccounts for fakes.

// workerContext is cancelled on SIGINT and SIGTERM, so a Ctrl-C reaches the
// git and host calls a verb has in flight instead of leaving them running.
func workerContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// workerEnvCtx is the worker Env bound to ctx.
func workerEnvCtx(c *Ctx, ctx context.Context) worker.Env {
	e := c.deps().workerEnv()
	e.Ctx = ctx
	return e
}

func workerCommands() *Command {
	return &Command{Name: "worker", Summary: "/rota-work worker slots, hosts and accounts", Subs: []*Command{
		{Name: "pool", Summary: "slot registry, worktrees and branches", Subs: []*Command{
			{Name: "init", Summary: "create the slots' worktrees and register them", Verb: poolInit},
			{Name: "list", Summary: "list the registered slots", Verb: noFlags(runPoolList)},
			{Name: "reap", Summary: "remove slots, their worktrees and branches", Verb: poolReap},
		}},
		{Name: "dispatch", Summary: "send a brief into a slot's session", Verb: workerDispatch},
		{Name: "prompt-check", Summary: "Codex UserPromptSubmit hook: pass only signed or maintainer input", Verb: workerPromptCheck},
		{Name: "poll", Summary: "classify slot states from their panes", Verb: workerPoll},
		{Name: "session", Summary: "attachable host session guarantee", Subs: []*Command{
			{Name: "check", Summary: "inside a managed host session? (exit 1 when outside)", Verb: sessionCheck},
			{Name: "ensure", Summary: "hand the orchestrator off into a host session", Verb: sessionEnsure},
		}},
		{Name: "gate", Summary: "merge gate for one slot's branch or PR", Verb: workerGate},
		{Name: "train", Summary: "verify several PRs merged together once, then land them", Verb: workerTrain},
		{Name: "reset", Summary: "refuse a slot that holds work, else cut a fresh task branch", Verb: workerReset},
		{Name: "account", Summary: "per-account usage headroom and slot assignment", Subs: []*Command{
			{Name: "list", Summary: "list accounts with their usage verdict", Verb: noFlags(runAccountList)},
			{Name: "pick", Summary: "name the account with the most headroom", Verb: accountPick},
			{Name: "assign", Summary: "put an account's config dir on a slot", Verb: accountAssign},
		}},
	}}
}

func slotList(slots []*jsonx.Object) []any {
	out := make([]any, 0, len(slots))
	for _, s := range slots {
		out = append(out, s)
	}
	return out
}

func slotLine(o *jsonx.Object) string {
	s := worker.AsSlot(o)
	return strings.Join([]string{s.Name(), s.State(), s.Branch(), s.Worktree()}, "\t")
}

func poolInit(fs *flag.FlagSet) RunFunc {
	slots := fs.String("slots", "", "number of slots to create")
	base := fs.String("base", "", "base branch (default: the current branch)")
	session := fs.String("session", "", "host session name (default: rota)")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		n, err := strconv.Atoi(*slots)
		if *slots == "" || err != nil || n < 1 || strings.TrimLeft(*slots, "0123456789") != "" {
			return Result{}, Usage("--slots must be a positive integer")
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		ctx, stop := workerContext()
		defer stop()
		res, err := workerEnvCtx(c, ctx).PoolInit(ctx, root,
			worker.InitOpts{Slots: n, Base: *base, Session: *session}, c.deps().WorkerAccounts())
		if err != nil {
			return Result{}, err
		}
		for _, w := range res.Warnings {
			c.Warn("%s", w)
		}
		d := jsonx.NewObject()
		d.Set("session", res.Session)
		d.Set("base", res.Base)
		d.Set("slots", slotList(res.Slots))
		d.Set("changed", res.Changed)
		var lines []string
		for _, s := range res.Slots {
			lines = append(lines, slotLine(s))
		}
		return Result{Data: d, Text: strings.Join(lines, "\n")}, nil
	}
}

func runPoolList(c *Ctx, args []string) (Result, error) {
	if err := noArgs(args); err != nil {
		return Result{}, err
	}
	root, err := c.Root()
	if err != nil {
		return Result{}, err
	}
	session, round, slots := worker.PoolList(root)
	d := jsonx.NewObject()
	if session != nil {
		d.Set("session", session)
	}
	if round != nil {
		d.Set("round", round)
	}
	d.Set("slots", slotList(slots))
	var lines []string
	for _, s := range slots {
		lines = append(lines, slotLine(s))
	}
	return Result{Data: d, Text: strings.Join(lines, "\n")}, nil
}

func poolReap(fs *flag.FlagSet) RunFunc {
	all := fs.Bool("all", false, "reap every slot")
	return func(c *Ctx, args []string) (Result, error) {
		if (len(args) == 0) == !*all {
			return Result{}, Usage("reap needs slot names or --all, not both")
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		ctx, stop := workerContext()
		defer stop()
		reaped, err := workerEnvCtx(c, ctx).Reap(root, args, *all)
		if err != nil {
			return Result{}, err
		}
		d := jsonx.NewObject()
		list := make([]any, 0, len(reaped))
		for _, r := range reaped {
			list = append(list, r)
		}
		d.Set("reaped", list)
		d.Set("changed", len(reaped) > 0)
		return Result{Data: d, Text: strings.Join(reaped, "\n")}, nil
	}
}

func resetData(r worker.ResetResult) *jsonx.Object {
	d := jsonx.NewObject()
	d.Set("slot", r.Slot)
	d.Set("clean", r.Clean)
	d.Set("retained", r.Retained)
	if r.Branch != "" {
		d.Set("branch", r.Branch)
	}
	if r.Base != "" {
		d.Set("base", r.Base)
	}
	if r.SHA != "" {
		d.Set("sha", r.SHA)
	}
	if len(r.Dirty) > 0 {
		d.Set("dirty", strList(r.Dirty))
	}
	if len(r.Unmerged) > 0 {
		d.Set("unmerged", strList(r.Unmerged))
	}
	d.Set("changed", r.Changed)
	return d
}

func strList(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

func workerReset(fs *flag.FlagSet) RunFunc {
	task := fs.String("task", "", "task id; names the per-task branch")
	check := fs.Bool("check-only", false, "stop after the guard: exit 0 only when the slot holds no work")
	return func(c *Ctx, args []string) (Result, error) {
		slot, err := oneArg(args, "slot")
		if err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		ctx, stop := workerContext()
		defer stop()
		r, err := workerEnvCtx(c, ctx).Reset(root, slot, *task, *check)
		res := Result{Data: resetData(r)}
		if err != nil {
			var we *exitcode.Error
			if errors.As(err, &we) && we.Data != nil {
				if we.Exit == ExitRefused { // exit 4 failure data names what blocked it
					res.Data.(*jsonx.Object).Set("blockedBy", "slot holds work")
				}
				return res, err
			}
			return Result{}, err
		}
		switch {
		case r.Retained:
			res.Text = fmt.Sprintf("retry: %s keeps its work on %s (same task %s)", slot, r.Branch, *task)
		case r.Changed:
			res.Text = fmt.Sprintf("reset: %s on %s at %s", slot, r.Branch, r.SHA)
		default:
			res.Text = fmt.Sprintf("%s holds no work", slot)
		}
		return res, nil
	}
}

func accountObject(m worker.Meter) *jsonx.Object {
	o := jsonx.NewObject()
	o.Set("name", m.Name)
	o.Set("configDir", m.ConfigDir)
	o.Set("verdict", m.Verdict)
	o.Set("reason", m.Reason)
	for _, f := range []struct {
		key string
		v   *float64
	}{{"fiveHour", m.FiveHour}, {"sevenDay", m.SevenDay}} {
		if f.v != nil {
			o.Set(f.key, *f.v)
		}
	}
	if m.ResetsAt != nil {
		o.Set("resetsAt", worker.ISOFormat(*m.ResetsAt))
	}
	if m.Headroom != nil {
		o.Set("headroom", *m.Headroom)
	}
	return o
}

func runAccountList(c *Ctx, args []string) (Result, error) {
	if err := noArgs(args); err != nil {
		return Result{}, err
	}
	root, err := c.Root()
	if err != nil {
		return Result{}, err
	}
	ctx, stop := workerContext()
	defer stop()
	rows := c.deps().WorkerAccounts().Meters(ctx, root)
	list := make([]any, 0, len(rows))
	var lines []string
	for _, m := range rows {
		o := accountObject(m)
		list = append(list, o)
		pct := func(v *float64) string {
			if v == nil {
				return "-"
			}
			return fmt.Sprintf("%.0f%%", *v)
		}
		note := m.Reason
		if note == "" && m.ResetsAt != nil {
			note = worker.ISOFormat(*m.ResetsAt)
		}
		lines = append(lines, fmt.Sprintf("%-12s %-9s 5h=%-5s 7d=%-5s %s", m.Name, m.Verdict, pct(m.FiveHour), pct(m.SevenDay), note))
	}
	if len(rows) == 0 {
		lines = []string{"no accounts configured (slots inherit the ambient CLAUDE_CONFIG_DIR)"}
	}
	d := jsonx.NewObject()
	d.Set("accounts", list)
	return Result{Data: d, Text: strings.Join(lines, "\n")}, nil
}

func accountPick(fs *flag.FlagSet) RunFunc {
	exclude := fs.String("exclude", "", "comma-separated account names to skip")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		var skip []string
		for _, n := range strings.Split(*exclude, ",") {
			if strings.TrimSpace(n) != "" {
				skip = append(skip, n)
			}
		}
		if err := worker.SoloRefusal(root, "every solo subagent runs on the orchestrator's own account"); err != nil {
			return Result{}, err
		}
		ctx, stop := workerContext()
		defer stop()
		name, found := c.deps().WorkerAccounts().Pick(ctx, root, skip)
		d := jsonx.NewObject()
		d.Set("found", found)
		if !found {
			return Result{Data: d}, Failed("every configured account is cooling down, or none is configured")
		}
		d.Set("account", name)
		return Result{Data: d, Text: name}, nil
	}
}

func accountAssign(fs *flag.FlagSet) RunFunc {
	account := fs.String("account", "", "account name (default: the best pick)")
	return func(c *Ctx, args []string) (Result, error) {
		slot, err := oneArg(args, "slot")
		if err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		if err := worker.SoloRefusal(root, "every solo subagent runs on the orchestrator's own account"); err != nil {
			return Result{}, err
		}
		ctx, stop := workerContext()
		defer stop()
		name, changed, err := c.deps().WorkerAccounts().Assign(ctx, root, slot, *account)
		if err != nil {
			return Result{}, err
		}
		d := jsonx.NewObject()
		d.Set("slot", slot)
		d.Set("account", name)
		d.Set("changed", changed)
		return Result{Data: d, Text: fmt.Sprintf("assigned: %s -> %s", slot, name)}, nil
	}
}

// bodyPath resolves a --body-file value: "-" spools stdin to a temp file.
func bodyPath(c *Ctx, path string) (string, func(), error) {
	if path != "-" {
		return path, func() {}, nil
	}
	f, err := os.CreateTemp("", "rota-body-*")
	if err != nil {
		return "", nil, err
	}
	if _, err := io.Copy(f, c.Stdin); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", nil, err
	}
	f.Close()
	return f.Name(), func() { os.Remove(f.Name()) }, nil
}

func workerDispatch(fs *flag.FlagSet) RunFunc {
	body := fs.String("body-file", "", "brief to send; - reads stdin")
	task := fs.String("task", "", "task id: recreates the slot's session and cuts its branch")
	relay := fs.Bool("relay", false, "inject into the running session as an orchestrator relay")
	round := fs.String("round", "", "orchestrator round for the signature")
	boot := fs.Int("boot-timeout", 60, "seconds to wait for the session to boot")
	kind := fs.String("kind", "", "harness kind: claude or codex (default the slot's, else claude)")
	acceptCodex := fs.Bool("accept-codex-version", false, "let this call through a Codex CLI outside the supported range")
	return func(c *Ctx, args []string) (Result, error) {
		slot, err := oneArg(args, "slot")
		if err != nil {
			return Result{}, err
		}
		if *body == "" {
			return Result{}, Usage("--body-file is required")
		}
		if *kind != "" && !harness.Valid(*kind) {
			return Result{}, Usage("--kind must be one of %s", strings.Join(harness.Kinds, ", "))
		}
		opts := worker.DispatchOpts{Slot: slot, Task: *task, Relay: *relay, BootTimeout: *boot,
			Kind: *kind, AcceptCodexVersion: *acceptCodex}
		if *round != "" {
			n, err := strconv.Atoi(*round)
			if err != nil || n < 0 || strings.TrimLeft(*round, "0123456789") != "" {
				return Result{}, Usage("--round must be a number")
			}
			opts.Round = &n
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		path, cleanup, err := bodyPath(c, *body)
		if err != nil {
			return Result{}, err
		}
		defer cleanup()
		opts.BodyFile = path
		ctx, stop := workerContext()
		defer stop()
		res, err := workerEnvCtx(c, ctx).Dispatch(ctx, root, opts)
		for _, w := range res.Warnings {
			c.Warn("%s", w)
		}
		if err != nil {
			var we *exitcode.Error
			if errors.As(err, &we) {
				if bd, ok := we.Data.(worker.BlockData); ok && we.Exit == ExitRefused {
					return Result{Data: knObj("blockedBy", bd.BlockedBy, "changed", bd.Changed)}, err
				}
			}
			return Result{}, err
		}
		d := jsonx.NewObject()
		d.Set("slot", res.Slot)
		d.Set("handle", res.Handle)
		if res.Task != "" {
			d.Set("task", res.Task)
		}
		if res.Round != nil {
			d.Set("round", *res.Round)
		}
		d.Set("relay", res.Relay)
		setIf(d, "kind", res.Kind)
		d.Set("changed", true)
		return Result{Data: d, Text: fmt.Sprintf("dispatched: %s (%s)", res.Slot, res.Handle)}, nil
	}
}

// workerPromptCheck is the Codex UserPromptSubmit hook of a codex worker (#3).
// It speaks Codex's hook protocol, not rota's envelope: a pass is exit 0 with
// nothing written, a block is exit 2 with the reason on stderr. It fails
// closed: every error, a --json flag included, is exit 2.
func workerPromptCheck(fs *flag.FlagSet) RunFunc {
	keyPath := fs.String("key", "", "file holding the slot's prompt key")
	return func(c *Ctx, args []string) (Result, error) {
		asked := c.JSON
		c.JSON = false // stdout belongs to Codex: no envelope, not even for an error
		if asked {
			return Result{}, Usage("blocked: prompt-check does not take --json: stdout belongs to Codex")
		}
		if len(args) > 0 {
			return Result{}, Usage("blocked: prompt-check takes no arguments")
		}
		if *keyPath == "" {
			return Result{}, Usage("blocked: --key is required")
		}
		key, err := harness.LoadPromptKey(*keyPath)
		if err != nil {
			return Result{}, Usage("blocked: cannot read the prompt key: %v", err)
		}
		var in struct {
			Prompt *string `json:"prompt"`
		}
		if err := json.Unmarshal(readInput(c), &in); err != nil || in.Prompt == nil {
			return Result{}, Usage("blocked: the hook input is not JSON with a prompt field")
		}
		if ok, why := harness.CheckPrompt(key, *in.Prompt); !ok {
			return Result{}, Usage("%s", strings.TrimPrefix(why, "rota: "))
		}
		return Result{}, nil
	}
}

func workerPoll(fs *flag.FlagSet) RunFunc {
	settle := fs.Float64("settle", 3, "seconds between the two pane captures")
	lines := fs.Int("lines", 60, "pane lines to classify")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) > 1 {
			return Result{}, Usage("expected at most one slot, got %d arguments", len(args))
		}
		slot := ""
		if len(args) == 1 {
			slot = args[0]
		}
		var res worker.PollResult
		var err error
		// ROTA_TEST_POLL_FIXTURE classifies a file as a static pane: no host, no
		// registry writes. Test hooks, not part of the CLI.
		if fx := os.Getenv("ROTA_TEST_POLL_FIXTURE"); fx != "" {
			res, err = worker.PollFixture(fx, slot, os.Getenv("ROTA_TEST_POLL_STATUS"), *lines)
		} else {
			var root string
			if root, err = c.Root(); err != nil {
				return Result{}, err
			}
			ctx, stop := workerContext()
			defer stop()
			res, err = workerEnvCtx(c, ctx).Poll(ctx, root, worker.PollOpts{
				Slot: slot, Settle: time.Duration(*settle * float64(time.Second)), Lines: *lines})
		}
		if err != nil {
			return Result{}, err
		}
		rows := make([]any, 0, len(res.Slots))
		var text []string
		for _, r := range res.Slots {
			o := jsonx.NewObject()
			o.Set("name", r.Name)
			o.Set("state", strings.ToLower(r.State))
			o.Set("evidence", r.Evidence)
			line := fmt.Sprintf("%s\t%s\t%s", r.Name, strings.ToLower(r.State), r.Evidence)
			if n := res.Notes[r.Name]; n != "" {
				o.Set("note", n)
				line += "\t(" + n + ")"
			}
			rows = append(rows, o)
			text = append(text, line)
		}
		d := jsonx.NewObject()
		d.Set("slots", rows)
		d.Set("changed", res.Changed)
		return Result{Data: d, Text: strings.Join(text, "\n")}, nil
	}
}

func sessionData(st worker.SessionState) *jsonx.Object {
	d := jsonx.NewObject()
	d.Set("inside", st.Inside)
	if st.Where != "" {
		d.Set("where", st.Where)
	}
	return d
}

func sessionCheck(fs *flag.FlagSet) RunFunc {
	fs.String("session", "", "tmux session name (unused by check)")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		if err := worker.SoloRefusal(root, "a solo round has no host session; there is nothing to check"); err != nil {
			return Result{}, err
		}
		ctx, stop := workerContext()
		defer stop()
		st := workerEnvCtx(c, ctx).SessionCheck(ctx, root)
		if !st.Inside {
			return Result{Data: sessionData(st), Text: "outside"}, Failed("not inside a managed host session")
		}
		return Result{Data: sessionData(st), Text: "inside " + st.Where}, nil
	}
}

func sessionEnsure(fs *flag.FlagSet) RunFunc {
	session := fs.String("session", "", "tmux session name (default: rota)")
	body := fs.String("body-file", "", "instruction pasted into the operator window; - reads stdin")
	boot := fs.Int("boot-timeout", 60, "seconds to wait for the operator session to boot")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		opts := worker.SessionOpts{Session: *session, BootTimeout: *boot}
		if *body != "" {
			path, cleanup, err := bodyPath(c, *body)
			if err != nil {
				return Result{}, err
			}
			defer cleanup()
			opts.BodyFile = path
		}
		ctx, stop := workerContext()
		defer stop()
		st, err := workerEnvCtx(c, ctx).SessionEnsure(ctx, root, opts)
		if err != nil {
			var we *exitcode.Error
			if errors.As(err, &we) {
				if bd, ok := we.Data.(worker.BlockData); ok && we.Exit == ExitRefused {
					return Result{Data: knObj("blockedBy", bd.BlockedBy, "changed", bd.Changed)}, err
				}
			}
			return Result{}, err
		}
		d := sessionData(st)
		d.Set("handedOff", st.HandedOff)
		if st.HandedOff {
			d.Set("session", st.Session)
		}
		d.Set("changed", st.HandedOff)
		if !st.HandedOff {
			return Result{Data: d, Text: "inside " + st.Where + " — no handoff needed"}, nil
		}
		return Result{Data: d, Text: fmt.Sprintf("handed off to tmux session '%s'\n\n  Attach:   tmux attach -t %s\n  Windows:  operator  — the cycle continues here\n            w1..wN    — workers (created on dispatch)\n\nThis terminal is no longer the orchestrator and can be closed.\nAnswer a worker's question in that worker's window, or in 'operator'.", st.Session, st.Session)}, nil
	}
}

func gateData(r worker.GateResult) *jsonx.Object {
	d := jsonx.NewObject()
	d.Set("slot", r.Slot)
	d.Set("verdict", r.Verdict)
	d.Set("base", r.Base)
	if r.Branch != "" {
		d.Set("branch", r.Branch)
	}
	if r.SHA != "" {
		d.Set("sha", r.SHA)
	}
	if r.PR != "" {
		d.Set("pr", r.PR)
	}
	d.Set("verified", strList(r.Verified))
	d.Set("verifySkipped", r.VerifySkipped)
	d.Set("changed", r.Changed)
	return d
}

func workerGate(fs *flag.FlagSet) RunFunc {
	base := fs.String("base", "", "the cycle branch the slot merges into")
	check := fs.Bool("check-only", false, "judge freshness, PR identity and provenance; merge nothing")
	noVerify := fs.Bool("no-verify", false, "merge without running refactor.verifyCommands")
	confirm := approvalFlags(fs)
	return func(c *Ctx, args []string) (Result, error) {
		slot, err := oneArg(args, "slot")
		if err != nil {
			return Result{}, err
		}
		if *base == "" {
			return Result{}, Usage("--base is required")
		}
		conf, req, err := confirm()
		if err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		policy, err := mergePolicy(c)
		if err != nil {
			return Result{}, err
		}
		var gateRes Result
		var gateErr error
		approve := func(files func() ([]string, error)) error {
			req.Thread = func() (approvalThread, error) { return slotApprovalThread(root, slot) }
			gateRes, gateErr = clearMerge(c, policy, slot+" into "+*base, conf, req, files, nil)
			return gateErr
		}
		ctx, stop := workerContext()
		defer stop()
		issue := gateIssue(root, slot)
		r, err := workerEnvCtx(c, ctx).Gate(ctx, root, worker.GateOpts{Slot: slot, Base: *base, CheckOnly: *check, NoVerify: *noVerify, Approve: approve})
		if gateErr != nil {
			var e *Error
			if !errors.As(gateErr, &e) {
				return Result{}, Unavailable("%v", gateErr) // listing the merge's files failed
			}
			d := gateData(r)
			if g, ok := gateRes.Data.(*jsonx.Object); ok {
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
			return Result{Data: d}, gateErr
		}
		if err != nil {
			return Result{}, err
		}
		for _, n := range r.Notes {
			fmt.Fprintln(c.Stderr, n)
		}
		res := Result{Data: gateData(r)}
		if !*check {
			n, parked, berr := gateBounce(c, root, issue, r)
			if berr != nil {
				fmt.Fprintln(c.Stderr, "BOUNCE-COUNT "+slot+" — "+berr.Error())
			}
			if n > 0 {
				d := res.Data.(*jsonx.Object)
				d.Set("bounces", n)
				d.Set("parked", parked)
			}
			if parked {
				r.Hint = fmt.Sprintf("parked: %s bounced %d times, labelled needs-human; do not re-dispatch it until a human clears the label", issue, n)
			}
		}
		if r.OK() {
			if r.Verdict == worker.GateFresh {
				res.Text = fmt.Sprintf("fresh: %s %s", slot, r.Branch)
			} else {
				res.Text = fmt.Sprintf("pass: %s %s -> %s (%s)", slot, r.Branch, r.Base, r.SHA)
			}
			return res, nil
		}
		e := Failed("%s", r.Err)
		e.Hint = r.Hint
		return res, e
	}
}

// slotApprovalThread is the approval thread of a worker gate (C5): the slot's
// recorded PR, else the slot's issue. The argument may also be a PR ref, which
// resolves to a queued PR record. Neither is exit 2.
func slotApprovalThread(root, slot string) (approvalThread, error) {
	t, err := worker.LoadRegistry(root).GateTarget(slot)
	if err != nil {
		var we *exitcode.Error
		if errors.As(err, &we) && we.Exit == exitcode.ExitResolution {
			return approvalThread{}, Resolution("%s", we.Message)
		}
		return approvalThread{}, err
	}
	// The arg may have been a PR ref. A queued record's slot has moved on, so
	// its thread names none.
	slot = t.Name
	branch := t.Branch
	if n, ok := round.PRNumber(t.PR); ok {
		return approvalThread{Kind: "pr", Number: n, Slot: slot, Title: fmt.Sprintf("Merge approval: PR #%d", n)}, nil
	}
	if n, err := strconv.Atoi(round.SlotIssue(t.Task, branch, slot)); err == nil {
		return approvalThread{Kind: "issue", Number: n, Slot: slot, Title: fmt.Sprintf("Merge approval: %s (%s)", slot, branch)}, nil
	}
	return approvalThread{}, Usage("--approval and --escalate need an approval thread: slot %s has no PR and no issue number", slot)
}
