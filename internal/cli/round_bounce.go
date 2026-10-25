package cli

import (
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/verdict"
	"github.com/l4ci/rota/internal/worker"
)

// roundBounce is the orchestrator's count of a review bounce (SKILL §7): the
// merge gate counts its own stale and provenance bounces, this counts the ones
// the orchestrator sends back by hand. It refuses once the item sits at
// round.maxBounces, so a further bounce is never the answer: the item goes to a
// higher tier or the human instead.
func roundBounce(fs *flag.FlagSet) RunFunc {
	head := fs.String("head", "", "PR head sha being bounced; the same head twice counts once")
	slot := fs.String("slot", "", "slot holding the bounced PR; names the ledger row of a best-of attempt")
	force := fs.Bool("force", false, "count the bounce although the PR's current review verdicts hold no blocking finding")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) != 1 {
			return Result{}, Usage("round bounce takes one item ID")
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		set, err := c.deps().roundSettings(root, roundConfigBounce)
		if err != nil {
			return Result{}, err
		}
		issue := worker.NormID(args[0])
		if slotName, lead := bundleHolder(root, issue); lead != "" {
			d := jsonx.NewObject()
			d.Set("issue", issue)
			d.Set("blockedBy", round.BlockBundle)
			d.Set("lead", lead)
			d.Set("changed", false)
			return Result{Data: d}, &Error{Exit: ExitRefused, Message: fmt.Sprintf("%s is bundled under %s (slot %s): a bundle bounces once, on its lead", tracker.RefOf(issue), tracker.RefOf(lead), slotName),
				Hint: fmt.Sprintf("run `rota round bounce %s`", lead)}
		}
		if !*force {
			if refused := noBlockingFindings(c, root, issue, *slot, *head); refused != nil {
				d := jsonx.NewObject()
				d.Set("issue", issue)
				d.Set("blockedBy", blockedby.NoBlockingFinds)
				d.Set("changed", false)
				return Result{Data: d}, refused
			}
		}
		env := c.deps().workerEnv()
		rec, err := recordBounce(env, root, issue, *slot, *head, set.MaxBounces)
		if err != nil {
			return Result{}, err
		}
		n, capped := rec.N, rec.Capped
		if !capped {
			// Idempotent per head, so a head the store deduped posts too: a
			// failed post whose undo failed leaves the head counted, and the
			// retry must still reach the support worker.
			posted, pr, err := postSupportBounce(env, root, issue, *slot, *head)
			var noPR *worker.NoSupportPRError
			if errors.As(err, &noPR) {
				if undoErr := rec.Undo(); undoErr != nil {
					return Result{}, errors.Join(err, undoErr)
				}
				slotName := *slot
				if slotName == "" {
					for _, s := range worker.LoadRegistryTolerant(root).Slots() {
						if s.HeldID() == issue {
							slotName = s.Name()
							break
						}
					}
				}
				d := jsonx.NewObject()
				d.Set("issue", issue)
				d.Set("branch", noPR.Branch)
				d.Set("slot", slotName)
				return blockedRefusal(d, blockedby.NoPRRecorded, fmt.Sprintf("%s is the support machine's PR but the slot records none, so the bounce has no PR to land on (nothing counted)", noPR.Branch),
					noPRHint(issue, slotName, *head, *force))
			}
			if err != nil {
				return Result{}, errors.Join(err, rec.Undo())
			}
			if posted {
				postSupportPointer(c, root, issue, "bounce", pr)
			}
		}
		d := jsonx.NewObject()
		d.Set("issue", issue)
		d.Set("bounces", n)
		d.Set("max", set.MaxBounces)
		d.Set("changed", !capped)
		if capped {
			return Result{Data: d}, &Error{Exit: ExitRefused, Message: fmt.Sprintf("%s was already sent back %d time(s) (round.maxBounces is %d)", issue, n, set.MaxBounces),
				Hint: round.CapHint(issue)}
		}
		return Result{Data: d, Text: fmt.Sprintf("bounce %d of %s for %s", n, maxText(set.MaxBounces), issue)}, nil
	}
}

// bundleHolder is the slot (or the queued PR's origin slot) and the lead of the
// bundle that holds issue as a member; lead is "" when issue is no member.
func bundleHolder(root, issue string) (slot, lead string) {
	s, q := worker.LoadRegistryTolerant(root).BundleHolder(issue)
	switch {
	case s != nil:
		return s.Name(), s.HeldID()
	case q != nil:
		return q.From, q.Issue
	}
	return "", ""
}

