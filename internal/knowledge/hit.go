package knowledge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
)

// DefaultPromoteThreshold is used when learn.promoteThreshold is unset or
// not a positive integer.
const DefaultPromoteThreshold = 3

// HitResult reports Hit.
type HitResult struct {
	Entry            Entry
	Promoted         bool
	PromotionBlocked bool
	Changed          bool
}

// PromoteThreshold reads learn.promoteThreshold from the project config.
func (s Store) PromoteThreshold() int {
	cfg := config.Load(filepath.Join(s.Root, ".rota", "config.json"))
	if v, ok := config.Lookup(cfg, "learn.promoteThreshold"); ok {
		if n, isNum := v.(json.Number); isNum {
			if i, err := n.Int64(); err == nil && i > 0 {
				return int(i)
			}
		}
	}
	return DefaultPromoteThreshold
}

// Hit registers that a bullet was consulted: it bumps the hit counter and
// promotes a provisional bullet to confirmed once hits reach the threshold,
// unless the pair sits in the contradiction queue. Glossary terms are never
// tiered, so a Glossary hit changes nothing.
func (s Store) Hit(scope, topic, title string) (HitResult, error) {
	if topic == GlossaryTopic {
		return HitResult{Entry: Entry{Topic: topic, Title: title, Tier: Provisional}}, nil
	}
	p, err := s.TierPath(scope)
	if err != nil {
		return HitResult{}, err
	}
	threshold := s.PromoteThreshold()
	var res HitResult
	err = Update(p, func(sc *Sidecar) (bool, error) {
		e := sc.Bump(topic, title)
		if e.Tier == Provisional && e.Hits >= threshold {
			has, err := s.HasContradiction(topic, title)
			if err != nil {
				return false, err
			}
			if has {
				res.PromotionBlocked = true
			} else {
				sc.Promote(topic, title)
				e.Tier = Confirmed
				res.Promoted = true
			}
		}
		res.Entry = e
		res.Changed = true
		return true, nil
	})
	return res, err
}

// Contradiction is one pending entry of the queue.
type Contradiction struct{ Topic, Title, Text, LoggedAt string }

func (s Store) queuePath() string {
	return filepath.Join(s.Root, ".rota", "knowledge-contradictions.json")
}

// loadQueue reads the queue; a missing file is an empty queue, a version
// other than 1 is an error.
func (s Store) loadQueue() (*jsonx.Object, []any, error) {
	var doc *jsonx.Object
	raw, err := os.ReadFile(s.queuePath())
	switch {
	case os.IsNotExist(err):
	case err != nil:
		return nil, nil, err
	default:
		v, err := jsonx.Decode(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", filepath.Base(s.queuePath()), err)
		}
		if doc, _ = v.(*jsonx.Object); doc == nil {
			return nil, nil, fmt.Errorf("%s: not a JSON object", filepath.Base(s.queuePath()))
		}
	}
	if doc == nil {
		doc = jsonx.NewObject()
		doc.Set("version", json.Number("1"))
		doc.Set("pending", []any{})
		return doc, []any{}, nil
	}
	if err := checkVersion(s.queuePath(), doc); err != nil {
		return nil, nil, err
	}
	pv, _ := doc.Get("pending")
	pending, _ := pv.([]any)
	if pending == nil {
		pending = []any{}
	}
	return doc, pending, nil
}

func entryString(o *jsonx.Object, k string) string {
	v, _ := o.Get(k)
	s, _ := v.(string)
	return s
}

// Contradictions lists the pending queue in order.
func (s Store) Contradictions() ([]Contradiction, error) {
	_, pending, err := s.loadQueue()
	if err != nil {
		return nil, err
	}
	out := []Contradiction{}
	for _, p := range pending {
		if o, ok := p.(*jsonx.Object); ok {
			out = append(out, Contradiction{entryString(o, "topic"), entryString(o, "title"), entryString(o, "correctionText"), entryString(o, "loggedAt")})
		}
	}
	return out, nil
}

// HasContradiction reports whether (topic, title) is in the queue.
func (s Store) HasContradiction(topic, title string) (bool, error) {
	list, err := s.Contradictions()
	if err != nil {
		return false, err
	}
	for _, c := range list {
		if c.Topic == topic && c.Title == title {
			return true, nil
		}
	}
	return false, nil
}

// AddContradiction appends an entry (no dedup) and returns the queue length.
func (s Store) AddContradiction(topic, title, text string) (int, error) {
	n := 0
	err := fsio.Locked(s.queuePath(), fsio.LockTimeout, func() error {
		doc, pending, err := s.loadQueue()
		if err != nil {
			return err
		}
		e := jsonx.NewObject()
		e.Set("topic", topic)
		e.Set("title", title)
		e.Set("correctionText", text)
		e.Set("loggedAt", time.Now().UTC().Format("2006-01-02T15:04:05Z"))
		pending = append(pending, e)
		doc.Set("pending", pending)
		n = len(pending)
		return s.saveQueue(doc)
	})
	return n, err
}

// ClearContradictions empties the queue and returns how many entries it held.
// Like the old helper it rewrites the file even when the queue was empty.
func (s Store) ClearContradictions() (int, error) {
	n := 0
	err := fsio.Locked(s.queuePath(), fsio.LockTimeout, func() error {
		doc, pending, err := s.loadQueue()
		if err != nil {
			return err
		}
		n = len(pending)
		doc.Set("pending", []any{})
		return s.saveQueue(doc)
	})
	return n, err
}

// ClearContradiction drops every entry for (topic, title), keeps the rest in
// order and returns how many it dropped.
func (s Store) ClearContradiction(topic, title string) (int, error) {
	n := 0
	err := fsio.Locked(s.queuePath(), fsio.LockTimeout, func() error {
		doc, pending, err := s.loadQueue()
		if err != nil {
			return err
		}
		kept := []any{}
		for _, p := range pending {
			if o, ok := p.(*jsonx.Object); ok && entryString(o, "topic") == topic && entryString(o, "title") == title {
				n++
				continue
			}
			kept = append(kept, p)
		}
		if n == 0 {
			return nil
		}
		doc.Set("pending", kept)
		return s.saveQueue(doc)
	})
	return n, err
}

func (s Store) saveQueue(doc *jsonx.Object) error {
	return writeJSON(s.queuePath(), doc)
}
