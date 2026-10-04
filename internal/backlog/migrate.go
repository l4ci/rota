package backlog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// `rota migrate issues` (bin/hv-migrate-issues): move a file-backend project's
// open backlog and its planned or active milestones onto the issue tracker.
// The map .rota/issue-map.json is written after every successful write, so a
// run that stops (a tracker error, a rate limit, --limit) resumes from it.

// Migration refusals; the verb maps them to exits 3 and 4.
var (
	// ErrNothingToMigrate: .rota/BACKLOG.md does not exist.
	ErrNothingToMigrate = errors.New("nothing to migrate")
	// ErrUmbrellaMigrate: .rota/repos.json registers sub-repos.
	ErrUmbrellaMigrate = errors.New("umbrella mode: migrate each sub-repo separately")
	// ErrBadMap: .rota/issue-map.json is valid JSON but not an object.
	ErrBadMap = errors.New("issue-map.json is not a JSON object")
)

// MigrateTracker is the part of tracker.Adapter the migration calls: the
// issue backend's subset plus the native milestone calls.
type MigrateTracker = MilestoneTracker

// MigrateOptions are the inputs of one run.
type MigrateOptions struct {
	Root  string // the project root, the directory that holds .rota/
	Apply bool   // false only reports what it would do
	Limit int    // create at most this many items; negative is no limit
	Cfg   any    // loaded config
	// Tracker builds the forge adapter on first use. A preview never calls it.
	Tracker func() (MigrateTracker, error)
	Sleep   func(time.Duration) // the issues.bulkPaceMs pause; nil is time.Sleep
	Warn    func(string)        // notices (dropped tags and milestones, duplicate tracking issues)
	Today   func() string       // YYYY-MM-DD; nil is the local date
	Ctx     context.Context     // for every tracker call; nil is context.Background()
}

// MigrateOp is one planned or run operation: Action is the first word of the
// old helper's output line, Text the rest.
type MigrateOp struct{ Action, Text string }

// MigrateResult is what a run did. It is returned with the error too, so a
// failed run still reports its progress.
type MigrateResult struct {
	Lines    []string // the old helper's stdout, line by line
	Ops      []MigrateOp
	Map      *jsonx.Object // preview: the would-be map; apply: .rota/issue-map.json afterwards
	Migrated int           // items with an issue in .rota/issue-map.json
	Total    int           // open items found
	Changed  bool          // the map or BACKLOG.md was written
	Done     bool          // apply only: every item and milestone exists and BACKLOG.md is frozen
}

type migrator struct {
	o      MigrateOptions
	ctx    context.Context
	apply  bool
	items  []*migItem
	ms     []*migMilestone
	msIDs  map[string]bool
	imap   *jsonx.Object
	mapRel string
	res    *MigrateResult

	tr      MigrateTracker
	be      *Issues
	doneOps int
	pending int
	created int
	msCache map[string]bool
}

var (
	migItemKey = regexp.MustCompile(`\A[` + ItemLetters + `]\p{Nd}+\z`)
)

// frozenPrefix starts the banner a finished migration puts on BACKLOG.md.
const frozenPrefix = "> Frozen:"

