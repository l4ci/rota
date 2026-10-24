package tracker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/proc"
	"github.com/l4ci/rota/internal/projects"
)

// Exec starts name with args in dir and returns its output and exit code.
// A nil stdin gives the process an empty one. err is set only when the
// process could not run at all, or ctx ended it.
type Exec func(ctx context.Context, dir, name string, args []string, stdin []byte) (stdout, stderr []byte, code int, err error)

// DefaultTimeout bounds one CLI attempt when CLI.Timeout is zero.
const DefaultTimeout = 2 * time.Minute

// Result is one forge CLI call. Stderr ends with the truncation warning when
// a list hit the injected limit.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// CLI runs gh or glab for one resolved provider, as bin/hv-tracker-call did:
// list limits and GET pagination are injected when absent, a secondary rate
// limit stops at once, a primary one waits RetryWait and retries once, and an
// unauthenticated CLI is reported as unavailable.
type CLI struct {
	Provider  string // github | gitlab
	Dir       string // where the CLI runs; "" is the process cwd
	RetryWait time.Duration
	Timeout   time.Duration // per attempt; 0 is DefaultTimeout

	Exec     Exec
	LookPath func(string) (string, error)
	Sleep    func(time.Duration)

	// Cache, when set, serves repeated reads and is dropped by any other call.
	Cache *ReadCache
	// Conn finds the connection of an HTTP tracker (jira, linear, itsaplan); nil reads the
	// machine's `rota connect` connections.
	Conn ConnResolver
	// Env and Keychain are the machine the default lookup reads (WithMachine).
	Env      projects.Env
	Keychain func() connections.Store
}

var (
	reSecondary = regexp.MustCompile(`(?i)secondary rate limit|abuse detection`)
	rePrimary   = regexp.MustCompile(`(?i)api rate limit exceeded|http 429|429 too many requests|rate limit exceeded`)
	reAuth      = regexp.MustCompile(`(?i)gh auth login|glab auth login|not logged in|http 401|401 unauthorized`)
	reLimitGH   = regexp.MustCompile(`^-L\d+$`)
	reLimitGL   = regexp.MustCompile(`^-P\d+$`)
)

func (c *CLI) exec() Exec {
	if c.Exec != nil {
		return c.Exec
	}
	return osExec
}

func (c *CLI) lookPath(name string) (string, error) {
	if c.LookPath != nil {
		return c.LookPath(name)
	}
	return exec.LookPath(name)
}

func osExec(ctx context.Context, dir, name string, args []string, stdin []byte) ([]byte, []byte, int, error) {
	res, err := proc.Run(ctx, proc.Cmd{Name: name, Args: args, Dir: dir, Stdin: stdin})
	if err != nil {
		return nil, nil, 0, err
	}
	return []byte(res.Stdout), []byte(res.Stderr), res.ExitCode, nil
}

// ForgeKind is who asks which forge a directory uses. It decides one case
// alone, an origin remote that names no forge; see ResolveProvider.
type ForgeKind int

const (
	// ForgeActor is a verb that must act on a real forge: worker gate, worker
	// train, ship pr and release.
	ForgeActor ForgeKind = iota
	// ForgeReader is a read-only or display path: doctor, issues provider
	// and the round poll's best-effort PR reads.
	ForgeReader
)

// How a Resolution found its provider.
const (
	HowFlag     = "flag"      // the want argument, a --provider value
	HowConfig   = "config"    // forge.provider
	HowOrigin   = "origin"    // the origin host names the forge
	HowNoOrigin = "no-origin" // no origin remote: the local forge
	HowAssumed  = "assumed"   // a reader's github for an origin naming no forge
)

// Resolution is the forge a directory uses and how it was found.
type Resolution struct {
	// Provider is "github", "gitlab" or "local".
	Provider string
	// How is one of the How* constants.
	How string
	// Origin is the origin URL read, "" when a named provider decided or
	// there is no origin.
	Origin string
}

// ErrUnknownForge is what an UnknownForgeError Is: an origin naming no forge,
// met by a ForgeActor.
var ErrUnknownForge = errors.New("the origin names no forge")

// UnknownForgeHint is the one fix for an UnknownForgeError.
const UnknownForgeHint = "set forge.provider to github, gitlab or local"

// UnknownForgeError is ResolveProvider's refusal for an actor whose origin
// names no forge. The CLI exits 4 with it, blockedBy forge (exitmap).
type UnknownForgeError struct {
	// Origin is the origin URL that names no forge.
	Origin string
}

