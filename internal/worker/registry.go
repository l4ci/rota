// Package worker is the Go port of the /rota-work worker helpers
// (bin/hv-worker-pool, -reset, -account, -dispatch, -poll, -gate, -session).
// The registry is .rota/workers.json at the project root; its files stay
// byte-identical to what the old helpers write.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/l4ci/rota/internal/exitcode"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker"
)

func fail(exit int, msg string) *exitcode.Error { return &exitcode.Error{Exit: exit, Message: msg} }

// RegistryPath is the registry file under the project root.
func RegistryPath(root string) string { return filepath.Join(root, ".rota", "workers.json") }

// Registry is a loaded .rota/workers.json.
type Registry struct {
	Doc    *jsonx.Object
	Exists bool
}

// LoadRegistry reads the registry; a missing or corrupt file reads as
// {"slots": []} with Exists false, like hvlib_io.load_json.
func LoadRegistry(root string) Registry {
	def := jsonx.NewObject()
	def.Set("slots", []any{})
	if o, ok := fsio.LoadJSON(RegistryPath(root), nil).(*jsonx.Object); ok {
		return Registry{Doc: o, Exists: true}
	}
	return Registry{Doc: def}
}

// Slots lists the slot objects of the registry.
func (r Registry) Slots() []*Slot {
	raw, _ := r.Doc.Get("slots")
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

// PRs lists the queued PR records: open PRs whose slot moved on to another
// issue (`prs` in the registry). A record is {issue, branch, pr, base, from,
// claimId, round, relays}; the issue and its claim stay taken while it waits.
func (r Registry) PRs() []*jsonx.Object { return queuedOf(r.Doc) }

func queuedOf(doc *jsonx.Object) []*jsonx.Object {
	raw, _ := doc.Get("prs")
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
func (r Registry) QueuedPR(ref string) *jsonx.Object {
	n, ok := PRRefNumber(ref)
	if !ok {
		return nil
	}
	for _, q := range r.PRs() {
		if m, ok := PRRefNumber(Str(q, "pr")); ok && m == n {
			return q
		}
	}
	return nil
}

// QueuedIssue finds the queued record holding an issue (backend spelling,
// compared case-insensitively, `#` ignored).
func (r Registry) QueuedIssue(id string) *jsonx.Object {
	want := strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(id), "#"))
	for _, q := range r.PRs() {
		if want != "" && strings.ToUpper(strings.TrimPrefix(Str(q, "issue"), "#")) == want {
			return q
		}
	}
	return nil
}

// QueuePR appends a record to doc under an Update, replacing one for the same
// issue so a repeated call does not duplicate it.
func QueuePR(doc *jsonx.Object, rec *jsonx.Object) {
	DropQueued(doc, func(q *jsonx.Object) bool { return Str(q, "issue") == Str(rec, "issue") })
	raw, _ := doc.Get("prs")
	l, _ := raw.([]any)
	doc.Set("prs", append(l, rec))
}

// DropQueued removes the records match accepts from doc under an Update.
func DropQueued(doc *jsonx.Object, match func(*jsonx.Object) bool) {
	raw, ok := doc.Get("prs")
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
	doc.Set("prs", keep)
}

// RemoveQueuedPR drops the record for a PR ref, locked.
func RemoveQueuedPR(root, ref string) error {
	return Update(root, slotsDefault(), func(doc *jsonx.Object) {
		if q := (Registry{Doc: doc}).QueuedPR(ref); q != nil {
			DropQueued(doc, func(o *jsonx.Object) bool { return o == q })
		}
	})
}

// Str reads a string field, "" when absent, null or not a string.
func Str(o *jsonx.Object, key string) string {
	v, _ := o.Get(key)
	s, _ := v.(string)
	return s
}

// Bool reads a bool field, false when absent, null or not a bool.
func Bool(o *jsonx.Object, key string) bool {
	v, _ := o.Get(key)
	b, _ := v.(bool)
	return b
}

// Update is a locked read-modify-write of the registry. def is used when the
// file is missing or corrupt, mutate edits the document in place.
func Update(root string, def *jsonx.Object, mutate func(doc *jsonx.Object)) error {
	return fsio.UpdateJSON(RegistryPath(root), def, func(v any) (any, error) {
		doc, ok := v.(*jsonx.Object)
		if !ok {
			doc = def
		}
		mutate(doc)
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

// GitFunc runs git in dir and returns stdout, stderr and the exit code. A
// non-nil error means git could not run at all.
type GitFunc func(ctx context.Context, dir string, args ...string) (stdout, stderr string, code int, err error)

// ExecGit is the production GitFunc. git is safe to run for real in tests that
// use their own temp repositories; herdr and tmux never go through here.
func ExecGit(ctx context.Context, dir string, args ...string) (string, string, int, error) {
	res, err := git.Repo{Dir: dir}.Run(ctx, args...)
	return res.Stdout, res.Stderr, res.Code, err
}

// git runs git and trims one trailing newline from stdout, like $(...).
func (e Env) git(dir string, args ...string) (string, int) {
	out, _, code, err := e.Git(e.context(), dir, args...)
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
	Git GitFunc
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
	Forge func(provider, dir string, retryWait time.Duration) (Forge, error)
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
		e.Git = ExecGit
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
		e.Forge = func(provider, dir string, wait time.Duration) (Forge, error) {
			return tracker.New(e.context(), tracker.Settings{Provider: provider, RetryWait: wait}, provider, dir)
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
	ctx, cancel := context.WithTimeout(ctx, host.CallTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	r := host.Result{Stdout: out.String(), Stderr: errb.String()}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		r.ExitCode = ee.ExitCode()
		return r, nil
	}
	return r, err
}

func execShell(ctx context.Context, dir, command string) (string, int) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	// Own process group, killed whole on cancel: a killed sh leaves its child
	// holding the output pipe, which would block Wait.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return string(out), ee.ExitCode()
	}
	if err != nil {
		return err.Error(), 127
	}
	return string(out), 0
}

// RecordBounce counts one gate bounce against an item (`bounces` in the
// registry maps the issue to how often the gate sent its PR back) and returns
// the new count. Counts live per item, not per slot, so a transfer to another
// slot does not reset them. head is the PR head the gate refused: the same head
// seen again (a re-gate before the worker pushed anything) is not a new bounce
// and returns the count unchanged. head "" always counts.
func RecordBounce(root, issue, head string) (n int, err error) {
	err = Update(root, slotsDefault(), func(doc *jsonx.Object) {
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

// ClearBounces forgets an item's count: its PR merged or it was handed over.
func ClearBounces(root, issue string) error {
	return Update(root, slotsDefault(), func(doc *jsonx.Object) {
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
