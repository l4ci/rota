// Package tracker is the gh/glab layer: one normalized issue and pull-request
// API over both forges, ported from bin/hvlib_tracker.py, and the single place
// the forge CLI runs (CLI.Run, ported from bin/hv-tracker-call), with list
// limits, pagination and bounded rate-limit handling.
//
// Adapters cache nothing, except the GitLab username that AssignSelf resolves
// once per adapter. List calls page until the forge returns a short page, so
// a result is never silently truncated. Every call takes a context; each CLI
// attempt also has its own timeout (CLI.Timeout).
package tracker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/config"
)

// Kind classifies a tracker failure for the CLI exit table.
type Kind int

const (
	// KindFailed: the forge CLI ran and failed, or its output did not parse.
	KindFailed Kind = iota
	// KindUnavailable: no provider resolves, the CLI is missing, not
	// authenticated, or timed out (old helper exit 3).
	KindUnavailable
	// KindRateLimited: the forge rate-limited the call (old exit 4).
	KindRateLimited
	// KindNotFound: the issue, PR, comment, milestone or label does not
	// exist (the CLI said so on a failed call).
	KindNotFound
	// KindInternal: the CLI could not be started for a reason that is not
	// the forge's (a bad working directory), or the caller cancelled.
	KindInternal
)

// Exit is the 5.0 exit code for the kind (docs/design/5.0-cli-conventions.md):
// not found is 3 (resolution), a missing or failing forge is 5, a rate limit
// is 6, an internal failure is 70.
func (k Kind) Exit() int {
	switch k {
	case KindNotFound:
		return 3
	case KindRateLimited:
		return 6
	case KindInternal:
		return 70
	}
	return 5
}

// Error is a tracker failure. Code is what the Python TrackerError carried:
// 3 unavailable, 4 rate-limited, the CLI's own exit code when it failed
// (also for KindNotFound), and 1 for output that did not parse. Use
// Kind.Exit, not Code, for a rota exit code.
type Error struct {
	Kind    Kind
	Code    int
	Message string
}

func (e *Error) Error() string { return e.Message }

func unavailable(format string, a ...any) *Error {
	return &Error{Kind: KindUnavailable, Code: 3, Message: fmt.Sprintf(format, a...)}
}

func rateLimited(format string, a ...any) *Error {
	return &Error{Kind: KindRateLimited, Code: 4, Message: fmt.Sprintf(format, a...)}
}

func failed(format string, a ...any) *Error {
	return &Error{Kind: KindFailed, Code: 1, Message: fmt.Sprintf(format, a...)}
}

func internal(format string, a ...any) *Error {
	return &Error{Kind: KindInternal, Code: 1, Message: fmt.Sprintf(format, a...)}
}

// reNotFound is how gh and glab say the object of a call does not exist:
// gh "Could not resolve to an Issue", "no pull requests found", "HTTP 404";
// glab "404 Not Found". reRepoMissing is the same wording about the
// repository or project itself, which is a setup problem, not a missing
// object (gh "Could not resolve to a Repository", glab "404 Project Not Found").
var (
	reNotFound    = regexp.MustCompile(`(?i)not found|could not resolve|404|no pull requests found`)
	reRepoMissing = regexp.MustCompile(`(?i)project not found|repository not found|could not resolve to a repository`)
)

// IsKind reports whether err is a tracker *Error of kind k.
func IsKind(err error, k Kind) bool {
	var e *Error
	return errors.As(err, &e) && e.Kind == k
}

// Issue is the normalized issue. Empty Milestone, StateReason and ClosedAt
// stand for Python's None. Comments is filled only by Get(n, true).
type Issue struct {
	Number      int
	Title       string
	Body        string
	Labels      []string
	Milestone   string
	State       string // open | closed
	StateReason string // completed | not_planned | ""
	ClosedAt    string
	URL         string
	Assignees   []string
	Author      string
	Comments    []Comment
}

// Comment is an issue comment (a GitLab note). ID is the forge's numeric REST
// id as text, the one EditComment and DeleteComment take, on every path.
type Comment struct {
	ID     string
	Body   string
	Author string
}

// Milestone is a native milestone. Number is what the forge's milestone API
// takes: the number on GitHub, the id on GitLab.
type Milestone struct {
	Number      int
	Title       string
	Description string
	State       string // open | closed
}

// PR is an open pull request (GitLab: merge request).
type PR struct {
	Number int
	Title  string
	Branch string
	URL    string
	Body   string
}

