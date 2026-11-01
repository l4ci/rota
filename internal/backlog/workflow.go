package backlog

import (
	"context"
	"strings"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/tracker"
)

// Workflow is the part of a backlog that coordinates workers on an item: the
// claim lock and the state label. Both backends implement it; the file backend
// keeps the lock in status.json, so its claim, release and state verbs are
// successful no-ops.
type Workflow interface {
	// Claim takes the item for claimID. won is false when another claim holds
	// it; holder is then that claim's ID. changed is false when the backend
	// has no lock to take (file mode).
	Claim(ref, claimID string) (won bool, holder string, err error)
	// Release gives the item back; false when claimID held no open claim.
	Release(ref, claimID string) (bool, error)
	// SetState leaves exactly one state label (in-progress, needs-review,
	// changes-requested) or none ("none"); false when nothing changed.
	SetState(ref, state string) (bool, error)
}

// ItemTracker is the part of a backlog that reads an item back from its
// tracker: the status and the durable notes. Only the issue backends implement
// it; the file backend keeps that state in BACKLOG.md, status.json and the
// detail file, so it does not satisfy this interface and a caller branches on
// the assertion (or Capabilities.ItemStatus).
type ItemTracker interface {
	// Status is the read-back of the item.
	Status(ref string) (*Status, error)
	// NoteGet returns the durable note of a kind (proof, design, plan or
	// plan:S<NN>); ok is false when the item has none.
	NoteGet(ref, kind string) (text string, ok bool, err error)
	// NotePut writes the note; false when it already read as text.
	NotePut(ref, kind, text string) (bool, error)
	// NoteRm deletes the note; false when there was none.
	NoteRm(ref, kind string) (bool, error)
}

// SharedClaimer is a backend whose item can hold more than one claim at once:
// a best-of:2 issue is built by two slots, each with its own claim.
type SharedClaimer interface {
	// ClaimShared is Claim where claimID wins when it is among the first
	// holders open claims (holders < 1 counts as 1). On a loss holder is the
	// earliest open claim.
	ClaimShared(ref, claimID string, holders int) (won bool, holder string, err error)
}

// ClaimHistorian reads the raw tracker comments that carry claim markers, for
// a caller that audits who held an item. ok is false when the backend has no
// tracker to read; only the single-repo issues backend implements it.
type ClaimHistorian interface {
	ClaimComments(ctx context.Context, key tracker.IssueKey) (comments []tracker.Comment, ok bool, err error)
}

// ClaimComments lists the issue's tracker comments.
func (b *Issues) ClaimComments(ctx context.Context, key tracker.IssueKey) ([]tracker.Comment, bool, error) {
	if b.Tracker == nil {
		return nil, false, nil
	}
	comments, err := b.Tracker.Comments(ctx, key)
	return comments, true, err
}

// ClaimLabelSyncer repairs visible labels derived from tracker claims and
// removes in-progress and claim labels on closed issues. Claims remain the
// source of truth; file mode has no labels.
type ClaimLabelSyncer interface {
	ClaimLabelDrift(ref string) (add, remove []string, err error)
	SyncClaimLabels(ref string) (changed bool, err error)
}

var (
	_ ClaimLabelSyncer = (*Issues)(nil)
	_ ClaimLabelSyncer = (*Umbrella)(nil)
)

var (
	_ Workflow      = (*File)(nil)
	_ Workflow      = (*Issues)(nil)
	_ ItemTracker   = (*Issues)(nil)
	_ SharedClaimer = (*Issues)(nil)
	_ SharedClaimer = (*Umbrella)(nil)
)

// FileNoteHint names where a file-mode project keeps what issue mode keeps in
// notes (_FILE_NOTE_MSG); FileShowHint where it keeps what item show reads.
const (
	FileNoteHint = "rota design or rota plan"
	FileShowHint = "see BACKLOG.md, status.json and the detail file"
)

func fileIssueOnly(what, hint string) error {
	return &RefusedError{BlockedBy: blockedby.Backend, Err: ErrWrongBackend, Hint: hint,
		Msg: what + ` is not available when issues.source is file`}
}

func (f *File) exists(ref string) error {
	if _, err := f.Get(ref); err != nil {
		return err
	}
	return nil
}

// Claim is a no-op in file mode: status.json is the lock. The item must exist.
func (f *File) Claim(ref, claimID string) (bool, string, error) {
	return true, claimID, f.exists(ref)
}

// HandReleaser is a backend whose release can be marked as a person's own, so
// reconcile does not post the claim again.
type HandReleaser interface {
	ReleaseByHand(ref, claimID string) (bool, error)
}

var (
	_ HandReleaser = (*Issues)(nil)
	_ HandReleaser = (*Umbrella)(nil)
)

// Release is a no-op in file mode.
func (f *File) Release(ref, claimID string) (bool, error) { return false, f.exists(ref) }

// SetState is a no-op in file mode.
func (f *File) SetState(ref, state string) (bool, error) {
	if _, ok := StateRoleFor[state]; !ok {
		return false, errf(ErrInvalid, "state must be one of %s", strings.Join(States, "/"))
	}
	return false, f.exists(ref)
}
