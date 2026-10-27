package tracker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/l4ci/rota/internal/strutil"
)

// GitHub is the gh adapter.
type GitHub struct {
	base
	// noStateReason is set once gh has rejected the stateReason field.
	noStateReason atomic.Bool
}

// ghCacheKey names the GitHub layer inside a shared ReadCache.
type ghCacheKey struct{}

// ghCache is what the GitHub adapter keeps beside the generic argv results:
// typed issue rows and whole open lists, plus the gh stateReason quirk.
type ghCache struct {
	mu   sync.Mutex
	rows map[string]Issue   // issue list rows, by provider+dir+number
	open map[string][]Issue // whole open lists (no filter), by provider+dir

	// noStateReason is set once gh rejected the stateReason field, so the
	// adapters sharing this cache do not each fail and retry the same list.
	// It describes gh, not repository data, so Reset keeps it.
	noStateReason atomic.Bool
}

// Reset drops the held rows and lists after a write.
func (gc *ghCache) Reset() {
	gc.mu.Lock()
	defer gc.mu.Unlock()
	clear(gc.rows)
	clear(gc.open)
}

// cache returns the GitHub layer of the shared read cache, nil without one.
func (g *GitHub) cache() *ghCache {
	if g.cli.Cache == nil {
		return nil
	}
	return cacheLayer(g.cli.Cache, ghCacheKey{}, func() *ghCache {
		return &ghCache{rows: map[string]Issue{}, open: map[string][]Issue{}}
	})
}

// putOpen holds the complete, unfiltered open list of a repository.
func (gc *ghCache) putOpen(scope string, list []Issue) {
	gc.mu.Lock()
	defer gc.mu.Unlock()
	gc.open[scope] = list
}

// openMatching answers a label-filtered open list from the held complete open
// list. Label names compare case-insensitively, as the forge does; several
// labels all have to be present.
func (gc *ghCache) openMatching(scope string, labels []string) ([]Issue, bool) {
	gc.mu.Lock()
	defer gc.mu.Unlock()
	all, ok := gc.open[scope]
	if !ok {
		return nil, false
	}
	out := []Issue{}
next:
	for _, is := range all {
		for _, want := range labels {
			if !slices.ContainsFunc(is.Labels, func(l string) bool { return strings.EqualFold(l, want) }) {
				continue next
			}
		}
		out = append(out, is)
	}
	return out, true
}

func (gc *ghCache) putIssues(scope string, list []Issue) {
	gc.mu.Lock()
	defer gc.mu.Unlock()
	for _, is := range list {
		gc.rows[scope+"#"+string(is.Key)] = is
	}
}

func (gc *ghCache) issue(scope string, k IssueKey) (Issue, bool) {
	gc.mu.Lock()
	defer gc.mu.Unlock()
	is, ok := gc.rows[scope+"#"+string(k)]
	return is, ok
}

// ghLimit is the first --limit a list asks gh for; a full result asks again
// with twice as many.
const ghLimit = 1000

func ghPaging(size, _ int) ([]string, bool) { return []string{"--limit", strconv.Itoa(size)}, true }

var reCommentURL = regexp.MustCompile(`#issuecomment-([0-9]+)$`)

const ghFields = "number,title,body,labels,milestone,state,stateReason,closedAt,url,assignees"

// reNoStateReason is gh older than 2.4x refusing the stateReason field.
var reNoStateReason = regexp.MustCompile(`Unknown JSON field: "stateReason"`)

// issueFields is ghFields, minus stateReason once gh has rejected it; the
// reason of a closed issue is then unknown.
func (g *GitHub) issueFields() string {
	if g.lacksStateReason() {
		return strings.Replace(ghFields, "stateReason,", "", 1)
	}
	return ghFields
}

// lacksStateReason is whether gh is known to reject the field: this adapter
// saw it, or another one sharing the read cache did (one gh per process).
func (g *GitHub) lacksStateReason() bool {
	return g.noStateReason.Load() || (g.cache() != nil && g.cache().noStateReason.Load())
}

// withIssueFields runs call with the --json field list, once more without
// stateReason when gh does not know that field.
func (g *GitHub) withIssueFields(call func(fields string) error) error {
	err := call(g.issueFields())
	if err != nil && !g.lacksStateReason() && reNoStateReason.MatchString(err.Error()) {
		g.noStateReason.Store(true)
		if gc := g.cache(); gc != nil {
			gc.noStateReason.Store(true)
		}
		return call(g.issueFields())
	}
	return err
}

