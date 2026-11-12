package round

import (
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/worker"
)

// Review is the registry's `architectureReview`: when the last review was
// minted, the review items still recognised, and the cached count of items
// closed since. The zero value reads as no review.
type Review struct {
	At      string
	Seeded  bool
	Round   int    // the round that minted the review
	Trigger string // what fired it
	Items   []string
	Closed  *ClosedCount
}

// ClosedCount is the cached number of items closed since a review.
type ClosedCount struct {
	Since, Checked string
	Count          int
}

// reviewSection is the typed codec for the registry's `architectureReview`.
var reviewSection = worker.ObjectSection[Review]{
	Key:    "architectureReview",
	Decode: decodeReview,
	Encode: encodeReview,
}

func decodeReview(o *jsonx.Object) Review {
	rv := Review{At: jsonx.Str(o, "at"), Seeded: jsonx.Bool(o, "seeded"), Trigger: jsonx.Str(o, "trigger")}
	if v, ok := o.Get("round"); ok {
		rv.Round, _ = jsonx.Int(v)
	}
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

func encodeReview(rv Review) *jsonx.Object {
	o := jsonx.NewObject()
	if rv.At != "" {
		o.Set("at", rv.At)
	}
	if rv.Seeded {
		o.Set("seeded", true)
	}
	if rv.Trigger != "" || rv.Round != 0 {
		o.Set("round", rv.Round)
		o.Set("trigger", rv.Trigger)
	}
	if rv.Items != nil {
		o.Set("items", jsonx.Strings(rv.Items))
	}
	if rv.Closed != nil {
		c := jsonx.NewObject()
		c.Set("checked", rv.Closed.Checked)
		c.Set("since", rv.Closed.Since)
		c.Set("count", rv.Closed.Count)
		o.Set("closed", c)
	}
	return o
}

// ReviewOf reads the architecture review state.
func ReviewOf(r worker.Registry) Review { return reviewSection.Read(r) }

// SeedReview starts the review clock at at, with no review minted yet.
func SeedReview(d *worker.Doc, at string) {
	reviewSection.Edit(d, func(rv Review) Review { rv.At, rv.Seeded = at, true; return rv })
}

// CacheClosedCount saves the closed-item count for the review recorded at since.
func CacheClosedCount(d *worker.Doc, checked, since string, count int) {
	reviewSection.Edit(d, func(rv Review) Review {
		rv.Closed = &ClosedCount{Since: since, Checked: checked, Count: count}
		return rv
	})
}

// RecordReviewItems adds ids to the review's cumulative item list.
func RecordReviewItems(d *worker.Doc, ids []string) {
	reviewSection.Edit(d, func(rv Review) Review {
		rv.Items = append(rv.Items, ids...)
		return rv
	})
}

// StartReview replaces the review with a freshly minted one. Items of earlier
// reviews stay recognised, and ids join them.
func StartReview(d *worker.Doc, at string, round int, trigger string, ids []string) {
	reviewSection.Edit(d, func(old Review) Review {
		items := append(append([]string{}, old.Items...), ids...)
		return Review{At: at, Round: round, Trigger: trigger, Items: items}
	})
}
