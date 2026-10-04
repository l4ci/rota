// Package issues is the upstream-issue side of rota: provider detection, list,
// label, close and the open-only filter of the cross-reference index. It
// ports bin/hv-issues-{provider,list,label,close,imported}, which called
// bin/hv-tracker-call; every forge call here goes through tracker.CLI, with
// the arguments the old helpers sent.
package issues

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/tracker"
)

// ErrCommitNotFound: the commit is not in the repository (exit 3).
var ErrCommitNotFound = errors.New("commit not found")

// ErrNoProvider: neither the origin remote nor issues.provider names a forge
// (exit 3).
var ErrNoProvider = errors.New("no issue provider")

// ErrBadOutput: the forge CLI printed something that is not the expected JSON.
var ErrBadOutput = errors.New("unexpected forge output")

// Env is what every call runs with.
type Env struct {
	Settings tracker.Settings
	// Opts reach every forge CLI the calls build (tests swap the executor).
	Opts []tracker.Option
}

// Provider is "github", "gitlab" or "unknown" for dir ("" is the process
// cwd). The origin host decides, as in hv-issues-provider; issues.provider
// is only the fallback when origin is missing or names no forge.
func Provider(ctx context.Context, env Env, dir string) string {
	p, err := tracker.ResolveProvider(ctx, "", "", dir, execOf(env))
	if err == nil {
		return p
	}
	if c := env.Settings.Provider; c == "github" || c == "gitlab" {
		return c
	}
	return "unknown"
}

func noProvider(verb string) error {
	return fmt.Errorf("%w: cannot %s (no origin remote naming github or gitlab, and issues.provider is not set)", ErrNoProvider, verb)
}

// execOf is the executor the env's options install, or nil for the real one.
func execOf(env Env) tracker.Exec {
	c := &tracker.CLI{}
	for _, o := range env.Opts {
		o(c)
	}
	return c.Exec
}

func unavailable(format string, a ...any) *tracker.Error {
	return &tracker.Error{Kind: tracker.KindUnavailable, Code: 3, Message: fmt.Sprintf(format, a...)}
}

func (e Env) cli(ctx context.Context, provider, dir string) (*tracker.CLI, error) {
	return tracker.NewCLI(ctx, e.Settings, provider, dir, e.Opts...)
}

// call is one forge call: the CLI's stdout, and its exit code. An error is
// the tracker's own (the CLI missing, a rate limit, an unreadable start).
func call(ctx context.Context, cl *tracker.CLI, args ...string) (string, string, int, error) {
	r, err := cl.Run(ctx, args, nil)
	if err != nil {
		return "", "", 0, err
	}
	return string(r.Stdout), string(r.Stderr), r.ExitCode, nil
}

// failed is a forge call that exited non-zero, as a tracker failure: exit 3
// when the forge says the object is not found, else exit 5.
func failed(stderr string, code int) *tracker.Error {
	e := tracker.FailedCall(stderr, code)
	if e.Message == "" {
		e.Message = "forge CLI exited " + strconv.Itoa(code)
	}
	return e
}

func cliName(provider string) string {
	if provider == "gitlab" {
		return "glab"
	}
	return "gh"
}

// authed checks that the CLI is installed and logged in, like the old
// `hv-tracker-call -- auth status` guard; any failure is unavailable.
func authed(ctx context.Context, cl *tracker.CLI) error {
	_, _, code, err := call(ctx, cl, "auth", "status")
	if err != nil || code != 0 {
		return unavailable("%s not installed or not authenticated", cliName(cl.Provider))
	}
	return nil
}

// ---- label ----------------------------------------------------------------

var reLabelErr = regexp.MustCompile(`(?i)label|not found|could not`)

// Label adds or removes one label on an upstream issue and reports whether it
// changed anything. A label already there (or already absent) is a no-op:
// changed is read from the issue first, and a failed read leaves it true.
// autoCreate lets a missing label be created on the forge.
func Label(ctx context.Context, env Env, dir string, number int, label string, add, autoCreate bool) (bool, error) {
	provider := Provider(ctx, env, dir)
	if provider == "unknown" {
		return false, noProvider("label upstream issue")
	}
	cl, err := env.cli(ctx, provider, dir)
	if err != nil {
		return false, err
	}
	n := strconv.Itoa(number)
	changed := true
	if has, ok := hasLabel(ctx, cl, provider, n, label); ok {
		changed = has != add
	}
	run := func(args ...string) (string, int, error) {
		_, stderr, code, err := call(ctx, cl, args...)
		return stderr, code, err
	}
	must := func(args ...string) error {
		stderr, code, err := run(args...)
		if err != nil {
			return err
		}
		if code != 0 {
			return failed(stderr, code)
		}
		return nil
	}
	switch {
	case provider == "github" && add:
		stderr, code, err := run("issue", "edit", n, "--add-label", label)
		if err != nil {
			// The old helper judged a failed edit by its stderr alone, a rate limit included.
			var te *tracker.Error
			if !errors.As(err, &te) {
				return false, err
			}
			stderr, code = te.Message, 1
		}
		if code != 0 {
			if !autoCreate || !reLabelErr.MatchString(stderr) {
				return false, failed(stderr, code)
			}
			if err := must("label", "create", label, "--force"); err != nil {
				return false, err
			}
			if err := must("issue", "edit", n, "--add-label", label); err != nil {
				return false, err
			}
		}
	case provider == "github":
		if err := must("issue", "edit", n, "--remove-label", label); err != nil {
			return false, err
		}
	case add:
		if autoCreate {
			// glab exits non-zero on a duplicate; the old helper hid every failure here.
			_, _, _ = run("label", "create", "--name", label)
		}
		if err := must("issue", "update", n, "--label", label); err != nil {
			return false, err
		}
	default:
		if err := must("issue", "update", n, "--unlabel", label); err != nil {
			return false, err
		}
	}
	return changed, nil
}