// PRInfo is one PR/MR as the merge gate reads it. State is OPEN, MERGED or
// CLOSED on both forges. MergeSHA is the merge commit, the squash commit on a
// GitLab squash merge, and "" when the forge has none (a fast-forward or
// rebase merge, or not merged yet).
type PRInfo struct {
	Head     string // source branch
	HeadSHA  string
	Base     string // target branch
	State    string
	MergeSHA string
	Body     string
}

// CheckRun is one CI check reported on a commit: a GitHub check run or
// commit status, or a GitLab pipeline.
type CheckRun struct {
	Name  string
	State string // CheckPending, CheckSuccess, CheckSkipped or CheckFailure
	URL   string
}

const (
	CheckPending = "pending"
	CheckSuccess = "success"
	CheckSkipped = "skipped" // finished without testing anything (skipped, neutral)
	CheckFailure = "failure"
)

// MergeOpts shapes a PR merge. HeadSHA pins the merge: the forge refuses it
// when the PR head is no longer that commit, so a push after the check cannot
// land unreviewed. "" merges whatever the head is.
type MergeOpts struct {
	HeadSHA      string
	DeleteBranch bool
}

// ListFilter selects issues. An empty State means "open". Mine keeps the
// issues assigned to the authenticated user; a Limit above zero keeps the
// first Limit of the result.
type ListFilter struct {
	State     string // open | closed | all
	Labels    []string
	Milestone string
	Mine      bool
	Limit     int
}

// IssueEdit is a partial issue update; nil and empty fields are left alone.
type IssueEdit struct {
	Title           *string
	Body            *string
	AddLabels       []string
	RemoveLabels    []string
	Milestone       string
	RemoveMilestone bool
}

// MilestoneEdit renames, re-describes or opens/closes a native milestone.
type MilestoneEdit struct {
	Title       *string
	Description *string
	State       *string // open | closed
}

// Adapter is the normalized API both forges implement.
type Adapter interface {
	Provider() string
	// CheckAuth fails with KindUnavailable unless the forge CLI is installed
	// and logged in.
	CheckAuth(ctx context.Context) error

	Create(ctx context.Context, title, body string, labels []string, milestone string) (int, error)
	Get(ctx context.Context, number int, withComments bool) (Issue, error)
	List(ctx context.Context, f ListFilter) ([]Issue, error)
	Edit(ctx context.Context, number int, e IssueEdit) error
	EnsureLabels(ctx context.Context, names []string, autoCreate bool) error
	AddLabels(ctx context.Context, number int, labels []string, autoCreate bool) error
	RemoveLabels(ctx context.Context, number int, labels []string) error
	// Close with reason "completed" (or "") or "not_planned"; comment is optional.
	Close(ctx context.Context, number int, reason, comment string) error
	Reopen(ctx context.Context, number int) error
	AssignSelf(ctx context.Context, number int) error

	Comments(ctx context.Context, number int) ([]Comment, error)
	AddComment(ctx context.Context, number int, body string) (string, error)
	EditComment(ctx context.Context, number int, commentID, body string) error
	DeleteComment(ctx context.Context, number int, commentID string) error
	// MRNotes lists the comments of PR/MR number oldest first (GitLab: the
	// merge request notes, system notes excluded; GitHub: the issue comments
	// of the same number, which is where PR conversation comments live).
	MRNotes(ctx context.Context, number int) ([]Comment, error)
	// AddMRNote posts a comment on PR/MR number and returns its id.
	AddMRNote(ctx context.Context, number int, body string) (string, error)
	// CommentURL is the web URL of comment id on the issue (pr false) or PR/MR
	// number; it costs one forge call.
	CommentURL(ctx context.Context, pr bool, number int, commentID string) (string, error)

	// FindMilestone returns the title of the milestone whose leading token is
	// rotaID, preferring open ones; ok is false when none matches.
	FindMilestone(ctx context.Context, rotaID string) (title string, ok bool, err error)
	// Milestones lists by state "open", "closed" or "all" (or "").
	Milestones(ctx context.Context, state string) ([]Milestone, error)
	CreateMilestone(ctx context.Context, title, description string) (int, error)
	EditMilestone(ctx context.Context, number int, e MilestoneEdit) error
	IssuesInMilestone(ctx context.Context, title, state string) ([]Issue, error)

	// ClosedNumbers returns the issue numbers a PR body closes through a
	// closing keyword, in order of first appearance.
	ClosedNumbers(body string) []int
	OpenPRs(ctx context.Context) ([]PR, error)
	PRsClosing(ctx context.Context, number int) ([]PR, error)
	PRCheckout(ctx context.Context, pr int) error
	// PRView reads PR pr: branches, head sha, state, merge sha and body.
	PRView(ctx context.Context, pr int) (PRInfo, error)
	// PRRequestMerge asks the forge to merge PR pr with a merge commit and
	// returns once it answers; it does not confirm the merge landed (the gate
	// does that itself). Auto-merge is off on GitLab, which would otherwise
	// schedule a merge, report success and merge nothing.
	PRRequestMerge(ctx context.Context, pr int, o MergeOpts) error
	// PRMerge is PRRequestMerge plus the confirmation: it returns the merge
	// commit sha, or fails when nothing landed.
	PRMerge(ctx context.Context, pr int, o MergeOpts) (string, error)
	// PRFiles lists the repo-relative paths a PR changes, for the
	// merge-approval gate (B1).
	PRFiles(ctx context.Context, pr int) ([]string, error)
	PRComment(ctx context.Context, pr int, body string) error
	// PRClose closes PR pr unmerged, leaving comment on it first.
	PRClose(ctx context.Context, pr int, comment string) error
	// CommitChecks lists the CI checks reported on sha, latest run per check.
	// None reported is an empty list, not an error.
	CommitChecks(ctx context.Context, sha string) ([]CheckRun, error)
	// PRState is "open", "merged" or "closed".
	PRState(ctx context.Context, pr int) (string, error)

	// PRNeedsBase reports whether PRCreate needs PRSpec.Base (GitLab does).
	PRNeedsBase() bool
	// PRCreate opens a PR/MR from s.Head and returns its URL.
	PRCreate(ctx context.Context, s PRSpec) (string, error)

	// ReleaseDrafts reports whether the forge has draft releases.
	ReleaseDrafts() bool
	// ReleaseView reads the release for tag; checked is false when the forge
	// is not asked (GitLab), and the tag then counts as unreleased.
	ReleaseView(ctx context.Context, tag string) (rel Release, checked bool, err error)
	// ReleaseCreate makes a release and returns its URL.
	ReleaseCreate(ctx context.Context, s ReleaseSpec) (string, error)
	// ReleaseEdit finishes an existing release (a draft the release workflow
	// made) and returns its URL.
	ReleaseEdit(ctx context.Context, s ReleaseSpec) (string, error)
}

