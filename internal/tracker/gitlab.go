package tracker

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// GitLab is the glab adapter. glab has no close reason, so a not-planned
// close adds NotPlannedLabel and reads it back as the state reason.
type GitLab struct {
	base
	NotPlannedLabel string

	mu sync.Mutex
	me string
}

// glPerPage is the page size of glab list calls; full pages fetch the next.
const glPerPage = 100

func glPaging(size, page int) ([]string, bool) {
	args := []string{"--per-page", strconv.Itoa(size)}
	if page > 1 {
		args = append(args, "--page", strconv.Itoa(page))
	}
	return args, false
}

type glIssue struct {
	IID         int                         `json:"iid"`
	Title       string                      `json:"title"`
	Description string                      `json:"description"`
	Labels      []string                    `json:"labels"`
	Milestone   *struct{ Title string }     `json:"milestone"`
	State       string                      `json:"state"`
	ClosedAt    string                      `json:"closed_at"`
	WebURL      string                      `json:"web_url"`
	Assignees   []struct{ Username string } `json:"assignees"`
	Author      *struct{ Username string }  `json:"author"`
	Notes       []glNote                    `json:"notes"`
}

type glNote struct {
	ID     json.RawMessage            `json:"id"`
	Body   string                     `json:"body"`
	System bool                       `json:"system"`
	Author *struct{ Username string } `json:"author"`
}

func (n glNote) comment() Comment {
	c := Comment{ID: idText(n.ID), Body: n.Body}
	if n.Author != nil {
		c.Author = n.Author.Username
	}
	return c
}

func (g *GitLab) norm(d glIssue) Issue {
	is := Issue{Number: d.IID, Title: d.Title, Body: d.Description, State: "open", ClosedAt: d.ClosedAt, URL: d.WebURL,
		Labels: append([]string{}, d.Labels...), Assignees: []string{}}
	if d.Milestone != nil {
		is.Milestone = d.Milestone.Title
	}
	if d.State == "closed" {
		is.State, is.StateReason = "closed", "completed"
		for _, l := range d.Labels {
			if l == g.NotPlannedLabel {
				is.StateReason = "not_planned"
			}
		}
	}
	for _, a := range d.Assignees {
		is.Assignees = append(is.Assignees, a.Username)
	}
	if d.Author != nil {
		is.Author = d.Author.Username
	}
	return is
}

func (g *GitLab) Create(ctx context.Context, title, body string, labels []string, milestone string) (int, error) {
	args := []string{"issue", "create", "--title", title, "--description", body}
	if len(labels) > 0 {
		args = append(args, "--label", strings.Join(labels, ","))
	}
	if milestone != "" {
		args = append(args, "--milestone", milestone)
	}
	out, err := g.run(ctx, append(args, "-y"), "")
	if err != nil {
		return 0, err
	}
	return numberFromURL(out)
}

// EnsureLabels is a no-op: GitLab creates labels on first use.
func (g *GitLab) EnsureLabels(ctx context.Context, names []string, autoCreate bool) error { return nil }

func (g *GitLab) AddLabels(ctx context.Context, number int, labels []string, autoCreate bool) error {
	return g.addLabels(ctx, g, number, labels, autoCreate)
}

func (g *GitLab) RemoveLabels(ctx context.Context, number int, labels []string) error {
	return g.removeLabels(ctx, g, number, labels)
}

func (g *GitLab) FindMilestone(ctx context.Context, rotaID string) (string, bool, error) {
	ms, err := g.milestones(ctx, "projects/:id/milestones?per_page=100")
	if err != nil {
		return "", false, err
	}
	t, ok := matchMilestone(ms, rotaID)
	return t, ok, nil
}

// Milestones numbers each milestone by its API id (what PUT
// .../milestones/<id> takes).
func (g *GitLab) Milestones(ctx context.Context, state string) ([]Milestone, error) {
	want := map[string]string{"open": "&state=active", "closed": "&state=closed"}[state]
	return g.milestones(ctx, "projects/:id/milestones?per_page=100"+want)
}

func (g *GitLab) milestones(ctx context.Context, path string) ([]Milestone, error) {
	var raw []rawMilestone
	if err := g.pages(ctx, path, &raw); err != nil {
		return nil, err
	}
	out := []Milestone{}
	for _, r := range raw {
		out = append(out, r.norm(r.ID))
	}
	return out, nil
}

