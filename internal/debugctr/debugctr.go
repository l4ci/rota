// Package debugctr ports hv-debug-counter: the persistent fix-attempt
// counter behind /rota-debug's Iron Law. State lives in
// .rota/debug/<session>.json, where session is the current git branch with
// "/" replaced by "-". The file keeps its old snake_case keys so rota and the
// old helper can share it.
package debugctr

import (
	"context"
	"encoding/json"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/git"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
)

// Counter addresses one session's state file.
type Counter struct {
	Session string
	Path    string
}

// Open derives the session from the current branch of the git repository
// at root and returns its Counter. Not being in a git repository is exit 5.
func Open(root string) (*Counter, error) {
	res, err := git.Repo{Dir: root}.Run(context.Background(), "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || res.Code != 0 {
		return nil, exitcode.Errf(exitcode.ExitUnavailable, "not in a git repository (a debug session is keyed by the current branch)")
	}
	branch := strings.TrimSpace(res.Stdout)
	if branch == "" {
		return nil, exitcode.Errf(exitcode.ExitUnavailable, "could not determine current git branch")
	}
	session := strings.ReplaceAll(branch, "/", "-")
	return &Counter{Session: session, Path: filepath.Join(root, ".rota", "debug", session+".json")}, nil
}

func nowISO() string {
	t := time.Now().UTC()
	if t.Nanosecond()/1000 == 0 {
		return t.Format("2006-01-02T15:04:05") + "+00:00"
	}
	return t.Format("2006-01-02T15:04:05.000000") + "+00:00"
}

func (c *Counter) def() *jsonx.Object {
	o := jsonx.NewObject()
	o.Set("session", c.Session)
	o.Set("bug_id", "")
	o.Set("started_at", "")
	o.Set("failed_fixes", 0)
	o.Set("hypothesis_cycles", 0)
	o.Set("attempts", []any{})
	return o
}

func (c *Counter) exists() bool {
	_, err := os.Stat(c.Path)
	return err == nil
}

func (c *Counter) require() error {
	if !c.exists() {
		return exitcode.Errf(exitcode.ExitResolution, "no debug session file at .rota/debug/%s.json", c.Session).
			WithHint("run: rota debug counter init <bugId>")
	}
	return nil
}

func asObj(v any) *jsonx.Object {
	if o, ok := v.(*jsonx.Object); ok {
		return o
	}
	return jsonx.NewObject()
}

func intOf(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case json.Number:
		i, _ := strconv.Atoi(n.String())
		return i
	}
	return 0
}

func attemptsOf(o *jsonx.Object) []any {
	v, _ := o.Get("attempts")
	l, _ := v.([]any)
	return l
}

// Init creates the state file; false when it already existed (idempotent).
// The existence check runs under the file's lock, so of two concurrent
// calls exactly one reports changed.
func (c *Counter) Init(bugID string) (created bool, err error) {
	err = fsio.Locked(c.Path, fsio.LockTimeout, func() error {
		if c.exists() {
			return nil
		}
		o := c.def()
		o.Set("bug_id", bugID)
		o.Set("started_at", nowISO())
		if err := fsio.WriteJSONAtomic(c.Path, o); err != nil {
			return err
		}
		created = true
		return nil
	})
	return
}

func (c *Counter) update(mutate func(*jsonx.Object) error) error {
	return fsio.UpdateJSON(c.Path, c.def(), func(v any) (any, error) {
		o := asObj(v)
		if err := mutate(o); err != nil {
			return nil, err
		}
		return o, nil
	})
}

// RecordAttempt appends a pending attempt and returns its number.
func (c *Counter) RecordAttempt(hypothesis, commit string) (int, error) {
	if err := c.require(); err != nil {
		return 0, err
	}
	n := 0
	err := c.update(func(o *jsonx.Object) error {
		list := attemptsOf(o)
		n = len(list) + 1
		a := jsonx.NewObject()
		a.Set("n", n)
		a.Set("started_at", nowISO())
		a.Set("hypothesis", hypothesis)
		a.Set("commit", commit)
		a.Set("outcome", "pending")
		o.Set("attempts", append(list, a))
		return nil
	})
	return n, err
}

