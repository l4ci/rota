package worker

import (
	"github.com/l4ci/rota/internal/stamp"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
)

// RecordItemStart stamps when an item was first assigned (`itemStarts` in the
// registry maps the issue to an RFC 3339 UTC time). Counted per item, not per
// slot, so a transfer to another slot keeps the clock running; an item that
// already has a start keeps it.
func RecordItemStart(root, issue string, at time.Time) error {
	return Update(root, func(d *Doc) {
		doc := d.doc
		m := itemStartsOf(doc)
		if _, ok := m.Get(issue); ok {
			return
		}
		m.Set(issue, stamp.Format(at))
		doc.Set("itemStarts", m)
	})
}

// ItemStart is when an item was first assigned; false when never recorded.
func (r Registry) ItemStart(issue string) (time.Time, bool) {
	v, _ := itemStartsOf(r.doc).Get(issue)
	s, _ := v.(string)
	t, err := stamp.Parse(s)
	return t, err == nil
}

// ClearItemStart forgets an item's clock: its PR merged or it was parked for a
// human, so a later assignment starts afresh.
func ClearItemStart(root, issue string) error {
	// Update rewrites the file, so look first: no entry means no write (and no
	// workers.json created where there was none).
	reg, err := LoadRegistry(root)
	if err != nil {
		return err
	}
	if _, ok := itemStartsOf(reg.doc).Get(issue); !ok {
		return nil
	}
	return Update(root, func(d *Doc) {
		doc := d.doc
		m := itemStartsOf(doc)
		m.Delete(issue)
		doc.Set("itemStarts", m)
	})
}

func itemStartsOf(doc *jsonx.Object) *jsonx.Object {
	if v, _ := doc.Get("itemStarts"); v != nil {
		if m, ok := v.(*jsonx.Object); ok {
			return m
		}
	}
	return jsonx.NewObject()
}