func (g *GitLab) CreateMilestone(ctx context.Context, title, description string) (int, error) {
	var d struct{ ID json.RawMessage }
	if err := g.json(ctx, []string{"api", "-X", "POST", "projects/:id/milestones",
		"-f", "title=" + title, "-f", "description=" + description}, &d); err != nil {
		return 0, err
	}
	n, ok := intOf(d.ID)
	if !ok {
		return 0, failed("cannot parse milestone id from: %q", clip(string(d.ID)))
	}
	return n, nil
}

func (g *GitLab) EditMilestone(ctx context.Context, number int, e MilestoneEdit) error {
	args := []string{"api", "-X", "PUT", fmt.Sprintf("projects/:id/milestones/%d", number)}
	if e.Title != nil {
		args = append(args, "-f", "title="+*e.Title)
	}
	if e.Description != nil {
		args = append(args, "-f", "description="+*e.Description)
	}
	if e.State != nil {
		ev := "activate"
		if *e.State == "closed" {
			ev = "close"
		}
		args = append(args, "-f", "state_event="+ev)
	}
	_, err := g.run(ctx, args, "")
	return err
}

func (g *GitLab) IssuesInMilestone(ctx context.Context, title, state string) ([]Issue, error) {
	if state == "" {
		state = "all"
	}
	return g.List(ctx, ListFilter{State: state, Milestone: title})
}

func (g *GitLab) Get(ctx context.Context, number int, withComments bool) (Issue, error) {
	args := []string{"issue", "view", strconv.Itoa(number), "--output", "json"}
	if withComments {
		args = append(args, "--comments")
	}
	var d glIssue
	if err := g.json(ctx, args, &d); err != nil {
		return Issue{}, err
	}
	is := g.norm(d)
	if withComments {
		is.Comments = []Comment{}
		for _, n := range d.Notes {
			if !n.System { // as in Comments
				is.Comments = append(is.Comments, n.comment())
			}
		}
	}
	return is, nil
}

func (g *GitLab) List(ctx context.Context, f ListFilter) ([]Issue, error) {
	args := []string{"issue", "list", "--output", "json"}
	switch f.State {
	case "all":
		args = append(args, "--all")
	case "closed":
		args = append(args, "--closed")
	}
	for _, l := range f.Labels {
		args = append(args, "--label", l)
	}
	if f.Milestone != "" {
		args = append(args, "--milestone", f.Milestone)
	}
	if f.Mine {
		// glab takes usernames for --assignee, not @me (see AssignSelf).
		me, err := g.username(ctx)
		if err != nil {
			return nil, err
		}
		args = append(args, "--assignee", me)
	}
	var raw []glIssue
	if err := g.list(ctx, args, glPerPage, glPaging, &raw); err != nil {
		return nil, err
	}
	out := []Issue{}
	for _, d := range raw {
		out = append(out, g.norm(d))
	}
	return firstN(out, f.Limit), nil
}

func (g *GitLab) Edit(ctx context.Context, number int, e IssueEdit) error {
	args := []string{"issue", "update", strconv.Itoa(number)}
	if e.Title != nil {
		args = append(args, "--title", *e.Title)
	}
	if e.Body != nil {
		args = append(args, "--description", *e.Body)
	}
	if len(e.AddLabels) > 0 {
		args = append(args, "--label", strings.Join(e.AddLabels, ","))
	}
	if len(e.RemoveLabels) > 0 {
		args = append(args, "--unlabel", strings.Join(e.RemoveLabels, ","))
	}
	if e.Milestone != "" {
		args = append(args, "--milestone", e.Milestone)
	} else if e.RemoveMilestone {
		// glab has no remove flag; an empty title clears the milestone.
		args = append(args, "--milestone", "")
	}
	_, err := g.run(ctx, args, "")
	return err
}

// Comments returns the comments oldest first, system notes excluded.
func (g *GitLab) Comments(ctx context.Context, number int) ([]Comment, error) {
	var raw []glNote
	if err := g.pages(ctx, fmt.Sprintf("projects/:id/issues/%d/notes?sort=asc&order_by=created_at", number), &raw); err != nil {
		return nil, err
	}
	out := []Comment{}
	for _, n := range raw {
		if !n.System {
			out = append(out, n.comment())
		}
	}
	return out, nil
}