// MigrateIssues runs the migration. The error is ErrNothingToMigrate,
// ErrUmbrellaMigrate, ErrBadMap, a *tracker.Error when the forge stopped the
// run (the map keeps the progress), or another failure of a write; the result
// is non-nil whenever a run got as far as planning.
func MigrateIssues(o MigrateOptions) (*MigrateResult, error) {
	if o.Sleep == nil {
		o.Sleep = time.Sleep
	}
	if o.Warn == nil {
		o.Warn = func(string) {}
	}
	if o.Today == nil {
		o.Today = func() string { return time.Now().Format("2006-01-02") }
	}
	rota := filepath.Join(o.Root, ".rota")
	backlogPath := filepath.Join(rota, "BACKLOG.md")
	text, err := fsio.ReadText(backlogPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: .rota/BACKLOG.md not found", ErrNothingToMigrate)
		}
		return nil, err
	}
	if len(repos.Load(o.Root)) > 0 {
		return nil, ErrUmbrellaMigrate
	}
	m := &migrator{o: o, ctx: migrateCtx(o.Ctx), apply: o.Apply, msCache: map[string]bool{},
		mapRel: filepath.Join(rota, "issue-map.json"), res: &MigrateResult{}}
	m.items = planItems(o.Root, text, o.Warn)
	m.ms = planMilestones(o.Root)
	m.msIDs = map[string]bool{}
	for _, ms := range m.ms {
		m.msIDs[ms.id] = true
	}
	m.res.Total = len(m.items)
	m.imap = jsonx.NewObject()
	if _, statErr := os.Stat(m.mapRel); statErr == nil {
		switch v := fsio.LoadJSON(m.mapRel, jsonx.NewObject()).(type) {
		case *jsonx.Object:
			m.imap = v
		default:
			return m.res, ErrBadMap
		}
	}
	before := m.state()
	if o.Apply {
		defer func() { m.res.Changed = m.state() != before }()
	}

	complete, runErr := m.run()
	var te *tracker.Error
	if runErr != nil && !errors.As(runErr, &te) {
		return m.finish(), runErr
	}
	if te != nil {
		migrated := 0
		for _, it := range m.items {
			if m.entryID(it.id) != "" {
				migrated++
			}
		}
		if te.Kind == tracker.KindRateLimited {
			m.say(fmt.Sprintf("rate limited: %d of %d items migrated; the map is saved in .rota/issue-map.json", migrated, len(m.items)))
			m.say("re-run to continue")
		} else {
			m.say("stopped on a tracker error; the map is saved in .rota/issue-map.json; fix the cause and re-run to continue")
		}
	}
	if !m.apply {
		m.say("would-be map:")
		shown := jsonx.NewObject()
		inItems := map[string]bool{}
		for _, it := range m.items {
			inItems[it.id] = true
		}
		for _, k := range m.imap.Keys() {
			if !inItems[k] && !m.msIDs[k] {
				continue
			}
			e, _ := m.imap.Get(k)
			eo, _ := e.(*jsonx.Object)
			row := jsonx.NewObject()
			for _, f := range []string{"id", "number", "url"} {
				if eo != nil {
					if v, ok := eo.Get(f); ok {
						row.Set(f, v)
						continue
					}
				}
				row.Set(f, "")
			}
			shown.Set(k, row)
		}
		b, _ := jsonx.Marshal(shown)
		m.say(string(b))
		m.res.Map = shown
		m.finish()
		return m.res, nil
	}
	if te != nil {
		m.finish()
		return m.res, runErr
	}
	if !complete {
		m.say(fmt.Sprintf("%d items remaining; notes, plans and Related rewrites run once every item exists. re-run to continue", m.pending))
		return m.finish(), nil
	}
	if !strings.HasPrefix(strings.TrimLeftFunc(text, pystr.IsSpace), frozenPrefix) {
		banner := fmt.Sprintf("%s this backlog moved to the issue tracker on %s (see .rota/issue-map.json). Edit issues, not this file.", frozenPrefix, o.Today())
		if err := fsio.WriteFileAtomic(backlogPath, []byte(banner+"\n\n"+text)); err != nil {
			return m.finish(), err
		}
		m.say("froze .rota/BACKLOG.md")
	}
	if err := m.remapRegistry(); err != nil {
		return m.finish(), err
	}
	m.say("Next: rota config set backlog.backend issues")
	m.res.Done = true
	return m.finish(), nil
}

// remapRegistry rewrites the file-mode IDs a mid-round registry still holds to
// the issue numbers the map gives them: a slot's task and a queued PR's issue.
// Without it the slot keeps `B31`, which no tracker call resolves, and the
// round cannot release or reassign it. A claimId is `<agent>@<round>`, not an
// item ID, so it stays. It runs only once every item exists, since until then
// the backlog is still the file backend and the old IDs still resolve.
func (m *migrator) remapRegistry() error {
	if _, err := os.Stat(worker.RegistryPath(m.o.Root)); err != nil {
		return nil
	}
	to := func(raw string) (string, bool) {
		old := strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(raw), "#"))
		e := m.entry(old)
		if e == nil || !migItemKey.MatchString(old) {
			return "", false
		}
		if n, ok := e.Get("number"); ok && truthy(n) {
			return fmt.Sprint(n), true
		}
		return "", false
	}
	var notes []string
	err := worker.UpdateDoc(m.o.Root, func(doc *jsonx.Object) {
		reg := worker.Registry{Doc: doc}
		for _, s := range reg.Slots() {
			if n, ok := to(s.Task()); ok {
				notes = append(notes, fmt.Sprintf("remap slot %s task %s -> #%s", s.Name(), s.Task(), n))
				s.SetTask(n)
			}
		}
		for _, q := range reg.PRs() {
			if n, ok := to(worker.Str(q, "issue")); ok {
				notes = append(notes, fmt.Sprintf("remap queued PR %s issue %s -> #%s", worker.Str(q, "pr"), worker.Str(q, "issue"), n))
				q.Set("issue", n)
			}
		}
	})
	for _, n := range notes {
		m.say(n)
	}
	return err
}

