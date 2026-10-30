package worker

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
)

// IssueComments is the part of an issue tracker the issue bounce store reads
// and writes.
type IssueComments interface {
	Comments(ctx context.Context, key tracker.IssueKey) ([]tracker.Comment, error)
	AddComment(ctx context.Context, key tracker.IssueKey, body string) (string, error)
	DeleteComment(ctx context.Context, key tracker.IssueKey, commentID string) error
}

// BounceStore keeps how often each item's PR was sent back (round.maxBounces).
// Counts are per item, not per slot, so a transfer does not reset them, and a
// bounce at a PR head already counted does not count again. With round.support
// empty the store is the registry (`bounces` in workers.json, this machine's);
// with it set it is the issue itself (#1598), so the main and the support
// machine read one count.
type BounceStore interface {
	// Count is the item's bounces so far and the PR head of the last one ("" when
	// none or unknown). A branch still at that head has not answered it.
	Count(issue string) (n int, head string, err error)
	// Record counts one bounce at head unless the item already holds limit (0 =
	// no cap). slot names the slot holding the bounced PR, for the ledger row; ""
	// falls back to the first slot holding the issue.
	Record(m *RoundMemo, issue, slot, head string, limit int) (BounceRecord, error)
	// Clear forgets the item's count: its PR merged, it was parked for a human
	// or picked.
	Clear(issue string) error
}

// AtCap reports whether n bounces already hold the cap. limit is
// round.maxBounces; 0 means no cap. It is the one spelling of the rule: Record
// refuses at it, the gate parks at it and round review relays no more at it.
func AtCap(n, limit int) bool {
	return limit > 0 && n >= limit
}

// BounceRecord is what one Record did. N is the count after it; Counted is
// false when the head was already counted or the cap refused (Capped, N
// unchanged).
type BounceRecord struct {
	N               int
	Counted, Capped bool
	undo            func() error
}

// Undo takes back a bounce this record counted, for a relay that did not go
// out. A bounce another writer counted meanwhile stays; a record that did not
// count has nothing to undo.
func (r BounceRecord) Undo() error {
	if !r.Counted || r.undo == nil {
		return nil
	}
	return r.undo()
}

// ErrNoPushTrust is BounceStore's refusal when the issue tracker cannot say who
// has write access (a forge without PushTrust, GitLab today).
var ErrNoPushTrust = errors.New("round.support keeps bounces on the issue, and this issue tracker cannot say who has write access to trust them")

// PushTrust says whether a login holds write access on the repository
// (tracker.GitHub.CanPush).
type PushTrust interface {
	CanPush(ctx context.Context, login string) (bool, error)
}

// MarkerTrust answers whether a comment's author may post rota's bounce
// markers: anyone who can comment could otherwise reset the cap or force a
// park. Write access keeps the machines' shared login and drops strangers. A
// source that cannot say trusts nobody. Answers are kept for the life of the
// value, so one pass asks the forge once per login.
type MarkerTrust struct {
	ctx   context.Context
	trust PushTrust
	known map[string]bool
}

// NewMarkerTrust takes the trust check from src when it implements PushTrust.
func NewMarkerTrust(ctx context.Context, src any) *MarkerTrust {
	t, _ := src.(PushTrust)
	return &MarkerTrust{ctx: ctx, trust: t, known: map[string]bool{}}
}

// Writer reports whether login may post bounce markers. A failed lookup is an
// error, not a refusal: the count must not move on a guess.
func (m *MarkerTrust) Writer(login string) (bool, error) {
	if m.trust == nil || login == "" {
		return false, nil
	}
	if ok, seen := m.known[login]; seen {
		return ok, nil
	}
	ok, err := m.trust.CanPush(m.ctx, login)
	if err != nil {
		return false, fmt.Errorf("checking whether %s may post bounce markers: %w", login, err)
	}
	m.known[login] = ok
	return ok, nil
}

// sameHead reports whether a and b name one commit: equal, or one the prefix of
// the other with at least 7 characters (the merge gate records a 7-character
// head, the relay a full one).
func sameHead(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	if len(a) > len(b) {
		a, b = b, a
	}
	return a != "" && (a == b || (len(a) >= 7 && strings.HasPrefix(b, a)))
}

// newIssueComments resolves the optional factory only when a consumer needs
// comment access. WithDefaults deliberately leaves IssueComments optional.
func (e Env) newIssueComments(root string, s tracker.Settings) (IssueComments, error) {
	if e.IssueComments != nil {
		return e.IssueComments(root, s)
	}
	return tracker.NewIssueTracker(e.Ctx, s, "", root, machineOption())
}

// BounceStore is the store round.support picks for root (see BounceStore).
// round.support is read alone, so a bad value in another key cannot move the
// count between stores.
func (e Env) BounceStore(root string) (BounceStore, error) {
	e = e.WithDefaults()
	cfg := e.Config(root)
	if roundcfg.SupportName(cfg) == "" {
		return registryBounces{e: e, root: root}, nil
	}
	s, err := tracker.ReadSettings(cfg)
	if err != nil {
		return nil, err
	}
	it, err := e.newIssueComments(root, s)
	if errors.Is(err, tracker.ErrNoIssueTracker) || (err == nil && it == nil) {
		return nil, fmt.Errorf("round.support keeps bounces on the issue, and issues.source has no issue tracker")
	}
	if err != nil {
		return nil, err
	}
	if _, ok := it.(PushTrust); !ok {
		return nil, ErrNoPushTrust
	}
	return issueBounces{e: e, root: root, it: it}, nil
}

