package tracker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/proc"
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

// ResolveProvider picks the provider: want (a --provider value) unless it is
// "" or "auto", then configured (issues.provider) on the same terms, then
// origin-URL detection in dir. It is KindUnavailable when none resolves.
func ResolveProvider(ctx context.Context, want, configured, dir string, x Exec) (string, error) {
	c := &CLI{Dir: dir, Exec: x}
	return c.resolve(ctx, want, configured)
}

func (c *CLI) resolve(ctx context.Context, want, configured string) (string, error) {
	p := want
	if p == "" || p == "auto" {
		p = configured
	}
	if p != "github" && p != "gitlab" {
		p = c.detect(ctx)
	}
	if p != "github" && p != "gitlab" {
		return "", unavailable("cannot determine provider (set issues.provider)")
	}
	return p, nil
}

// reHost takes the hostname out of an SSH shorthand, SSH or HTTPS remote URL.
var reHost = regexp.MustCompile(`(?i)^(https?://|ssh://)?(git@)?([^:/\n]+)[:/].*`)

// detect classifies dir's origin URL as hv-issues-provider did: "github" or
// "gitlab" when the host contains that word, else "unknown".
func (c *CLI) detect(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()
	out, _, code, err := c.exec()(ctx, c.Dir, "git", []string{"remote", "get-url", "origin"}, nil)
	if err != nil || code != 0 {
		return "unknown"
	}
	return ProviderFromURL(strings.TrimRight(string(out), "\n"))
}

// CLIName is the forge CLI binary for provider: glab for "gitlab", gh otherwise.
func CLIName(provider string) string {
	if provider == "gitlab" {
		return "glab"
	}
	return "gh"
}

// ProviderFromURL is the hv-issues-provider classification of a remote URL.
func ProviderFromURL(url string) string {
	if url == "" {
		return "unknown"
	}
	host := url
	if m := reHost.FindStringSubmatch(url); m != nil {
		host = m[3]
	}
	host = strings.ToLower(host)
	switch {
	case strings.Contains(host, "github"):
		return "github"
	case strings.Contains(host, "gitlab"):
		return "gitlab"
	}
	return "unknown"
}

// Run makes one forge CLI call. A non-zero CLI exit is not an error: it comes
// back in Result.ExitCode with the CLI's output. Errors are KindUnavailable
// (CLI missing, unauthenticated, or an attempt past Timeout), KindRateLimited,
// and KindInternal (the process could not start, or ctx was cancelled).
// stdin is read once, and only when an argument takes it (`-`, or ending in
// `=-` or `@-`).
func (c *CLI) Run(ctx context.Context, args []string, stdin io.Reader) (Result, error) {
	cli := CLIName(c.Provider)
	if _, err := c.lookPath(cli); err != nil {
		return Result{}, unavailable("%s is not installed", cli)
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
			return Result{}, unavailable("%s is not installed", cli)
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
	if len(args) >= 1 && args[0] == "api" && !contains(args, "--paginate") {
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

func contains(args []string, s string) bool {
	for _, a := range args {
		if a == s {
			return true
		}
	}
	return false
}