type ghIssue struct {
	Number      int                      `json:"number"`
	Title       string                   `json:"title"`
	Body        string                   `json:"body"`
	Labels      []struct{ Name string }  `json:"labels"`
	Milestone   *struct{ Title string }  `json:"milestone"`
	State       string                   `json:"state"`
	StateReason string                   `json:"stateReason"`
	ClosedAt    string                   `json:"closedAt"`
	URL         string                   `json:"url"`
	Assignees   []struct{ Login string } `json:"assignees"`
	Author      *struct{ Login string }  `json:"author"`
	Comments    []struct {
		URL    string                  `json:"url"`
		Body   string                  `json:"body"`
		Author *struct{ Login string } `json:"author"`
	} `json:"comments"`
}

func (d ghIssue) norm() Issue {
	is := Issue{Key: NumKey(d.Number), Title: d.Title, Body: d.Body, State: "open", ClosedAt: d.ClosedAt, URL: d.URL,
		Labels: []string{}, Assignees: []string{}}
	for _, l := range d.Labels {
		is.Labels = append(is.Labels, l.Name)
	}
	if d.Milestone != nil {
		is.Milestone = d.Milestone.Title
	}
	// `gh issue view` also answers for a pull request number, with state MERGED.
	switch strings.ToUpper(d.State) {
	case "CLOSED":
		is.State = "closed"
	case "MERGED":
		is.State, is.StateReason = "closed", CloseCompleted
	}
	switch strings.ToUpper(d.StateReason) {
	case "COMPLETED":
		is.StateReason = CloseCompleted
	case "NOT_PLANNED":
		is.StateReason = CloseNotPlanned
	}
	for _, a := range d.Assignees {
		is.Assignees = append(is.Assignees, a.Login)
	}
	if d.Author != nil {
		is.Author = d.Author.Login
	}
	return is
}

func (g *GitHub) Create(ctx context.Context, title, body string, labels []string, milestone string) (IssueKey, error) {
	args := []string{"issue", "create", "--title", title, "--body-file", "-"}
	for _, l := range labels {
		args = append(args, "--label", l)
	}
	if milestone != "" {
		args = append(args, "--milestone", milestone)
	}
	out, err := g.run(ctx, args, body)
	if err != nil {
		return "", err
	}
	n, err := numberFromURL(out)
	return NumKey(n), err
}

// EnsureLabels makes sure every label exists (gh refuses unknown ones),
// creating missing ones when autoCreate.
func (g *GitHub) EnsureLabels(ctx context.Context, names []string, autoCreate bool) error {
	names = uniq(names)
	if len(names) == 0 {
		return nil
	}
	var have []struct{ Name string }
	if err := g.list(ctx, []string{"label", "list", "--json", "name"}, ghLimit, ghPaging, &have); err != nil {
		return err
	}
	set := map[string]bool{}
	for _, l := range have {
		set[l.Name] = true
	}
	for _, n := range names {
		if set[n] {
			continue
		}
		if !autoCreate {
			// A missing label is a missing object (exit 3); Code stays 1, as Python's.
			return &Error{Kind: KindNotFound, Code: 1, Message: fmt.Sprintf("label '%s' does not exist (issues.autoCreateLabel is off)", n)}
		}
		args := []string{"label", "create", n, "--force"}
		if lb, ok := labelMetadata(n, g.labelEntries); ok {
			args = append(args, "--description", lb.Description, "--color", labelColor(lb.Color))
		}
		if _, err := g.run(ctx, args, ""); err != nil {
			return err
		}
	}
	return nil
}

func (g *GitHub) AddLabels(ctx context.Context, key IssueKey, labels []string, autoCreate bool) error {
	return g.addLabels(ctx, g, key, labels, autoCreate)
}

func (g *GitHub) RemoveLabels(ctx context.Context, key IssueKey, labels []string) error {
	return g.removeLabels(ctx, g, key, labels)
}

func (g *GitHub) FindMilestone(ctx context.Context, rotaID string) (string, bool, error) {
	ms, err := g.milestones(ctx, "all")
	if err != nil {
		return "", false, err
	}
	t, ok := matchMilestone(ms, rotaID)
	return t, ok, nil
}

func (g *GitHub) Milestones(ctx context.Context, state string) ([]Milestone, error) {
	if state == "" {
		state = "all"
	}
	return g.milestones(ctx, state)
}

func (g *GitHub) milestones(ctx context.Context, state string) ([]Milestone, error) {
	var raw []rawMilestone
	if err := g.pages(ctx, fmt.Sprintf("repos/{owner}/{repo}/milestones?state=%s&per_page=100", state), &raw); err != nil {
		return nil, err
	}
	out := []Milestone{}
	for _, r := range raw {
		out = append(out, r.norm(r.Number))
	}
	return out, nil
}

