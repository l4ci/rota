package cli

import (
	"errors"
	"flag"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"slices"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/debugctr"
	"github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/verdict"
	"github.com/l4ci/rota/internal/worker"
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
	// ref is the revision the caller named (the branch, or <remote>/<branch>
	// when a remote prefix was stripped); dir is the checkout git runs in;
	// remote is the stripped prefix, "" when the argument was a bare branch.
	ref, dir, remote string
}

// splitRemote strips a leading `<remote>/` from a branch argument so
// `origin/feat` and `feat` share one record key. A local branch of that
// exact name wins: `origin/feat` stays whole when refs/heads/origin/feat exists.
func splitRemote(c *Ctx, dir, branch string) (bare, remote string, err error) {
	r := c.deps().Repo(dir)
	local, err := r.Verify(c.Context(), "refs/heads/"+branch)
	if err != nil {
		return "", "", gitErr(err)
	}
	if local {
		return branch, "", nil
	}
	out, err := reviewGit(c.Context(), c.deps(), dir, "remote")
	if err != nil {
		return "", "", err
	}
	for _, name := range strings.Fields(out) {
		if rest, ok := strings.CutPrefix(branch, name+"/"); ok && rest != "" {
			return rest, name, nil
		}
	}
	return branch, "", nil
}

func resolveVerdictBranch(c *Ctx, args []string) (verdictBranch, error) {
	t, err := resolveBranch(c, args)
	if err != nil {
		return verdictBranch{}, err
	}
	cwd, err := c.deps().Getwd()
	if err != nil {
		return verdictBranch{}, err
	}
	root, err := git.FindRoot(cwd, registeredRels)
	if err != nil {
		return verdictBranch{}, Resolution("no .rota/ directory here or in any parent").WithHint("run: rota init")
	}
	bare, remote, err := splitRemote(c, t.Dir, t.Branch)
	if err != nil {
		return verdictBranch{}, err
	}
	v := verdictBranch{root: root, repo: c.Repo, branch: bare, ref: t.Branch, dir: t.Dir, remote: remote}
	if v.repo == "" && repos.Umbrella(root) {
		if r, err := repos.Which(t.Dir); err == nil {
			v.repo = r.Name
		}
	}
	v.key = verdict.BranchKey(v.repo, v.branch)
	out, err := reviewGit(c.Context(), c.deps(), t.Dir, "rev-parse", "--short", t.Branch)
	if err != nil {
		return verdictBranch{}, err
	}
	v.head = strings.TrimSpace(out)
	return v, nil
}

