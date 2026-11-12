package worker

import "github.com/l4ci/rota/internal/jsonx"

// ObjectSection is a typed single object kept under one registry key: the
// architecture review state. The owning package supplies the key and a codec;
// reading, the write-back and the locked update live here once, so no caller
// touches the raw document. ListSection is the same seam for lists.
type ObjectSection[T any] struct {
	Key    string
	Decode func(*jsonx.Object) T
	Encode func(T) *jsonx.Object
}

// Read is the section's value; a missing or malformed section decodes from an
// empty object, so the zero value of the owner's type is the no-section state.
func (s ObjectSection[T]) Read(r Registry) T {
	v, _ := r.Section(s.Key)
	o, _ := v.(*jsonx.Object)
	if o == nil {
		o = jsonx.NewObject()
	}
	return s.Decode(o)
}

// Write replaces the section. Call it inside Update, under the registry lock.
func (s ObjectSection[T]) Write(d *Doc, v T) { d.SetSection(s.Key, s.Encode(v)) }

// Edit rewrites the section inside an Update: mutate gets the value as it is
// and returns the new one.
func (s ObjectSection[T]) Edit(d *Doc, mutate func(T) T) { s.Write(d, mutate(s.Read(d.Registry))) }

// Update edits the section under the registry lock.
func (s ObjectSection[T]) Update(root string, mutate func(T) T) error {
	return Update(root, func(d *Doc) { s.Edit(d, mutate) })
}