func (g *GitHub) CreateMilestone(ctx context.Context, title, description string) (int, error) {
	var d struct{ Number json.RawMessage }
	if err := g.json(ctx, []string{"api", "-X", "POST", "repos/{owner}/{repo}/milestones",
		"-f", "title=" + title, "-f", "description=" + description}, &d); err != nil {
		return 0, err
	}
	n, ok := intOf(d.Number)
	if !ok {
		return 0, failed("cannot parse milestone number from: %q", strutil.Clip(string(d.Number), clipLen))
	}
	return n, nil
}

func (g *GitHub) EditMilestone(ctx context.Context, number int, e MilestoneEdit) error {
	args := []string{"api", "-X", "PATCH", fmt.Sprintf("repos/{owner}/{repo}/milestones/%d", number)}
	for _, kv := range []struct {
		k string
		v *string
	}{{"title", e.Title}, {"description", e.Description}, {"state", e.State}} {
		if kv.v != nil {
			args = append(args, "-f", kv.k+"="+*kv.v)
		}
	}
	_, err := g.run(ctx, args, "")
	return err
}

func (g *GitHub) IssuesInMilestone(ctx context.Context, title, state string) ([]Issue, error) {
	if state == "" {
		state = "all"
	}
	return g.List(ctx, ListFilter{State: state, Milestone: title})
}

func (g *GitHub) Get(ctx context.Context, key IssueKey, withComments bool) (Issue, error) {
	number, err := numOf(key)
	if err != nil {
		return Issue{}, err
	}
	if gc := g.cache(); !withComments && gc != nil {
		// A list this process already made carried the same fields.
		if is, ok := gc.issue(g.cli.cacheScope(), key); ok {
			return is, nil
		}
	}
	var d ghIssue
	err = g.withIssueFields(func(fields string) error {
		if withComments {
			fields += ",comments"
		}
		return g.json(ctx, []string{"issue", "view", strconv.Itoa(number), "--json", fields}, &d)
	})
	if err != nil {
		return Issue{}, err
	}
	is := d.norm()
	if withComments {
		is.Comments = []Comment{}
		for _, c := range d.Comments {
			// `issue view` gives GraphQL node ids; the REST id the comment
			// API takes is in the URL fragment.
			m := reCommentURL.FindStringSubmatch(c.URL)
			if m == nil {
				return Issue{}, failed("cannot read the id of a comment on #%d from its url %q", number, c.URL)
			}
			cm := Comment{ID: m[1], Body: c.Body}
			if c.Author != nil {
				cm.Author = c.Author.Login
			}
			is.Comments = append(is.Comments, cm)
		}
	}
	return is, nil
}

func (g *GitHub) List(ctx context.Context, f ListFilter) ([]Issue, error) {
	state := f.State
	if state == "" {
		state = "open"
	}
	// An open list narrowed by labels alone is a subset of the whole open
	// list when this invocation already read it. Closed, assignee and
	// milestone lists are not.
	wholeOpen := state == "open" && !f.Mine && f.Milestone == ""
	gc := g.cache()
	if wholeOpen && len(f.Labels) > 0 && gc != nil {
		if out, ok := gc.openMatching(g.cli.cacheScope(), f.Labels); ok {
			return firstN(out, f.Limit), nil
		}
	}
	var raw []ghIssue
	err := g.withIssueFields(func(fields string) error {
		args := []string{"issue", "list", "--state", state, "--json", fields + ",author"}
		if f.Mine {
			args = append(args, "--assignee", "@me")
		}
		for _, l := range f.Labels {
			args = append(args, "--label", l)
		}
		if f.Milestone != "" {
			args = append(args, "--milestone", f.Milestone)
		}
		return g.list(ctx, args, ghLimit, ghPaging, &raw)
	})
	if err != nil {
		return nil, err
	}
	out := []Issue{}
	for _, d := range raw {
		out = append(out, d.norm())
	}
	if gc != nil {
		gc.putIssues(g.cli.cacheScope(), out)
		if wholeOpen && len(f.Labels) == 0 {
			gc.putOpen(g.cli.cacheScope(), out)
		}
	}
	return firstN(out, f.Limit), nil
}