func (g *GitLab) AddComment(ctx context.Context, number int, body string) (string, error) {
	return g.createdID(ctx, []string{"api", "-X", "POST", fmt.Sprintf("projects/:id/issues/%d/notes", number), "-f", "body=" + body})
}

// MRNotes returns the merge request's notes oldest first, system notes excluded.
func (g *GitLab) MRNotes(ctx context.Context, number int) ([]Comment, error) {
	var raw []glNote
	if err := g.pages(ctx, fmt.Sprintf("projects/:id/merge_requests/%d/notes?sort=asc&order_by=created_at", number), &raw); err != nil {
		return nil, err
	}
	out := []Comment{}
	for _, n := range raw {
		if !n.System {
			out = append(out, n.comment())
		}
	}
	return out, nil
}

func (g *GitLab) AddMRNote(ctx context.Context, number int, body string) (string, error) {
	return g.createdID(ctx, []string{"api", "-X", "POST", fmt.Sprintf("projects/:id/merge_requests/%d/notes", number), "-f", "body=" + body})
}

// CommentURL: a note has no URL of its own in the API, so it is the thread's
// web_url plus the #note_<id> anchor GitLab renders.
func (g *GitLab) CommentURL(ctx context.Context, pr bool, number int, commentID string) (string, error) {
	kind := "issue"
	if pr {
		kind = "mr"
	}
	var d struct {
		WebURL string `json:"web_url"`
	}
	if err := g.json(ctx, []string{kind, "view", strconv.Itoa(number), "--output", "json"}, &d); err != nil {
		return "", err
	}
	if d.WebURL == "" {
		return "", nil
	}
	return d.WebURL + "#note_" + commentID, nil
}

func (g *GitLab) EditComment(ctx context.Context, number int, commentID, body string) error {
	_, err := g.run(ctx, []string{"api", "-X", "PUT", fmt.Sprintf("projects/:id/issues/%d/notes/%s", number, commentID), "-f", "body=" + body}, "")
	return err
}

func (g *GitLab) DeleteComment(ctx context.Context, number int, commentID string) error {
	_, err := g.run(ctx, []string{"api", "-X", "DELETE", fmt.Sprintf("projects/:id/issues/%d/notes/%s", number, commentID)}, "")
	return err
}

func (g *GitLab) Close(ctx context.Context, number int, reason, comment string) error {
	n := strconv.Itoa(number)
	if comment != "" {
		if _, err := g.run(ctx, []string{"issue", "note", n, "-m", comment}, ""); err != nil {
			return err
		}
	}
	if reason == "not_planned" {
		if err := g.AddLabels(ctx, number, []string{g.NotPlannedLabel}, true); err != nil {
			return err
		}
	}
	_, err := g.run(ctx, []string{"issue", "close", n}, "")
	return err
}

func (g *GitLab) Reopen(ctx context.Context, number int) error {
	if _, err := g.run(ctx, []string{"issue", "reopen", strconv.Itoa(number)}, ""); err != nil {
		return err
	}
	return g.RemoveLabels(ctx, number, []string{g.NotPlannedLabel})
}

// AssignSelf adds the authenticated user without replacing other assignees.
// glab documents --assignee as usernames (no @me), so the username is
// resolved once per adapter.
func (g *GitLab) AssignSelf(ctx context.Context, number int) error {
	me, err := g.username(ctx)
	if err != nil {
		return err
	}
	_, err = g.run(ctx, []string{"issue", "update", strconv.Itoa(number), "--assignee", "+" + me}, "")
	return err
}

// username resolves the authenticated user once; a failure is retried on the
// next call.
func (g *GitLab) username(ctx context.Context) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.me == "" {
		var u struct {
			Username string `json:"username"`
		}
		if err := g.json(ctx, []string{"api", "user"}, &u); err != nil {
			return "", err
		}
		if u.Username == "" {
			return "", failed("cannot resolve the authenticated GitLab username")
		}
		g.me = u.Username
	}
	return g.me, nil
}