func (e *UnknownForgeError) Error() string {
	return "cannot determine the forge from origin " + e.Origin + " (set forge.provider)"
}

// Is makes errors.Is(err, ErrUnknownForge) true.
func (e *UnknownForgeError) Is(target error) bool { return target == ErrUnknownForge }

// Note is the one line a reader prints when it assumed github, "" otherwise.
func (r Resolution) Note() string {
	if r.How != HowAssumed {
		return ""
	}
	return "origin " + r.Origin + " names no forge; assumed github (set forge.provider)"
}

// ResolveProvider is the one answer to "which forge does dir use". want (a
// --provider value) decides unless it is "" or "auto", then configured
// (forge.provider) on the same terms, then the origin host: one naming github
// or gitlab is that forge. The fallback rule, per caller kind:
//
//   - no origin remote at all is the local forge, for both kinds;
//   - an origin naming no forge is an *UnknownForgeError for a ForgeActor
//     (the message names the origin; the CLI refuses with exit 4, blockedBy
//     forge), and github for a ForgeReader, with How HowAssumed so the verb
//     prints Note.
func ResolveProvider(ctx context.Context, kind ForgeKind, want, configured, dir string, x Exec) (Resolution, error) {
	c := &CLI{Dir: dir, Exec: x}
	return c.resolveForge(ctx, kind, want, configured)
}

// resolve is resolveForge for an actor that needs a forge CLI: a local forge
// is ErrLocalForge.
func (c *CLI) resolve(ctx context.Context, want, configured string) (string, error) {
	r, err := c.resolveForge(ctx, ForgeActor, want, configured)
	if err != nil {
		return "", err
	}
	if r.Provider == ProviderLocal {
		return "", ErrLocalForge
	}
	return r.Provider, nil
}

func (c *CLI) resolveForge(ctx context.Context, kind ForgeKind, want, configured string) (Resolution, error) {
	for _, n := range []struct{ p, how string }{{want, HowFlag}, {configured, HowConfig}} {
		switch n.p {
		case "github", "gitlab", ProviderLocal:
			return Resolution{Provider: n.p, How: n.how}, nil
		}
		if n.p != "" && n.p != "auto" {
			break // an unknown value is not a choice: detect from origin
		}
	}
	origin := c.originURL(ctx)
	if p := ProviderFromURL(origin); p != ProviderUnknown {
		return Resolution{Provider: p, How: HowOrigin, Origin: origin}, nil
	}
	if origin == "" {
		return Resolution{Provider: ProviderLocal, How: HowNoOrigin}, nil
	}
	if kind == ForgeReader {
		return Resolution{Provider: "github", How: HowAssumed, Origin: origin}, nil
	}
	return Resolution{}, &UnknownForgeError{Origin: origin}
}

// resolveIssues picks the provider of an issue tracker: want unless it is
// "" or "auto", then configured when it names github or gitlab (a local or auto
// forge says nothing about the tracker), then origin-URL detection. A repo
// with no origin cannot tell.
func (c *CLI) resolveIssues(ctx context.Context, want, configured string) (string, error) {
	p := want
	if p == "" || p == "auto" {
		p = configured
	}
	if p != "github" && p != "gitlab" {
		p = ProviderFromURL(c.originURL(ctx))
	}
	if p != "github" && p != "gitlab" {
		return "", unavailable("cannot determine the issue tracker provider (set issues.source to github or gitlab, or forge.provider)")
	}
	return p, nil
}

// ProviderUnknown is ProviderFromURL's answer for a remote naming no forge.
// What a caller does with it is ResolveProvider's rule, not the caller's.
const ProviderUnknown = "unknown"

// ProviderLocal is the answer for a forge with no remote: forge.provider local,
// or auto with no origin. It names no CLI and has no PRs.
const ProviderLocal = "local"

// reHost takes the hostname out of an SSH shorthand, SSH or HTTPS remote URL.
var reHost = regexp.MustCompile(`(?i)^(https?://|ssh://)?(git@)?([^:/\n]+)[:/].*`)

// RemoteHost is the lowercased hostname of a remote URL ("" for an empty one).
func RemoteHost(url string) string {
	host := url
	if m := reHost.FindStringSubmatch(url); m != nil {
		host = m[3]
	}
	return strings.ToLower(host)
}