// hasLabel reads whether the issue carries label; ok is false when the read
// failed.
func hasLabel(ctx context.Context, cl *tracker.CLI, provider, n, label string) (has, ok bool) {
	var args []string
	if provider == "github" {
		args = []string{"issue", "view", n, "--json", "labels"}
	} else {
		args = []string{"issue", "view", n, "--output", "json"}
	}
	out, _, code, err := call(ctx, cl, args...)
	if err != nil || code != 0 {
		return false, false
	}
	doc, err := jsonx.Decode([]byte(out))
	o, isObj := doc.(*jsonx.Object)
	if err != nil || !isObj {
		return false, false
	}
	ls, _ := o.Get("labels")
	list, _ := ls.([]any)
	for _, l := range list {
		switch t := l.(type) {
		case string:
			if t == label {
				return true, true
			}
		case *jsonx.Object:
			if name, _ := t.Get("name"); name == label {
				return true, true
			}
		}
	}
	return false, true
}

// ---- close ----------------------------------------------------------------

// Close closes one upstream issue with a comment naming the commit that
// shipped it. changed is false when the issue was already closed. dir is the
// checkout the commit lives in.
func Close(ctx context.Context, env Env, dir string, number int, commit, item string) (bool, error) {
	short, err := shortSHA(dir, commit)
	if err != nil {
		return false, err
	}
	body := "Closed by rota: shipped in " + short
	if item != "" {
		body += " ([" + item + "])"
	}
	body += "\n\n" + marker.Line("shipped")
	provider := Provider(ctx, env, dir)
	if provider == "unknown" {
		return false, noProvider("close upstream issue")
	}
	cl, err := env.cli(ctx, provider, dir)
	if err != nil {
		return false, err
	}
	if err := authed(ctx, cl); err != nil {
		return false, err
	}
	n := strconv.Itoa(number)
	if provider == "github" {
		// A failed state read counts as "not closed", as the old guard did.
		state, _, code, err := call(ctx, cl, "issue", "view", n, "--json", "state", "-q", ".state")
		if err == nil && code == 0 && strings.TrimRight(state, "\n") == "CLOSED" {
			return false, nil
		}
		_, stderr, code, err := call(ctx, cl, "issue", "close", n, "--comment", body)
		if err != nil {
			return false, err
		}
		if code != 0 {
			return false, failed(stderr, code)
		}
		return true, nil
	}
	out, _, code, err := call(ctx, cl, "issue", "view", n, "--output", "json")
	if err == nil && code == 0 {
		if doc, derr := jsonx.Decode([]byte(out)); derr == nil {
			if o, ok := doc.(*jsonx.Object); ok {
				if st, _ := o.Get("state"); st == "closed" {
					return false, nil
				}
			}
		}
	}
	for _, args := range [][]string{{"issue", "note", n, "--message", body}, {"issue", "close", n}} {
		_, stderr, code, err := call(ctx, cl, args...)
		if err != nil {
			return false, err
		}
		if code != 0 {
			return false, failed(stderr, code)
		}
	}
	return true, nil
}

// shortSHA is `git rev-parse --short <commit>` in dir.
func shortSHA(dir, commit string) (string, error) {
	notFound := fmt.Errorf("%w: commit '%s' not found in current repository", ErrCommitNotFound, commit)
	if strings.HasPrefix(commit, "-") {
		return "", notFound
	}
	res, err := git.Repo{Dir: dir}.Run(context.Background(), "rev-parse", "--short", commit)
	if errors.Is(err, git.ErrNoGit) {
		return "", unavailable("git is not installed")
	}
	if err != nil || res.Code != 0 {
		return "", notFound
	}
	return pystr.Strip(res.Stdout), nil
}

// ---- imported, open-only ----------------------------------------------------------

// StillOpen reports whether the upstream issue of a cross-reference is open:
// provider says which forge, dir is the checkout of the entry's repo. An issue
// whose state cannot be read (no CLI, no auth, deleted) is not open.
func StillOpen(ctx context.Context, env Env, provider, dir string, issue int) bool {
	cl, err := env.cli(ctx, provider, dir)
	if err != nil {
		return false
	}
	n := strconv.Itoa(issue)
	if provider == "github" {
		out, _, code, err := call(ctx, cl, "issue", "view", n, "--json", "state", "-q", ".state")
		return err == nil && code == 0 && strings.TrimRight(out, "\n") == "OPEN"
	}
	out, _, code, err := call(ctx, cl, "issue", "view", n, "--output", "json")
	if err != nil || code != 0 {
		return false
	}
	doc, err := jsonx.Decode([]byte(out))
	if err != nil {
		return false
	}
	o, ok := doc.(*jsonx.Object)
	if !ok {
		return false
	}
	st, _ := o.Get("state")
	return st == "opened"
}