func (g *GitLab) OpenPRs(ctx context.Context) ([]PR, error) {
	var raw []struct {
		IID          int    `json:"iid"`
		Title        string `json:"title"`
		Description  string `json:"description"`
		SourceBranch string `json:"source_branch"`
		WebURL       string `json:"web_url"`
	}
	if err := g.list(ctx, []string{"mr", "list", "--output", "json"}, glPerPage, glPaging, &raw); err != nil {
		return nil, err
	}
	out := []PR{}
	for _, d := range raw {
		out = append(out, PR{Number: d.IID, Title: d.Title, Branch: d.SourceBranch, URL: d.WebURL, Body: d.Description})
	}
	return out, nil
}

func (g *GitLab) PRsClosing(ctx context.Context, number int) ([]PR, error) {
	return g.prsClosing(ctx, g, number)
}

func (g *GitLab) PRCheckout(ctx context.Context, pr int) error {
	_, err := g.run(ctx, []string{"mr", "checkout", strconv.Itoa(pr)}, "")
	return err
}

func (g *GitLab) PRView(ctx context.Context, pr int) (PRInfo, error) {
	var d struct {
		Source      string `json:"source_branch"`
		SHA         string `json:"sha"`
		Target      string `json:"target_branch"`
		State       string `json:"state"`
		Description string `json:"description"`
		Merge       string `json:"merge_commit_sha"`
		Squash      string `json:"squash_commit_sha"`
	}
	if err := g.json(ctx, []string{"api", fmt.Sprintf("projects/:id/merge_requests/%d", pr)}, &d); err != nil {
		return PRInfo{}, err
	}
	if d.Source == "" && d.SHA == "" {
		return PRInfo{}, failed("cannot read MR %d", pr)
	}
	info := PRInfo{Head: d.Source, HeadSHA: d.SHA, Base: d.Target, State: "CLOSED", Body: d.Description, MergeSHA: d.Merge}
	switch d.State {
	case "opened":
		info.State = "OPEN"
	case "merged":
		info.State = "MERGED"
	}
	if info.MergeSHA == "" {
		info.MergeSHA = d.Squash
	}
	return info, nil
}

// PRRequestMerge runs `glab mr merge` with auto-merge off.
func (g *GitLab) PRRequestMerge(ctx context.Context, pr int, o MergeOpts) error {
	args := []string{"mr", "merge", strconv.Itoa(pr), "--yes"}
	if o.DeleteBranch {
		args = append(args, "--remove-source-branch")
	}
	args = append(args, "--auto-merge=false")
	if o.HeadSHA != "" {
		args = append(args, "--sha", o.HeadSHA)
	}
	_, err := g.run(ctx, args, "")
	return err
}

// PRMerge merges with auto-merge off (glab otherwise schedules a merge while a
// pipeline runs, reports success and merges nothing). A squash merge returns
// the squash commit. A fast-forward or rebase merge has no merge commit: the
// MR must then be merged and its head an ancestor of origin/<target>, and the
// head sha is returned.
func (g *GitLab) PRMerge(ctx context.Context, pr int, o MergeOpts) (string, error) {
	n := strconv.Itoa(pr)
	if err := g.PRRequestMerge(ctx, pr, o); err != nil {
		return "", err
	}
	var d struct {
		State          string `json:"state"`
		SHA            string `json:"sha"`
		TargetBranch   string `json:"target_branch"`
		MergeCommitSHA string `json:"merge_commit_sha"`
		SquashSHA      string `json:"squash_commit_sha"`
	}
	if err := g.json(ctx, []string{"mr", "view", n, "--output", "json"}, &d); err != nil {
		return "", err
	}
	switch {
	case d.MergeCommitSHA != "":
		return d.MergeCommitSHA, nil
	case d.SquashSHA != "":
		return d.SquashSHA, nil
	case d.State != "merged":
		return "", failed("MR %d is %s after the merge call (a scheduled auto-merge?); nothing was merged", pr, d.State)
	case d.SHA == "" || d.TargetBranch == "":
		return "", failed("cannot read the merge commit of MR %d", pr)
	}
	return d.SHA, g.onBase(ctx, pr, d.SHA, d.TargetBranch)
}

