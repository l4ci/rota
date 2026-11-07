package round

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/worker"
)

// Follow-ups (#2580): a PR waits in the review queue because its slot moved on,
// and a person then asks for more work on it. That request is not backlog work
// and not automated feedback, so the autopilot starts it ahead of new
// assignments, on an idle round slot or a follow-up reserve slot (Reserve).

// FollowUp is a queued PR with new review input from at least one person.
type FollowUp struct {
	Issue, PR, From string
	// Head is the PR head the bounce is counted against.
	Head string
	// Humans are the people who asked: writers whose login is no bot.
	Humans []string
	// At is when the first of them asked; follow-ups go oldest first.
	At time.Time
	// Seen is the record's cursor the batch was read after; Continue refuses
	// when the record moved on meanwhile.
	Seen string
	// Batch is every new item on the PR, the people's and the rest, which goes
	// to the worker as one review relay.
	Batch worker.ReviewBatch
}

// botLogin matches the forges' service accounts: a GitHub app ("name[bot]")
// and GitLab's project and group bot users ("project_12_bot_9f2c").
var botLogin = regexp.MustCompile(`\[bot\]$|^(project|group)_\d+_bot(_[0-9a-z]+)?$`)

// IsBotLogin says whether a forge login is a service account rather than a person.
func IsBotLogin(login string) bool { return botLogin.MatchString(strings.ToLower(login)) }

// FollowUps lists the queued PRs a person asked to continue, oldest request
// first. A person is a login with write access (the forge's CanPush) that is
// no bot. Comments carrying a rota marker (main's review-bounce included: rota
// posts it under the maintainer's login), approvals, verdicts and input from
// bots or strangers never make a follow-up. Once a person asked, the bot and
// stranger comments and a review-bounce ride along in the brief; approvals,
// verdicts and other marked comments are not review input. A record without a cursor (queued before cursors were kept)
// or whose PR is not open is skipped. A PR the forge cannot read is skipped
// with a warning, so one bad PR does not hold the others.
func (e Env) FollowUps(ctx context.Context, root string, f ReviewForge) ([]FollowUp, []string, error) {
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return nil, nil, err
	}
	trust := worker.NewMarkerTrust(ctx, f)
	var out []FollowUp
	var warns []string
	for _, q := range reg.PRs() {
		if q.ReviewSeen == "" {
			continue
		}
		n, ok := worker.PRRefNumber(q.PR)
		if !ok {
			continue
		}
		info, err := f.PRView(ctx, n)
		if err != nil {
			warns = append(warns, fmt.Sprintf("follow-up poll of %s: %v", q.PR, err))
			continue
		}
		if !strings.EqualFold(info.State, "OPEN") {
			continue
		}
		b, err := worker.PendingReview(ctx, f, q.ReviewSlot(), nil, e.Worker.Now())
		if err != nil {
			warns = append(warns, fmt.Sprintf("follow-up poll of %s: %v", q.PR, err))
			continue
		}
		u := FollowUp{Issue: q.Issue, PR: q.PR, From: q.From, Head: info.HeadSHA, Seen: q.ReviewSeen, Batch: b}
		for _, it := range b.Items {
			// A marked comment is rota's own post, whatever login it came from.
			if it.Author == "" || IsBotLogin(it.Author) || marker.Has(it.Body) {
				continue
			}
			w, err := trust.Writer(it.Author)
			if err != nil {
				warns = append(warns, fmt.Sprintf("follow-up poll of %s: %v", q.PR, err))
				u.Humans = nil
				break
			}
			if !w {
				continue
			}
			if len(u.Humans) == 0 {
				u.At = it.CreatedAt
			}
			if !slices.Contains(u.Humans, it.Author) {
				u.Humans = append(u.Humans, it.Author)
			}
		}
		if len(u.Humans) > 0 {
			out = append(out, u)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, warns, nil
}

// ContinueOpts are the inputs of Continue.
type ContinueOpts struct {
	FollowUp FollowUp
	// To is the receiving slot; Reserve says it is a follow-up reserve slot.
	To      string
	Reserve bool
	// MaxBounces is round.maxBounces; 0 turns the cap off.
	MaxBounces int
}

// Continue starts a follow-up: the queued PR's issue moves to the receiving
// slot (Transfer) with the review input as its brief, counted as a bounce, and
// the receiver's review cursor starts at the batch so the same input is not
// relayed again; Transfer sets the cursor in the same registry write that hands
// the receiver the queued PR. Everything Transfer checks still holds: the claim, overlap,
// the harness and account preflight, the receiver's guard. At round.maxBounces
// it refuses (BlockMaxBounces), and a record that moved since FollowUps read
// it (another start, a merge) refuses too; either way nothing moves, so the
// follow-up stays pending.
func (e Env) Continue(ctx context.Context, root string, be Board, o ContinueOpts) (Transferred, error) {
	u := o.FollowUp
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return Transferred{}, err
	}
	if q := reg.QueuedIssue(u.Issue); q == nil || q.PR != u.PR || q.ReviewSeen != u.Seen {
		return Transferred{}, blocked(BlockSlotBusy, "the follow-up on %s moved since it was read (started, merged or requeued); nothing to start", u.PR)
	}
	store, err := e.Worker.BounceStore(root)
	if err != nil {
		return Transferred{}, err
	}
	n, _, err := store.Count(u.Issue)
	if err != nil {
		return Transferred{}, err
	}
	capped := func(n int) error {
		return blocked(BlockMaxBounces, "%s was already sent back %d time(s) (round.maxBounces is %d); %s", u.Issue, n, o.MaxBounces, CapHint(u.Issue))
	}
	if worker.AtCap(n, o.MaxBounces) {
		return Transferred{}, capped(n)
	}
	who := strings.Join(u.Humans, ", ")
	var res Transferred
	var terr error
	bres, err := worker.Bounce(store, nil, u.Issue, u.From, u.Head, o.MaxBounces, func(rec worker.BounceRecord) error {
		res, terr = e.Transfer(ctx, root, be, TransferOpts{
			Issue: u.Issue, To: o.To, Reserve: o.Reserve, ReviewSeen: u.Batch.Cursor,
			Decisions: ReviewRelayText(u.Batch),
			Note:      fmt.Sprintf("follow-up asked by %s on %s (bounce %d): the PR waited in the review queue, so it moves to %s", strutil.FirstNonEmpty(who, "a person"), prLabel(u.PR), rec.N, o.To),
		})
		// A transfer that changed something keeps the bounce, whatever it returned.
		if terr != nil && !res.Changed {
			return terr
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	if bres.Capped {
		return res, capped(bres.N)
	}
	if bres.NotifyErr != nil {
		return res, bres.Err()
	}
	return res, terr
}