// GitExec is r as an Exec, for a caller whose origin read goes through its own
// git seam (a git.Runner a test fakes) rather than the forge executor. It runs
// git only.
func GitExec(r git.Runner) Exec {
	return func(ctx context.Context, dir, name string, args []string, _ []byte) ([]byte, []byte, int, error) {
		if name != "git" {
			return nil, nil, 0, fmt.Errorf("tracker: GitExec runs git, not %s", name)
		}
		res, err := r(ctx, dir, args...)
		if err != nil {
			return nil, nil, 0, err
		}
		return []byte(res.Stdout), []byte(res.Stderr), res.ExitCode, nil
	}
}

// OriginURL is dir's origin remote URL, "" when git fails or has no origin.
func OriginURL(ctx context.Context, dir string, x Exec) string {
	c := &CLI{Dir: dir, Exec: x}
	return c.originURL(ctx)
}

func (c *CLI) originURL(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()
	out, _, code, err := c.exec()(ctx, c.Dir, "git", []string{"remote", "get-url", "origin"}, nil)
	if err != nil || code != 0 {
		return ""
	}
	return strings.TrimRight(string(out), "\n")
}

// CLIName is the forge CLI binary for provider: glab for "gitlab", gh otherwise.
func CLIName(provider string) string {
	if provider == "gitlab" {
		return "glab"
	}
	return "gh"
}

// CheckAuth runs `auth status`; any failure, a missing CLI included, is
// KindUnavailable.
func (c *CLI) CheckAuth(ctx context.Context) error {
	if res, err := c.Run(ctx, []string{"auth", "status"}, nil); err != nil || res.ExitCode != 0 {
		e := unavailable("%s not installed or not authenticated", CLIName(c.Provider))
		e.CLIMissing = IsCLIMissing(err)
		return e
	}
	return nil
}

// ProviderFromURL classifies a remote URL as hv-issues-provider did: "github"
// or "gitlab" when the host contains that word, else ProviderUnknown.
func ProviderFromURL(url string) string {
	host := RemoteHost(url)
	switch {
	case strings.Contains(host, "github"):
		return "github"
	case strings.Contains(host, "gitlab"):
		return "gitlab"
	}
	return ProviderUnknown
}

// Run makes one forge CLI call. A non-zero CLI exit is not an error: it comes
// back in Result.ExitCode with the CLI's output. Errors are KindUnavailable
// (CLI missing, unauthenticated, or an attempt past Timeout), KindRateLimited,
// and KindInternal (the process could not start, or ctx was cancelled).
// stdin is read once, and only when an argument takes it (`-`, or ending in
// `=-` or `@-`).
func (c *CLI) Run(ctx context.Context, args []string, stdin io.Reader) (Result, error) {
	if c.Cache == nil || keepsCache(args) {
		return c.run(ctx, args, stdin)
	}
	if !isRead(args) {
		// Drop before and after: a write that fails midway may still have landed.
		c.Cache.Clear()
		defer c.Cache.Clear()
		return c.run(ctx, args, stdin)
	}
	key := cacheKey(c.cacheScope(), args)
	if r, ok := c.Cache.get(key); ok {
		return r, nil
	}
	r, err := c.run(ctx, args, stdin)
	if err == nil && r.ExitCode == 0 {
		c.Cache.put(key, r)
	}
	return r, err
}

