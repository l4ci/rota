package artifact

import (
	"errors"

	"github.com/l4ci/rota/internal/exitcode"
)

// DocStore is the read, replace and remove half of a keyed-doc store, as plan
// and design both keep it.
type DocStore interface {
	Read(key string) (string, error)
	Replace(key, text string) (changed bool, err error)
	Remove(key string) error
}

// Verbs is the shared show, put and rm of a keyed-doc kind: Check is the
// store's key rule, applied before the store is touched; Kind names the kind
// in the add hint ("plan", "design").
type Verbs struct {
	Kind  string
	Check func(key string) error
	Store DocStore
}

// Show is the stored document.
func (v Verbs) Show(key string) (string, error) {
	if err := v.Check(key); err != nil {
		return "", err
	}
	return v.Store.Read(key)
}

// Put replaces an existing document's text; changed is false when identical.
// A missing document is exit 3 with the `rota <kind> add` hint.
func (v Verbs) Put(key, text string) (bool, error) {
	if err := v.Check(key); err != nil {
		return false, err
	}
	changed, err := v.Store.Replace(key, text)
	var ae *exitcode.Error
	if errors.As(err, &ae) && ae.Exit == exitcode.ExitResolution {
		ae.WithHint("rota " + v.Kind + " add " + key + " --title <text>")
	}
	return changed, err
}

// Rm deletes a document; a missing one is exit 3, never a no-op.
func (v Verbs) Rm(key string) error {
	if err := v.Check(key); err != nil {
		return err
	}
	return v.Store.Remove(key)
}