// WriterTrust is the write-access check for root's issue tracker, for readers
// of rota-marked comments that steer an agent (bounce markers, learnings). A
// tracker that cannot say is ErrNoPushTrust.
func (e Env) WriterTrust(root string) (*MarkerTrust, error) {
	e = e.WithDefaults()
	s, err := tracker.ReadSettings(e.Config(root))
	if err != nil {
		return nil, err
	}
	it, err := e.newIssueComments(root, s)
	if errors.Is(err, tracker.ErrNoIssueTracker) || (err == nil && it == nil) {
		return nil, fmt.Errorf("issues.source has no issue tracker")
	}
	if err != nil {
		return nil, err
	}
	if _, ok := it.(PushTrust); !ok {
		return nil, ErrNoPushTrust
	}
	return NewMarkerTrust(e.Ctx, it), nil
}

// registryBounces is the store with round.support empty: the registry, as
// before #1598.
type registryBounces struct {
	e    Env
	root string
}

func (s registryBounces) Count(issue string) (int, string, error) {
	reg, err := LoadRegistry(s.root)
	if err != nil {
		return 0, "", err
	}
	return reg.Bounces(issue), reg.BounceHead(issue), nil
}

func (s registryBounces) Record(m *RoundMemo, issue, slot, head string, limit int) (BounceRecord, error) {
	prev := LoadRegistryTolerant(s.root)
	n, counted, capped, err := recordBounce(s.e, m, s.root, issue, slot, head, limit)
	return BounceRecord{N: n, Counted: counted, Capped: capped, undo: func() error { return UnrecordBounce(s.root, issue, head, prev) }}, err
}

func (s registryBounces) Clear(issue string) error { return ClearBounces(s.root, issue) }

// issueBounces is the store with round.support set: one marked comment per
// bounce on the issue (`<!-- rota:bounce head=<sha> -->`), and a reset comment
// (`<!-- rota:bounce reset -->`) where a count was cleared. The count is the
// distinct heads after the last reset; a bounce with no known head always
// counts. Main's gate and the support machine's review relay both write, so
// two writers can race across machines: Record re-reads after it writes and
// takes its own comment back when it landed past the cap. Only comments from
// authors with write access count.
type issueBounces struct {
	e    Env
	root string
	it   IssueComments
}

// bounceTally is an issue's bounces since the last reset: the heads in order
// ("" for an unknown head), and the comment id of each.
type bounceTally struct {
	heads, ids []string
}

func (t bounceTally) has(head string) bool {
	for _, h := range t.heads {
		if sameHead(h, head) {
			return true
		}
	}
	return false
}

func (t bounceTally) last() string {
	if len(t.heads) == 0 {
		return ""
	}
	return t.heads[len(t.heads)-1]
}

func (s issueBounces) key(issue string) (tracker.IssueKey, error) {
	k, ok := tracker.ParseKey(issue)
	if !ok {
		return "", fmt.Errorf("%s is not an issue key: bounces live on the issue with round.support set", issue)
	}
	return k, nil
}

func (s issueBounces) tally(issue string) (tracker.IssueKey, bounceTally, error) {
	k, err := s.key(issue)
	if err != nil {
		return k, bounceTally{}, err
	}
	comments, err := s.it.Comments(s.e.Ctx, k)
	if err != nil {
		return k, bounceTally{}, fmt.Errorf("reading the bounces on %s: %w", tracker.RefOf(issue), err)
	}
	var t bounceTally
	trust := NewMarkerTrust(s.e.Ctx, s.it)
	for _, c := range comments {
		kind, head, reset, ok := marker.ParseBounce(c.Body)
		if !ok || kind != marker.KindBounce {
			continue
		}
		if w, err := trust.Writer(c.Author); err != nil {
			return k, bounceTally{}, err
		} else if !w {
			continue
		}
		switch {
		case reset:
			t = bounceTally{}
		case head == "" || !t.has(head):
			t.heads, t.ids = append(t.heads, head), append(t.ids, c.ID)
		}
	}
	return k, t, nil
}

func (s issueBounces) Count(issue string) (int, string, error) {
	_, t, err := s.tally(issue)
	return len(t.heads), t.last(), err
}

