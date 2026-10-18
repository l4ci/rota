package milestone

import (
	"github.com/l4ci/rota/internal/acceptance"
	"github.com/l4ci/rota/internal/itembody"
)

// minFeatures is how many feature children a milestone needs before Check
// holds them to its criteria.
const minFeatures = 3

// Child is one issue of a milestone as Check reads it: its id as the backend
// prints it, its type letter (B, F or T) and its body.
type Child struct {
	ID, Type, Body string
}

// Unknown is a feature whose Serves line names criteria the milestone lacks.
type Unknown struct {
	ID  string
	IDs []string
}

// Missing is a REMOVED feature without a Reason or Migration.
type Missing struct {
	ID     string
	Fields []string
}

// BadTouches is a feature whose Touches name things the milestone does not list.
type BadTouches struct {
	ID      string
	Entries []string
}

// Report is what Check finds. Every list is non-nil.
type Report struct {
	Strict, BelowThreshold bool
	Features               int
	Criteria, Uncovered    []string
	NoChange               []string
	Unknown                []Unknown
	NoMigration            []Missing
	BadTouches             []BadTouches
}

// Ok is false on any fault; an uncovered criterion is a fault only in strict mode.
func (r Report) Ok() bool {
	if len(r.NoChange)+len(r.Unknown)+len(r.NoMigration)+len(r.BadTouches) > 0 {
		return false
	}
	return !(r.Strict && len(r.Uncovered) > 0)
}

// Check holds a milestone's feature children against its criteria and
// capabilities. Only features (Type F) count, and under three of them it
// reports no finding. Pure.
func Check(body string, children []Child, strict bool) Report {
	r := Report{Strict: strict, Criteria: []string{}, Uncovered: []string{}, NoChange: []string{},
		Unknown: []Unknown{}, NoMigration: []Missing{}, BadTouches: []BadTouches{}}
	for _, cr := range acceptance.IDs(body) {
		if cr.ID != "" {
			r.Criteria = append(r.Criteria, cr.ID)
		}
	}
	var feats []Child
	for _, ch := range children {
		if ch.Type == "F" {
			feats = append(feats, ch)
		}
	}
	r.Features = len(feats)
	if r.Features < minFeatures {
		r.BelowThreshold = true
		return r
	}
	known := map[string]bool{}
	for _, id := range r.Criteria {
		known[id] = true
	}
	caps := map[string]bool{}
	for _, n := range CapabilityNames(Capabilities(body)) {
		caps[n] = true
	}
	served := map[string]bool{}
	for _, f := range feats {
		c := itembody.ParseChange(f.Body)
		if !c.OK {
			r.NoChange = append(r.NoChange, f.ID)
			continue
		}
		var unknown []string
		for _, id := range c.Serves {
			if known[id] {
				served[id] = true
			} else {
				unknown = append(unknown, id)
			}
		}
		if len(unknown) > 0 {
			r.Unknown = append(r.Unknown, Unknown{ID: f.ID, IDs: unknown})
		}
		if c.Kind == "REMOVED" {
			var miss []string
			if c.Reason == "" {
				miss = append(miss, "Reason")
			}
			if c.Migration == "" {
				miss = append(miss, "Migration")
			}
			if len(miss) > 0 {
				r.NoMigration = append(r.NoMigration, Missing{ID: f.ID, Fields: miss})
			}
		}
		if sec, ok := itembody.Section(f.Body, itembody.TouchesHeadRe); ok {
			var bad []string
			for _, e := range itembody.Entries(sec) {
				if !caps[e] {
					bad = append(bad, e)
				}
			}
			if len(bad) > 0 {
				r.BadTouches = append(r.BadTouches, BadTouches{ID: f.ID, Entries: bad})
			}
		}
	}
	for _, id := range r.Criteria {
		if !served[id] {
			r.Uncovered = append(r.Uncovered, id)
		}
	}
	return r
}
