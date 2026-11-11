package worker

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/rotatree"
	"os"
	"slices"
	"sort"
	"sync"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
)

func fail(exit int, msg string) *exitcode.Error { return &exitcode.Error{Exit: exit, Message: msg} }

// RegistryPath is the registry file under the project root.
func RegistryPath(root string) string { return rotatree.Workers(root) }

// Registry is a loaded .rota/workers.json. The document stays private: callers
// read it through the accessors and write it through a Doc under Update.
type Registry struct {
	doc    *jsonx.Object
	Exists bool
}

// Doc is the registry document under an Update: the reads of Registry plus the
// writes. Keys it does not know are left alone.
type Doc struct{ Registry }

// LoadRegistry reads the registry strictly. A missing file is an empty pool
// ({"slots": []}, Exists false, like hvlib_io.load_json); a file that exists
// but cannot be read or is not a JSON object is an error wrapping
// fsio.ErrUnreadable, never an empty pool, so no verb acts on "no slot owns
// this" off a corrupt registry (#579, #712). Display and diagnostic paths that
// cannot fail use LoadRegistryTolerant.
func LoadRegistry(root string) (Registry, error) {
	p := RegistryPath(root)
	raw, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return Registry{doc: slotsDefault()}, nil
	}
	if err != nil {
		return Registry{}, fmt.Errorf("%w: %s: %v", fsio.ErrUnreadable, p, err)
	}
	v, err := jsonx.Decode(raw)
	if o, ok := v.(*jsonx.Object); err == nil && ok {
		return Registry{doc: o, Exists: true}, nil
	}
	return Registry{}, fmt.Errorf("%w: %s does not parse as a JSON object; fix or remove it", fsio.ErrUnreadable, p)
}

// LoadRegistryTolerant is the explicit opt-out of LoadRegistry: a missing or
// corrupt file reads as {"slots": []} with Exists false. For display and
// diagnostic readers that cannot refuse.
func LoadRegistryTolerant(root string) Registry {
	if o, ok := fsio.LoadJSON(RegistryPath(root), nil).(*jsonx.Object); ok {
		return Registry{doc: o, Exists: true}
	}
	return Registry{doc: slotsDefault()}
}

// ParseRegistry reads a registry from its JSON text, tolerant like
// LoadRegistryTolerant: text that is not a JSON object reads as no slots. It is
// the in-memory twin of the disk read, for an Env.Registry that has no file.
func ParseRegistry(raw string) Registry {
	v, err := jsonx.Decode([]byte(raw))
	if o, ok := v.(*jsonx.Object); err == nil && ok {
		return Registry{doc: o, Exists: true}
	}
	return Registry{doc: slotsDefault()}
}

// CheckRegistry is nil when the registry is missing or a JSON object, and an
// error when it exists but cannot be read or parsed.
func CheckRegistry(root string) error {
	_, err := LoadRegistry(root)
	return err
}

// Slots lists the slot objects of the registry.
func (r Registry) Slots() []*Slot {
	var out []*Slot
	for _, o := range r.objects("slots") {
		out = append(out, AsSlot(o))
	}
	return out
}