// Edit changes the issue over REST and reads it back. `gh issue edit` queries
// the retired Projects (classic) cards on older gh and can leave the issue
// unchanged without a usable exit code, so the PATCH goes through `gh api` and
// the read-back is what proves the change landed.
func (g *GitHub) Edit(ctx context.Context, key IssueKey, e IssueEdit) error {
	number, err := numOf(key)
	if err != nil {
		return err
	}
	patch := map[string]any{}
	if e.Title != nil {
		patch["title"] = *e.Title
	}
	if e.Body != nil {
		patch["body"] = *e.Body
	}
	if e.Milestone != "" {
		ms, err := g.milestones(ctx, "all")
		if err != nil {
			return err
		}
		n := 0
		for _, m := range ms {
			if m.Title == e.Milestone {
				n = m.Number
				break
			}
		}
		if n == 0 {
			return &Error{Kind: KindNotFound, Code: 1, Message: fmt.Sprintf("milestone '%s' not found", e.Milestone)}
		}
		patch["milestone"] = n
	} else if e.RemoveMilestone {
		patch["milestone"] = nil
	}
	path := fmt.Sprintf("repos/{owner}/{repo}/issues/%d", number)
	if len(patch) > 0 {
		buf, err := json.Marshal(patch)
		if err != nil {
			return err
		}
		if _, err := g.run(ctx, []string{"api", "-X", "PATCH", path, "--input", "-"}, string(buf)); err != nil {
			return err
		}
	}
	// Labels go through the incremental endpoints, never a PATCH of the whole
	// set: a concurrent label write between a read and that PATCH would be lost.
	if len(e.AddLabels) > 0 {
		buf, err := json.Marshal(map[string]any{"labels": e.AddLabels})
		if err != nil {
			return err
		}
		if _, err := g.run(ctx, []string{"api", "-X", "POST", path + "/labels", "--input", "-"}, string(buf)); err != nil {
			return err
		}
	}
	for _, l := range e.RemoveLabels {
		_, err := g.run(ctx, []string{"api", "-X", "DELETE", path + "/labels/" + url.PathEscape(l)}, "")
		var te *Error
		// A label the issue does not carry is already removed.
		if err != nil && !(errors.As(err, &te) && te.Kind == KindNotFound) {
			return err
		}
	}
	if len(patch) == 0 && len(e.AddLabels) == 0 && len(e.RemoveLabels) == 0 {
		return nil
	}
	got, err := g.issueREST(ctx, path)
	if err != nil {
		return err
	}
	return got.verify(number, e)
}

// restIssue is the part of a REST issue Edit reads back.
type restIssue struct {
	Title     string                  `json:"title"`
	Body      string                  `json:"body"`
	Labels    []struct{ Name string } `json:"labels"`
	Milestone *struct{ Title string } `json:"milestone"`
}

func (g *GitHub) issueREST(ctx context.Context, path string) (restIssue, error) {
	var d restIssue
	err := g.json(ctx, []string{"api", path}, &d)
	return d, err
}

