package round

import (
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/worker"
)

// reviewKey is the registry section the architecture review owns.
const reviewKey = "architectureReview"

// Review is the registry's `architectureReview`: when the last review was
// minted, the review items still recognised, and the cached count of items
// closed since. The zero value reads as no review.
type Review struct {
	At     string
	Seeded bool
	Items  []string
	Closed *ClosedCount
}

// ClosedCount is the cached number of items closed since a review.
type ClosedCount struct {
	Since, Checked string
	Count          int
}

// ReviewOf reads the architecture review state.
func ReviewOf(r worker.Registry) Review {
	v, _ := r.Section(reviewKey)
	o, _ := v.(*jsonx.Object)
	if o == nil {
		return Review{}
	}
	rv := Review{At: jsonx.Str(o, "at"), Seeded: jsonx.Bool(o, "seeded")}
	items, _ := o.Get("items")
	list, _ := items.([]any)
	for _, e := range list {
		if id, ok := e.(string); ok {
			rv.Items = append(rv.Items, id)
		}
	}
	if cv, _ := o.Get("closed"); cv != nil {
		if c, ok := cv.(*jsonx.Object); ok {
			cnt, _ := c.Get("count")
			n, _ := jsonx.Int(cnt)
			rv.Closed = &ClosedCount{Since: jsonx.Str(c, "since"), Checked: jsonx.Str(c, "checked"), Count: n}
		}
	}
	return rv
}

// reviewObject is the review object, created when missing.
func reviewObject(d *worker.Doc) *jsonx.Object {
	v, _ := d.Section(reviewKey)
	o, _ := v.(*jsonx.Object)
	if o == nil {
		o = jsonx.NewObject()
		d.SetSection(reviewKey, o)
	}
	return o
}

// SeedReview starts the review clock at at, with no review minted yet.
func SeedReview(d *worker.Doc, at string) {
	o := reviewObject(d)
	o.Set("at", at)
	o.Set("seeded", true)
}

// CacheClosedCount saves the closed-item count for the review recorded at since.
func CacheClosedCount(d *worker.Doc, checked, since string, count int) {
	c := jsonx.NewObject()
	c.Set("checked", checked)
	c.Set("since", since)
	c.Set("count", count)
	reviewObject(d).Set("closed", c)
}

// RecordReviewItems adds ids to the review's cumulative item list.
func RecordReviewItems(d *worker.Doc, ids []string) { recordItems(reviewObject(d), ids) }

// StartReview replaces the review with a freshly minted one. Items of earlier
// reviews stay recognised, and ids join them.
func StartReview(d *worker.Doc, at string, round int, trigger string, ids []string) {
	o := jsonx.NewObject()
	o.Set("at", at)
	o.Set("round", round)
	o.Set("trigger", trigger)
	if v, ok := d.Section(reviewKey); ok {
		if old, _ := v.(*jsonx.Object); old != nil {
			l, _ := old.Get("items")
			o.Set("items", l)
		}
	}
	recordItems(o, ids)
	d.SetSection(reviewKey, o)
}

func recordItems(o *jsonx.Object, ids []string) {
	l, _ := o.Get("items")
	list, _ := l.([]any)
	for _, id := range ids {
		list = append(list, id)
	}
	o.Set("items", list)
}