// onBase checks a fast-forwarded MR's head is on origin/<target>.
func (g *GitLab) onBase(ctx context.Context, pr int, sha, target string) error {
	x := func(args ...string) ([]byte, []byte, int, error) {
		actx, cancel := context.WithTimeout(ctx, g.cli.timeout())
		defer cancel()
		return g.cli.exec()(actx, g.cli.Dir, "git", args, nil)
	}
	if _, errb, code, err := x("fetch", "-q", "origin"); err != nil || code != 0 {
		return failed("MR %d merged without a merge commit, and git fetch origin failed: %s", pr, strings.TrimSpace(string(errb)))
	}
	ref := "origin/" + target
	_, errb, code, err := x("merge-base", "--is-ancestor", sha, ref)
	switch {
	case err == nil && code == 0:
		return nil
	case err == nil && code == 1:
		return failed("MR %d is merged, but its head %s is not on %s", pr, sha, ref)
	}
	return failed("MR %d merged without a merge commit, and git merge-base --is-ancestor %s %s failed: %s", pr, sha, ref, strings.TrimSpace(string(errb)))
}

// PRFiles lets the CLI paginate the MR diffs API. A renamed file counts
// under both its paths; each path is returned once, in first-seen order.
func (g *GitLab) PRFiles(ctx context.Context, pr int) ([]string, error) {
	var d []struct {
		Old string `json:"old_path"`
		New string `json:"new_path"`
	}
	path := fmt.Sprintf("projects/:id/merge_requests/%d/diffs?per_page=100", pr)
	if err := g.pages(ctx, path, &d); err != nil {
		return nil, err
	}
	var files []string
	seen := make(map[string]bool)
	for _, f := range d {
		for _, path := range []string{f.New, f.Old} {
			if path != "" && !seen[path] {
				seen[path] = true
				files = append(files, path)
			}
		}
	}
	return files, nil
}

func (g *GitLab) PRComment(ctx context.Context, pr int, body string) error {
	_, err := g.run(ctx, []string{"mr", "note", strconv.Itoa(pr), "--message", body}, "")
	return err
}

func (g *GitLab) PRState(ctx context.Context, pr int) (string, error) {
	var d struct {
		State string `json:"state"`
	}
	if err := g.json(ctx, []string{"mr", "view", strconv.Itoa(pr), "--output", "json"}, &d); err != nil {
		return "", err
	}
	st := strings.ToLower(d.State)
	if st == "opened" {
		st = "open"
	}
	return st, nil
}

// CommitChecks reports the newest pipeline per ref on sha, ordered by ref,
// each followed by its jobs under their job names (latest attempt only), so
// test.ciChecks can name a job. A failed job allowed to fail and a manual job
// that never ran count as skipped: neither tested anything that gates.
func (g *GitLab) CommitChecks(ctx context.Context, sha string) ([]CheckRun, error) {
	var d []struct {
		ID     int    `json:"id"`
		Status string `json:"status"`
		Ref    string `json:"ref"`
		WebURL string `json:"web_url"`
	}
	if err := g.pages(ctx, "projects/:id/pipelines?sha="+sha+"&per_page=100", &d); err != nil {
		return nil, err
	}
	newest := make(map[string]int)
	for i, p := range d {
		if j, ok := newest[p.Ref]; !ok || p.ID > d[j].ID {
			newest[p.Ref] = i
		}
	}
	refs := make([]string, 0, len(newest))
	for ref := range newest {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	checks := []CheckRun{}
	for _, ref := range refs {
		p := d[newest[ref]]
		state := CheckPending
		switch p.Status {
		case "success":
			state = CheckSuccess
		case "skipped":
			state = CheckSkipped
		case "failed", "canceled", "manual":
			state = CheckFailure
		}
		checks = append(checks, CheckRun{Name: fmt.Sprintf("pipeline #%d (%s)", p.ID, ref), State: state, URL: p.WebURL})
		var jobs []struct {
			Name         string `json:"name"`
			Status       string `json:"status"`
			AllowFailure bool   `json:"allow_failure"`
			WebURL       string `json:"web_url"`
		}
		if err := g.pages(ctx, fmt.Sprintf("projects/:id/pipelines/%d/jobs?per_page=100", p.ID), &jobs); err != nil {
			return nil, err
		}
		for _, j := range jobs {
			state := CheckPending
			switch j.Status {
			case "success":
				state = CheckSuccess
			case "skipped", "manual":
				state = CheckSkipped
			case "failed", "canceled":
				state = CheckFailure
				if j.AllowFailure {
					state = CheckSkipped
				}
			}
			checks = append(checks, CheckRun{Name: j.Name, State: state, URL: j.WebURL})
		}
	}
	return checks, nil
}