// finish loads the map as it is on disk and counts what it holds.
func (m *migrator) finish() *MigrateResult {
	disk, _ := fsio.LoadJSON(m.mapRel, jsonx.NewObject()).(*jsonx.Object)
	if disk == nil {
		disk = jsonx.NewObject()
	}
	if m.res.Map == nil {
		m.res.Map = disk
	}
	m.res.Migrated = 0
	for _, k := range disk.Keys() {
		if !migItemKey.MatchString(k) {
			continue
		}
		if e, ok := disk.Get(k); ok {
			if eo, ok := e.(*jsonx.Object); ok {
				if id, _ := eo.Get("id"); truthy(id) {
					m.res.Migrated++
				}
			}
		}
	}
	return m.res
}

func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case string:
		return t != ""
	case bool:
		return t
	}
	return true
}

// state is the map file and BACKLOG.md as bytes, to tell whether a run wrote.
func (m *migrator) state() string {
	a, _ := os.ReadFile(m.mapRel)
	b, _ := os.ReadFile(filepath.Join(m.o.Root, ".rota", "BACKLOG.md"))
	return string(a) + "\x00" + string(b)
}

var opActions = []struct{ prefix, action string }{
	{"create milestone ", "create-milestone"}, {"create issue ", "create-issue"}, {"adopt ", "adopt"},
	{"note ", "note"}, {"rewrite ", "rewrite"}, {"skip ", "skip"}, {"set ", "set"},
}

// say records one output line of the old helper.
func (m *migrator) say(line string) {
	m.res.Lines = append(m.res.Lines, line)
	if strings.HasPrefix(line, "would-be map:") {
		return
	}
	for _, p := range opActions {
		if strings.HasPrefix(line, p.prefix) {
			m.res.Ops = append(m.res.Ops, MigrateOp{p.action, strings.TrimPrefix(line, p.prefix)})
			return
		}
	}
}

// op prints (preview) or runs (apply, paced) one operation.
func (m *migrator) op(desc string, fn func() error) error {
	if !m.apply {
		m.say(desc)
		return nil
	}
	if m.doneOps > 0 {
		if d := m.pace(); d > 0 {
			m.o.Sleep(d)
		}
	}
	m.doneOps++
	m.say(desc)
	return fn()
}

// pace is issues.bulkPaceMs as a duration: int() of the value, at least 0.
func (m *migrator) pace() time.Duration {
	v, _ := config.Value(m.o.Cfg, "issues.bulkPaceMs")
	var ms float64
	switch t := v.(type) {
	case bool:
		if t {
			ms = 1
		}
	case string:
		f, err := strconv.ParseFloat(pystr.Strip(t), 64)
		if err == nil {
			ms = f
		}
	case interface{ String() string }:
		f, err := strconv.ParseFloat(t.String(), 64)
		if err == nil {
			ms = f
		}
	}
	if ms < 1 {
		return 0
	}
	return time.Duration(int64(ms)) * time.Millisecond
}

// backend is the issue backend over the lazily built tracker.
func (m *migrator) backend() (*Issues, error) {
	if m.be != nil {
		return m.be, nil
	}
	tr, err := m.o.Tracker()
	if err != nil {
		return nil, err
	}
	m.tr = tr
	m.be = &Issues{Cfg: m.o.Cfg, Tracker: tr, Ctx: m.ctx, Warn: m.o.Warn}
	return m.be, nil
}

// ---- the map ------------------------------------------------------------------

func (m *migrator) entry(old string) *jsonx.Object {
	if v, ok := m.imap.Get(old); ok {
		if e, ok := v.(*jsonx.Object); ok {
			return e
		}
	}
	return nil
}