// noBlockingFindings is the refusal of a bounce when the verdicts recorded at
// the PR's current head judged it and none of their findings blocks: a nit does
// not cost a bounce (#963). The branch is the named slot's, else the slot
// holding the issue. Without a branch, a head, or a review verdict at that head
// the bounce is not a verdict-driven one and counts as before. A FAIL record,
// or one with an item FAIL, always counts as blocking, findings or not. The
// store is read under the plain branch key, so an umbrella root (keys
// "<repo>:<branch>") finds no record and fails open: the bounce counts.
func noBlockingFindings(c *Ctx, root, issue, slot, head string) error {
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return nil
	}
	var branch string
	for _, s := range reg.Slots() {
		if (slot != "" && s.Name() == slot) || (slot == "" && s.Issue() == issue) {
			branch = s.Branch()
			break
		}
	}
	if branch == "" {
		return nil
	}
	if head == "" {
		out, err := reviewGit(c.Context(), c.deps(), root, "rev-parse", "--short", branch)
		if err != nil {
			return nil
		}
		head = strings.TrimSpace(out)
	}
	list := verdict.Load(root).Branches[verdict.BranchKey("", branch)]
	judged := false
	for _, kind := range append([]string{verdict.ReviewSpec, verdict.ReviewQuality, verdict.ReviewCross}, verdict.LensKinds...) {
		r, ok := verdict.Latest(list, kind)
		if !ok || !verdict.AtHead(r, head) {
			continue
		}
		if r.Failing() {
			return nil
		}
		judged = true
	}
	if !judged {
		return nil
	}
	return &Error{Exit: ExitRefused, Message: fmt.Sprintf("%s has no blocking finding in its current review verdicts: a bounce would relay only nits", issue),
		Hint: "file the deferred findings with `rota verdict add --file-deferred`, or pass --force to bounce anyway"}
}

// recordBounce counts one bounce unless the item is already at max (0 = no
// cap); the bounce store enforces it, the issue store by re-reading after its
// write. capped reports the refusal; n is then the unchanged count. A slot that
// is not in the pool is refused before anything is written.
func recordBounce(env worker.Env, root, issue, slot, head string, max int) (worker.BounceRecord, error) {
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return worker.BounceRecord{}, err
	}
	if slot != "" && reg.Slot(slot) == nil {
		return worker.BounceRecord{}, &Error{Exit: ExitRefused, Message: fmt.Sprintf("unknown slot %q: not in the worker pool", slot),
			Hint: "see `rota worker pool list` for the slot names"}
	}
	store, err := env.BounceStore(root)
	if err != nil {
		return worker.BounceRecord{}, err
	}
	return store.Record(nil, issue, slot, head, max)
}

// postSupportBounce posts a counted bounce on the PR when the support machine
// built it (#1598 AC-8), with the open findings of the branch's review verdict
// as the reason. The PR is the named slot's, else the one of the slot or the
// queued record holding the issue; any other PR posts nothing.
func postSupportBounce(env worker.Env, root, issue, slot, head string) (posted bool, pr string, err error) {
	reg := worker.LoadRegistryTolerant(root)
	var branch string
	for _, s := range reg.Slots() {
		if (slot != "" && s.Name() == slot) || (slot == "" && s.HeldID() == issue) {
			branch, pr = s.Branch(), s.PR()
			break
		}
	}
	if q := reg.QueuedIssue(issue); branch == "" && slot == "" && q != nil {
		branch, pr = q.Branch, q.PR
	}
	why := "The orchestrator sent the PR back after review: read the review on this PR."
	if fs := openFindings(root, branch); len(fs) > 0 {
		why = "Open findings:\n- " + strings.Join(fs, "\n- ")
	}
	posted, err = env.PostReviewBounce(root, branch, pr, head, why)
	return posted, pr, err
}

// noPRHint is the remedy of a bounce refused as "no PR recorded": reconcile
// records the open PR of the slot's branch under any round host, and the retry
// repeats the flags of this run with the slot filled in.
func noPRHint(issue, slot, head string, force bool) string {
	retry := "rota round bounce " + issue
	if head != "" {
		retry += " --head " + head
	}
	if slot != "" {
		retry += " --slot " + slot
	}
	if force {
		retry += " --force"
	}
	return fmt.Sprintf("run `rota round reconcile --apply` to record the branch's open PR on the slot, then `%s`", retry)
}

// postSupportPointer leaves the pointer comment on the issue after a post on
// its PR (#1827), with the support split active only. The post on the PR
// already went out, so a failure here is a warning.
func postSupportPointer(c *Ctx, root, issue, kind, url string) {
	if roundcfg.SupportName(c.deps().Config(root)) == "" {
		return
	}
	env, be, err := moveEnv(c, root)
	if err == nil {
		_, err = env.PostPointer(c.Context(), be, issue, kind, url)
	}
	if err != nil {
		c.Warn("pointer comment on %s not posted: %v", tracker.RefOf(issue), err)
	}
}

func maxText(max int) string {
	if max == 0 {
		return "unlimited"
	}
	return fmt.Sprint(max)
}