// Settings are the issues.* config values the tracker reads.
type Settings struct {
	Provider        string // auto | github | gitlab
	RetryWait       time.Duration
	NotPlannedLabel string
}

// SettingsFromConfig reads issues.provider, issues.retryWaitSeconds and
// issues.labels.notPlanned, with the hvlib_config defaults for absent or
// null keys.
func SettingsFromConfig(cfg any) Settings {
	s := Settings{Provider: "auto", RetryWait: 60 * time.Second, NotPlannedLabel: "not-planned"}
	if v, ok := config.Lookup(cfg, "issues.provider"); ok && v != nil {
		s.Provider = fmt.Sprint(v)
	}
	if v, ok := config.Lookup(cfg, "issues.retryWaitSeconds"); ok && v != nil {
		if f, err := strconv.ParseFloat(fmt.Sprint(v), 64); err == nil {
			s.RetryWait = time.Duration(f * float64(time.Second))
		}
	}
	if v, ok := config.Lookup(cfg, "issues.labels.notPlanned"); ok && v != nil {
		s.NotPlannedLabel = fmt.Sprint(v)
	}
	return s
}

// New returns the adapter for provider ("" or "auto" falls back to
// s.Provider, then to origin-URL detection in dir). Every CLI call runs in
// dir ("" is the process cwd); in umbrella mode that is the sub-repo.
func New(ctx context.Context, s Settings, provider, dir string, opts ...Option) (Adapter, error) {
	c, err := NewCLI(ctx, s, provider, dir, opts...)
	if err != nil {
		return nil, err
	}
	if c.Provider == "github" {
		return &GitHub{base: base{cli: c, closing: closingGH}}, nil
	}
	return &GitLab{base: base{cli: c, closing: closingGL}, NotPlannedLabel: s.NotPlannedLabel}, nil
}

// NewFromConfig is New with the settings read from cfg (SettingsFromConfig),
// the recipe every verb that holds a loaded config repeats.
func NewFromConfig(ctx context.Context, cfg any, provider, dir string, opts ...Option) (Adapter, error) {
	return New(ctx, SettingsFromConfig(cfg), provider, dir, opts...)
}