// entryID is the issue ID the map holds for old, or "".
func (m *migrator) entryID(old string) string {
	if e := m.entry(old); e != nil {
		if v, ok := e.Get("id"); ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}

func (m *migrator) save() error { return fsio.WriteJSONAtomic(m.mapRel, m.imap) }

func (m *migrator) setEntry(old, id string, number any, url string) error {
	e := jsonx.NewObject()
	e.Set("id", id)
	e.Set("number", number)
	e.Set("url", url)
	e.Set("done", []any{})
	m.imap.Set(old, e)
	return m.save()
}

func (m *migrator) hasStep(old, step string) bool {
	if e := m.entry(old); e != nil {
		if d, ok := e.Get("done"); ok {
			if list, ok := d.([]any); ok {
				for _, s := range list {
					if s == step {
						return true
					}
				}
			}
		}
	}
	return false
}

// addStep records a finished step (setdefault of the entry and its done list).
func (m *migrator) addStep(old, step string) {
	e := m.entry(old)
	if e == nil {
		e = jsonx.NewObject()
		m.imap.Set(old, e)
	}
	d, ok := e.Get("done")
	list, isList := d.([]any)
	if !ok || !isList {
		list = []any{}
	}
	for _, s := range list {
		if s == step {
			e.Set("done", list)
			return
		}
	}
	e.Set("done", append(list, step))
}

func (m *migrator) stepDone(old, step string) error {
	m.addStep(old, step)
	return m.save()
}

// newIDs is the old ID to new ID mapping of the migrated items.
func (m *migrator) newIDs() map[string]string {
	out := map[string]string{}
	for _, k := range m.imap.Keys() {
		if migItemKey.MatchString(k) {
			out[k] = m.entryID(k)
		}
	}
	return out
}

// rewrite swaps old item IDs for the new ones (exact tokens only; bracketedOnly
// leaves bare IDs alone).
func (m *migrator) rewrite(text string, bracketedOnly bool) string {
	ids := m.newIDs()
	if bracketedOnly {
		return bracketedRe.ReplaceAllStringFunc(text, func(s string) string {
			id := s[1 : len(s)-1]
			if n, ok := ids[id]; ok {
				return "[" + n + "]"
			}
			return s
		})
	}
	var b strings.Builder
	last := 0
	for _, t := range tokenMatches(text) {
		b.WriteString(text[last:t.start])
		if n, ok := ids[t.id]; ok {
			b.WriteString(n)
		} else {
			b.WriteString(t.id)
		}
		last = t.end
	}
	b.WriteString(text[last:])
	return b.String()
}

// ---- the run --------------------------------------------------------------------

func (m *migrator) labelNames(it *migItem) []string {
	role := map[string]string{"bugs": "types.bug", "features": "types.feature", "tasks": "types.task"}[it.kind]
	labels := []string{config.Label(m.o.Cfg, role)}
	if it.tag != "" && it.kind == "bugs" {
		labels = append(labels, config.Label(m.o.Cfg, "priorityPrefix")+it.tag[1:])
	} else if it.tag != "" {
		labels = append(labels, config.Label(m.o.Cfg, "sizePrefix")+it.tag)
	}
	return labels
}

func (m *migrator) milestoneAvailable(mid string) (bool, error) {
	if _, ok := m.imap.Get(mid); ok || m.msIDs[mid] {
		return true, nil
	}
	if !m.apply {
		return false, nil
	}
	if v, ok := m.msCache[mid]; ok {
		return v, nil
	}
	be, err := m.backend()
	if err != nil {
		return false, err
	}
	_, found, err := be.Tracker.FindMilestone(m.ctx, mid)
	if err != nil {
		return false, err
	}
	m.msCache[mid] = found
	return found, nil
}

// run is the three phases: milestones, items, then (once every item exists)
// notes, slice plans, milestone bodies and Related rewrites. It returns false
// when --limit left items for the next run.
func (m *migrator) run() (bool, error) {
	// phase 1: milestones
	for _, ms := range m.ms {
		mid := ms.id
		if _, ok := m.imap.Get(mid); !ok {
			ms := ms
			err := m.op(fmt.Sprintf("create milestone %s (%s)", mid, ms.status), func() error {
				if err := m.milestoneAdd(mid, ms); err != nil {
					return err
				}
				is, err := m.trackerIssue(mid)
				if err != nil {
					return err
				}
				return m.setEntry(mid, mid, jn(is.Number), is.URL)
			})
			if err != nil {
				return false, err
			}
			if !m.apply {
				e := jsonx.NewObject()
				e.Set("id", mid)
				e.Set("number", "?")
				e.Set("url", "")
				e.Set("done", []any{})
				m.imap.Set(mid, e)
			}
		}
		if ms.status != "planned" && !m.hasStep(mid, "status") {
			ms := ms
			err := m.op(fmt.Sprintf("set milestone %s status %s", mid, ms.status), func() error {
				if err := m.milestoneStatus(mid, ms.status); err != nil {
					return err
				}
				return m.stepDone(mid, "status")
			})
			if err != nil {
				return false, err
			}
			if !m.apply {
				m.addStep(mid, "status")
			}
		}
	}
	// phase 2: items
	for _, it := range m.items {
		old := it.id
		if _, ok := m.imap.Get(old); ok {
			m.say(fmt.Sprintf("skip %s (already migrated as %s)", old, m.entryID(old)))
			continue
		}
		if m.o.Limit >= 0 && m.created >= m.o.Limit {
			m.pending++
			continue
		}
		if it.adopt > 0 {
			if err := m.adopt(it); err != nil {
				return false, err
			}
			continue
		}
		fields := []Field{}
		for _, n := range migOrder {
			if v := it.fields[n]; v != "" {
				fields = append(fields, Field{n, v})
			}
		}
		note := ""
		if it.milestone != "" {
			ok, err := m.milestoneAvailable(it.milestone)
			if err != nil {
				return false, err
			}
			if ok {
				fields = append(fields, Field{"Milestone", it.milestone})
				note = " milestone " + it.milestone
			} else {
				m.o.Warn(fmt.Sprintf("%s: milestone %s is not on the tracker (shipped/archived milestones are not migrated); Milestone field dropped", old, it.milestone))
			}
		}
		if len(it.unmapped) > 0 {
			note += " (Related not migrated: " + strings.Join(it.unmapped, ", ") + ")"
		}
		it, fields := it, fields
		desc := fmt.Sprintf("create issue %s → %s \"%s\" [%s]%s", old, strings.TrimSuffix(it.kind, "s"), it.title,
			strings.Join(m.labelNames(it), ", "), note)
		err := m.op(desc, func() error {
			be, err := m.backend()
			if err != nil {
				return err
			}
			in := CreateInput{Kind: it.kind, Title: it.title, Tag: it.tag, Desc: it.desc, Fields: fields, Since: it.since}
			if it.body != nil {
				in.Body, in.HasBody = []byte(*it.body), true
			}
			res, err := be.Create(in)
			if err != nil {
				return err
			}
			n, _ := strconv.Atoi(res.ID)
			got, err := be.Tracker.Get(m.ctx, n, false)
			if err != nil {
				return err
			}
			return m.setEntry(old, res.Type+res.ID, jn(n), got.URL)
		})
		if err != nil {
			return false, err
		}
		m.created++
		if !m.apply {
			e := jsonx.NewObject()
			e.Set("id", old[:1]+"?")
			e.Set("number", "?")
			e.Set("url", "")
			e.Set("done", []any{})
			m.imap.Set(old, e)
		}
	}
	if m.pending > 0 {
		return false, nil
	}
	// phase 3
	for _, it := range m.items {
		old, nid := it.id, m.entryID(it.id)
		for _, n := range []struct {
			kind string
			text *string
		}{{"proof", it.proof}, {"design", it.design}, {"plan", it.plan}} {
			if n.text == nil || pystr.Strip(*n.text) == "" || m.hasStep(old, n.kind) {
				continue
			}
			body := *n.text
			if n.kind != "proof" {
				body = m.rewrite(body, false)
			}
			kind := n.kind
			err := m.op(fmt.Sprintf("note %s on %s", kind, old), func() error {
				be, err := m.backend()
				if err != nil {
					return err
				}
				if _, err := be.NotePut(nid, kind, body); err != nil {
					return err
				}
				return m.stepDone(old, kind)
			})
			if err != nil {
				return false, err
			}
			if !m.apply {
				m.addStep(old, kind)
			}
		}
		rel := it.fields["Related"]
		if rel != "" && !m.hasStep(old, "related") {
			newRel := m.rewrite(rel, false)
			var ids []string
			for _, t := range tokenMatches(rel) {
				if m.entry(t.id) != nil {
					ids = append(ids, t.id)
				}
			}
			if newRel != rel || (!m.apply && len(ids) > 0) {
				var shown []string
				for _, t := range ids {
					to := "#?"
					if m.apply {
						to = m.entryID(t)
					}
					shown = append(shown, t+" → "+to)
				}
				newRel := newRel
				err := m.op(fmt.Sprintf("rewrite Related on %s: %s", old, strings.Join(shown, ", ")), func() error {
					be, err := m.backend()
					if err != nil {
						return err
					}
					if _, err := be.SetField(nid, "related", newRel); err != nil {
						return err
					}
					return m.stepDone(old, "related")
				})
				if err != nil {
					return false, err
				}
				if !m.apply {
					m.addStep(old, "related")
				}
			} else if m.apply {
				if err := m.stepDone(old, "related"); err != nil {
					return false, err
				}
			}
		}
	}
	for _, ms := range m.ms {
		mid, ms := ms.id, ms
		if !m.hasStep(mid, "body") {
			err := m.op(fmt.Sprintf("set plan body of milestone %s", mid), func() error {
				if err := m.putMilestone(mid, ms); err != nil {
					return err
				}
				return m.stepDone(mid, "body")
			})
			if err != nil {
				return false, err
			}
			if !m.apply {
				m.addStep(mid, "body")
			}
		}
		for _, s := range ms.slices {
			step := "plan:" + s.unit
			if pystr.Strip(s.text) == "" || m.hasStep(mid, step) {
				continue
			}
			s := s
			err := m.op(fmt.Sprintf("note %s on %s", step, mid), func() error {
				be, err := m.backend()
				if err != nil {
					return err
				}
				is, err := m.trackerIssue(mid)
				if err != nil {
					return err
				}
				if _, err := be.NotePut(strconv.Itoa(is.Number), step, m.rewrite(s.text, true)); err != nil {
					return err
				}
				return m.stepDone(mid, step)
			})
			if err != nil {
				return false, err
			}
			if !m.apply {
				m.addStep(mid, step)
			}
		}
	}
	return true, nil
}

// adopt maps an item that already has a tracker issue (a `GH: #N` tag) onto
// it instead of creating a duplicate. Only the type and size or priority
// labels are added; the issue keeps its own title and body, and the notes and Related rewrites of phase 3 run on it.
func (m *migrator) adopt(it *migItem) error {
	old, n := it.id, it.adopt
	labels := m.labelNames(it)
	desc := fmt.Sprintf("adopt %s → #%d \"%s\" [%s]", old, n, it.title, strings.Join(labels, ", "))
	err := m.op(desc, func() error {
		be, err := m.backend()
		if err != nil {
			return err
		}
		got, err := be.Tracker.Get(m.ctx, n, false)
		if err != nil {
			return err
		}
		if err := be.Tracker.EnsureLabels(m.ctx, labels, be.autoCreate()); err != nil {
			return err
		}
		if err := be.Tracker.AddLabels(m.ctx, n, labels, be.autoCreate()); err != nil {
			return err
		}
		return m.setEntry(old, old[:1]+strconv.Itoa(n), jn(n), got.URL)
	})
	if err != nil {
		return err
	}
	m.created++
	if !m.apply {
		e := jsonx.NewObject()
		e.Set("id", old[:1]+strconv.Itoa(n))
		e.Set("number", jn(n))
		e.Set("url", "")
		e.Set("done", []any{})
		m.imap.Set(old, e)
	}
	return nil
}

// jn is an int as a JSON number.
func jn(n int) json.Number { return json.Number(strconv.Itoa(n)) }

// ---- milestones: a native milestone and a tracking issue ----------------------------

// The milestone calls live on Issues (milestones.go), shared with rota
// milestone; the migrator only builds the backend and passes its inputs.

func (m *migrator) trackerIssue(mid string) (Issue, error) {
	be, err := m.backend()
	if err != nil {
		return Issue{}, err
	}
	return be.TrackerIssue(mid)
}

func (m *migrator) milestoneAdd(mid string, ms *migMilestone) error {
	be, err := m.backend()
	if err != nil {
		return err
	}
	_, err = be.MilestoneAdd(mid, ms.title, ms.summary, ms.depends, m.o.Today())
	return err
}

func (m *migrator) milestoneStatus(mid, status string) error {
	be, err := m.backend()
	if err != nil {
		return err
	}
	return be.MilestoneStatus(mid, status)
}

// putMilestone copies the milestone plan text to the tracking issue; a text
// without `id: <mid>` frontmatter is skipped with a notice.
func (m *migrator) putMilestone(mid string, ms *migMilestone) error {
	be, err := m.backend()
	if err != nil {
		return err
	}
	err = be.MilestonePut(mid, m.rewrite(ms.text, false))
	if errors.Is(err, ErrMilestoneText) {
		m.o.Warn(fmt.Sprintf("%s: plan body not copied (%s)", mid, err.Error()))
		return nil
	}
	return err
}

func migrateCtx(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
