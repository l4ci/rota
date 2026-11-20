package backlog

import (
	"context"

	"github.com/l4ci/rota/internal/tracker"
)

// BodyDoc is an issue body split once: the text above the fields block, the
// block's fields and their order.
type BodyDoc struct {
	Text   string
	Fields map[string]string
	Order  []string
	raw    *string
}

// SetRaw replaces the whole body, fields block included, verbatim.
func (d *BodyDoc) SetRaw(body string) { d.raw = &body }

// bodyWriter is the one tracker call a body edit makes.
type bodyWriter interface {
	Edit(ctx context.Context, key tracker.IssueKey, e tracker.IssueEdit) error
}

// editBody is the single read-modify-write seam for issue bodies. It splits
// body once (ParseFieldsBlock), lets mutate change the text and fields, renders
// the result (RenderFieldsBlock) and writes it with one tracker Edit. mutate
// returns false to leave the issue alone; a body that renders identical to the
// one read is not written. It reports whether a write happened.
func (b *Issues) editBody(w bodyWriter, key tracker.IssueKey, body string, mutate func(*BodyDoc) (bool, error)) (bool, error) {
	text, fields, order := ParseFieldsBlock(body)
	d := &BodyDoc{Text: text, Fields: fields, Order: order}
	changed, err := mutate(d)
	if err != nil || !changed {
		return false, err
	}
	out := ""
	if d.raw != nil {
		out = *d.raw
	} else {
		out = RenderFieldsBlock(d.Text, d.Order, d.Fields)
	}
	if out == body {
		return false, nil
	}
	if err := w.Edit(b.ctx(), key, tracker.IssueEdit{Body: &out}); err != nil {
		return false, err
	}
	return true, nil
}

// textOf is the text of body above its fields block.
func textOf(body string) string {
	text, _, _ := ParseFieldsBlock(body)
	return text
}
