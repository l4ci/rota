package knowledge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/section"
)

// Tiers a bullet can hold.
const (
	Provisional = "provisional"
	Confirmed   = "confirmed"
	Deprecated  = "deprecated"
)

// ValidTier reports whether t is one of the three tiers.
func ValidTier(t string) bool { return t == Provisional || t == Confirmed || t == Deprecated }

// GlossaryTopic holds terms, which are canonical names and never tiered.
const GlossaryTopic = "Glossary"

// Today is the date stamped on new entries; tests replace it.
var Today = func() string { return time.Now().Format("2006-01-02") }

// Entry is one sidecar record, keyed "<topic>::<title>" in the file.
type Entry struct {
	Topic, Title string
	Tier         string
	Hits         int
	LastSeen     string
}

func key(topic, title string) string { return topic + "::" + title }

// Sidecar is a loaded knowledge-tier.json.
type Sidecar struct {
	path string
	obj  *jsonx.Object // whole document, so unknown fields survive a rewrite
	ents *jsonx.Object
}

func newSidecarDoc() *jsonx.Object {
	o := jsonx.NewObject()
	o.Set("version", json.Number("1"))
	o.Set("entries", jsonx.NewObject())
	return o
}

// LoadSidecar reads the sidecar at path. A missing file is an empty sidecar.
// A version other than 1 is an error ("<file> schema version mismatch …");
// a file without a version is treated as version 1, as load_sidecar does.
func LoadSidecar(path string) (*Sidecar, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		d := newSidecarDoc()
		ents, _ := d.Get("entries")
		return &Sidecar{path: path, obj: d, ents: ents.(*jsonx.Object)}, nil
	}
	if err != nil {
		return nil, err
	}
	v, err := jsonx.Decode(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	doc, ok := v.(*jsonx.Object)
	if !ok {
		return nil, fmt.Errorf("%s: not a JSON object", filepath.Base(path))
	}
	if err := checkVersion(path, doc); err != nil {
		return nil, err
	}
	ev, _ := doc.Get("entries")
	ents, ok := ev.(*jsonx.Object)
	if !ok {
		return nil, fmt.Errorf("%s: entries is not an object", filepath.Base(path))
	}
	return &Sidecar{path: path, obj: doc, ents: ents}, nil
}

// checkVersion applies load_sidecar's schema rule to doc: version 1, or no
// version at all, which is stamped 1.
func checkVersion(path string, doc *jsonx.Object) error {
	if got, has := doc.Get("version"); has && got != nil {
		if n, isNum := got.(json.Number); !isNum || n.String() != "1" {
			return fmt.Errorf("%s schema version mismatch — expected 1, got %s", filepath.Base(path), fmtJSON(got))
		}
		return nil
	}
	doc.Set("version", json.Number("1"))
	return nil
}

// writeJSON creates the parent directory and writes v as indented JSON.
func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return err
	}
	return fsio.WriteJSONAtomic(path, v)
}

