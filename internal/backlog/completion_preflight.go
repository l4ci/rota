package backlog

import (
	"errors"
	"io/fs"
	"strings"

	"github.com/l4ci/rota/internal/acceptance"
	"github.com/l4ci/rota/internal/fsio"
)

// AcceptanceChecker lets a caller check completion's acceptance rule before
// parking a worktree or posting a comment. Complete rechecks when it writes.
type AcceptanceChecker interface {
	CheckAcceptance(ref string, in CompleteInput) error
}

var (
	_ AcceptanceChecker = (*File)(nil)
	_ AcceptanceChecker = (*Issues)(nil)
	_ AcceptanceChecker = (*Umbrella)(nil)
)

func (f *File) CheckAcceptance(ref string, in CompleteInput) error {
	it, err := f.Get(ref)
	if err != nil || it.Closed {
		return err
	}
	_, err = f.completionAcceptance(ref, in)
	return err
}

func (b *Issues) CheckAcceptance(ref string, in CompleteInput) error {
	is, id, err := b.require(ref)
	if err != nil || is.State == "closed" {
		return err
	}
	_, err = b.completionAcceptance(is, id, in)
	return err
}

func (u *Umbrella) CheckAcceptance(ref string, in CompleteInput) error {
	_, err := viaOwner(u, ref, func(b *Issues, plain string) (bool, error) { return false, b.CheckAcceptance(plain, in) })
	return err
}

func (f *File) completionAcceptance(ref string, in CompleteInput) (string, error) {
	path := DetailPath(f.Root, ref)
	if in.Reason != "done" || path == "" {
		return "", nil
	}
	body, err := fsio.ReadText(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	record, err := completionAcceptanceRecord(ref, unmetAcceptance(body, "", "", nil), in)
	if refusal, ok := err.(*RefusedError); ok {
		refusal.Hint = "file mode has no acceptance notes; pass --unmet-reason <text> to record why these criteria remain unmet"
	}
	return record, err
}

func (b *Issues) completionAcceptance(is Issue, id string, in CompleteInput) (string, error) {
	if in.Reason != "done" {
		return "", nil
	}
	unmet, err := b.itemUnmetAcceptance(is)
	if err != nil {
		return "", err
	}
	return completionAcceptanceRecord(id, unmet, in)
}

func completionAcceptanceRecord(id string, unmet []acceptance.Status, in CompleteInput) (string, error) {
	if len(unmet) == 0 {
		return "", nil
	}
	if strings.TrimSpace(in.UnmetReason) == "" {
		return "", AcceptanceRefusal(id, unmet)
	}
	return AcceptanceRecord(unmet, in.UnmetReason), nil
}
