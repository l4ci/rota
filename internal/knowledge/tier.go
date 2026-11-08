package knowledge

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/bulletfmt"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/pystr"
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

// Entry is one sidecar record, identified by its topic and title.
type Entry struct {
	Topic, Title string
	Tier         string
	Hits         int
	LastSeen     string
}

func key(topic, title string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(topic)) + "." + base64.RawURLEncoding.EncodeToString([]byte(title))
}

func decodeKey(k string) (topic, title string, ok bool) {
	a, b, found := strings.Cut(k, ".")
	if !found {
		return "", "", false
	}
	t, e1 := base64.RawURLEncoding.DecodeString(a)
	u, e2 := base64.RawURLEncoding.DecodeString(b)
	return string(t), string(u), e1 == nil && e2 == nil && key(string(t), string(u)) == k
}

// legacyOwners counts overlapping separators too: A:::B has two owners.
func legacyOwners(k string) [][2]string {
	var out [][2]string
	for i := 0; i+1 < len(k); i++ {
		if k[i:i+2] == "::" {
			out = append(out, [2]string{k[:i], k[i+2:]})
		}
	}
	return out
}

// Sidecar is a loaded knowledge-tier.json.
type Sidecar struct {
	path    string
	obj     *jsonx.Object // whole document, so unknown fields survive a rewrite
	ents    *jsonx.Object
	legacy  *jsonx.Object
	saveErr error // reserved-field collisions can be read but cannot be rewritten
}

func newSidecarDoc() *jsonx.Object {
	o := jsonx.NewObject()
	o.Set("version", json.Number("2"))
	o.Set("entries", jsonx.NewObject())
	return o
}

// LoadSidecar reads the sidecar at path. A missing file is an empty sidecar.
// Versions 1 and 2 are accepted; a missing version means version 1. Legacy
// records are migrated in memory, and unresolved owners are preserved separately.
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
	version := "1"
	if got, has := doc.Get("version"); has && got != nil {
		n, numeric := got.(json.Number)
		if !numeric || (n.String() != "1" && n.String() != "2") {
			return nil, fmt.Errorf("%s schema version mismatch — expected 1 or 2, got %s", filepath.Base(path), fmtJSON(got))
		}
		version = n.String()
	}
	ev, _ := doc.Get("entries")
	ents, ok := ev.(*jsonx.Object)
	if !ok {
		return nil, fmt.Errorf("%s: entries is not an object", filepath.Base(path))
	}
	legacy := jsonx.NewObject()
	var saveErr error
	if v, exists := doc.Get("legacyEntries"); exists && version == "2" {
		var valid bool
		legacy, valid = v.(*jsonx.Object)
		if !valid {
			return nil, fmt.Errorf("%s: legacyEntries is not an object", filepath.Base(path))
		}
	}
	if version == "1" {
		if _, exists := doc.Get("legacyEntries"); exists {
			saveErr = fmt.Errorf("%s: version 1 field legacyEntries conflicts with version 2 migration; preserve and rename that field before writing", filepath.Base(path))
		}
		migrated := jsonx.NewObject()
		for _, k := range ents.Keys() {
			v, _ := ents.Get(k)
			owners := legacyOwners(k)
			if len(owners) == 1 {
				migrated.Set(key(owners[0][0], owners[0][1]), v)
			} else {
				legacy.Set(k, v)
			}
		}
		ents = migrated
		doc.Set("entries", ents)
	}
	if version == "2" {
		for _, k := range ents.Keys() {
			if _, _, ok := decodeKey(k); !ok {
				return nil, fmt.Errorf("%s: invalid encoded entry key %q", filepath.Base(path), k)
			}
		}
	}
	doc.Set("version", json.Number("2"))
	if len(legacy.Keys()) > 0 && saveErr == nil {
		doc.Set("legacyEntries", legacy)
	}
	return &Sidecar{path: path, obj: doc, ents: ents, legacy: legacy, saveErr: saveErr}, nil
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
	if s.saveErr != nil {
		return s.saveErr
	}
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