// bindVerdictHead is the sha a new verdict records. With --sha it is that
// commit, which must be the head of the remote branch (the local branch when
// nothing was pushed): a verdict bound to a commit no reviewer saw, or to a
// stale worktree, would pass the merge gate on code nobody read. Without
// --sha the local tip is recorded, and a local ref that differs from the
// remote branch is refused rather than guessed at.
func bindVerdictHead(c *Ctx, t verdictBranch, sha string) (string, error) {
	ctx := c.Context()
	r := c.deps().Repo(t.dir)
	remote := t.remote
	if remote == "" {
		remote = "origin"
	}
	remoteRef := remote + "/" + t.branch
	pushed, err := r.Verify(ctx, "refs/remotes/"+remoteRef)
	if err != nil {
		return "", gitErr(err)
	}
	rev := func(args ...string) (string, error) {
		out, err := reviewGit(ctx, c.deps(), t.dir, append([]string{"rev-parse"}, args...)...)
		return strings.TrimSpace(out), err
	}
	if sha == "" {
		if !pushed || t.remote != "" {
			return t.head, nil
		}
		local, err := rev(t.ref)
		if err != nil {
			return "", err
		}
		tip, err := rev(remoteRef)
		if err != nil {
			return "", err
		}
		if local != tip {
			return "", Refused("local %s (%s) differs from %s (%s)", t.branch, local[:7], remoteRef, tip[:7]).
				WithHint("pass --sha <the sha the reviewers were briefed on> to bind the verdict to it")
		}
		return t.head, nil
	}
	if found, err := r.Verify(ctx, sha+"^{commit}"); err != nil {
		return "", gitErr(err)
	} else if !found {
		return "", Resolution("--sha %s is not a commit here", sha)
	}
	full, err := rev(sha + "^{commit}")
	if err != nil {
		return "", err
	}
	target := t.ref
	if pushed {
		target = remoteRef
	}
	tip, err := rev(target)
	if err != nil {
		return "", err
	}
	if full != tip {
		if pushed {
			if behind, err := r.Ok(ctx, "merge-base", "--is-ancestor", tip, full); err != nil {
				return "", gitErr(err)
			} else if behind {
				return "", Refused("--sha %s is ahead of %s (%s): the remote-tracking ref is behind", sha, target, tip[:7]).
					WithHint("git fetch " + remote + ", then retry")
			}
		}
		extra, _ := reviewGit(ctx, c.deps(), t.dir, "log", "--format=%h %s", full+".."+tip)
		msg := fmt.Sprintf("--sha %s is not the head of %s (%s)", sha, target, tip[:7])
		if extra = strings.TrimSpace(extra); extra != "" {
			msg += ", which has commits after it:\n" + extra
		}
		return "", Refused("%s", msg).WithHint("review the current head, then pass its sha")
	}
	return rev("--short", full)
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
	kind := fs.String("kind", "", "review-spec, review-quality, review-cross, second-opinion, qa or behaviour")
	v := fs.String("verdict", "", "PASS, CONCERNS or FAIL (qa also INFRA-FAIL; behaviour takes PASS or FAIL)")
	transcript := fs.String("transcript", "", "path of the run's transcript (behaviour only)")
	fileDeferred := fs.Bool("file-deferred", false, "add each deferred (non-blocking, non-info) finding at or above review.fileDeferred to the one follow-up item for --pr")
	pr := fs.String("pr", "", "the PR the deferred items cite in --related (with --file-deferred)")
	reviewer := fs.String("harness", "", "the reviewer's harness: "+harness.KindList()+" (required for review-cross)")
	model := fs.String("model", "", "the reviewer's model (required for review-cross)")
	sha := fs.String("sha", "", "the reviewed commit: record it, refusing unless it is the head of origin/<branch>, or the local branch when it is not pushed")
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
		*reviewer, *model = strings.TrimSpace(*reviewer), strings.TrimSpace(*model)
		if *reviewer != "" && !harness.Valid(*reviewer) {
			return Result{}, Usage("--harness must be one of %s", harness.KindList())
		}
		if *kind == verdict.ReviewCross && (*reviewer == "" || *model == "") {
			return Result{}, Usage("--kind %s needs --harness and --model: the record names who reviewed", verdict.ReviewCross)
		}
		if *transcript != "" && *kind != verdict.Behaviour {
			return Result{}, Usage("--transcript is only for --kind %s", verdict.Behaviour)
		}
		if *pr != "" && !*fileDeferred {
			return Result{}, Usage("--pr is only for --file-deferred")
		}
		if *fileDeferred && strings.TrimSpace(*pr) == "" {
			return Result{}, Usage("--file-deferred needs --pr, the PR the items cite")
		}
		b, err := verdictBody(c, *body)
		if err != nil {
			return Result{}, err
		}
		if err := sameVerdict(b, *v); err != nil {
			return Result{}, err
		}
		var deferred []verdict.Finding
		floor := verdict.FloorMajor
		var fileRoot string
		var followUpPR bool
		if *fileDeferred {
			// Resolved before the record is written: a config the floor cannot
			// be read from stops the call instead of guessing the default.
			if fileRoot, err = backlogScope(c); err != nil {
				return Result{}, err
			}
			if f := config.StringIfSet(c.deps().Config(fileRoot), "review.fileDeferred"); f != "" {
				floor = f
			}
			if floor != verdict.FloorMinor && floor != verdict.FloorMajor && floor != verdict.FloorOff {
				return Result{}, Usage("review.fileDeferred is %q: want minor, major or off", floor)
			}
			deferred = verdict.DeferredAbove(b.Findings, floor)
		}
		t, err := resolveVerdictBranch(c, args)
		if err != nil {
			return Result{}, err
		}
		if *fileDeferred && len(deferred) > 0 {
			followUpPR = branchIsFollowUp(c, fileRoot, t.branch)
		}
		head, err := bindVerdictHead(c, t, strings.TrimSpace(*sha))
		if err != nil {
			return Result{}, err
		}
		r := verdict.NewRecord(*kind, *v, head, b, c.deps().Now())
		r.Branch, r.Repo = t.branch, t.repo
		r.Transcript = *transcript
		r.Harness, r.Model = *reviewer, *model
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
		if note := raisedDepthNote(c, args, t, head, r.Kind); note != "" {
			d.Set("depthNote", note)
			text += " (" + note + ")"
		}
		if *fileDeferred {
			d.Set("filed", []any{})
			d.Set("appended", 0)
		}
		if *fileDeferred && floor == verdict.FloorOff {
			why := "review.fileDeferred is off: no deferred finding is filed"
			d.Set("filedWhy", why)
			text += " (" + why + ")"
		}
		if *fileDeferred {
			var kept []string
			for _, f := range verdict.DeferredAbove(b.Findings, verdict.FloorMinor) {
				if followUpPR || !slices.ContainsFunc(deferred, func(g verdict.Finding) bool { return g.Title == f.Title && g.File == f.File }) {
					kept = append(kept, f.Title)
				}
			}
			if len(kept) > 0 {
				d.Set("kept", kept)
				if floor != verdict.FloorOff {
					text += fmt.Sprintf(" (kept in the record, not filed: %s)", strings.Join(kept, "; "))
				}
			}
		}
		if followUpPR {
			why := "this PR closes a deferred-findings item: no follow-up is filed from it"
			d.Set("filedWhy", why)
			text += " (" + why + ")"
			deferred = nil
		}
		if len(deferred) > 0 {
			// The record is already written, so a failure here reports what was
			// filed and leaves the rest to a by-hand `item create`.
			var id string
			var added int
			_, err = withBackend(c, fileRoot, false, "", func(be backlog.Backend) (Result, error) {
				var ferr error
				if id, added, ferr = verdict.FileFollowUps(be, fileRoot, deferred, strings.TrimSpace(*pr), *kind, *v); ferr != nil {
					return backlogFail(ferr)
				}
				return Result{}, nil
			})
			if id != "" {
				d.Set("filed", []any{id})
			}
			d.Set("appended", added)
			if err != nil {
				return Result{Data: d, Text: text}, err
			}
			if added > 0 {
				text += fmt.Sprintf(" (follow-up %s: %d added)", id, added)
			}
		}
		return Result{Data: d, Text: text + " -> " + next}, nil
	}
}

