package round

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/worker"
)

// reClaimID is a claim `assign` takes: `<agent>@<round>`.
var reClaimID = regexp.MustCompile(`^([a-z][a-z0-9-]*)@\d+$`)

// claimFindings adds claim-mismatch drift: the tracker and the registry
// disagree about who holds an issue. Two shapes: a slot's claimId is not the
// issue's earliest open claim (gone, or another holds it), and an open claim
// `<agent>@<round>` on an in-progress issue names a registry slot that does not
// hold that issue. Only a claim that is gone has a repair (clear the claimId);
// the tracker is the source of truth and is never edited. Needs the Board and,
// for the second shape, the labelled issues; file mode has no claims to read.
func (e Env) claimFindings(ctx context.Context, rep *Report, rows []*Row, slotObj map[string]*worker.Slot, queued []*jsonx.Object, labelled map[int]bool, labelsOK bool) {
	if e.Board == nil {
		return
	}
	status := func(ref string) (*backlog.Status, bool) {
		st, err := e.Board.Status(ref)
		if err != nil {
			if !errors.Is(err, backlog.ErrWrongBackend) {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("claim check #%s: %v", ref, err))
			}
			return nil, false
		}
		return st, st != nil
	}
	held := map[string]bool{}
	for _, r := range rows {
		s := slotObj[r.Name]
		if s == nil || r.Issue == "" {
			continue
		}
		held[r.Issue] = true
		want := s.ClaimID()
		if want == "" {
			continue // adopted or pre-C3 slot: no claim to compare
		}
		st, ok := status(r.Issue)
		if !ok {
			continue
		}
		switch {
		case st.Claim == want:
		case st.Claim == "":
			rep.add(Finding{Kind: ClaimMismatch, Slot: r.Name, Issue: r.Issue,
				Detail: fmt.Sprintf("slot records claim %s on #%s, which has no open claim", want, r.Issue), Repair: "clear claimId"})
		default:
			rep.add(Finding{Kind: ClaimMismatch, Slot: r.Name, Issue: r.Issue,
				Detail: fmt.Sprintf("slot records claim %s on #%s, which %s holds", want, r.Issue, st.Claim)})
		}
	}
	// A queued PR holds its issue and its claim like a slot, but names no slot
	// to repair: the slot it came from has moved on.
	for _, q := range queued {
		id := queuedIssue(q)
		if id == "" {
			continue
		}
		held[id] = true
		want := worker.Str(q, "claimId")
		if want == "" {
			continue
		}
		st, ok := status(id)
		if !ok {
			continue
		}
		switch {
		case st.Claim == want:
		case st.Claim == "":
			rep.add(Finding{Kind: ClaimMismatch, Issue: id,
				Detail: fmt.Sprintf("PR in review records claim %s on #%s, which has no open claim", want, id)})
		default:
			rep.add(Finding{Kind: ClaimMismatch, Issue: id,
				Detail: fmt.Sprintf("PR in review records claim %s on #%s, which %s holds", want, id, st.Claim)})
		}
	}
	if !labelsOK {
		return
	}
	var nums []int
	for n := range labelled {
		if !held[strconv.Itoa(n)] {
			nums = append(nums, n)
		}
	}
	sort.Ints(nums)
	for _, n := range nums {
		st, ok := status(strconv.Itoa(n))
		if !ok {
			continue
		}
		m := reClaimID.FindStringSubmatch(st.Claim)
		if m == nil || slotObj[m[1]] == nil {
			continue
		}
		rep.add(Finding{Kind: ClaimMismatch, Slot: m[1], Issue: strconv.Itoa(n),
			Detail: fmt.Sprintf("open claim %s on #%d names slot %s, which does not hold it", st.Claim, n, m[1])})
	}
}

// openClaimWith returns the earliest open claim on id when its id starts
// with prefix, else "".
func openClaimWith(be Board, id, prefix string) string {
	st, err := be.Status(id)
	if err != nil || st == nil || !strings.HasPrefix(st.Claim, prefix) {
		return ""
	}
	return st.Claim
}