// checkIdentity refuses any possible owner of an unresolved legacy record.
func (s *Sidecar) checkIdentity(topic, title string) error {
	if s.legacy == nil {
		return nil
	}
	for _, raw := range s.legacy.Keys() {
		owners := legacyOwners(raw)
		needed := false
		var labels []string
		for _, owner := range owners {
			labels = append(labels, fmt.Sprintf("(%q, %q)", owner[0], owner[1]))
			if owner == [2]string{topic, title} {
				needed = true
			}
		}
		if needed {
			return fmt.Errorf("%w: %s ambiguous legacy key %q has possible owners %s; repair legacyEntries by assigning the record to the intended encoded topic/title entry; see docs/usage/learning.md#tier-identity-and-legacy-repair", ErrMultiMatch, filepath.Base(s.path), raw, strings.Join(labels, ", "))
		}
	}
	return nil
}

// Get returns the entry for (topic, title); ok is false when untracked.
func (s *Sidecar) Get(topic, title string) (Entry, bool, error) {
	if err := s.checkIdentity(topic, title); err != nil {
		return Entry{}, false, err
	}
	r := s.rec(key(topic, title))
	if r == nil {
		return Entry{}, false, nil
	}
	return entryOf(topic, title, r), true, nil
}

// Set assigns a tier, retaining hits and unknown fields. It returns the previous tier.
func (s *Sidecar) Set(topic, title, tier, today string) (previous string, err error) {
	if err = s.checkIdentity(topic, title); err != nil {
		return "", err
	}
	k := key(topic, title)
	if r := s.rec(k); r != nil {
		if v, ok := r.Get("tier"); ok {
			previous, _ = v.(string)
		}
		r.Set("tier", tier)
		return previous, nil
	}
	s.ents.Set(k, newRec(tier, 0, today))
	return "", nil
}

// Init adds a provisional record at zero hits unless the pair is already tracked.
func (s *Sidecar) Init(topic, title, today string) (bool, error) {
	if err := s.checkIdentity(topic, title); err != nil {
		return false, err
	}
	k := key(topic, title)
	if s.rec(k) != nil {
		return false, nil
	}
	s.ents.Set(k, newRec(Provisional, 0, today))
	return true, nil
}

// Bump counts one hit, starting an untracked pair as provisional.
func (s *Sidecar) Bump(topic, title, today string) (Entry, error) {
	if err := s.checkIdentity(topic, title); err != nil {
		return Entry{}, err
	}
	k := key(topic, title)
	if r := s.rec(k); r != nil {
		e := entryOf(topic, title, r)
		r.Set("hits", json.Number(fmt.Sprint(e.Hits+1)))
		r.Set("lastSeen", today)
		return entryOf(topic, title, r), nil
	}
	r := newRec(Provisional, 1, today)
	s.ents.Set(k, r)
	return entryOf(topic, title, r), nil
}

// Promote moves a tracked pair to confirmed.
func (s *Sidecar) Promote(topic, title string) error {
	if err := s.checkIdentity(topic, title); err != nil {
		return err
	}
	if r := s.rec(key(topic, title)); r != nil {
		r.Set("tier", Confirmed)
	}
	return nil
}

