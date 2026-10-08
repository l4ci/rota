package tracker

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// GitHub is the gh adapter.
type GitHub struct {
	base
	// noStateReason is set once gh has rejected the stateReason field.
	noStateReason atomic.Bool
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
	return g.noStateReason.Load() || (g.cli.Cache != nil && g.cli.Cache.noStateReason.Load())
}

// withIssueFields runs call with the --json field list, once more without
// stateReason when gh does not know that field.
func (g *GitHub) withIssueFields(call func(fields string) error) error {
	err := call(g.issueFields())
	if err != nil && !g.lacksStateReason() && reNoStateReason.MatchString(err.Error()) {
		g.noStateReason.Store(true)
		if g.cli.Cache != nil {
			g.cli.Cache.noStateReason.Store(true)
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
	is := Issue{Number: d.Number, Title: d.Title, Body: d.Body, State: "open", ClosedAt: d.ClosedAt, URL: d.URL,
		Labels: []string{}, Assignees: []string{}}
	for _, l := range d.Labels {
		is.Labels = append(is.Labels, l.Name)
	}
	if d.Milestone != nil {
		is.Milestone = d.Milestone.Title
	}
	if strings.ToUpper(d.State) == "CLOSED" {
		is.State = "closed"
	}
	switch strings.ToUpper(d.StateReason) {
	case "COMPLETED":
		is.StateReason = "completed"
	case "NOT_PLANNED":
		is.StateReason = "not_planned"
	}
	for _, a := range d.Assignees {
		is.Assignees = append(is.Assignees, a.Login)
	}
	if d.Author != nil {
		is.Author = d.Author.Login
	}
	return is
}

func (g *GitHub) Create(ctx context.Context, title, body string, labels []string, milestone string) (int, error) {
	args := []string{"issue", "create", "--title", title, "--body-file", "-"}
	for _, l := range labels {
		args = append(args, "--label", l)
	}
	if milestone != "" {
		args = append(args, "--milestone", milestone)
	}
	out, err := g.run(ctx, args, body)
	if err != nil {
		return 0, err
	}
	return numberFromURL(out)
}

// EnsureLabels makes sure every label exists (gh refuses unknown ones),
// creating missing ones when autoCreate.
func (g *GitHub) EnsureLabels(ctx context.Context, names []string, autoCreate bool) error {
	names = uniq(names)
	if len(names) == 0 {
		return nil
	}
	var have []struct{ Name string }
	if err := g.json(ctx, []string{"label", "list", "--json", "name", "--limit", "1000"}, &have); err != nil {
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
		if _, err := g.run(ctx, []string{"label", "create", n, "--force"}, ""); err != nil {
			return err
		}
	}
	return nil
}

func (g *GitHub) AddLabels(ctx context.Context, number int, labels []string, autoCreate bool) error {
	return g.addLabels(ctx, g, number, labels, autoCreate)
}

func (g *GitHub) RemoveLabels(ctx context.Context, number int, labels []string) error {
	return g.removeLabels(ctx, g, number, labels)
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
		return 0, failed("cannot parse milestone number from: %q", clip(string(d.Number)))
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

func (g *GitHub) Get(ctx context.Context, number int, withComments bool) (Issue, error) {
	if !withComments && g.cli.Cache != nil {
		// A list this process already made carried the same fields.
		if is, ok := g.cli.Cache.issue(g.cli.cacheScope(), number); ok {
			return is, nil
		}
	}
	var d ghIssue
	err := g.withIssueFields(func(fields string) error {
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
	if wholeOpen && len(f.Labels) > 0 && g.cli.Cache != nil {
		if out, ok := g.cli.Cache.openMatching(g.cli.cacheScope(), f.Labels); ok {
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
	if g.cli.Cache != nil {
		g.cli.Cache.putIssues(g.cli.cacheScope(), out)
		if wholeOpen && len(f.Labels) == 0 {
			g.cli.Cache.putOpen(g.cli.cacheScope(), out)
		}
	}
	return firstN(out, f.Limit), nil
}

func (g *GitHub) Edit(ctx context.Context, number int, e IssueEdit) error {
	args := []string{"issue", "edit", strconv.Itoa(number)}
	if e.Title != nil {
		args = append(args, "--title", *e.Title)
	}
	body := ""
	if e.Body != nil {
		args = append(args, "--body-file", "-")
		body = *e.Body
	}
	for _, l := range e.AddLabels {
		args = append(args, "--add-label", l)
	}
	for _, l := range e.RemoveLabels {
		args = append(args, "--remove-label", l)
	}
	if e.Milestone != "" {
		args = append(args, "--milestone", e.Milestone)
	}
	if e.RemoveMilestone {
		args = append(args, "--remove-milestone")
	}
	_, err := g.run(ctx, args, body)
	return err
}

// Comments returns the comments oldest first.
func (g *GitHub) Comments(ctx context.Context, number int) ([]Comment, error) {
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

func (g *GitHub) AddComment(ctx context.Context, number int, body string) (string, error) {
	return g.createdID(ctx, []string{"api", "-X", "POST", fmt.Sprintf("repos/{owner}/{repo}/issues/%d/comments", number), "-f", "body=" + body})
}

// MRNotes: a PR's conversation comments are the issue comments of its number.
func (g *GitHub) MRNotes(ctx context.Context, number int) ([]Comment, error) {
	return g.Comments(ctx, number)
}

func (g *GitHub) AddMRNote(ctx context.Context, number int, body string) (string, error) {
	return g.AddComment(ctx, number, body)
}

func (g *GitHub) CommentURL(ctx context.Context, _ bool, _ int, commentID string) (string, error) {
	var d struct {
		HTMLURL string `json:"html_url"`
	}
	if err := g.json(ctx, []string{"api", "repos/{owner}/{repo}/issues/comments/" + commentID}, &d); err != nil {
		return "", err
	}
	return d.HTMLURL, nil
}

func (g *GitHub) EditComment(ctx context.Context, number int, commentID, body string) error {
	_, err := g.run(ctx, []string{"api", "-X", "PATCH", "repos/{owner}/{repo}/issues/comments/" + commentID, "-f", "body=" + body}, "")
	return err
}

func (g *GitHub) DeleteComment(ctx context.Context, number int, commentID string) error {
	_, err := g.run(ctx, []string{"api", "-X", "DELETE", "repos/{owner}/{repo}/issues/comments/" + commentID}, "")
	return err
}

func (g *GitHub) Close(ctx context.Context, number int, reason, comment string) error {
	r := "completed"
	if reason == "not_planned" {
		r = "not planned"
	}
	args := []string{"issue", "close", strconv.Itoa(number), "--reason", r}
	if comment != "" {
		args = append(args, "--comment", comment)
	}
	_, err := g.run(ctx, args, "")
	return err
}

func (g *GitHub) Reopen(ctx context.Context, number int) error {
	_, err := g.run(ctx, []string{"issue", "reopen", strconv.Itoa(number)}, "")
	return err
}

func (g *GitHub) AssignSelf(ctx context.Context, number int) error {
	_, err := g.run(ctx, []string{"issue", "edit", strconv.Itoa(number), "--add-assignee", "@me"}, "")
	return err
}

func (g *GitHub) OpenPRs(ctx context.Context) ([]PR, error) {
	var raw []struct {
		Number      int    `json:"number"`
		Title       string `json:"title"`
		Body        string `json:"body"`
		HeadRefName string `json:"headRefName"`
		URL         string `json:"url"`
	}
	if err := g.list(ctx, []string{"pr", "list", "--state", "open", "--json", "number,title,body,headRefName,url"}, ghLimit, ghPaging, &raw); err != nil {
		return nil, err
	}
	out := []PR{}
	for _, d := range raw {
		out = append(out, PR{Number: d.Number, Title: d.Title, Branch: d.HeadRefName, URL: d.URL, Body: d.Body})
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
		Body        string `json:"body"`
		MergeCommit *struct {
			Oid string `json:"oid"`
		} `json:"mergeCommit"`
	}
	if err := g.json(ctx, []string{"pr", "view", strconv.Itoa(pr), "--json", "headRefName,headRefOid,baseRefName,state,mergeCommit,body"}, &d); err != nil {
		return PRInfo{}, err
	}
	if d.HeadRefName == "" {
		return PRInfo{}, failed("cannot read PR %d", pr)
	}
	info := PRInfo{Head: d.HeadRefName, HeadSHA: d.HeadRefOid, Base: d.BaseRefName, State: d.State, Body: d.Body}
	if d.MergeCommit != nil {
		info.MergeSHA = d.MergeCommit.Oid
	}
	return info, nil
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
				return nil, failed("unparseable tracker output: %q", clip(out))
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