func (r restIssue) labelNames() []string {
	out := []string{}
	for _, l := range r.Labels {
		out = append(out, l.Name)
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// verify errors when the issue read back lacks a requested change.
func (r restIssue) verify(number int, e IssueEdit) error {
	var miss []string
	if e.Title != nil && r.Title != *e.Title {
		miss = append(miss, "title")
	}
	if e.Body != nil && strings.ReplaceAll(r.Body, "\r\n", "\n") != strings.ReplaceAll(*e.Body, "\r\n", "\n") {
		miss = append(miss, "body")
	}
	// GitHub label names are case-insensitive.
	have := map[string]bool{}
	for _, l := range r.labelNames() {
		have[strings.ToLower(l)] = true
	}
	for _, l := range e.AddLabels {
		if !have[strings.ToLower(l)] {
			miss = append(miss, "label "+l)
		}
	}
	for _, l := range e.RemoveLabels {
		if have[strings.ToLower(l)] {
			miss = append(miss, "label "+l+" still set")
		}
	}
	if e.Milestone != "" && (r.Milestone == nil || r.Milestone.Title != e.Milestone) {
		miss = append(miss, "milestone")
	}
	if e.Milestone == "" && e.RemoveMilestone && r.Milestone != nil {
		miss = append(miss, "milestone still set")
	}
	if len(miss) > 0 {
		return failed("edit of #%d did not apply (read back without: %s)", number, strings.Join(miss, ", "))
	}
	return nil
}

// Comments returns the comments oldest first.
func (g *GitHub) Comments(ctx context.Context, key IssueKey) ([]Comment, error) {
	number, err := numOf(key)
	if err != nil {
		return nil, err
	}
	return g.comments(ctx, number)
}

func (g *GitHub) comments(ctx context.Context, number int) ([]Comment, error) {
	var raw []struct {
		ID        json.RawMessage         `json:"id"`
		Body      string                  `json:"body"`
		User      *struct{ Login string } `json:"user"`
		CreatedAt string                  `json:"created_at"`
	}
	if err := g.pages(ctx, fmt.Sprintf("repos/{owner}/{repo}/issues/%d/comments", number), &raw); err != nil {
		return nil, err
	}
	out := []Comment{}
	for _, c := range raw {
		cm := Comment{ID: idText(c.ID), Body: c.Body, CreatedAt: parseTime(c.CreatedAt)}
		if c.User != nil {
			cm.Author = c.User.Login
		}
		out = append(out, cm)
	}
	return out, nil
}

// Reviews returns the PR's submitted reviews and its inline diff comments,
// oldest first. A pending review has no submission time and is left out.
func (g *GitHub) Reviews(ctx context.Context, number int) ([]Review, error) {
	var reviews []struct {
		ID          json.RawMessage         `json:"id"`
		Body        string                  `json:"body"`
		State       string                  `json:"state"`
		User        *struct{ Login string } `json:"user"`
		SubmittedAt string                  `json:"submitted_at"`
	}
	if err := g.pages(ctx, fmt.Sprintf("repos/{owner}/{repo}/pulls/%d/reviews", number), &reviews); err != nil {
		return nil, err
	}
	var inline []struct {
		ID        json.RawMessage         `json:"id"`
		Body      string                  `json:"body"`
		User      *struct{ Login string } `json:"user"`
		CreatedAt string                  `json:"created_at"`
	}
	if err := g.pages(ctx, fmt.Sprintf("repos/{owner}/{repo}/pulls/%d/comments", number), &inline); err != nil {
		return nil, err
	}
	out := []Review{}
	for _, r := range reviews {
		at := parseTime(r.SubmittedAt)
		if at.IsZero() {
			continue
		}
		rv := Review{Comment: Comment{ID: idText(r.ID), Body: r.Body, CreatedAt: at}, State: reviewState(r.State)}
		if r.User != nil {
			rv.Author = r.User.Login
		}
		out = append(out, rv)
	}
	for _, c := range inline {
		rv := Review{Comment: Comment{ID: idText(c.ID), Body: c.Body, CreatedAt: parseTime(c.CreatedAt)}, State: ReviewCommented, Inline: true}
		if c.User != nil {
			rv.Author = c.User.Login
		}
		out = append(out, rv)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// reviewState maps GitHub's review state onto the Review* constants; a state
// it does not know (DISMISSED) reads as a comment.
func reviewState(s string) string {
	switch strings.ToLower(s) {
	case "approved":
		return ReviewApproved
	case "changes_requested":
		return ReviewChangesRequested
	}
	return ReviewCommented
}

// parseTime reads a forge timestamp; zero when it is empty or unreadable.
func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

func (g *GitHub) AddComment(ctx context.Context, key IssueKey, body string) (string, error) {
	number, err := numOf(key)
	if err != nil {
		return "", err
	}
	return g.addComment(ctx, number, body)
}

func (g *GitHub) addComment(ctx context.Context, number int, body string) (string, error) {
	return g.createdID(ctx, []string{"api", "-X", "POST", fmt.Sprintf("repos/{owner}/{repo}/issues/%d/comments", number), "-f", "body=" + body})
}

// MRNotes: a PR's conversation comments are the issue comments of its number.
func (g *GitHub) MRNotes(ctx context.Context, number int) ([]Comment, error) {
	return g.comments(ctx, number)
}

func (g *GitHub) AddMRNote(ctx context.Context, number int, body string) (string, error) {
	return g.addComment(ctx, number, body)
}

func (g *GitHub) CommentURL(ctx context.Context, _ IssueKey, commentID string) (string, error) {
	return g.commentURL(ctx, commentID)
}

func (g *GitHub) PRCommentURL(ctx context.Context, _ int, commentID string) (string, error) {
	return g.commentURL(ctx, commentID)
}

func (g *GitHub) commentURL(ctx context.Context, commentID string) (string, error) {
	var d struct {
		HTMLURL string `json:"html_url"`
	}
	if err := g.json(ctx, []string{"api", "repos/{owner}/{repo}/issues/comments/" + commentID}, &d); err != nil {
		return "", err
	}
	return d.HTMLURL, nil
}

func (g *GitHub) EditComment(ctx context.Context, key IssueKey, commentID, body string) error {
	if _, err := numOf(key); err != nil {
		return err
	}
	_, err := g.run(ctx, []string{"api", "-X", "PATCH", "repos/{owner}/{repo}/issues/comments/" + commentID, "-f", "body=" + body}, "")
	return err
}

func (g *GitHub) DeleteComment(ctx context.Context, key IssueKey, commentID string) error {
	if _, err := numOf(key); err != nil {
		return err
	}
	_, err := g.run(ctx, []string{"api", "-X", "DELETE", "repos/{owner}/{repo}/issues/comments/" + commentID}, "")
	return err
}

func (g *GitHub) Close(ctx context.Context, key IssueKey, reason, comment string) error {
	number, err := numOf(key)
	if err != nil {
		return err
	}
	r := CloseCompleted
	if reason == CloseNotPlanned {
		r = "not planned"
	}
	args := []string{"issue", "close", strconv.Itoa(number), "--reason", r}
	if comment != "" {
		args = append(args, "--comment", comment)
	}
	_, err = g.run(ctx, args, "")
	return err
}

func (g *GitHub) Reopen(ctx context.Context, key IssueKey) error {
	number, err := numOf(key)
	if err != nil {
		return err
	}
	_, err = g.run(ctx, []string{"issue", "reopen", strconv.Itoa(number)}, "")
	return err
}

func (g *GitHub) AssignSelf(ctx context.Context, key IssueKey) error {
	number, err := numOf(key)
	if err != nil {
		return err
	}
	_, err = g.run(ctx, []string{"issue", "edit", strconv.Itoa(number), "--add-assignee", "@me"}, "")
	return err
}

func (g *GitHub) OpenPRs(ctx context.Context) ([]PR, error) {
	var raw []struct {
		UpdatedAt         time.Time `json:"updatedAt"`
		Number            int       `json:"number"`
		Title             string    `json:"title"`
		Body              string    `json:"body"`
		HeadRefName       string    `json:"headRefName"`
		URL               string    `json:"url"`
		IsCrossRepository *bool     `json:"isCrossRepository"`
	}
	if err := g.list(ctx, []string{"pr", "list", "--state", "open", "--json", "number,title,body,headRefName,url,updatedAt,isCrossRepository"}, ghLimit, ghPaging, &raw); err != nil {
		return nil, err
	}
	out := []PR{}
	for _, d := range raw {
		out = append(out, PR{Number: d.Number, Title: d.Title, Branch: d.HeadRefName, URL: d.URL, UpdatedAt: d.UpdatedAt, Body: d.Body, SameRepository: d.IsCrossRepository != nil && !*d.IsCrossRepository})
	}
	return out, nil
}

func (g *GitHub) MergedPRs(ctx context.Context, branch string) ([]PR, error) {
	var raw []struct {
		Number      int    `json:"number"`
		Title       string `json:"title"`
		Body        string `json:"body"`
		HeadRefName string `json:"headRefName"`
		URL         string `json:"url"`
		HeadRefOid  string `json:"headRefOid"`
	}
	if err := g.list(ctx, []string{"pr", "list", "--state", "merged", "--head", branch, "--json", "number,title,body,headRefName,headRefOid,url"}, ghLimit, ghPaging, &raw); err != nil {
		return nil, err
	}
	out := []PR{}
	for _, d := range raw {
		out = append(out, PR{Number: d.Number, Title: d.Title, Branch: d.HeadRefName, URL: d.URL, Body: d.Body, HeadSHA: d.HeadRefOid})
	}
	return out, nil
}

func (g *GitHub) PRsClosing(ctx context.Context, number int) ([]PR, error) {
	return g.prsClosing(ctx, g, number)
}

func (g *GitHub) PRCheckout(ctx context.Context, pr int) error {
	_, err := g.run(ctx, []string{"pr", "checkout", strconv.Itoa(pr)}, "")
	return err
}

func (g *GitHub) PRView(ctx context.Context, pr int) (PRInfo, error) {
	var d struct {
		HeadRefName string `json:"headRefName"`
		HeadRefOid  string `json:"headRefOid"`
		BaseRefName string `json:"baseRefName"`
		State       string `json:"state"`
		Title       string `json:"title"`
		Body        string `json:"body"`
		MergeCommit *struct {
			Oid string `json:"oid"`
		} `json:"mergeCommit"`
	}
	if err := g.json(ctx, []string{"pr", "view", strconv.Itoa(pr), "--json", "headRefName,headRefOid,baseRefName,state,mergeCommit,body,title"}, &d); err != nil {
		return PRInfo{}, err
	}
	if d.HeadRefName == "" {
		return PRInfo{}, failed("cannot read PR %d", pr)
	}
	info := PRInfo{Head: d.HeadRefName, HeadSHA: d.HeadRefOid, Base: d.BaseRefName, State: d.State, Title: d.Title, Body: d.Body}
	if d.MergeCommit != nil {
		info.MergeSHA = d.MergeCommit.Oid
	}
	return info, nil
}

// ClosingRefs lists the issue numbers merging the PR closes, as the forge
// resolves them (closingIssuesReferences): a closing keyword anywhere in the
// body counts. `gh pr view --json` has no such field in older gh releases, so it
// goes through GraphQL.
func (g *GitHub) ClosingRefs(ctx context.Context, pr int) ([]int, bool, error) {
	const q = `query($owner:String!,$repo:String!,$n:Int!){repository(owner:$owner,name:$repo){pullRequest(number:$n){closingIssuesReferences(first:50){nodes{number}}}}}`
	var d struct {
		Data struct {
			Repository struct {
				PullRequest *struct {
					Closing struct {
						Nodes []struct {
							Number int `json:"number"`
						} `json:"nodes"`
					} `json:"closingIssuesReferences"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	args := []string{"api", "graphql", "-f", "query=" + q, "-F", "owner={owner}", "-F", "repo={repo}", "-F", "n=" + strconv.Itoa(pr)}
	if err := g.json(ctx, args, &d); err != nil {
		return nil, false, err
	}
	if d.Data.Repository.PullRequest == nil {
		return nil, false, failed("cannot read PR %d", pr)
	}
	var out []int
	for _, n := range d.Data.Repository.PullRequest.Closing.Nodes {
		out = append(out, n.Number)
	}
	return out, true, nil
}

// PRMergeable reads `mergeable` (MERGEABLE, CONFLICTING or UNKNOWN while GitHub
// computes it); mergeStateStatus names the reason.
func (g *GitHub) PRMergeable(ctx context.Context, pr int) (Mergeability, error) {
	var d struct {
		Mergeable        string `json:"mergeable"`
		MergeStateStatus string `json:"mergeStateStatus"`
	}
	if err := g.json(ctx, []string{"pr", "view", strconv.Itoa(pr), "--json", "mergeable,mergeStateStatus"}, &d); err != nil {
		return Mergeability{}, err
	}
	m := Mergeability{State: MergeUnknown, Reason: d.MergeStateStatus}
	switch d.Mergeable {
	case "MERGEABLE":
		m.State = MergeClean
	case "CONFLICTING":
		m.State = MergeConflict
	}
	return m, nil
}

func (g *GitHub) PRRequestMerge(ctx context.Context, pr int, o MergeOpts) error {
	args := []string{"pr", "merge", strconv.Itoa(pr), "--merge"}
	if o.DeleteBranch {
		args = append(args, "--delete-branch")
	}
	if o.HeadSHA != "" {
		args = append(args, "--match-head-commit", o.HeadSHA)
	}
	_, err := g.run(ctx, args, "")
	return err
}

func (g *GitHub) PRSetBase(ctx context.Context, pr int, base string) error {
	_, err := g.run(ctx, []string{"pr", "edit", strconv.Itoa(pr), "--base", base}, "")
	return err
}

func (g *GitHub) PRMerge(ctx context.Context, pr int, o MergeOpts) (string, error) {
	if err := g.PRRequestMerge(ctx, pr, o); err != nil {
		return "", err
	}
	var d struct {
		MergeCommit *struct{ Oid string } `json:"mergeCommit"`
	}
	if err := g.json(ctx, []string{"pr", "view", strconv.Itoa(pr), "--json", "mergeCommit"}, &d); err != nil {
		return "", err
	}
	if d.MergeCommit == nil || d.MergeCommit.Oid == "" {
		return "", failed("cannot read the merge commit of PR %d", pr)
	}
	return d.MergeCommit.Oid, nil
}

// PRFiles reads `gh pr diff --name-only`, which lists every file; the
// `files` field of `gh pr view` stops at 100.
func (g *GitHub) PRFiles(ctx context.Context, pr int) ([]string, error) {
	out, err := g.run(ctx, []string{"pr", "diff", strconv.Itoa(pr), "--name-only"}, "")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			files = append(files, l)
		}
	}
	return files, nil
}

func (g *GitHub) PRComment(ctx context.Context, pr int, body string) error {
	_, err := g.run(ctx, []string{"pr", "comment", strconv.Itoa(pr), "--body-file", "-"}, body)
	return err
}

func (g *GitHub) PRClose(ctx context.Context, pr int, comment string) error {
	_, err := g.run(ctx, []string{"pr", "close", strconv.Itoa(pr), "--comment", comment}, "")
	return err
}

func (g *GitHub) PRState(ctx context.Context, pr int) (string, error) {
	var d struct {
		State string `json:"state"`
	}
	if err := g.json(ctx, []string{"pr", "view", strconv.Itoa(pr), "--json", "state"}, &d); err != nil {
		return "", err
	}
	return strings.ToLower(d.State), nil
}

// CommitChecks joins the commit's check suites, check runs and combined
// status. A suite stays in progress until its last job finishes, so it covers
// the jobs (`needs:`) that have no check run yet; a suite with no runs is an
// app that never reported and is left out. The status covers CI that reports
// through the older API. All three answer the latest result already. The CLI
// paginates these calls, so a long answer is several JSON objects back to
// back; each is decoded in turn.
func (g *GitHub) CommitChecks(ctx context.Context, sha string) ([]CheckRun, error) {
	type page struct {
		CheckSuites []struct {
			App struct {
				Name string `json:"name"`
			} `json:"app"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			Runs       int    `json:"latest_check_runs_count"`
		} `json:"check_suites"`
		CheckRuns []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			HTMLURL    string `json:"html_url"`
		} `json:"check_runs"`
		Statuses []struct {
			Context   string `json:"context"`
			State     string `json:"state"`
			TargetURL string `json:"target_url"`
		} `json:"statuses"`
	}
	var all page
	for _, path := range []string{"check-suites?per_page=100", "check-runs?per_page=100", "status"} {
		out, err := g.run(ctx, []string{"api", "repos/{owner}/{repo}/commits/" + sha + "/" + path}, "")
		if err != nil {
			return nil, err
		}
		dec := json.NewDecoder(strings.NewReader(out))
		for dec.More() {
			var p page
			if err := dec.Decode(&p); err != nil {
				return nil, failed("unparseable tracker output: %q", strutil.Clip(out, clipLen))
			}
			all.CheckSuites = append(all.CheckSuites, p.CheckSuites...)
			all.CheckRuns = append(all.CheckRuns, p.CheckRuns...)
			all.Statuses = append(all.Statuses, p.Statuses...)
		}
	}
	checks := []CheckRun{}
	for _, su := range all.CheckSuites {
		if su.Runs > 0 {
			checks = append(checks, CheckRun{Name: "suite " + su.App.Name, State: ghCheckState(su.Status, su.Conclusion)})
		}
	}
	for _, r := range all.CheckRuns {
		checks = append(checks, CheckRun{Name: r.Name, State: ghCheckState(r.Status, r.Conclusion), URL: r.HTMLURL})
	}
	for _, s := range all.Statuses {
		state := CheckPending
		switch s.State {
		case "success":
			state = CheckSuccess
		case "failure", "error":
			state = CheckFailure
		}
		checks = append(checks, CheckRun{Name: s.Context, State: state, URL: s.TargetURL})
	}
	return checks, nil
}

// ghCheckState maps a check suite's or run's status and conclusion. Neutral
// and skipped finished without testing anything; any other conclusion but
// success is a failure.
func ghCheckState(status, conclusion string) string {
	if status != "completed" {
		return CheckPending
	}
	switch conclusion {
	case "success":
		return CheckSuccess
	case "neutral", "skipped":
		return CheckSkipped
	}
	return CheckFailure
}

// Authorship is who wrote an issue body and the body they wrote, read in one
// call so the trust check judges the text that is then used.
type Authorship struct {
	// Author and Editor are logins; "" for a deleted account, and for Editor an
	// issue never edited.
	Author, Editor string
	// Edited says the body was edited at all, so an edit by a deleted account
	// (no Editor) is not mistaken for none.
	Edited bool
	Body   string
}

// IssueAuthorship names the author and last editor of an issue's body and
// returns that body.
func (g *GitHub) IssueAuthorship(ctx context.Context, key IssueKey) (Authorship, error) {
	number, err := numOf(key)
	if err != nil {
		return Authorship{}, err
	}
	const q = `query($owner:String!,$repo:String!,$n:Int!){repository(owner:$owner,name:$repo){issue(number:$n){author{login} editor{login} lastEditedAt body}}}`
	var d struct {
		Data struct {
			Repository struct {
				Issue struct {
					Author       *struct{ Login string } `json:"author"`
					Editor       *struct{ Login string } `json:"editor"`
					LastEditedAt *string                 `json:"lastEditedAt"`
					Body         string                  `json:"body"`
				} `json:"issue"`
			} `json:"repository"`
		} `json:"data"`
	}
	args := []string{"api", "graphql", "-f", "query=" + q, "-F", "owner={owner}", "-F", "repo={repo}", "-F", "n=" + strconv.Itoa(number)}
	if err := g.json(ctx, args, &d); err != nil {
		return Authorship{}, err
	}
	i := d.Data.Repository.Issue
	a := Authorship{Edited: i.LastEditedAt != nil, Body: i.Body}
	if i.Author != nil {
		a.Author = i.Author.Login
	}
	if i.Editor != nil {
		a.Editor = i.Editor.Login
	}
	return a, nil
}

// reLogin is a GitHub login, optionally an app's "[bot]" account. Checked
// before a login becomes part of an API path.
var reLogin = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*(\[bot\])?$`)

// CanPush says whether login holds write access or more on the repository. A
// user who is not a collaborator answers read or none, so false.
func (g *GitHub) CanPush(ctx context.Context, login string) (bool, error) {
	if !reLogin.MatchString(login) {
		return false, failed("not a GitHub login: %q", login)
	}
	var d struct {
		Permission string `json:"permission"`
	}
	if err := g.json(ctx, []string{"api", "repos/{owner}/{repo}/collaborators/" + url.PathEscape(login) + "/permission"}, &d); err != nil {
		return false, err
	}
	switch d.Permission {
	case "admin", "maintain", "write":
		return true, nil
	}
	return false, nil
}
