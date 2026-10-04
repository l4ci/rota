// Package worker is the Go port of the /rota-work worker helpers
// (bin/hv-worker-pool, -reset, -account, -dispatch, -poll, -gate, -session).
// The registry is .rota/workers.json at the project root; its files stay
// byte-identical to what the old helpers write.
package worker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker"
)

// Error is a verb failure with its exit code from the exit table. Data is the
// failure data a verb may attach on exit 1 and 4.
type Error struct {
	Exit    int
	Message string
	Hint    string
	Data    any
}

func (e *Error) Error() string { return e.Message }

func fail(exit int, msg string) *Error { return &Error{Exit: exit, Message: msg} }

// Exit codes, mirrored from internal/cli so this package does not import it.
const (
	ExitFailed      = 1
	ExitUsage       = 2
	ExitResolution  = 3
	ExitRefused     = 4
	ExitUnavailable = 5
	ExitRetry       = 6
)

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
func (r Registry) Slots() []*jsonx.Object {
	raw, _ := r.Doc.Get("slots")
	list, _ := raw.([]any)
	var out []*jsonx.Object
	for _, e := range list {
		if o, ok := e.(*jsonx.Object); ok {
			out = append(out, o)
		}
	}
	return out
}

// Slot finds a slot by name.
func (r Registry) Slot(name string) *jsonx.Object {
	for _, s := range r.Slots() {
		if Str(s, "name") == name {
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
	def := jsonx.NewObject()
	def.Set("slots", []any{})
	return Update(root, def, func(doc *jsonx.Object) {
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

// updateSlot edits one slot under the lock, reporting whether it was found.
func updateSlot(root, name string, mutate func(s *jsonx.Object)) (found bool, err error) {
	def := jsonx.NewObject()
	def.Set("slots", []any{})
	err = Update(root, def, func(doc *jsonx.Object) {
		for _, s := range (Registry{Doc: doc}).Slots() {
			if Str(s, "name") == name {
				mutate(s)
				found = true
			}
		}
	})
	return found, err
}

// SlotData is a slot as `data` shows it: null registry fields are absent.
func SlotData(s *jsonx.Object) *jsonx.Object {
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

// gitTimeout bounds one git call.
const gitTimeout = 2 * time.Minute

// ExecGit is the production GitFunc. git is safe to run for real in tests that
// use their own temp repositories; herdr and tmux never go through here.
func ExecGit(ctx context.Context, dir string, args ...string) (string, string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	// git translates its messages ("CONFLICT (content)" among them) in some
	// locales; the gate matches on them, so ask for the C locale.
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANGUAGE=C")
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out.String(), errb.String(), ee.ExitCode(), nil
	}
	return out.String(), errb.String(), 0, err
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
	// Forge returns the forge CLI runner for a provider ("github" or
	// "gitlab") running in dir. Every forge call goes through internal/tracker,
	// never an exec of gh or glab from here. Tests inject a fake Exec.
	Forge func(provider, dir string, retryWait time.Duration) *tracker.CLI
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
		e.Forge = func(provider, dir string, wait time.Duration) *tracker.CLI {
			return &tracker.CLI{Provider: provider, Dir: dir, RetryWait: wait}
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
