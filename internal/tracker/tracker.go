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
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/strutil"
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

// Exit is the 5.0 exit code for the kind (docs/contributing/contract/cli-conventions.md):
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
	// CLIMissing marks a KindUnavailable failure whose cause is a forge CLI
	// that is not installed, as opposed to one that is unauthenticated or
	// timed out. Read it with IsCLIMissing.
	CLIMissing bool
}

func (e *Error) Error() string { return e.Message }

func unavailable(format string, a ...any) *Error {
	return &Error{Kind: KindUnavailable, Code: 3, Message: fmt.Sprintf(format, a...)}
}

func cliMissing(cli string) *Error {
	e := unavailable("%s is not installed", cli)
	e.CLIMissing = true
	return e
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

// IsCLIMissing reports whether err is a tracker failure because the forge CLI
// is not installed. It is KindUnavailable too; callers that skip a forge check
// on a missing CLI but fail closed on an auth failure ask this, not the text.
func IsCLIMissing(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.CLIMissing
}

// Issue is the normalized issue. Empty Milestone, StateReason and ClosedAt
// stand for Python's None. Comments is filled only by Get(n, true).
type Issue struct {
	Key         IssueKey
	Title       string
	Body        string
	Labels      []string
	Milestone   string
	State       string // open | closed
	StateReason string // CloseCompleted | CloseNotPlanned | ""
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
	// CreatedAt is when the forge recorded the comment; zero when the forge
	// did not say (the issue-view path of Get).
	CreatedAt time.Time
}

// Close reasons: the values of Issue.StateReason and the reason argument of
// Tracker.Close. Each adapter maps them onto its forge's own vocabulary.
const (
	CloseCompleted  = "completed"
	CloseNotPlanned = "not_planned"
)

// Review states, lowercased from the forge's word.
const (
	ReviewApproved         = "approved"
	ReviewChangesRequested = "changes_requested"
	ReviewCommented        = "commented"
)

// Review is one piece of review input on a PR: a submitted review, or an
// inline comment on the diff (State commented). Comment.ID is the forge's id
// within its own kind, so ids of a review and an inline comment may collide:
// order by CreatedAt, not by id.
type Review struct {
	Comment
	State string
	// Inline is true for an inline diff comment, false for a submitted review.
	Inline bool
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
	// HeadSHA is the PR's head commit; set by MergedPRs, which callers use to
	// tell a branch's own PR from an older one that reused the name.
	HeadSHA string
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
	Title    string
	Body     string
}

// Mergeability is the forge's own answer on whether a PR can be merged cleanly.
type Mergeability struct {
	State  string // MergeClean, MergeConflict or MergeUnknown
	Reason string // the forge's word for it (GitHub mergeStateStatus, GitLab detailed_merge_status)
}

const (
	MergeClean    = "clean"
	MergeConflict = "conflict" // the forge cannot create the merge commit
	MergeUnknown  = "unknown"  // still computing
)

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

// IssueTracker is the issue side of the seam: where issues, their comments,
// labels and milestones live (issues.source). GitHub, GitLab, Jira and
// Linear implement it. Issues are named by IssueKey; PR numbers stay ints.
type IssueTracker interface {
	Provider() string
	// CheckAuth fails with KindUnavailable unless the tracker's CLI is
	// installed and logged in.
	CheckAuth(ctx context.Context) error

	Create(ctx context.Context, title, body string, labels []string, milestone string) (IssueKey, error)
	Get(ctx context.Context, key IssueKey, withComments bool) (Issue, error)
	List(ctx context.Context, f ListFilter) ([]Issue, error)
	Edit(ctx context.Context, key IssueKey, e IssueEdit) error
	EnsureLabels(ctx context.Context, names []string, autoCreate bool) error
	AddLabels(ctx context.Context, key IssueKey, labels []string, autoCreate bool) error
	RemoveLabels(ctx context.Context, key IssueKey, labels []string) error
	// Close with reason CloseCompleted (or "") or CloseNotPlanned; comment is optional.
	Close(ctx context.Context, key IssueKey, reason, comment string) error
	Reopen(ctx context.Context, key IssueKey) error
	AssignSelf(ctx context.Context, key IssueKey) error

	Comments(ctx context.Context, key IssueKey) ([]Comment, error)
	AddComment(ctx context.Context, key IssueKey, body string) (string, error)
	EditComment(ctx context.Context, key IssueKey, commentID, body string) error
	DeleteComment(ctx context.Context, key IssueKey, commentID string) error
	// CommentURL is the web URL of comment id on the issue; it costs one call.
	CommentURL(ctx context.Context, key IssueKey, commentID string) (string, error)

	// FindMilestone returns the title of the milestone whose leading token is
	// rotaID, preferring open ones; ok is false when none matches.
	FindMilestone(ctx context.Context, rotaID string) (title string, ok bool, err error)
	// ClosesIssue reports whether a PR (its body, title and head branch in c)
	// closes issue key by this tracker's convention: GitHub and GitLab read a
	// closing keyword (`Closes #N`) in the body; a key-based tracker reads the
	// key in the branch or the PR title.
	ClosesIssue(key IssueKey, c ClosingSource) bool
	// Milestones lists by state "open", "closed" or "all" (or "").
	Milestones(ctx context.Context, state string) ([]Milestone, error)
	CreateMilestone(ctx context.Context, title, description string) (int, error)
	EditMilestone(ctx context.Context, number int, e MilestoneEdit) error
	IssuesInMilestone(ctx context.Context, title, state string) ([]Issue, error)
}

// ClaimStore is an issue tracker that keeps claims out of comment markers:
// one record per claimer, read back earliest first. A tracker that implements
// it (Jira and Linear, as plain `Claimed by` comments) takes claims this way
// instead of as claim and release marker comments.
type ClaimStore interface {
	// Claims lists the claim ids held on key, earliest first.
	Claims(ctx context.Context, key IssueKey) ([]string, error)
	// PutClaim records id's claim on key; a claim already held keeps its place.
	PutClaim(ctx context.Context, key IssueKey, id string) error
	// DropClaim removes id's claim; one not held is a no-op.
	DropClaim(ctx context.Context, key IssueKey, id string) error
}

// MergeCloser is an issue tracker a merged PR does not close by itself
// (Jira, Linear): whoever merges the PR closes the issue it names, as GitHub and
// GitLab do on a closing keyword.
type MergeCloser interface {
	// CloseAfterMerge closes key as completed, naming the merged PR; an issue
	// already closed is left alone.
	CloseAfterMerge(ctx context.Context, key IssueKey, pr string) error
}

// ClosingSource is the text of a PR that can name the issues it closes.
type ClosingSource struct {
	Body   string
	Title  string
	Branch string
}

// Forge is the code side of the seam: where code lands (forge.provider), with
// its pull requests, checks and releases. GitHub and GitLab implement it. The
// local forge has no implementation: NewForge answers ErrLocalForge, and the
// gate merges directly.
type Forge interface {
	Provider() string
	// CheckAuth fails with KindUnavailable unless the forge CLI is installed
	// and logged in.
	CheckAuth(ctx context.Context) error

	// MRNotes lists the comments of PR/MR number oldest first (GitLab: the
	// merge request notes, system notes excluded; GitHub: the issue comments
	// of the same number, which is where PR conversation comments live).
	MRNotes(ctx context.Context, number int) ([]Comment, error)
	// Reviews lists the review input of PR/MR number beside its conversation
	// comments: submitted reviews and inline diff comments, oldest first,
	// pending (unsubmitted) reviews excluded. GitLab keeps both in MRNotes, so
	// it returns none.
	Reviews(ctx context.Context, number int) ([]Review, error)
	// AddMRNote posts a comment on PR/MR number and returns its id.
	AddMRNote(ctx context.Context, number int, body string) (string, error)
	// PRCommentURL is the web URL of comment id on PR/MR pr; it costs one forge
	// call.
	PRCommentURL(ctx context.Context, pr int, commentID string) (string, error)

	// ClosedNumbers returns the issue numbers a PR body closes through a
	// closing keyword, in order of first appearance. It parses the forge's
	// keyword syntax; reading the issues it names is the issue source's job.
	ClosedNumbers(body string) []int
	OpenPRs(ctx context.Context) ([]PR, error)
	// MergedPRs lists the merged PRs headed by branch, for a branch whose PR
	// opened and merged between two looks at the open list.
	MergedPRs(ctx context.Context, branch string) ([]PR, error)
	PRsClosing(ctx context.Context, number int) ([]PR, error)
	PRCheckout(ctx context.Context, pr int) error
	// PRView reads PR pr: branches, head sha, state, merge sha and body.
	PRView(ctx context.Context, pr int) (PRInfo, error)
	// PRMergeable asks the forge whether PR pr merges cleanly right now. Only a
	// conflict is a refusal: branch protection and pending checks are not.
	PRMergeable(ctx context.Context, pr int) (Mergeability, error)
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

// Adapter is what the GitHub and GitLab implementations are: an issue tracker
// and a forge in one value. Code that needs one side takes IssueTracker or
// Forge; the concrete types satisfy both.
type Adapter interface {
	IssueTracker
	Forge
}

// Settings are the config values the tracker reads.
type Settings struct {
	// Provider is forge.provider: auto | github | gitlab | local.
	Provider string
	// Source is issues.source (file | github | gitlab | jira | linear), or
	// config.SourceIssues for a legacy config that has not named its tracker.
	Source          string
	RetryWait       time.Duration
	NotPlannedLabel string
	// Connection is issues.connection, the `rota connect` connection an HTTP
	// tracker (jira, linear) reaches its site through; JiraProject is
	// issues.jira.project, LinearTeam issues.linear.team.
	Connection      string
	JiraProject     string
	LinearTeam      string
	ItsaplanProject string
}

// SettingsFromConfig reads forge.provider, issues.source,
// issues.retryWaitSeconds, issues.labels.notPlanned, issues.connection,
// issues.jira.project, issues.linear.team and issues.itsaplan.project, with the schema
// defaults for absent or null keys. An invalid forge.provider or issues.source
// reads as its default here; ValidateConfig, ReadSettings (and the *FromConfig
// constructors) fail on it instead.
func SettingsFromConfig(cfg any) Settings {
	s := Settings{Provider: config.ForgeAuto, Source: config.SourceFile, RetryWait: retryWait(cfg), NotPlannedLabel: config.Label(cfg, "notPlanned")}
	if p, err := config.ForgeProvider(cfg); err == nil {
		s.Provider = p
	}
	if src, err := config.IssueSource(cfg); err == nil {
		s.Source = src
	}
	for key, dst := range map[string]*string{"issues.connection": &s.Connection, "issues.jira.project": &s.JiraProject, "issues.linear.team": &s.LinearTeam, "issues.itsaplan.project": &s.ItsaplanProject} {
		if v, ok := config.Lookup(cfg, key); ok && v != nil {
			*dst = strings.TrimSpace(fmt.Sprint(v))
		}
	}
	return s
}

// ReadSettings is SettingsFromConfig for a caller that acts on the result: it
// fails with ValidateConfig's error instead of defaulting an invalid
// forge.provider or issues.source.
func ReadSettings(cfg any) (Settings, error) {
	if err := ValidateConfig(cfg); err != nil {
		return Settings{}, err
	}
	return SettingsFromConfig(cfg), nil
}

// retryWait is issues.retryWaitSeconds, fractions allowed. A value that is not
// a number reads as the schema default.
func retryWait(cfg any) time.Duration {
	v, _ := config.Value(cfg, "issues.retryWaitSeconds")
	f, err := strconv.ParseFloat(fmt.Sprint(v), 64)
	if err != nil {
		k, _ := config.SchemaKey("issues.retryWaitSeconds")
		f, _ = strconv.ParseFloat(fmt.Sprint(config.Default(k)), 64)
	}
	return time.Duration(f * float64(time.Second))
}

// ValidateConfig is the corrupt-config check of the keys SettingsFromConfig
// reads: an invalid forge.provider or issues.source is a KindInternal error
// (exit 70), as an invalid issues.source is for every backlog verb.
func ValidateConfig(cfg any) error {
	if _, err := config.ForgeProvider(cfg); err != nil {
		return internal("%s", err.Error())
	}
	if _, err := config.IssueSource(cfg); err != nil {
		return internal("%s", err.Error())
	}
	return nil
}

// ErrLocalForge is what NewForge, New and NewCLI answer when the forge is
// local: forge.provider is local, or auto with no origin remote. A local forge
// has no pull requests, checks or releases; code lands by direct merge.
// Callers test it with errors.Is and take their no-PR path.
var ErrLocalForge error = &Error{Kind: KindUnavailable, Code: 3, Message: "the forge is local (forge.provider is local, or auto with no origin remote): there are no pull requests"}

// ErrNoIssueTracker is what NewIssueTracker answers for issues.source file:
// the issues live in BACKLOG.md, not on a tracker.
var ErrNoIssueTracker error = &Error{Kind: KindUnavailable, Code: 3, Message: "issues.source is file: the issues live in BACKLOG.md, not on a tracker"}

// New returns the adapter, issue tracker and forge in one, for provider ("" or
// "auto" falls back to s.Provider, then to origin-URL detection in dir). It is
// the forge resolution, so a local forge is ErrLocalForge. Every CLI call runs
// in dir ("" is the process cwd); in umbrella mode that is the sub-repo.
func New(ctx context.Context, s Settings, provider, dir string, opts ...Option) (Adapter, error) {
	c, err := NewCLI(ctx, s, provider, dir, opts...)
	if err != nil {
		return nil, err
	}
	return adapterFor(c, s), nil
}

func adapterFor(c *CLI, s Settings) Adapter {
	if c.Provider == "github" {
		return &GitHub{base: base{cli: c, closing: closingGH}}
	}
	return &GitLab{base: base{cli: c, closing: closingGL}, NotPlannedLabel: s.NotPlannedLabel}
}

// NewForge returns the forge for forgeProvider ("" or "auto" falls back to
// s.Provider, then to origin detection in dir; no origin remote is local).
// A local forge is ErrLocalForge.
func NewForge(ctx context.Context, s Settings, forgeProvider, dir string, opts ...Option) (Forge, error) {
	a, err := New(ctx, s, forgeProvider, dir, opts...)
	if err != nil {
		return nil, err
	}
	return a, nil
}

// NewIssueTracker returns the issue tracker for source ("" falls back to
// s.Source). file is ErrNoIssueTracker; jira and linear are the HTTP adapters
// of issues.connection; github and gitlab build their adapter whatever the
// origin says (rota doctor flags a mismatch); forge resolves the tracker from
// dir's own forge, per sub-repo. The legacy config.SourceIssues,
// an unmigrated "issues backend" with no named provider, resolves from the
// origin like the old issues.provider auto.
func NewIssueTracker(ctx context.Context, s Settings, source, dir string, opts ...Option) (IssueTracker, error) {
	if source == "" {
		source = s.Source
	}
	provider := source
	switch source {
	case config.SourceFile, "":
		return nil, ErrNoIssueTracker
	case config.SourceJira:
		j, err := newJiraTracker(ctx, newCLI(s, dir, opts...), s)
		if err != nil {
			return nil, err // never a nil *Jira inside a non-nil IssueTracker
		}
		return j, nil
	case config.SourceLinear:
		l, err := newLinearTracker(ctx, newCLI(s, dir, opts...), s)
		if err != nil {
			return nil, err // never a nil *Linear inside a non-nil IssueTracker
		}
		return l, nil
	case config.SourceItsaplan:
		t, err := newItsaplanTracker(ctx, newCLI(s, dir, opts...), s)
		if err != nil {
			return nil, err // never a nil *Itsaplan inside a non-nil IssueTracker
		}
		return t, nil
	case config.SourceIssues:
		provider = config.ForgeAuto
	case config.SourceForge:
		// The issues live on dir's own forge: the repo's forge resolution
		// (forge.provider, else its origin) names the tracker.
		// It resolves as a ForgeReader: an origin naming no forge reads as
		// github, and every write refuses as an actor would (assumedForge).
		c := newCLI(s, dir, opts...)
		r, err := c.resolveForge(ctx, ForgeReader, config.ForgeAuto, s.Provider)
		if err != nil {
			return nil, err
		}
		if r.Provider == config.ForgeLocal {
			return nil, unavailable("issues.source is forge, but the forge of %s is local (no origin remote): there is no tracker to read", dirLabel(dir))
		}
		c.Provider = r.Provider
		if r.How == HowAssumed {
			return assumedForge{IssueTracker: adapterFor(c, s), r: r}, nil
		}
		return adapterFor(c, s), nil
	case config.SourceGitHub, config.SourceGitLab:
	default:
		return nil, unavailable("unknown issues.source %q", source)
	}
	c := newCLI(s, dir, opts...)
	p, err := c.resolveIssues(ctx, provider, s.Provider)
	if err != nil {
		return nil, err
	}
	c.Provider = p
	return adapterFor(c, s), nil
}

func dirLabel(dir string) string {
	if dir == "" {
		return "the working directory"
	}
	return dir
}

// NewFromConfig is New with the settings read from cfg (SettingsFromConfig),
// the recipe every verb that holds a loaded config repeats.
func NewFromConfig(ctx context.Context, cfg any, provider, dir string, opts ...Option) (Adapter, error) {
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}
	return New(ctx, SettingsFromConfig(cfg), provider, dir, opts...)
}

// NewIssueTrackerFromConfig is NewIssueTracker with the settings read from cfg.
func NewIssueTrackerFromConfig(ctx context.Context, cfg any, dir string, opts ...Option) (IssueTracker, error) {
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}
	return NewIssueTracker(ctx, SettingsFromConfig(cfg), "", dir, opts...)
}

