package verdict

import (
	"errors"
	"strings"

	"github.com/l4ci/rota/internal/git"
)

// BlockedError is a recorded FAIL, or a review at the gate's head with a
// blocking finding, that stops branch from shipping (B3). Stale is set when the
// branch has moved past the reviewed commit. Finding is the title of the
// blocking finding when that is what blocks.
type BlockedError struct {
	Branch  string
	Record  Record
	Stale   bool
	Finding string
}

func (e *BlockedError) Error() string {
	if e.Finding != "" {
		return e.Record.Kind + " " + e.Record.Verdict + " recorded for " + e.Branch + " with a blocking finding (" + e.Finding + "); not shipped"
	}
	return e.Record.Kind + " " + e.Record.Verdict + " recorded for " + e.Branch + "; not shipped"
}

// Runner runs git in one checkout.
type Runner interface {
	Run(args ...string) (git.Result, error)
}

// Check reads the verdict store for one checkout. It is the one owner of the
// merge-blocking rule: ship merge uses Block, the worker gate and train use
// BlockGate.
type Check struct {
	Git   Runner
	Store Store
	Repo  string // sub-repo name in an umbrella, else ""
	// StoreErr is the LoadStrict error for Store; set, every check refuses.
	StoreErr error
	// IgnoreStaleCross drops review-cross records recorded at a commit other
	// than the checked head, so an old cross FAIL no longer blocks. The caller
	// sets it only when cross review is switched off by an explicit review:none.
	IgnoreStaleCross bool
}

// Block returns a *BlockedError when a recorded FAIL blocks branch, nil
// when nothing does.
func (v Check) Block(branch string) error {
	return v.BlockAt(branch, "")
}

// Head is the short sha of branch in the checkout, "" when it cannot be read.
// Block and the callers that refresh the store before it share this lookup.
func (v Check) Head(branch string) string {
	if v.Git == nil {
		return ""
	}
	if res, err := v.Git.Run("rev-parse", "--short", branch); err == nil && res.ExitCode == 0 {
		return strings.TrimSpace(res.Stdout)
	}
	return ""
}

// BlockAt uses the known PR head when the branch is absent or stale locally.
// An empty head keeps Block's existing local-ref behavior.
func (v Check) BlockAt(branch, head string) error {
	if v.StoreErr != nil {
		return v.StoreErr
	}
	list := v.Store.Branch(v.Repo, branch)
	// Keep local records' old stale semantics, but bind recovered reports to
	// the supplied PR head (or the branch tip on the ordinary ship path).
	r, ok := Blocking(list)
	hasReports := false
	for _, record := range list {
		hasReports = hasReports || record.FromReport
	}
	if !ok && !hasReports {
		return nil
	}
	explicit := head != ""
	if head == "" {
		head = v.Head(branch)
	}
	list = ForHead(list, head)
	if v.IgnoreStaleCross {
		list = dropStaleCross(list, head)
	}
	r, ok = Blocking(list)
	if !ok {
		return nil
	}
	stale := r.Sha != head
	if explicit {
		stale = !AtHead(r, head)
	}
	return &BlockedError{Branch: branch, Record: r, Stale: stale}
}

// BlockGate is Block plus the bounce rule, for the merge gate and train: a
// review record at head (the commit the gate merges, which for a PR is the
// pushed head, not the local branch tip) with a blocking finding or a failed
// item refuses the merge even when its verdict is CONCERNS. Ship keeps Block,
// which follows the recorded FAIL alone.
func (v Check) BlockGate(branch, head string) error {
	if err := v.BlockAt(branch, head); err != nil {
		return err
	}
	r, ok := BlockingAtHead(v.Store.Branch(v.Repo, branch), strings.TrimSpace(head))
	if !ok {
		return nil
	}
	e := &BlockedError{Branch: branch, Record: r}
	for _, f := range r.Findings {
		if f.IsBlocking() {
			e.Finding = f.Title
			break
		}
	}
	return e
}

// BlockGateCrossOff is BlockGate plus the stale-cross relaxation (#3458): when
// a review-cross FAIL at another commit is what blocks and crossOff reports
// cross review is off (review:none), that record is ignored and the gate is
// judged again. relaxed is the ignored FAIL when that cleared the block. A FAIL
// at head, or one while cross is required, still blocks, and an unreadable head
// fails closed. BlockingCrossOff is the same decision for the train tick.
func (v Check) BlockGateCrossOff(branch, head string, crossOff func() bool) (relaxed *Record, err error) {
	err = v.BlockGate(branch, head)
	var blocked *BlockedError
	if !errors.As(err, &blocked) || !blocked.Stale || !crossOffRelaxes(blocked.Record, strings.TrimSpace(head), crossOff) {
		return nil, err
	}
	v.IgnoreStaleCross = true
	if err = v.BlockGate(branch, head); err != nil {
		return nil, err
	}
	return &blocked.Record, nil
}