// closeLast ends the last pending attempt with outcome and returns it.
func (c *Counter) closeLast(outcome string, mutate func(o *jsonx.Object)) (attempt, failed int, err error) {
	if err = c.require(); err != nil {
		return
	}
	err = c.update(func(o *jsonx.Object) error {
		list := attemptsOf(o)
		if len(list) == 0 {
			return exitcode.Errf(exitcode.ExitRefused, "no attempts recorded yet")
		}
		last := asObj(list[len(list)-1])
		if v, _ := last.Get("outcome"); v != "pending" {
			s, _ := v.(string)
			return exitcode.Errf(exitcode.ExitRefused, "last attempt outcome is '%s', not 'pending'", s)
		}
		last.Set("outcome", outcome)
		last.Set("ended_at", nowISO())
		mutate(o)
		attempt = intOf(mustGet(last, "n"))
		failed = intOf(mustGet(o, "failed_fixes"))
		return nil
	})
	return
}

func mustGet(o *jsonx.Object, k string) any { v, _ := o.Get(k); return v }

// Fail marks the last attempt failed and returns the new failed-fix count.
func (c *Counter) Fail() (int, error) {
	_, failed, err := c.closeLast("failed", func(o *jsonx.Object) {
		o.Set("failed_fixes", intOf(mustGet(o, "failed_fixes"))+1)
	})
	return failed, err
}

// Pass marks the last attempt passed and returns its number.
func (c *Counter) Pass() (int, error) {
	n, _, err := c.closeLast("passed", func(*jsonx.Object) {})
	return n, err
}

// IncCycle bumps hypothesis_cycles and returns the new value.
func (c *Counter) IncCycle() (int, error) {
	if err := c.require(); err != nil {
		return 0, err
	}
	n := 0
	err := c.update(func(o *jsonx.Object) error {
		n = intOf(mustGet(o, "hypothesis_cycles")) + 1
		o.Set("hypothesis_cycles", n)
		return nil
	})
	return n, err
}

// Clear removes the state file; false when there was none.
func (c *Counter) Clear() (bool, error) {
	err := os.Remove(c.Path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

// Raw is the state file's text, verbatim.
func (c *Counter) Raw() (string, error) {
	if err := c.require(); err != nil {
		return "", err
	}
	b, err := os.ReadFile(c.Path)
	return string(b), err
}

// State is the parsed state file.
func (c *Counter) State() (*jsonx.Object, error) {
	if err := c.require(); err != nil {
		return nil, err
	}
	return asObj(fsio.LoadJSON(c.Path, c.def())), nil
}

// Summary is the Iron Law halt note. The caller decides what an empty
// attempt list means; Summary renders whatever the state holds.
func (c *Counter) Summary(o *jsonx.Object) (md, bugID string, failed int) {
	bugID = "?"
	if v, ok := o.Get("bug_id"); ok {
		bugID, _ = v.(string)
	}
	failed = intOf(mustGet(o, "failed_fixes"))
	plural := "s"
	if failed == 1 {
		plural = ""
	}
	lines := []string{
		"# Iron Law triggered for [" + bugID + "]",
		"",
		strconv.Itoa(failed) + " fix attempt" + plural + " failed to resolve the bug. Halting.",
		"",
		"## Attempts",
		"",
	}
	for _, av := range attemptsOf(o) {
		a := asObj(av)
		hyp, _ := mustGet(a, "hypothesis").(string)
		if utf8.RuneCountInString(hyp) > 120 {
			hyp = string([]rune(hyp)[:117]) + "..."
		}
		commit := "unknown"
		if v, ok := a.Get("commit"); ok {
			commit, _ = v.(string)
		}
		lines = append(lines, strconv.Itoa(intOf(mustGet(a, "n")))+". "+commit+" — "+hyp)
	}
	lines = append(lines,
		"",
		"## Next steps",
		"",
		"- Run `/rota-pause` to leave a handoff note and step away.",
		"- Or re-read the symptom — the root cause is likely in a different subsystem than the hypotheses so far have explored.",
		"- The failed-fix count is per item and survives a new branch; only `rota debug reset "+bugID+"`, after a human approves it, starts it again.",
	)
	return strings.Join(lines, "\n"), bugID, failed
}