// branchIsFollowUp is whether the issue leading the branch is a deferred-findings
// item. Any lookup failure reads as no: the finding is then filed as before.
func branchIsFollowUp(c *Ctx, root, branch string) bool {
	ref := backlog.BranchIssueRef(branch)
	if ref == "" {
		return false
	}
	var yes bool
	withBackend(c, root, false, "", func(be backlog.Backend) (Result, error) {
		if it, err := be.Get(ref); err == nil {
			yes = verdict.IsFollowUpTitle(it.Title)
		}
		return Result{}, nil
	})
	return yes
}

// raisedDepthNote tells a reviewer's verdict add when the branch's review depth
// needs a verdict kind not yet recorded at head: a fix outside the issue's files
// can raise light to full after the review ran, and the gate then refuses with
// review-missing. It is empty for other kinds, when nothing is missing, or when
// the depth cannot be read (the record is already written; the gate will say).
func raisedDepthNote(c *Ctx, args []string, t verdictBranch, head, kind string) string {
	if kind != verdict.ReviewSpec && kind != verdict.ReviewQuality {
		return ""
	}
	bt, err := resolveBranch(c, args)
	if err != nil || bt.Branch == bt.Base {
		return ""
	}
	res, why, err := branchReviewDepth(c, bt, nil)
	if err != nil {
		return ""
	}
	hasSpec := worker.BranchHasSpec(t.branch)
	missing := verdict.MissingReviews(res.Depth, hasSpec, verdict.Recorded(t.root, t.branch, head))
	if len(missing) == 0 {
		return ""
	}
	return fmt.Sprintf("review depth %s -> %s (%s): still needs %s at head, or the gate refuses review-missing",
		verdict.CoveredDepth(verdict.LastReviewSet(t.root, t.branch), hasSpec), res.Depth, why, strings.Join(missing, " and "))
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
		o.Set("stale", !verdict.AtHead(r, t.head))
		records = append(records, o)
		line := kind + " " + r.Verdict + " @ " + r.Sha
		if who := strings.TrimSpace(r.Harness + " " + r.Model); who != "" {
			line += " (" + who + ")"
		}
		lines = append(lines, line)
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
		cfg := c.deps().Config(t.root)
		s := verdict.Settings{
			QAGate: config.String(cfg, "qa.gate"),
		}
		next := verdict.Route(*consumer, r.Verdict, s)
		d := jsonx.NewObject()
		d.Set("for", *consumer)
		d.Set("kind", r.Kind)
		d.Set("verdict", r.Verdict)
		d.Set("sha", r.Sha)
		d.Set("stale", !verdict.AtHead(r, t.head))
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
		out, err := reviewGit(c.Context(), c.deps(), root, "rev-parse", "--short", "HEAD")
		if err != nil {
			return Result{}, err
		}
		r := verdict.NewRecord(verdict.DebugFix, *v, strings.TrimSpace(out), b, c.deps().Now())
		bug, err = debugItem(c, bug)
		if err != nil {
			return backlogFail(err)
		}
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
		bug, err = debugItem(c, bug)
		if err != nil {
			return backlogFail(err)
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
		if out, err := reviewGit(c.Context(), c.deps(), root, "rev-parse", "--short", "HEAD"); err == nil {
			sha = strings.TrimSpace(out)
		}
		r := verdict.NewRecord(verdict.DebugReset, verdict.Reset, sha, verdict.Body{Summary: strings.TrimSpace(*reason)}, c.deps().Now())
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
