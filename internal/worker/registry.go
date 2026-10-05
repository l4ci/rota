// Package worker is the Go port of the /rota-work worker helpers
// (bin/hv-worker-pool, -reset, -account, -dispatch, -poll, -gate, -session).
// The registry is .rota/workers.json at the project root; its files stay
// byte-identical to what the old helpers write.
package worker

import (
	"context"
	"encoding/json"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/rotatree"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/proc"
	"github.com/l4ci/rota/internal/tracker"
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

// LoadRegistry reads the registry; a missing or corrupt file reads as
// {"slots": []} with Exists false, like hvlib_io.load_json.
func LoadRegistry(root string) Registry {
	if o, ok := fsio.LoadJSON(RegistryPath(root), nil).(*jsonx.Object); ok {
		return Registry{doc: o, Exists: true}
	}
	return Registry{doc: slotsDefault()}
}

// Slots lists the slot objects of the registry.
func (r Registry) Slots() []*Slot {
	raw, _ := r.doc.Get("slots")
	list, _ := raw.([]any)
	var out []*Slot
	for _, e := range list {
		if o, ok := e.(*jsonx.Object); ok {
			out = append(out, AsSlot(o))
		}
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
	return intOf(v)
}

func intOf(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case float64:
		return int(t), true
	case json.Number:
		f, err := t.Float64()
		return int(f), err == nil
	}
	return 0, false
}

// Host is the round host `round start` recorded, "" when none.
func (r Registry) Host() string { return jsonx.Str(r.doc, "host") }

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

// Slate is the recorded slate of a slate-scoped round, nil when none.
func (r Registry) Slate() []string {
	v, _ := r.doc.Get("slate")
	l, _ := v.([]any)
	var out []string
	for _, e := range l {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func (r Registry) objects(key string) []*jsonx.Object {
	raw, _ := r.doc.Get(key)
	list, _ := raw.([]any)
	var out []*jsonx.Object
	for _, e := range list {
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
	Round                                  int
	Relays                                 []any
}

func queuedFrom(o *jsonx.Object) QueuedPR {
	rv, _ := o.Get("round")
	round, _ := intOf(rv)
	relays, _ := o.Get("relays")
	l, _ := relays.([]any)
	return QueuedPR{
		Issue: jsonx.Str(o, "issue"), Branch: jsonx.Str(o, "branch"), PR: jsonx.Str(o, "pr"),
		Base: jsonx.Str(o, "base"), From: jsonx.Str(o, "from"), ClaimID: jsonx.Str(o, "claimId"),
		Round: round, Relays: l,
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

func (r Registry) queued() []*jsonx.Object {
	raw, _ := r.doc.Get("prs")
	list, _ := raw.([]any)
	var out []*jsonx.Object
	for _, e := range list {
		if o, ok := e.(*jsonx.Object); ok {
			out = append(out, o)
		}
	}
	return out
}

var rePRRef = regexp.MustCompile(`^(?:#|.*/(?:pull|merge_requests)/)?(\d+)/?$`)

// PRRefNumber reads the PR number from `#N`, `N` or a PR URL.
func PRRefNumber(ref string) (int, bool) {
	m := rePRRef.FindStringSubmatch(strings.TrimSpace(ref))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

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
	want := strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(id), "#"))
	for _, o := range r.queued() {
		if want != "" && strings.ToUpper(strings.TrimPrefix(jsonx.Str(o, "issue"), "#")) == want {
			q := queuedFrom(o)
			return &q
		}
	}
	return nil
}

// QueuePR appends a record, replacing one for the same issue so a repeated
// call does not duplicate it.
func (d *Doc) QueuePR(rec QueuedPR) {
	d.dropQueued(func(q *jsonx.Object) bool { return jsonx.Str(q, "issue") == rec.Issue })
	raw, _ := d.doc.Get("prs")
	l, _ := raw.([]any)
	d.doc.Set("prs", append(l, rec.object()))
}

// DropQueued removes the records match accepts.
func (d *Doc) DropQueued(match func(QueuedPR) bool) {
	d.dropQueued(func(o *jsonx.Object) bool { return match(queuedFrom(o)) })
}

func (d *Doc) dropQueued(match func(*jsonx.Object) bool) {
	raw, ok := d.doc.Get("prs")
	list, _ := raw.([]any)
	if !ok || len(list) == 0 {
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

// SetRound records the round number.
func (d *Doc) SetRound(n int) { d.doc.Set("round", n) }

// SetHost records the round host.
func (d *Doc) SetHost(h string) { d.doc.Set("host", h) }

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
	list, _ := d.doc.Get("slots")
	l, _ := list.([]any)
	d.doc.Set("slots", append(l, s.o))
}

// SortSlots orders the slot list by name.
func (d *Doc) SortSlots() {
	list, _ := d.doc.Get("slots")
	l, _ := list.([]any)
	sort.SliceStable(l, func(i, j int) bool {
		a, _ := l[i].(*jsonx.Object)
		b, _ := l[j].(*jsonx.Object)
		return a != nil && b != nil && jsonx.Str(a, "name") < jsonx.Str(b, "name")
	})
}

// Update is a locked read-modify-write of the registry; a missing or corrupt
// file starts from {"slots": []}.
func Update(root string, mutate func(d *Doc)) error {
	return update(root, slotsDefault(), mutate)
}

// update is Update with the document a missing or corrupt file starts from.
func update(root string, def *jsonx.Object, mutate func(d *Doc)) error {
	return fsio.UpdateJSON(RegistryPath(root), def, func(v any) (any, error) {
		doc, ok := v.(*jsonx.Object)
		if !ok {
			doc = def
		}
		mutate(&Doc{Registry{doc: doc}})
		return doc, nil
	})
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

// git runs git and trims one trailing newline from stdout, like $(...).
func (e Env) git(dir string, args ...string) (string, int) {
	res, err := e.Git(e.context(), dir, args...)
	out, code := res.Stdout, res.ExitCode
	if err != nil {
		return "", 127
	}
	return strings.TrimRight(out, "\n"), code
}

// Env is what the worker operations touch outside their own memory. Every
// field has a production default; tests replace them, and NewHost above all:
// this round runs inside herdr and tmux, so no test may reach the real ones.
type Env struct {
	// Ctx bounds every git call and is cancelled on SIGINT/SIGTERM by the
	// CLI. Nil means context.Background().
	Ctx context.Context
	Git git.Runner
	// NewHost returns the host for a work.dispatch value.
	NewHost func(dispatch string) host.Host
	// Sleep defaults to time.Sleep (poll's settle).
	Sleep func(time.Duration)
	// Now defaults to time.Now (relay timestamps).
	Now func() time.Time
	// Getenv defaults to os.Getenv (ROTA_GATE_SHA_WAIT).
	Getenv func(string) string
	// Forge returns the forge a provider ("github" or "gitlab") runs in dir.
	// Every forge call goes through internal/tracker, never an exec of gh or
	// glab from here. Tests inject a fake Forge.
	Forge func(provider, dir string, cfg any) (Forge, error)
	// Shell runs one verification command through `sh -c` in dir and returns
	// its combined output and exit code.
	Shell func(ctx context.Context, dir, command string) (output string, code int)
	// Run runs one tool with extra environment (CODEX_HOME) and returns what
	// it left behind; a command that ran and failed is a Result with ExitCode
	// set, an error means it could not run at all. Every codex and herdr call
	// of the codex preflight goes through it, so tests need no real binary.
	Run func(ctx context.Context, name string, args, env []string) (host.Result, error)
	// Executable is the running rota binary, which a codex worker's prompt-check
	// hook calls back; nil means os.Executable.
	Executable func() (string, error)
	// LookPath reports whether a binary is installed; nil means exec.LookPath.
	LookPath func(string) (string, error)
}

func (e Env) context() context.Context {
	if e.Ctx != nil {
		return e.Ctx
	}
	return context.Background()
}

func (e Env) withDefaults() Env {
	if e.Git == nil {
		e.Git = git.Exec
	}
	if e.NewHost == nil {
		e.NewHost = func(d string) host.Host { return host.New(d, host.Deps{}) }
	}
	if e.Sleep == nil {
		e.Sleep = time.Sleep
	}
	if e.Now == nil {
		e.Now = time.Now
	}
	if e.Getenv == nil {
		e.Getenv = os.Getenv
	}
	if e.Forge == nil {
		e.Forge = func(provider, dir string, cfg any) (Forge, error) {
			return tracker.NewFromConfig(e.context(), cfg, provider, dir)
		}
	}
	if e.Shell == nil {
		e.Shell = execShell
	}
	if e.Run == nil {
		e.Run = execRun
	}
	if e.Executable == nil {
		e.Executable = os.Executable
	}
	if e.LookPath == nil {
		e.LookPath = exec.LookPath
	}
	return e
}

// execRun is the production Env.Run. It bounds the call like the host runner.
func execRun(ctx context.Context, name string, args, env []string) (host.Result, error) {
	return proc.Run(ctx, proc.Cmd{Name: name, Args: args, Env: env, Timeout: host.CallTimeout})
}

// execShell is the production Env.Shell. A verification command may run as
// long as the caller's context allows, so it sets no timeout of its own.
func execShell(ctx context.Context, dir, command string) (string, int) {
	res, err := proc.Run(ctx, proc.Cmd{
		Name: "sh", Args: []string{"-c", command}, Dir: dir,
		Timeout: proc.NoTimeout, Combined: true, Group: true,
	})
	if err != nil {
		return err.Error(), 127
	}
	return res.Stdout, res.ExitCode
}

// RecordBounce counts one gate bounce against an item (`bounces` in the
// registry maps the issue to how often the gate sent its PR back) and returns
// the new count. Counts live per item, not per slot, so a transfer to another
// slot does not reset them. head is the PR head the gate refused: the same head
// seen again (a re-gate before the worker pushed anything) is not a new bounce
// and returns the count unchanged. head "" always counts.
func RecordBounce(root, issue, head string) (n int, err error) {
	err = Update(root, func(d *Doc) {
		doc := d.doc
		b := bouncesOf(doc)
		heads := bounceHeadsOf(doc)
		n = bounceCount(b, issue)
		if last, _ := heads.Get(issue); head != "" && last == any(head) && n > 0 {
			return
		}
		n++
		b.Set(issue, json.Number(strconv.Itoa(n)))
		doc.Set("bounces", b)
		if head != "" {
			heads.Set(issue, head)
			doc.Set("bounceHeads", heads)
		}
	})
	return n, err
}

// Bounces is how often an item's PR has been sent back so far (0 when never).
func (r Registry) Bounces(issue string) int {
	return bounceCount(bouncesOf(r.doc), issue)
}

// ClearBounces forgets an item's count: its PR merged or it was handed over.
func ClearBounces(root, issue string) error {
	return Update(root, func(d *Doc) {
		doc := d.doc
		if b := bouncesOf(doc); bounceCount(b, issue) > 0 {
			b.Delete(issue)
			doc.Set("bounces", b)
			heads := bounceHeadsOf(doc)
			heads.Delete(issue)
			doc.Set("bounceHeads", heads)
		}
	})
}

// bounceHeadsOf is the issue -> PR head SHA of the last counted bounce.
func bounceHeadsOf(doc *jsonx.Object) *jsonx.Object {
	if v, _ := doc.Get("bounceHeads"); v != nil {
		if b, ok := v.(*jsonx.Object); ok {
			return b
		}
	}
	return jsonx.NewObject()
}

func bouncesOf(doc *jsonx.Object) *jsonx.Object {
	if v, _ := doc.Get("bounces"); v != nil {
		if b, ok := v.(*jsonx.Object); ok {
			return b
		}
	}
	return jsonx.NewObject()
}

func bounceCount(b *jsonx.Object, issue string) int {
	v, _ := b.Get(issue)
	if n, ok := v.(json.Number); ok {
		if i, err := strconv.Atoi(n.String()); err == nil {
			return i
		}
	}
	return 0
}
