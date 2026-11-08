package worker

import "github.com/l4ci/rota/internal/jsonx"

// ListSection is a typed list of objects kept under one registry key: the
// escalation and limit records. The owning package supplies the key and a
// codec; reading, the write-back and the locked update live here once, so every
// section rewrites the registry the same way.
type ListSection[T any] struct {
	Key    string
	Decode func(*jsonx.Object) T
	Encode func(T) *jsonx.Object
}

// Read is the records in list order; a missing or malformed list reads as empty.
func (s ListSection[T]) Read(r Registry) []T {
	var out []T
	for _, o := range r.Objects(s.Key) {
		out = append(out, s.Decode(o))
	}
	return out
}

// Write replaces the list. Call it inside Update, under the registry lock.
func (s ListSection[T]) Write(d *Doc, list []T) {
	out := make([]any, 0, len(list))
	for _, v := range list {
		out = append(out, s.Encode(v))
	}
	d.SetSection(s.Key, out)
}

// Update edits the list under the registry lock: mutate gets the list as it is
// and returns the new one.
func (s ListSection[T]) Update(root string, mutate func(list []T) []T) error {
	return Update(root, func(d *Doc) { s.Write(d, mutate(s.Read(d.Registry))) })
}