// NewFromConfigOrGitHub is NewFromConfig that falls back to github when the
// provider cannot be resolved, e.g. an origin that is neither GitHub nor
// GitLab. A fresh clone with no recognizable remote still opens a PR that way.
func NewFromConfigOrGitHub(ctx context.Context, cfg any, provider, dir string, opts ...Option) (Adapter, error) {
	a, err := NewFromConfig(ctx, cfg, provider, dir, opts...)
	if err != nil {
		return NewFromConfig(ctx, cfg, "github", dir, opts...)
	}
	return a, nil
}

// NewCLI returns the forge CLI runner for provider, resolved as in New.
func NewCLI(ctx context.Context, s Settings, provider, dir string, opts ...Option) (*CLI, error) {
	c := &CLI{Dir: dir, RetryWait: s.RetryWait}
	for _, o := range opts {
		o(c)
	}
	p, err := c.resolve(ctx, provider, s.Provider)
	if err != nil {
		return nil, err
	}
	c.Provider = p
	return c, nil
}

// Option adjusts the CLI an adapter runs through (tests swap the executor).
type Option func(*CLI)

// WithExec routes every process the adapter starts through x, and resolves
// the CLI binary with lookPath.
func WithExec(x Exec, lookPath func(string) (string, error)) Option {
	return func(c *CLI) { c.Exec, c.LookPath = x, lookPath }
}

// WithTimeout sets the per-attempt timeout (CLI.Timeout).
func WithTimeout(d time.Duration) Option {
	return func(c *CLI) { c.Timeout = d }
}

// WithSleep replaces the rate-limit wait.
func WithSleep(sleep func(time.Duration)) Option {
	return func(c *CLI) { c.Sleep = sleep }
}

// base holds what both adapters share.
type base struct {
	cli     *CLI
	closing func(string) []int
}

func (b *base) Provider() string { return b.cli.Provider }

// CheckAuth runs `auth status`; any failure, a missing CLI included, is
// KindUnavailable.
func (b *base) CheckAuth(ctx context.Context) error { return b.cli.CheckAuth(ctx) }

func (b *base) ClosedNumbers(body string) []int { return b.closing(body) }

// run makes one call; stdin carries body only when an argument is exactly "-".
// A failed call is KindNotFound when the CLI says the object does not exist.
func (b *base) run(ctx context.Context, args []string, body string) (string, error) {
	var stdin io.Reader
	for _, a := range args {
		if a == "-" {
			stdin = strings.NewReader(body)
			break
		}
	}
	res, err := b.cli.Run(ctx, args, stdin)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", FailedCall(string(res.Stderr), res.ExitCode)
	}
	return string(res.Stdout), nil
}

// FailedCall is a forge call that exited non-zero, as a tracker failure:
// KindNotFound when the CLI says the object does not exist, else KindFailed.
func FailedCall(stderr string, code int) *Error {
	kind := KindFailed
	if reNotFound.MatchString(stderr) && !reRepoMissing.MatchString(stderr) {
		kind = KindNotFound
	}
	return &Error{Kind: kind, Code: code, Message: strings.TrimSpace(stderr)}
}

// list runs a list command with an explicit page size and keeps fetching
// while pages come back full, so a long list is never cut short. more turns
// the page size and the 1-based page number into the paging arguments; when
// it returns grow, the page size doubles and the whole list is re-fetched
// instead (gh pages internally up to --limit).
func (b *base) list(ctx context.Context, args []string, size int, more func(size, page int) (extra []string, grow bool), v any) error {
	var all []json.RawMessage
	for page := 1; ; page++ {
		extra, grow := more(size, page)
		var rows []json.RawMessage
		if err := b.json(ctx, append(append([]string(nil), args...), extra...), &rows); err != nil {
			return err
		}
		if grow {
			all = rows
			if len(rows) < size {
				break
			}
			size *= 2
			continue
		}
		all = append(all, rows...)
		if len(rows) < size {
			break
		}
	}
	joined, _ := json.Marshal(all)
	return json.Unmarshal(joined, v)
}

func (b *base) json(ctx context.Context, args []string, v any) error {
	out, err := b.run(ctx, args, "")
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(out), v); err != nil {
		return failed("unparseable tracker output: %q", clip(out))
	}
	return nil
}

