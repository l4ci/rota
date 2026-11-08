package artifact

import (
	"os"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/rotatree"
)

// FileDocs keeps one document per key as .rota/<Dir>/<key>.md under Root.
// Kind names the document in errors ("design", "plan").
type FileDocs struct{ Root, Dir, Kind string }

// Path is the file of key.
func (d FileDocs) Path(key string) string { return rotatree.Doc(d.Root, d.Dir, key) }

// NotFound is the exit-3 error for a key with no file.
func (d FileDocs) NotFound(key string) *exitcode.Error {
	return exitcode.Errf(exitcode.ExitResolution, "%s %s not found (.rota/%s/%s.md)", d.Kind, key, d.Dir, key)
}

// Read is the stored document, verbatim.
func (d FileDocs) Read(key string) (string, error) {
	b, err := os.ReadFile(d.Path(key))
	if err != nil {
		return "", d.NotFound(key)
	}
	return string(b), nil
}

// Replace overwrites an existing document; changed is false when it already
// reads text. The comparison is on the raw bytes.
func (d FileDocs) Replace(key, text string) (changed bool, err error) {
	return d.update(key, rawText, func(string) (string, error) { return text, nil })
}

// Update rewrites an existing document under its lock: fn gets the stored
// text (newlines normalised, like fsio.ReadText) and returns the new text.
// changed is false when fn returns the text it was given. An fn error aborts
// without a write.
func (d FileDocs) Update(key string, fn func(old string) (string, error)) (changed bool, err error) {
	return d.update(key, fsio.ReadText, fn)
}

func rawText(p string) (string, error) {
	b, err := os.ReadFile(p)
	return string(b), err
}

func (d FileDocs) update(key string, read func(string) (string, error), fn func(string) (string, error)) (changed bool, err error) {
	p := d.Path(key)
	if _, serr := os.Stat(p); serr != nil { // no lock file or directory for a missing doc
		return false, d.NotFound(key)
	}
	err = fsio.Locked(p, fsio.LockTimeout, func() error {
		old, rerr := read(p)
		if rerr != nil {
			return d.NotFound(key)
		}
		text, ferr := fn(old)
		if ferr != nil {
			return ferr
		}
		if text == old {
			return nil
		}
		changed = true
		return fsio.WriteFileAtomic(p, []byte(text))
	})
	return
}

// Create writes a new document under the lock of lockKey's file, refusing
// (exit 4) when it exists. build runs inside the lock and returns the key and
// text, so a caller can mint the key (the next plan slice) race-free; lockKey
// is the key itself or a shared one that serialises several keys. It returns
// the key written.
func (d FileDocs) Create(lockKey string, build func() (key, text string)) (key string, err error) {
	err = fsio.Locked(d.Path(lockKey), fsio.LockTimeout, func() error {
		var text string
		key, text = build()
		p := d.Path(key)
		if _, serr := os.Stat(p); serr == nil {
			return exitcode.Errf(exitcode.ExitRefused, ".rota/%s/%s.md already exists", d.Dir, key)
		}
		return fsio.WriteFileAtomic(p, []byte(text))
	})
	return key, err
}

// Remove deletes an existing document.
func (d FileDocs) Remove(key string) error {
	p := d.Path(key)
	if _, err := os.Stat(p); err != nil {
		return d.NotFound(key)
	}
	return fsio.Locked(p, fsio.LockTimeout, func() error {
		if err := os.Remove(p); err != nil {
			return d.NotFound(key)
		}
		return nil
	})
}

// NoteSlot is the one note a key names: an item's note of some kind, or a
// slice note on a milestone's tracking issue.
type NoteSlot interface {
	Get() (text string, ok bool, err error)
	Put(text string) (changed bool, err error)
	Rm() (removed bool, err error)
}

// NoteDocs keeps one document per key as a note. Open resolves key to its
// slot and the name errors use for it.
type NoteDocs struct {
	Kind string
	Open func(key string) (slot NoteSlot, what string, err error)
}

// Read adds the newline the old helper printed after a note.
func (d NoteDocs) Read(key string) (string, error) {
	slot, what, err := d.Open(key)
	if err != nil {
		return "", err
	}
	text, ok, err := slot.Get()
	if err != nil {
		return "", err
	}
	if !ok {
		return "", d.missing(what)
	}
	return text + "\n", nil
}

// Replace overwrites an existing note; changed is false when it already reads text.
func (d NoteDocs) Replace(key, text string) (bool, error) {
	slot, what, err := d.Open(key)
	if err != nil {
		return false, err
	}
	if _, ok, err := slot.Get(); err != nil {
		return false, err
	} else if !ok {
		return false, d.missing(what)
	}
	return slot.Put(text)
}

// Remove deletes an existing note.
func (d NoteDocs) Remove(key string) error {
	slot, what, err := d.Open(key)
	if err != nil {
		return err
	}
	removed, err := slot.Rm()
	if err != nil {
		return err
	}
	if !removed {
		return d.missing(what)
	}
	return nil
}

func (d NoteDocs) missing(what string) *exitcode.Error {
	return exitcode.Errf(exitcode.ExitResolution, "%s note for %s not found", d.Kind, what)
}

// ItemNote is the NoteSlot of one Notes ref and kind.
type ItemNote struct {
	Notes     Notes
	Ref, Kind string
}

func (n ItemNote) Get() (string, bool, error)    { return n.Notes.NoteGet(n.Ref, n.Kind) }
func (n ItemNote) Put(text string) (bool, error) { return n.Notes.NotePut(n.Ref, n.Kind, text) }
func (n ItemNote) Rm() (bool, error)             { return n.Notes.NoteRm(n.Ref, n.Kind) }