// NewForgeFromConfig is NewForge with the settings read from cfg.
func NewForgeFromConfig(ctx context.Context, cfg any, provider, dir string, opts ...Option) (Forge, error) {
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}
	return NewForge(ctx, SettingsFromConfig(cfg), provider, dir, opts...)
}

// NewCLI returns the forge CLI runner for provider, resolved as in New. A
// local forge is ErrLocalForge.
func NewCLI(ctx context.Context, s Settings, provider, dir string, opts ...Option) (*CLI, error) {
	c := newCLI(s, dir, opts...)
	p, err := c.resolve(ctx, provider, s.Provider)
	if err != nil {
		return nil, err
	}
	c.Provider = p
	return c, nil
}

func newCLI(s Settings, dir string, opts ...Option) *CLI {
	c := &CLI{Dir: dir, RetryWait: s.RetryWait}
	for _, o := range opts {
		o(c)
	}
	return c
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

// ClosesIssue: GitHub and GitLab close an issue through a closing keyword in
// the PR body, and name issues by number only.
func (b *base) ClosesIssue(key IssueKey, c ClosingSource) bool {
	n, ok := key.Number()
	return ok && slices.Contains(b.closing(c.Body), n)
}

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
		return failed("unparseable tracker output: %q", strutil.Clip(out, clipLen))
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
			return failed("unparseable tracker output: %q", strutil.Clip(out, clipLen))
		}
		all = append(all, page...)
	}
	joined, _ := json.Marshal(all)
	if err := json.Unmarshal(joined, v); err != nil {
		return failed("unparseable tracker output: %q", strutil.Clip(out, clipLen))
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
		return "", failed("cannot parse comment id from: %q", strutil.Clip(string(d.ID), clipLen))
	}
	return strconv.Itoa(n), nil
}

// clipLen caps the tracker output quoted in a parse error.
const clipLen = 200

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

func (b *base) addLabels(ctx context.Context, a Adapter, key IssueKey, labels []string, autoCreate bool) error {
	labels = uniq(labels)
	if len(labels) == 0 {
		return nil
	}
	if err := a.EnsureLabels(ctx, labels, autoCreate); err != nil {
		return err
	}
	return a.Edit(ctx, key, IssueEdit{AddLabels: labels})
}

func (b *base) removeLabels(ctx context.Context, a Adapter, key IssueKey, labels []string) error {
	labels = uniq(labels)
	if len(labels) == 0 {
		return nil
	}
	return a.Edit(ctx, key, IssueEdit{RemoveLabels: labels})
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
		return 0, failed("cannot parse issue number from: %q", strutil.Clip(strings.TrimSpace(out), clipLen))
	}
	n, err := strconv.Atoi(last[i:])
	if err != nil {
		return 0, failed("cannot parse issue number from: %q", strutil.Clip(strings.TrimSpace(out), clipLen))
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