// List returns entries in file order. An unresolved legacy owner prevents a full list.
func (s *Sidecar) List(tier string) ([]Entry, error) {
	if s.legacy != nil {
		for _, raw := range s.legacy.Keys() {
			for _, owner := range legacyOwners(raw) {
				if owner[0] != GlossaryTopic {
					if err := s.checkIdentity(owner[0], owner[1]); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	out := []Entry{}
	for _, k := range s.ents.Keys() {
		topic, title, ok := decodeKey(k)
		if !ok || topic == GlossaryTopic {
			continue
		}
		r := s.rec(k)
		if r == nil {
			continue
		}
		e := entryOf(topic, title, r)
		if tier == "" || e.Tier == tier {
			out = append(out, e)
		}
	}
	return out, nil
}

// Rekey moves one record between topics; Glossary and missing records are left alone.
func (s *Sidecar) Rekey(oldTopic, oldTitle, newTopic string) (bool, error) {
	if oldTopic == GlossaryTopic {
		return false, nil
	}
	return s.move(oldTopic, oldTitle, newTopic, oldTitle)
}

// Delete removes a record; Glossary and missing records are left alone.
func (s *Sidecar) Delete(topic, title string) (bool, error) {
	if topic == GlossaryTopic {
		return false, nil
	}
	if err := s.checkIdentity(topic, title); err != nil {
		return false, err
	}
	k := key(topic, title)
	if s.rec(k) == nil {
		return false, nil
	}
	s.ents.Delete(k)
	return true, nil
}

// Retitle moves one record to a new title within its topic.
func (s *Sidecar) Retitle(topic, oldTitle, newTitle string) (bool, error) {
	if topic == GlossaryTopic {
		return false, nil
	}
	return s.move(topic, oldTitle, topic, newTitle)
}

func (s *Sidecar) move(oldTopic, oldTitle, newTopic, newTitle string) (bool, error) {
	if err := s.checkIdentity(oldTopic, oldTitle); err != nil {
		return false, err
	}
	if err := s.checkIdentity(newTopic, newTitle); err != nil {
		return false, err
	}
	k := key(oldTopic, oldTitle)
	r := s.rec(k)
	if r == nil {
		return false, nil
	}
	s.ents.Delete(k)
	s.ents.Set(key(newTopic, newTitle), r)
	return true, nil
}

// RekeyTopic moves exactly the old topic's records, appending them in their original order.
func (s *Sidecar) RekeyTopic(oldTopic, newTopic string) (int, error) {
	// Preflight both source owners and every destination before changing anything.
	if s.legacy != nil {
		for _, raw := range s.legacy.Keys() {
			for _, owner := range legacyOwners(raw) {
				if owner[0] == oldTopic {
					if err := s.checkIdentity(owner[0], owner[1]); err != nil {
						return 0, err
					}
				}
			}
		}
	}
	var titles []string
	for _, k := range s.ents.Keys() {
		topic, title, ok := decodeKey(k)
		if ok && topic == oldTopic {
			if err := s.checkIdentity(newTopic, title); err != nil {
				return 0, err
			}
			titles = append(titles, title)
		}
	}
	for _, title := range titles {
		k := key(oldTopic, title)
		v, _ := s.ents.Get(k)
		s.ents.Delete(k)
		s.ents.Set(key(newTopic, title), v)
	}
	return len(titles), nil
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
	sc, err := s.effective(scope)
	if err != nil {
		return Entry{}, false, err
	}
	e, ok, err := sc.Get(topic, title)
	if err != nil || ok {
		return e, ok, err
	}
	km, err := s.KnowledgePath(scope)
	if err != nil {
		return Entry{}, false, err
	}
	content, err := ReadFile(km)
	if err != nil {
		return Entry{}, false, err
	}
	for _, t := range section.Topics(content) {
		if t.Name != topic {
			continue
		}
		for _, line := range pystr.Splitlines(t.Body) {
			if got, ok := bulletfmt.Title(line); ok && got == title {
				if _, err := sc.Init(topic, title, s.today()); err != nil {
					return Entry{}, false, err
				}
				return sc.Get(topic, title)
			}
		}
	}
	return Entry{}, false, nil
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
	if err := s.preflightTier(scope, func(sc *Sidecar) error { return sc.checkIdentity(topic, title) }); err != nil {
		return "", false, err
	}
	// Buffered hits predate this write: fold them first so the set is not
	// replayed over by hits that were already counted (a demotion would
	// otherwise promote again on read).
	if _, err := s.FoldHits(scope); err != nil {
		return "", false, err
	}
	err = Update(p, func(sc *Sidecar) (bool, error) {
		previous, err = sc.Set(topic, title, tier, s.today())
		if err != nil {
			return false, err
		}
		changed = previous != tier
		return true, nil
	})
	return previous, changed, err
}

// TierList lists scope's tracked entries, optionally for one tier.
func (s Store) TierList(scope, tier string) ([]Entry, error) {
	sc, err := s.effectiveBackfilled(scope)
	if err != nil {
		return nil, err
	}
	return sc.List(tier)
}

// effectiveBackfilled is effective plus, in memory, a provisional entry for
// every titled bullet of scope's KNOWLEDGE.md that has none, so bullets written
// before tiers existed read like new ones. It is the lazy form of the retired
// hv-knowledge-migrate and serves `tier get` and `tier list`; `query` stays
// read-only. Nothing is written: a read must not dirty the tracked sidecar.
func (s Store) effectiveBackfilled(scope string) (*Sidecar, error) {
	sc, err := s.effective(scope)
	if err != nil {
		return nil, err
	}
	km, err := s.KnowledgePath(scope)
	if err != nil {
		return sc, nil
	}
	content, err := ReadFile(km)
	if err != nil || content == "" {
		return sc, nil
	}
	for _, t := range section.Topics(content) {
		if t.Name == GlossaryTopic {
			continue
		}
		for _, line := range pystr.Splitlines(t.Body) {
			if title, ok := bulletfmt.Title(line); ok {
				if _, err := sc.Init(t.Name, title, s.today()); err != nil {
					return nil, err
				}
			}
		}
	}
	return sc, nil
}