func (s issueBounces) Record(m *RoundMemo, issue, slot, head string, limit int) (BounceRecord, error) {
	k, t, err := s.tally(issue)
	if err != nil {
		return BounceRecord{}, err
	}
	n := len(t.heads)
	if AtCap(n, limit) {
		return BounceRecord{N: n, Capped: true}, nil
	}
	if head != "" && t.has(head) {
		return BounceRecord{N: n}, nil
	}
	n++
	body := marker.Bounce(head) + "\n" + fmt.Sprintf("Bounce %d of %s: the PR was sent back%s.", n, capText(limit), atHead(head))
	id, err := s.it.AddComment(s.e.Ctx, k, body)
	if err != nil {
		return BounceRecord{N: n - 1}, fmt.Errorf("recording the bounce on %s: %w", tracker.RefOf(issue), err)
	}
	// The read above and the write are not atomic: another machine may have
	// counted meanwhile. Read again; a comment that landed past the cap, or
	// that another writer's same head already covers, is taken back.
	_, after, err := s.tally(issue)
	if err != nil {
		return BounceRecord{N: n - 1}, errors.Join(err, s.it.DeleteComment(s.e.Ctx, k, id))
	}
	at := slices.Index(after.ids, id)
	if at < 0 || AtCap(at, limit) {
		kept := len(after.heads)
		if at >= 0 {
			kept-- // this comment was one of them
		}
		if err := s.it.DeleteComment(s.e.Ctx, k, id); err != nil {
			return BounceRecord{N: kept}, fmt.Errorf("taking back the bounce on %s that landed past the cap: %w", tracker.RefOf(issue), err)
		}
		return BounceRecord{N: kept, Capped: at >= 0}, nil
	}
	LedgerNoteIn(s.e, m, s.root, ledger.Entry{Kind: ledger.KindBounce, Issue: issue, Slot: slot, Detail: ledger.Detail("count", n)})
	return BounceRecord{N: n, Counted: true, undo: func() error { return s.it.DeleteComment(s.e.Ctx, k, id) }}, nil
}

func (s issueBounces) Clear(issue string) error {
	k, t, err := s.tally(issue)
	if err != nil || len(t.heads) == 0 {
		return err
	}
	_, err = s.it.AddComment(s.e.Ctx, k, marker.BounceReset()+"\nThe bounce count starts again.")
	return err
}

func capText(limit int) string {
	if limit == 0 {
		return "unlimited"
	}
	return fmt.Sprint(limit)
}

func atHead(head string) string {
	if head == "" {
		return ""
	}
	return " at " + strings.TrimSpace(head)
}

// NoSupportPRError is PostReviewBounce's refusal when the support machine's
// branch has no PR number to post the bounce on, as with a slot `worker adopt`
// registered without --pr (#1850).
type NoSupportPRError struct{ Branch, PR string }

func (e *NoSupportPRError) Error() string {
	return fmt.Sprintf("%s is the support machine's branch, but its PR %q has no number to post the bounce on", e.Branch, e.PR)
}

// PostReviewBounce tells the support machine that main sent its PR back
// (#1598 AC-8): on main with round.support set, a PR headed by a
// `<round.support>-` branch gets a comment marked `rota:review-bounce
// head=<sha>` holding why. The support machine's review poll takes it as
// review input and relays it to the worker, and its reconcile reports a queued
// PR still at that head as bounced. The machines usually share one forge
// login, which cannot request changes on its own PR, so a comment it is.
// One comment per head: a head already carrying a trusted review-bounce posts
// nothing, so a caller whose count was deduped (or whose undo failed) can post
// again safely. Anywhere else it posts nothing and posted is false.
func (e Env) PostReviewBounce(root, branch, pr, head, why string) (posted bool, err error) {
	e = e.WithDefaults()
	cfg := e.Config(root)
	support := roundcfg.SupportName(cfg)
	if role, _ := roundcfg.MachineRole(cfg); support == "" || role == roundcfg.RoleSupport || !strings.HasPrefix(branch, support+"-") {
		return false, nil
	}
	n, ok := PRRefNumber(pr)
	if !ok {
		return false, &NoSupportPRError{Branch: branch, PR: pr}
	}
	gc := e.GateConfig(root)
	provider, err := e.detectProvider(e.Ctx, root, pr, gc.Tracker.Provider, tracker.ForgeActor)
	if err != nil {
		return false, err
	}
	forge, err := e.Forge(provider, root, gc.Tracker)
	if err != nil {
		return false, err
	}
	if rf, ok := forge.(ReviewForge); ok {
		notes, err := rf.MRNotes(e.Ctx, n)
		if err != nil {
			return false, fmt.Errorf("reading the comments of PR %d: %w", n, err)
		}
		trust := NewMarkerTrust(e.Ctx, forge)
		for _, c := range notes {
			if kind, h, _, ok := marker.ParseBounce(c.Body); !ok || kind != marker.KindReviewBounce || !sameHead(h, head) {
				continue
			}
			if w, err := trust.Writer(c.Author); err != nil {
				return false, err
			} else if w {
				return false, nil
			}
		}
	}
	body := marker.ReviewBounce(head) + "\n" + fmt.Sprintf("Sent back by the main machine%s. Fix it on this branch and push; the PR stays open.\n\n%s", atHead(head), strings.TrimSpace(why))
	if _, err := forge.AddMRNote(e.Ctx, n, body); err != nil {
		return false, fmt.Errorf("posting the bounce on PR %d: %w", n, err)
	}
	return true, nil
}
