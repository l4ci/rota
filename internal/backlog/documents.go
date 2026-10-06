package backlog

import (
	"errors"
	"os"

	"github.com/l4ci/rota/internal/fsio"
)

// withDocuments serializes the two-document lifecycle. Always acquire BACKLOG
// before ARCHIVE and retain both locks through the last write. Single-document
// backlog writers take only BACKLOG, so they cannot interleave with a transfer.
func (f *File) withDocuments(fn func(*documents) error) error {
	return fsio.Locked(f.backlogPath(), fsio.LockTimeout, func() error {
		return fsio.Locked(f.archivePath(), fsio.LockTimeout, func() error {
			d, err := f.readDocuments()
			if err != nil {
				return err
			}
			return fn(d)
		})
	})
}

type documents struct {
	f                *File
	backlog, archive string
	archiveExists    bool
}

func (f *File) readDocuments() (*documents, error) {
	read := func(path string) (string, bool, error) {
		text, err := fsio.ReadText(path)
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return text, err == nil, err
	}
	backlog, _, err := read(f.backlogPath())
	if err != nil {
		return nil, err
	}
	archive, archiveExists, err := read(f.archivePath())
	if err != nil {
		return nil, err
	}
	return &documents{f: f, backlog: backlog, archive: archive, archiveExists: archiveExists}, nil
}

// write persists the destination before removing the source. An interruption
// can leave two copies, never zero. Callers recognize those copies on retry:
// Archive skips identical archived lines; Reopen cleans up the archived done
// line even when the active destination already exists. Remove uses the same
// locks and writes, but intentionally deletes rather than transfers entries.
func (d *documents) write(backlog, archive string, toArchive bool) error {
	write := func(path, old, next string) error {
		if old == next {
			return nil
		}
		return fsio.WriteFileAtomic(path, []byte(next))
	}
	if toArchive {
		if err := write(d.f.archivePath(), d.archive, archive); err != nil {
			return err
		}
		return write(d.f.backlogPath(), d.backlog, backlog)
	}
	if err := write(d.f.backlogPath(), d.backlog, backlog); err != nil {
		return err
	}
	return write(d.f.archivePath(), d.archive, archive)
}