// Slot finds a slot by name.
func (r Registry) Slot(name string) *Slot {
	for _, s := range r.Slots() {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

// Round is the round number the registry last recorded; ok is false before a
// start. It is the one decode of the `round` field.
func (r Registry) Round() (int, bool) {
	v, _ := r.doc.Get("round")
	return jsonx.Int(v)
}

// Host is the round host `round start` recorded, "" when none.
func (r Registry) Host() host.Kind { return host.KindOf(jsonx.Str(r.doc, "host")) }

// Layout is the pane layout `rota layout split|tabs` recorded for the round:
// "split" or "", where "" means tabs (the default of a new round).
func (r Registry) Layout() string { return jsonx.Str(r.doc, "layout") }

// CLIPane is the herdr pane that launched the round, recorded by `rota layout
// split` run from it: the split keeps it on top of the orchestrator. "" when
// unknown.
func (r Registry) CLIPane() string { return jsonx.Str(r.doc, "cliPane") }

// Session is the pane session the pool registered, "" when none.
func (r Registry) Session() string { return jsonx.Str(r.doc, "session") }

// Scope is the recorded round scope, "" when none.
func (r Registry) Scope() string { return jsonx.Str(r.doc, "scope") }

// SlotCount is how many roster slots `round start` put in the round (--slots,
// else round.slots), 0 when no start recorded one.
func (r Registry) SlotCount() int {
	v, _ := r.doc.Get("slotCount")
	n, _ := jsonx.Int(v)
	return n
}

// Slate is the recorded slate of a slate-scoped round, nil when none.
func (r Registry) Slate() []string {
	var out []string
	for _, e := range r.list("slate") {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// list is the raw entries under key, nil when the key is missing or not a list.
func (r Registry) list(key string) []any { return listOf(r.doc, key) }

// listOf is the raw entries under key of o, nil when the key is missing or not
// a list. It is the one `.([]any)` decode of a registry or record field.
func listOf(o *jsonx.Object, key string) []any {
	raw, _ := o.Get(key)
	l, _ := raw.([]any)
	return l
}

// Section is the raw value under key, the read half of the shared document
// seam: the packages that own a round-level record (escalation, limits, the
// architecture review) decode their own section with it.
func (r Registry) Section(key string) (any, bool) { return r.doc.Get(key) }

// Objects is the object entries under key, skipping any entry that is not an
// object; a missing or malformed list reads as empty.
func (r Registry) Objects(key string) []*jsonx.Object { return r.objects(key) }

// SetSection stores v under key, the write half of the seam. It is meant to be
// called inside Update, so every section shares the registry's one lock.
func (d *Doc) SetSection(key string, v any) { d.doc.Set(key, v) }

// objects is the one decode of a registry list: the object entries under key,
// skipping any entry that is not an object.
func (r Registry) objects(key string) []*jsonx.Object {
	var out []*jsonx.Object
	for _, e := range r.list(key) {
		if o, ok := e.(*jsonx.Object); ok {
			out = append(out, o)
		}
	}
	return out
}

// QueuedPR is a record of the registry's `prs`: an open PR whose slot moved on
// to another issue. The issue and its claim stay taken while it waits.
type QueuedPR struct {
	Issue, Branch, PR, Base, From, ClaimID string
	DecisionContext                        string // assignment snapshot retained when its slot is reused
	Round                                  int
	Relays                                 []any
	// ReviewSeen is the review cursor the PR's slot held when it moved on
	// (Slot.ReviewSeen): input after it is new. "" on a record from before
	// cursors were kept.
	ReviewSeen string
	// Bundle is the member issues the PR closes beside Issue (a bundled slot's
	// PR that was queued), none for one issue.
	Bundle []string
}

// Issues is every issue the queued PR holds: Issue first, then its members.
func (q QueuedPR) Issues() []string {
	if q.Issue == "" {
		return nil
	}
	return append([]string{q.Issue}, q.Bundle...)
}

func queuedFrom(o *jsonx.Object) QueuedPR {
	rv, _ := o.Get("round")
	round, _ := jsonx.Int(rv)
	return QueuedPR{
		Issue: jsonx.Str(o, "issue"), Branch: jsonx.Str(o, "branch"), PR: jsonx.Str(o, "pr"),
		Base: jsonx.Str(o, "base"), From: jsonx.Str(o, "from"), ClaimID: jsonx.Str(o, "claimId"),
		DecisionContext: jsonx.Str(o, "decisionContext"), ReviewSeen: jsonx.Str(o, "reviewSeen"),
		Round: round, Relays: listOf(o, "relays"), Bundle: strList(o, "bundle"),
	}
}

func (q QueuedPR) object() *jsonx.Object {
	relays := q.Relays
	if relays == nil {
		relays = []any{}
	}
	o := jsonx.NewObject()
	o.Set("issue", q.Issue)
	o.Set("branch", q.Branch)
	o.Set("pr", q.PR)
	o.Set("base", q.Base)
	o.Set("from", q.From)
	o.Set("claimId", q.ClaimID)
	o.Set("round", q.Round)
	o.Set("relays", relays)
	setStrList(o, "bundle", q.Bundle)
	if q.DecisionContext != "" {
		o.Set("decisionContext", q.DecisionContext)
	}
	if q.ReviewSeen != "" {
		o.Set("reviewSeen", q.ReviewSeen)
	}
	return o
}

// PRs lists the queued PR records.
func (r Registry) PRs() []QueuedPR {
	var out []QueuedPR
	for _, o := range r.queued() {
		out = append(out, queuedFrom(o))
	}
	return out
}

func (r Registry) queued() []*jsonx.Object { return r.objects("prs") }

// QueuedPR finds a queued record by PR ref (`#N`, `N` or a PR URL), matching
// on the PR number.
func (r Registry) QueuedPR(ref string) *QueuedPR {
	if o := r.queuedByRef(ref); o != nil {
		q := queuedFrom(o)
		return &q
	}
	return nil
}

func (r Registry) queuedByRef(ref string) *jsonx.Object {
	n, ok := PRRefNumber(ref)
	if !ok {
		return nil
	}
	for _, q := range r.queued() {
		if m, ok := PRRefNumber(jsonx.Str(q, "pr")); ok && m == n {
			return q
		}
	}
	return nil
}

// QueuedIssue finds the queued record holding an issue (backend spelling,
// compared case-insensitively, `#` ignored).
func (r Registry) QueuedIssue(id string) *QueuedPR {
	want := NormID(id)
	for _, o := range r.queued() {
		if want != "" && NormID(jsonx.Str(o, "issue")) == want {
			q := queuedFrom(o)
			return &q
		}
	}
	return nil
}

// QueuePR appends a record, replacing one for the same issue and slot so a
// repeated call does not duplicate it. The two attempts of a best-of:2 issue
// each keep their own record.
func (d *Doc) QueuePR(rec QueuedPR) {
	d.dropQueued(func(q *jsonx.Object) bool {
		return jsonx.Str(q, "issue") == rec.Issue && jsonx.Str(q, "from") == rec.From
	})
	d.doc.Set("prs", append(d.list("prs"), rec.object()))
}

// QueuedFrom is the newest queued record sent from a slot, nil when none.
func (r Registry) QueuedFrom(slot string) *QueuedPR {
	var out *QueuedPR
	for _, o := range r.queued() {
		if jsonx.Str(o, "from") == slot {
			q := queuedFrom(o)
			out = &q
		}
	}
	return out
}

// AppendQueuedRelay logs a relay on the newest queued record sent from slot,
// where the gate of that PR reads it. False when the slot has no such record.
func (d *Doc) AppendQueuedRelay(slot string, entry *jsonx.Object) bool {
	var hit *jsonx.Object
	for _, o := range d.queued() {
		if jsonx.Str(o, "from") == slot {
			hit = o
		}
	}
	if hit == nil {
		return false
	}
	hit.Set("relays", append(listOf(hit, "relays"), entry))
	return true
}

// DropQueued removes the records match accepts.
func (d *Doc) DropQueued(match func(QueuedPR) bool) {
	d.dropQueued(func(o *jsonx.Object) bool { return match(queuedFrom(o)) })
}

func (d *Doc) dropQueued(match func(*jsonx.Object) bool) {
	list := d.list("prs")
	if len(list) == 0 {
		return
	}
	var keep []any
	for _, e := range list {
		if o, ok := e.(*jsonx.Object); ok && match(o) {
			continue
		}
		keep = append(keep, e)
	}
	if keep == nil {
		keep = []any{}
	}
	d.doc.Set("prs", keep)
}

// RetargetQueued renames the issue of every queued record to what issue
// returns, for those it accepts.
func (d *Doc) RetargetQueued(issue func(QueuedPR) (string, bool)) {
	for _, o := range d.queued() {
		if n, ok := issue(queuedFrom(o)); ok {
			o.Set("issue", n)
		}
	}
}

// RemoveQueuedPR drops the record for a PR ref, locked.
func RemoveQueuedPR(root, ref string) error {
	return Update(root, func(d *Doc) {
		if q := d.queuedByRef(ref); q != nil {
			d.dropQueued(func(o *jsonx.Object) bool { return o == q })
		}
	})
}

// LogRelays keeps the relays of a PR whose slot was freed without queuing it
// (round return, reclaim), under the registry's `relayLog`. Only the gate reads
// it: unlike a queued record it holds no issue, so candidates, claims,
// readiness and the autopilot ignore it (#1853). A repeated call for the same
// PR replaces the entry.
func (d *Doc) LogRelays(pr string, relays []any) {
	n, ok := PRRefNumber(pr)
	if !ok || len(relays) == 0 {
		return
	}
	var keep []any
	for _, e := range d.list("relayLog") {
		if o, ok := e.(*jsonx.Object); ok {
			if m, ok := PRRefNumber(jsonx.Str(o, "pr")); ok && m == n {
				continue
			}
		}
		keep = append(keep, e)
	}
	o := jsonx.NewObject()
	o.Set("pr", pr)
	o.Set("relays", slices.Clone(relays))
	d.doc.Set("relayLog", append(keep, o))
}

// ClearRelayLog drops every kept relay log, at wind-down.
func (d *Doc) ClearRelayLog() { d.doc.Delete("relayLog") }

// LoggedRelays is the relay log kept for a PR ref by LogRelays, nil when none.
func (r Registry) LoggedRelays(ref string) []any {
	n, ok := PRRefNumber(ref)
	if !ok {
		return nil
	}
	for _, o := range r.objects("relayLog") {
		if m, ok := PRRefNumber(jsonx.Str(o, "pr")); ok && m == n {
			return listOf(o, "relays")
		}
	}
	return nil
}

// BranchHolder finds who holds branch: the slot on it and the queued record
// for it, each nil when absent. Both can hold it at once: a same-agent fix
// round reuses the branch name and assign never drops the queued record.
func (r Registry) BranchHolder(branch string) (s *Slot, q *QueuedPR) {
	if branch == "" {
		return nil, nil
	}
	for _, x := range r.Slots() {
		if x.Branch() == branch {
			s = x
			break
		}
	}
	for _, x := range r.PRs() {
		if x.Branch == branch {
			q = &x
			break
		}
	}
	return s, q
}

// SlotHolding is the first slot that holds id as its task or branch issue (see
// Slot.Holds), nil when none does.
func (r Registry) SlotHolding(id string) *Slot {
	for _, s := range r.Slots() {
		if s.Holds(id) {
			return s
		}
	}
	return nil
}

// Holders is every slot that holds id, in registry order, minus o.Skip. A
// best-of issue has several; o.Lead is not read here.
func (r Registry) Holders(id string, o HolderOpts) []*Slot {
	var out []*Slot
	for _, s := range r.Slots() {
		if s.Holds(id) && (o.Skip == "" || s.Name() != o.Skip) {
			out = append(out, s)
		}
	}
	return out
}

// HolderOpts scopes BundleHolder and Holders. Lead also matches the lead issue of a slot or
// queued PR, not only its members. Skip names a slot that is passed over: the
// slot resuming its own bundle holds its members itself.
type HolderOpts struct {
	Lead bool
	Skip string
}

// BundleHolder finds who holds id: the slot whose bundle lists it (its lead too
// under o.Lead), else the queued record that does. Both are nil when nothing
// holds it. The queued record is only looked up when no slot matched.
func (r Registry) BundleHolder(id string, o HolderOpts) (*Slot, *QueuedPR) {
	want := NormID(id)
	has := func(ids []string) bool {
		return want != "" && slices.ContainsFunc(ids, func(m string) bool { return NormID(m) == want })
	}
	for _, s := range r.Slots() {
		if o.Skip != "" && s.Name() == o.Skip {
			continue
		}
		ids := s.Bundle()
		if o.Lead {
			ids = s.BundleIssues()
		}
		if has(ids) {
			return s, nil
		}
	}
	for _, q := range r.PRs() {
		ids := q.Bundle
		if o.Lead {
			ids = q.Issues()
		}
		if has(ids) {
			return nil, &q
		}
	}
	return nil, nil
}

// PromoteBundleLead drops lead from its slot or queued record and promotes the
// first member, locked. The branch, PR and running attempt stay with the work.
func PromoteBundleLead(root, lead string) error {
	want := NormID(lead)
	return Update(root, func(d *Doc) {
		for _, s := range d.Slots() {
			if b := s.Bundle(); s.HeldID() == want && len(b) > 0 {
				s.SetTask(b[0])
				s.o.Set("issue", b[0])
				s.SetBundle(b[1:])
			}
		}
		for _, o := range d.queued() {
			if b := strList(o, "bundle"); NormID(jsonx.Str(o, "issue")) == want && len(b) > 0 {
				o.Set("issue", b[0])
				setStrList(o, "bundle", b[1:])
			}
		}
	})
}

// DropBundleMember removes id from the bundle of the slot or queued record
// that lists it, locked. A bundle left empty loses the field.
func DropBundleMember(root, id string) error {
	want := NormID(id)
	keep := func(ids []string) []string {
		var out []string
		for _, m := range ids {
			if NormID(m) != want {
				out = append(out, m)
			}
		}
		return out
	}
	return Update(root, func(d *Doc) {
		for _, s := range d.Slots() {
			if b := s.Bundle(); len(keep(b)) != len(b) {
				s.SetBundle(keep(b))
			}
		}
		for _, o := range d.queued() {
			if b := strList(o, "bundle"); len(keep(b)) != len(b) {
				setStrList(o, "bundle", keep(b))
			}
		}
	})
}

// SetRound records the round number.
func (d *Doc) SetRound(n int) { d.doc.Set("round", n) }

// SetHost records the round host.
func (d *Doc) SetHost(h host.Kind) { d.doc.Set("host", h.String()) }

// ClearHost forgets the round host.
func (d *Doc) ClearHost() { d.doc.Delete("host") }

// SetLayout records the round's pane layout. Tabs is the default, so it
// forgets the key instead of storing it.
func (d *Doc) SetLayout(l string) {
	if l == "" || l == "tabs" {
		d.doc.Delete("layout")
		return
	}
	d.doc.Set("layout", l)
}

// SetCLIPane records the pane that launched the round; "" forgets it.
func (d *Doc) SetCLIPane(p string) {
	if p == "" {
		d.doc.Delete("cliPane")
		return
	}
	d.doc.Set("cliPane", p)
}

// SetSession records the pane session.
func (d *Doc) SetSession(s string) { d.doc.Set("session", s) }

// SetScope records the round scope.
func (d *Doc) SetScope(scope string) { d.doc.Set("scope", scope) }

// SetSlotCount records how many roster slots the round uses.
func (d *Doc) SetSlotCount(n int) { d.doc.Set("slotCount", n) }

// SetSlate records the slate of a slate-scoped round.
func (d *Doc) SetSlate(slate []string) {
	l := make([]any, 0, len(slate))
	for _, s := range slate {
		l = append(l, s)
	}
	d.doc.Set("slate", l)
}

// ClearSlate removes the slate.
func (d *Doc) ClearSlate() { d.doc.Delete("slate") }

// SetSlots replaces the slot list.
func (d *Doc) SetSlots(slots []*Slot) {
	l := make([]any, 0, len(slots))
	for _, s := range slots {
		l = append(l, s.o)
	}
	d.doc.Set("slots", l)
}

// AppendSlot adds s to the slot list.
func (d *Doc) AppendSlot(s *Slot) {
	d.doc.Set("slots", append(d.list("slots"), s.o))
}

// SortSlots orders the slot list by name.
func (d *Doc) SortSlots() {
	l := d.list("slots")
	sort.SliceStable(l, func(i, j int) bool {
		a, _ := l[i].(*jsonx.Object)
		b, _ := l[j].(*jsonx.Object)
		return a != nil && b != nil && jsonx.Str(a, "name") < jsonx.Str(b, "name")
	})
}

// Update is a locked read-modify-write of the registry; a missing file starts
// from {"slots": []}. A file that exists but does not parse is an error: the
// slots in it are the only record of who holds what (#579).
func Update(root string, mutate func(d *Doc)) error {
	_, err := UpdateChanged(root, mutate)
	return err
}

// UpdateChanged is Update that also reports whether mutate changed the
// registry document. A callback that sets a field to the value it already
// holds, or does nothing, reports false.
func UpdateChanged(root string, mutate func(d *Doc)) (bool, error) {
	return update(root, slotsDefault(), mutate)
}

// ReserveSlot is the compare-and-set that serialises two assigns on one slot:
// under the registry lock it takes the slot for task when the slot still holds
// nothing, and otherwise reports what it holds. An assign plans against a read
// that the lock does not cover, so the slot may have been filled since (by a
// manual assign or the autopilot tick); the later writer is refused here.
func ReserveSlot(root, name, task string) (held string, ok bool, err error) {
	found, err := UpdateSlot(root, name, func(s *Slot) {
		if held = s.HeldID(); held != "" {
			return
		}
		s.SetTask(task)
		ok = true
	})
	if err == nil && !found {
		err = fmt.Errorf("slot %s is not in the registry", name)
	}
	return held, ok, err
}

// ReleaseReserved gives back a ReserveSlot reservation that nothing bound yet;
// a slot that holds another task, or already carries its claim, is left alone.
func ReleaseReserved(root, name, task string) error {
	_, err := UpdateSlot(root, name, func(s *Slot) {
		if NormID(s.Task()) == NormID(task) && s.ClaimID() == "" {
			s.Unbind()
		}
	})
	return err
}

// writeGens counts, per registry path, the updates of this process that
// changed the document. A caller that runs many writers (pool init, poll)
// compares ChangeCount before and after instead of diffing the file's bytes.
var (
	writeGensMu sync.Mutex
	writeGens   = map[string]uint64{}
)

// ChangeCount is how many updates of this process changed root's registry.
func ChangeCount(root string) uint64 {
	writeGensMu.Lock()
	defer writeGensMu.Unlock()
	return writeGens[RegistryPath(root)]
}

// update is UpdateChanged with the document a missing or corrupt file starts
// from.
func update(root string, def *jsonx.Object, mutate func(d *Doc)) (bool, error) {
	changed := false
	err := fsio.UpdateJSONStrict(RegistryPath(root), def, func(v any) (any, error) {
		doc, ok := v.(*jsonx.Object)
		if !ok {
			return nil, fmt.Errorf("%w: %s is not a JSON object; fix or remove it, rota will not overwrite it", fsio.ErrUnreadable, RegistryPath(root))
		}
		before, err := jsonx.MarshalCompact(doc)
		if err != nil {
			return nil, err
		}
		mutate(&Doc{Registry{doc: doc}})
		after, err := jsonx.MarshalCompact(doc)
		if err != nil {
			return nil, err
		}
		changed = !bytes.Equal(before, after)
		return doc, nil
	})
	if err == nil && changed {
		writeGensMu.Lock()
		writeGens[RegistryPath(root)]++
		writeGensMu.Unlock()
	}
	return changed && err == nil, err
}

// SlotData is a slot as `data` shows it: null registry fields are absent.
func SlotData(sl *Slot) *jsonx.Object {
	s := sl.Raw()
	out := jsonx.NewObject()
	for _, k := range s.Keys() {
		v, _ := s.Get(k)
		if v == nil {
			continue
		}
		out.Set(k, v)
	}
	if _, ok := out.Get("relays"); !ok {
		out.Set("relays", []any{})
	}
	return out
}