func fmtJSON(v any) string {
	b, err := jsonx.MarshalCompact(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// Save writes the sidecar atomically.
func (s *Sidecar) Save() error {
	return writeJSON(s.path, s.obj)
}

func entryOf(topic, title string, rec *jsonx.Object) Entry {
	e := Entry{Topic: topic, Title: title}
	if v, ok := rec.Get("tier"); ok {
		e.Tier, _ = v.(string)
	}
	if v, ok := rec.Get("hits"); ok {
		if n, ok := v.(json.Number); ok {
			i, _ := n.Int64()
			e.Hits = int(i)
		}
	}
	if v, ok := rec.Get("lastSeen"); ok {
		e.LastSeen, _ = v.(string)
	}
	return e
}

func (s *Sidecar) rec(k string) *jsonx.Object {
	v, ok := s.ents.Get(k)
	if !ok {
		return nil
	}
	o, _ := v.(*jsonx.Object)
	return o
}

func newRec(tier string, hits int, today string) *jsonx.Object {
	o := jsonx.NewObject()
	o.Set("tier", tier)
	o.Set("hits", json.Number(fmt.Sprint(hits)))
	o.Set("lastSeen", today)
	return o
}

// Get returns the entry for (topic, title); ok is false when untracked.
func (s *Sidecar) Get(topic, title string) (Entry, bool) {
	r := s.rec(key(topic, title))
	if r == nil {
		return Entry{}, false
	}
	return entryOf(topic, title, r), true
}

// Set puts (topic, title) on tier. An untracked entry starts at 0 hits. It
// returns the previous tier ("" when there was none).
func (s *Sidecar) Set(topic, title, tier string) (previous string) {
	k := key(topic, title)
	if r := s.rec(k); r != nil {
		if v, ok := r.Get("tier"); ok {
			previous, _ = v.(string)
		}
		r.Set("tier", tier)
		return previous
	}
	s.ents.Set(k, newRec(tier, 0, Today()))
	return ""
}

// Init adds (topic, title) as provisional with 0 hits unless it is tracked.
func (s *Sidecar) Init(topic, title string) (created bool) {
	k := key(topic, title)
	if s.rec(k) != nil {
		return false
	}
	s.ents.Set(k, newRec(Provisional, 0, Today()))
	return true
}

// Bump registers a hit: an existing entry gets hits+1 and today's date, a
// new one starts provisional at 1 hit (bump_hit).
func (s *Sidecar) Bump(topic, title string) Entry {
	k := key(topic, title)
	if r := s.rec(k); r != nil {
		e := entryOf(topic, title, r)
		r.Set("hits", json.Number(fmt.Sprint(e.Hits+1)))
		r.Set("lastSeen", Today())
		return entryOf(topic, title, r)
	}
	r := newRec(Provisional, 1, Today())
	s.ents.Set(k, r)
	return entryOf(topic, title, r)
}

// Promote moves a tracked entry to confirmed.
func (s *Sidecar) Promote(topic, title string) {
	if r := s.rec(key(topic, title)); r != nil {
		r.Set("tier", Confirmed)
	}
}

// List returns the entries in file order, skipping Glossary and, when tier is
// not empty, every other tier.
func (s *Sidecar) List(tier string) []Entry {
	out := []Entry{}
	for _, k := range s.ents.Keys() {
		topic, title, ok := strings.Cut(k, "::")
		if !ok || topic == GlossaryTopic {
			continue
		}
		r := s.rec(k)
		if r == nil {
			continue
		}
		e := entryOf(topic, title, r)
		if tier != "" && e.Tier != tier {
			continue
		}
		out = append(out, e)
	}
	return out
}

// Rekey moves oldKey to newKey in place of nothing: a missing oldKey and any
// Glossary key are left alone. It reports whether anything moved.
func (s *Sidecar) Rekey(oldTopic, oldTitle, newTopic string) bool {
	if oldTopic == GlossaryTopic {
		return false
	}
	ok := key(oldTopic, oldTitle)
	r := s.rec(ok)
	if r == nil {
		return false
	}
	s.ents.Delete(ok)
	s.ents.Set(key(newTopic, oldTitle), r)
	return true
}

// Retitle moves the entry of a retitled bullet within one topic. A missing
// entry and the Glossary topic are left alone. It reports whether anything
// moved.
func (s *Sidecar) Retitle(topic, oldTitle, newTitle string) bool {
	if topic == GlossaryTopic {
		return false
	}
	ok := key(topic, oldTitle)
	r := s.rec(ok)
	if r == nil {
		return false
	}
	s.ents.Delete(ok)
	s.ents.Set(key(topic, newTitle), r)
	return true
}

// RekeyTopic moves every "<old>::*" entry to "<new>::*" and reports how many
// moved. Moved entries land after the existing ones, as in the Python helper.
func (s *Sidecar) RekeyTopic(oldTopic, newTopic string) int {
	prefix := oldTopic + "::"
	type kv struct {
		k string
		v any
	}
	var moved []kv
	for _, k := range s.ents.Keys() {
		if strings.HasPrefix(k, prefix) {
			v, _ := s.ents.Get(k)
			moved = append(moved, kv{newTopic + "::" + k[len(prefix):], v})
			s.ents.Delete(k)
		}
	}
	for _, m := range moved {
		s.ents.Set(m.k, m.v)
	}
	return len(moved)
}

// Update runs a locked read-modify-write of the sidecar at path. fn mutates
// the loaded sidecar; the file is rewritten only when fn returns write=true.
func Update(path string, fn func(*Sidecar) (write bool, err error)) error {
	return fsio.Locked(path, fsio.LockTimeout, func() error {
		s, err := LoadSidecar(path)
		if err != nil {
			return err
		}
		write, err := fn(s)
		if err != nil || !write {
			return err
		}
		return s.Save()
	})
}

// TierGet returns the entry for (topic, title) in scope's sidecar. Glossary
// terms are never tracked.
func (s Store) TierGet(scope, topic, title string) (Entry, bool, error) {
	if topic == GlossaryTopic {
		return Entry{}, false, nil
	}
	p, err := s.TierPath(scope)
	if err != nil {
		return Entry{}, false, err
	}
	s.backfillTiers(scope)
	sc, err := LoadSidecar(p)
	if err != nil {
		return Entry{}, false, err
	}
	e, ok := sc.Get(topic, title)
	return e, ok, nil
}

// TierSet puts (topic, title) on tier and returns the previous tier ("" for an
// untracked entry) and whether anything changed. Glossary is a no-op.
func (s Store) TierSet(scope, topic, title, tier string) (previous string, changed bool, err error) {
	if topic == GlossaryTopic {
		return "", false, nil
	}
	p, err := s.TierPath(scope)
	if err != nil {
		return "", false, err
	}
	err = Update(p, func(sc *Sidecar) (bool, error) {
		previous = sc.Set(topic, title, tier)
		changed = previous != tier
		return true, nil
	})
	return previous, changed, err
}

// TierList lists scope's tracked entries, optionally for one tier.
func (s Store) TierList(scope, tier string) ([]Entry, error) {
	p, err := s.TierPath(scope)
	if err != nil {
		return nil, err
	}
	s.backfillTiers(scope)
	sc, err := LoadSidecar(p)
	if err != nil {
		return nil, err
	}
	return sc.List(tier), nil
}

// backfillTiers registers every titled bullet of scope's KNOWLEDGE.md that has
// no sidecar entry as provisional, so bullets written before tiers existed read
// like new ones. It is the lazy form of the retired hv-knowledge-migrate and
// runs on `tier get` and `tier list`; `query` stays read-only. Like initTier it is best effort: a failure leaves the
// read to see the untracked bullets rather than failing it.
func (s Store) backfillTiers(scope string) {
	km, err := s.KnowledgePath(scope)
	if err != nil {
		return
	}
	p, err := s.TierPath(scope)
	if err != nil {
		return
	}
	content, err := ReadFile(km)
	if err != nil || content == "" {
		return
	}
	_ = Update(p, func(sc *Sidecar) (bool, error) {
		changed := false
		for _, t := range section.Topics(content) {
			if t.Name == GlossaryTopic {
				continue
			}
			for _, line := range section.Lines(t.Body) {
				if m := titleRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
					changed = sc.Init(t.Name, m[1]) || changed
				}
			}
		}
		return changed, nil
	})
}