func (c *CLI) run(ctx context.Context, args []string, stdin io.Reader) (Result, error) {
	cli := CLIName(c.Provider)
	if _, err := c.lookPath(cli); err != nil {
		return Result{}, cliMissing(cli)
	}
	args, limit := c.inject(append([]string(nil), args...))

	var data []byte
	if takesStdin(args) {
		data = []byte{}
		if stdin != nil {
			b, err := io.ReadAll(stdin)
			if err != nil {
				return Result{}, internal("reading stdin: %v", err)
			}
			data = b
		}
	}
	attempt := func() (Result, error) {
		actx, cancel := context.WithTimeout(ctx, c.timeout())
		defer cancel()
		out, errb, code, err := c.exec()(actx, c.Dir, cli, args, data)
		switch {
		case err == nil:
			return Result{Stdout: out, Stderr: errb, ExitCode: code}, nil
		case ctx.Err() != nil:
			return Result{}, internal("%s: %v", cli, ctx.Err())
		case actx.Err() != nil:
			return Result{}, unavailable("%s timed out after %s", cli, c.timeout())
		case errors.Is(err, exec.ErrNotFound):
			return Result{}, cliMissing(cli)
		}
		return Result{}, internal("cannot run %s: %v", cli, err)
	}

	r, err := attempt()
	if err != nil {
		return Result{}, err
	}
	for try := 1; try <= 2 && r.ExitCode != 0; try++ {
		if reSecondary.Match(r.Stderr) {
			return Result{}, rateLimited("%s secondary rate limit — stopped; wait several minutes before retrying", c.Provider)
		}
		if !rePrimary.Match(r.Stderr) {
			break
		}
		if try == 2 {
			return Result{}, rateLimited("%s rate limit — stopped after one retry", c.Provider)
		}
		if err := c.sleep(ctx, c.RetryWait); err != nil {
			return Result{}, err
		}
		if r, err = attempt(); err != nil {
			return Result{}, err
		}
	}
	if r.ExitCode != 0 {
		if reAuth.Match(r.Stderr) {
			return Result{}, unavailable("%s is not authenticated; run '%s auth login'", cli, cli)
		}
		return r, nil
	}
	if limit > 0 {
		var rows []json.RawMessage
		if json.Unmarshal(r.Stdout, &rows) == nil && len(rows) == limit {
			r.Stderr = append(r.Stderr, []byte("warning: rota tracker call: result hit the list limit ("+strconv.Itoa(limit)+"); results may be truncated\n")...)
		}
	}
	return r, nil
}

func (c *CLI) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return DefaultTimeout
}

// sleep waits d before a retry, or until ctx ends.
func (c *CLI) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		c.Sleep(d)
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return internal("rate-limit wait cancelled: %v", ctx.Err())
	}
}

// inject adds the list limit and GET pagination when the caller left them
// out, and returns the limit it added (0 for none).
func (c *CLI) inject(args []string) ([]string, int) {
	limit := 0
	head := ""
	if len(args) >= 2 {
		head = args[0] + " " + args[1]
	}
	switch {
	case c.Provider == "github" && (head == "issue list" || head == "issue ls" || head == "pr list" || head == "pr ls"):
		if !hasFlag(args, "-L", "--limit") && !anyMatch(args, reLimitGH) {
			args = append(args, "--limit", "1000")
			limit = 1000
		}
	case c.Provider == "gitlab" && (head == "issue list" || head == "issue ls" || head == "mr list" || head == "mr ls"):
		if !hasFlag(args, "-P", "--per-page") && !anyMatch(args, reLimitGL) {
			args = append(args, "--per-page", "100")
			limit = 100
		}
	}
	if len(args) >= 1 && args[0] == "api" && !slices.Contains(args, "--paginate") {
		if apiMethod(args) == "GET" && !hasFields(args) {
			args = append(args, "--paginate")
		}
	}
	return args, limit
}

// hasFlag reports whether any argument is one of flags, or `--long=value`
// for a long one.
func hasFlag(args []string, flags ...string) bool {
	for _, a := range args {
		for _, f := range flags {
			if a == f || (strings.HasPrefix(f, "--") && strings.HasPrefix(a, f+"=")) {
				return true
			}
		}
	}
	return false
}

func hasFields(args []string) bool {
	if hasFlag(args, "-f", "-F", "--field", "--raw-field", "--input") {
		return true
	}
	for _, a := range args {
		if len(a) >= 3 && a[0] == '-' && (a[1] == 'f' || a[1] == 'F') && a[2] != '-' {
			return true
		}
	}
	return false
}

// apiMethod is the HTTP method of an `api` call; the last -X/--method wins.
func apiMethod(args []string) string {
	method := "GET"
	for i, a := range args {
		switch {
		case (a == "-X" || a == "--method") && i+1 < len(args):
			method = strings.ToUpper(args[i+1])
		case strings.HasPrefix(a, "--method="):
			method = strings.ToUpper(strings.SplitN(a, "=", 2)[1])
		case strings.HasPrefix(a, "-X") && len(a) > 2:
			method = strings.ToUpper(a[2:])
		}
	}
	return method
}

func takesStdin(args []string) bool {
	for _, a := range args {
		if a == "-" || strings.HasSuffix(a, "=-") || strings.HasSuffix(a, "@-") {
			return true
		}
	}
	return false
}

func anyMatch(args []string, re *regexp.Regexp) bool {
	for _, a := range args {
		if re.MatchString(a) {
			return true
		}
	}
	return false
}