// pages decodes an `api` list call whose paginated output concatenates one
// JSON array per page.
func (b *base) pages(ctx context.Context, path string, v any) error {
	out, err := b.run(ctx, []string{"api", path}, "")
	if err != nil {
		return err
	}
	var all []json.RawMessage
	dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(out)))
	for dec.More() {
		var page []json.RawMessage
		if err := dec.Decode(&page); err != nil {
			return failed("unparseable tracker output: %q", clip(out))
		}
		all = append(all, page...)
	}
	joined, _ := json.Marshal(all)
	if err := json.Unmarshal(joined, v); err != nil {
		return failed("unparseable tracker output: %q", clip(out))
	}
	return nil
}

func (b *base) createdID(ctx context.Context, args []string) (string, error) {
	var d struct{ ID json.RawMessage }
	if err := b.json(ctx, args, &d); err != nil {
		return "", err
	}
	n, ok := intOf(d.ID)
	if !ok {
		return "", failed("cannot parse comment id from: %q", clip(string(d.ID)))
	}
	return strconv.Itoa(n), nil
}

func clip(s string) string {
	if len(s) > 200 {
		return s[:200]
	}
	return s
}

// intOf reads a JSON number, or a string holding a decimal integer, as int
// (Python's int(v)).
func intOf(raw json.RawMessage) (int, bool) {
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		i, err := strconv.Atoi(string(n))
		return i, err == nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		i, err := strconv.Atoi(strings.TrimSpace(s))
		return i, err == nil
	}
	return 0, false
}

// idText renders a comment id as hvlib_tracker._int_or_raw would: an integer
// when it reads as one, else the raw string.
func idText(raw json.RawMessage) string {
	if n, ok := intOf(raw); ok {
		return strconv.Itoa(n)
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

// firstN is the first n of list; n at or below zero keeps all of it.
func firstN(list []Issue, n int) []Issue {
	if n > 0 && len(list) > n {
		return list[:n]
	}
	return list
}

// uniq drops empty and repeated names, keeping first-seen order.
func uniq(names []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range names {
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

func (b *base) prsClosing(ctx context.Context, a Adapter, number int) ([]PR, error) {
	prs, err := a.OpenPRs(ctx)
	if err != nil {
		return nil, err
	}
	var out []PR
	for _, p := range prs {
		for _, n := range a.ClosedNumbers(p.Body) {
			if n == number {
				out = append(out, p)
				break
			}
		}
	}
	return out, nil
}

func (b *base) addLabels(ctx context.Context, a Adapter, number int, labels []string, autoCreate bool) error {
	labels = uniq(labels)
	if len(labels) == 0 {
		return nil
	}
	if err := a.EnsureLabels(ctx, labels, autoCreate); err != nil {
		return err
	}
	return a.Edit(ctx, number, IssueEdit{AddLabels: labels})
}

func (b *base) removeLabels(ctx context.Context, a Adapter, number int, labels []string) error {
	labels = uniq(labels)
	if len(labels) == 0 {
		return nil
	}
	return a.Edit(ctx, number, IssueEdit{RemoveLabels: labels})
}

// numberFromURL reads the issue number off the last line of a create call.
func numberFromURL(out string) (int, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	last := strings.TrimRightFunc(lines[len(lines)-1], isPySpace)
	i := len(last)
	for i > 0 && last[i-1] >= '0' && last[i-1] <= '9' {
		i--
	}
	if i == len(last) || i == 0 || last[i-1] != '/' {
		return 0, failed("cannot parse issue number from: %q", clip(strings.TrimSpace(out)))
	}
	n, err := strconv.Atoi(last[i:])
	if err != nil {
		return 0, failed("cannot parse issue number from: %q", clip(strings.TrimSpace(out)))
	}
	return n, nil
}

// matchMilestone is the title of the milestone whose leading token is rotaID,
// preferring open ones.
func matchMilestone(items []Milestone, rotaID string) (string, bool) {
	var closed string
	found := false
	for _, m := range items {
		t := strings.TrimSpace(m.Title)
		if !strings.HasPrefix(t, rotaID) || startsWithWord(t[len(rotaID):]) {
			continue
		}
		if m.State != "closed" {
			return m.Title, true
		}
		if !found {
			closed, found = m.Title, true
		}
	}
	return closed, found
}

type rawMilestone struct {
	Number      int    `json:"number"`
	ID          int    `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	State       any    `json:"state"`
}

func (r rawMilestone) norm(number int) Milestone {
	m := Milestone{Number: number, Title: r.Title, Description: r.Description, State: "open"}
	if strings.ToLower(fmt.Sprint(r.State)) == "closed" {
		m.State = "closed"
	}
	return m
}
