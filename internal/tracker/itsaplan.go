package tracker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/strutil"
)

// Itsaplan is the issue tracker of issues.source itsaplan: one project of a
// self-hosted It's a Plan instance (https://github.com/croffasia/itsaplan),
// over the REST API of a `rota connect` connection (the instance URL and a
// personal API key, sent as x-api-key). It runs on the shared REST client, so
// it inherits Jira's retries, Retry-After and status mapping.
//
// Columns are told apart by their stateType (backlog, unstarted, started,
// completed, canceled), never by name. rota's fields block lives in a project
// custom field named itsaplanFieldName: a human edit in the web editor
// re-serialises the description and may drop an HTML comment, a custom field it
// leaves alone. There are no milestones: the milestone calls fail as Linear's do.
//
// Reads use GET /projects/:key/issues/board (active issues) and
// /issues/archived: both return every issue with no limit, unlike the capped
// list route. Claims, the gate close and migrate (T3, T4 of #1100) follow.
type Itsaplan struct {
	rest    *REST
	site    string
	project string

	mu   sync.Mutex
	meta *itsaplanMeta
	me   string           // the key owner's user id
	ids  map[IssueKey]int // key -> issue id, which the feed route takes
}

var _ IssueTracker = (*Itsaplan)(nil)

// itsaplanFieldName is the project custom field that holds rota's fields block.
const itsaplanFieldName = "rota"

// newItsaplanTracker is NewIssueTracker for issues.source itsaplan.
func newItsaplanTracker(ctx context.Context, c *CLI, s Settings) (*Itsaplan, error) {
	if s.Connection == "" {
		return nil, unavailable("issues.source is itsaplan but issues.connection is not set (run: rota connect add <name> --kind itsaplan --site <url>, then rota config set issues.connection <name>)")
	}
	if s.ItsaplanProject == "" {
		return nil, unavailable("issues.source is itsaplan but issues.itsaplan.project is not set (run: rota config set issues.itsaplan.project <KEY>)")
	}
	resolve := c.Conn
	if resolve == nil {
		resolve = machineConn
	}
	conn, secret, err := resolve(ctx, s.Connection)
	if err != nil {
		return nil, unavailable("%v", err)
	}
	if conn.Kind != connections.KindItsaplan {
		return nil, unavailable("connection %q is kind %s, not itsaplan", conn.Name, conn.Kind)
	}
	if secret == "" {
		return nil, unavailable("connection %q has no secret (run: rota connect add %s --replace, or set %s)", conn.Name, conn.Name, connections.EnvVar(conn.Name))
	}
	t := NewItsaplan(conn, secret, s.ItsaplanProject)
	if sleep := c.Sleep; sleep != nil {
		t.rest.Sleep = func(ctx context.Context, d time.Duration) error {
			sleep(d)
			return ctx.Err()
		}
	}
	return t, nil
}

// NewItsaplan is the adapter for project (its key, such as MKT) on connection
// c, authenticated by a personal API key.
func NewItsaplan(c connections.Connection, apiKey, project string) *Itsaplan {
	site := strings.TrimRight(c.Site, "/")
	return &Itsaplan{
		site:    site,
		project: strings.ToUpper(strings.TrimSpace(project)),
		rest: &REST{Base: site, Auth: func(r *http.Request) { r.Header.Set("x-api-key", apiKey) },
			Client: &http.Client{Timeout: RESTTimeout, CheckRedirect: sameHostRedirect}},
		ids: map[IssueKey]int{},
	}
}

// sameHostRedirect refuses a redirect to another host. Go forwards a custom
// header such as x-api-key across hosts (it drops only Authorization and
// cookies), so following one would hand the key to whoever the instance, or
// a proxy in front of it, redirects to.
func sameHostRedirect(req *http.Request, via []*http.Request) error {
	if req.URL.Host != via[0].URL.Host {
		return fmt.Errorf("refusing a redirect from %s to %s: the API key is sent to the instance's own host only", via[0].URL.Host, req.URL.Host)
	}
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	return nil
}

// ItsaplanTester is the connections.Tester of kind itsaplan: one read of /me.
// A key the instance does not know is answered 200 with authenticated false,
// not 401, so the body decides.
type ItsaplanTester struct{}

func (ItsaplanTester) Test(ctx context.Context, c connections.Connection, secret string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	_, err := NewItsaplan(c, secret, "").viewer(ctx)
	return err
}

func (t *Itsaplan) Provider() string { return "itsaplan" }

// CheckAuth reads the key's user and the project; a bad key is KindUnavailable,
// and so is a project the key cannot see. A project with no rota field the key
// is not allowed to create fails too, with the field named.
func (t *Itsaplan) CheckAuth(ctx context.Context) error {
	if _, err := t.viewer(ctx); err != nil {
		return err
	}
	m, err := t.projectMeta(ctx)
	if err != nil {
		return err
	}
	if m.field() == nil && !m.permits("custom_fields", "create") {
		return unavailable("It's a Plan project %s has no custom field %q and this API key may not create one (ask a project admin to add a text field named %q, or give the key's user the custom fields permission)", t.project, itsaplanFieldName, itsaplanFieldName)
	}
	return nil
}

// ---- viewer, project ----------------------------------------------------------

// viewer is the key's user id, read once; a failure is retried next call.
func (t *Itsaplan) viewer(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.me != "" {
		return t.me, nil
	}
	var d struct {
		Authenticated bool `json:"authenticated"`
		User          struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := t.rest.Do(ctx, http.MethodGet, "/me", nil, &d); err != nil {
		return "", err
	}
	if !d.Authenticated || d.User.ID == "" {
		return "", unavailable("It's a Plan at %s did not accept the API key (check the connection's token: rota connect add <name> --replace)", t.site)
	}
	t.me = d.User.ID
	return t.me, nil
}

type itsaplanColumn struct {
	ID        int     `json:"id"`
	Name      string  `json:"name"`
	StateType string  `json:"stateType"`
	Position  float64 `json:"position"`
}

type itsaplanLabel struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type itsaplanField struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	FieldType string `json:"fieldType"`
}

type itsaplanMember struct {
	UserID   string `json:"userId"`
	Name     string `json:"name"`
	Username string `json:"username"`
}

// itsaplanMeta is GET /projects/:key, read once per process: the names that
// every write needs as numeric ids (columns, labels, custom fields).
type itsaplanMeta struct {
	Columns      []itsaplanColumn           `json:"columns"`
	Labels       []itsaplanLabel            `json:"labels"`
	CustomFields []itsaplanField            `json:"customFields"`
	Assignees    []itsaplanMember           `json:"assignees"`
	Permissions  map[string]map[string]bool `json:"permissions"`
}

// field is the project's rota custom field, or nil when it has none.
func (m *itsaplanMeta) field() *itsaplanField {
	for i, f := range m.CustomFields {
		if strings.EqualFold(f.Name, itsaplanFieldName) {
			return &m.CustomFields[i]
		}
	}
	return nil
}

func (m *itsaplanMeta) permits(area, action string) bool { return m.Permissions[area][action] }

func (m *itsaplanMeta) column(id int) *itsaplanColumn {
	for i := range m.Columns {
		if m.Columns[i].ID == id {
			return &m.Columns[i]
		}
	}
	return nil
}

func (m *itsaplanMeta) labelName(id int) string {
	for _, l := range m.Labels {
		if l.ID == id {
			return l.Name
		}
	}
	return ""
}

func (m *itsaplanMeta) memberName(userID string) string {
	for _, a := range m.Assignees {
		if a.UserID == userID {
			return strutil.FirstNonEmpty(a.Name, a.Username, a.UserID)
		}
	}
	return userID
}

// projectMeta reads the project once per adapter. A 404 is a project the key
// cannot see, which is a setup problem and so KindUnavailable.
func (t *Itsaplan) projectMeta(ctx context.Context) (*itsaplanMeta, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.meta != nil {
		return t.meta, nil
	}
	var m itsaplanMeta
	if err := t.rest.Do(ctx, http.MethodGet, t.projectPath(""), nil, &m); err != nil {
		if IsKind(err, KindNotFound) {
			return nil, unavailable("It's a Plan project %s is not visible to this API key (check issues.itsaplan.project, or the key's access: rota connect test)", t.project)
		}
		return nil, err
	}
	t.meta = &m
	return t.meta, nil
}

func (t *Itsaplan) projectPath(rest string) string {
	return "/projects/" + url.PathEscape(t.project) + rest
}

// ---- reading issues -----------------------------------------------------------

type itsaplanFieldValue struct {
	FieldID int    `json:"fieldId"`
	Value   string `json:"value"`
}

type itsaplanIssue struct {
	ID             int     `json:"id"`
	SequenceNumber int     `json:"sequenceNumber"`
	Identifier     string  `json:"identifier"`
	Title          string  `json:"title"`
	Description    string  `json:"description"`
	ColumnID       int     `json:"columnId"`
	AssigneeUserID *string `json:"assigneeUserId"`
	LabelIDs       []int   `json:"labelIds"`
	StatusSince    string  `json:"statusSince"`
	// The board lists a field's values as fieldValues; a single issue read
	// leaves that empty and lists them, named, as fields.
	FieldValues []itsaplanFieldValue `json:"fieldValues"`
	Fields      []itsaplanFieldValue `json:"fields"`
}

// fieldValue is the stored value of custom field id on the issue.
func (d itsaplanIssue) fieldValue(id int) (string, bool) {
	for _, v := range slices.Concat(d.FieldValues, d.Fields) {
		if v.FieldID == id {
			return v.Value, true
		}
	}
	return "", false
}

// norm maps one issue onto the shared Issue; the rota field's block, when the
// issue has one, ends the body as it does on the other trackers.
func (t *Itsaplan) norm(m *itsaplanMeta, d itsaplanIssue) Issue {
	body := d.Description
	if f := m.field(); f != nil {
		if block, ok := d.fieldValue(f.ID); ok && strings.TrimSpace(block) != "" {
			body = strings.TrimRight(body, "\n")
			if body != "" {
				body += "\n\n"
			}
			body += block
		}
	}
	key := strutil.FirstNonEmpty(d.Identifier, t.project+"-"+strconv.Itoa(d.SequenceNumber))
	is := Issue{Key: IssueKey(key), Title: d.Title, Body: body, Labels: []string{}, State: "open", Assignees: []string{}}
	for _, id := range d.LabelIDs {
		if n := m.labelName(id); n != "" {
			is.Labels = append(is.Labels, n)
		}
	}
	if d.AssigneeUserID != nil && *d.AssigneeUserID != "" {
		is.Assignees = append(is.Assignees, m.memberName(*d.AssigneeUserID))
	}
	if c := m.column(d.ColumnID); c != nil {
		switch c.StateType {
		case "completed":
			is.State, is.StateReason, is.ClosedAt = "closed", CloseCompleted, d.StatusSince
		case "canceled":
			is.State, is.StateReason, is.ClosedAt = "closed", CloseNotPlanned, d.StatusSince
		}
	}
	return is
}

// seq is the sequence number of key, which must belong to the project.
func (t *Itsaplan) seq(key IssueKey) (int, error) {
	k, ok := ParseKey(string(key))
	n, numbered := k.Seq()
	if !ok || !numbered || !strings.EqualFold(k.Project(), t.project) {
		return 0, &Error{Kind: KindNotFound, Code: 1, Message: fmt.Sprintf("not an issue of It's a Plan project %s: %q", t.project, string(key))}
	}
	return n, nil
}

// issue reads one issue by its key and remembers its id.
func (t *Itsaplan) issue(ctx context.Context, key IssueKey) (itsaplanIssue, error) {
	n, err := t.seq(key)
	if err != nil {
		return itsaplanIssue{}, err
	}
	var d itsaplanIssue
	if err := t.rest.Do(ctx, http.MethodGet, t.projectPath("/issues/"+strconv.Itoa(n)), nil, &d); err != nil {
		return itsaplanIssue{}, err
	}
	t.mu.Lock()
	t.ids[IssueKey(strutil.FirstNonEmpty(d.Identifier, string(key)))] = d.ID
	t.mu.Unlock()
	return d, nil
}

// issueID is key's issue id, read once per adapter.
func (t *Itsaplan) issueID(ctx context.Context, key IssueKey) (int, error) {
	k, ok := ParseKey(string(key))
	if !ok {
		return 0, &Error{Kind: KindNotFound, Code: 1, Message: fmt.Sprintf("not an issue of It's a Plan project %s: %q", t.project, string(key))}
	}
	t.mu.Lock()
	id := t.ids[k]
	t.mu.Unlock()
	if id != 0 {
		return id, nil
	}
	d, err := t.issue(ctx, k)
	return d.ID, err
}

func (t *Itsaplan) Get(ctx context.Context, key IssueKey, withComments bool) (Issue, error) {
	m, err := t.projectMeta(ctx)
	if err != nil {
		return Issue{}, err
	}
	d, err := t.issue(ctx, key)
	if err != nil {
		return Issue{}, err
	}
	is := t.norm(m, d)
	if withComments {
		if is.Comments, err = t.Comments(ctx, is.Key); err != nil {
			return Issue{}, err
		}
	}
	return is, nil
}

// itsaplanFeedPage is the largest page /issues/:id/feed serves.
const itsaplanFeedPage = 100

// Comments lists the issue's comments oldest first. The feed pages by a cursor
// the server hands back as a JSON object and takes back as its JSON text.
func (t *Itsaplan) Comments(ctx context.Context, key IssueKey) ([]Comment, error) {
	id, err := t.issueID(ctx, key)
	if err != nil {
		return nil, err
	}
	var out []Comment
	err = Pages(ctx, func(cursor string) (string, error) {
		q := url.Values{"filter": {"comments"}, "order": {"asc"}, "limit": {strconv.Itoa(itsaplanFeedPage)}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var page struct {
			Items []struct {
				ID        int    `json:"id"`
				Body      string `json:"body"`
				ActorName string `json:"actorName"`
				CreatedAt string `json:"createdAt"`
			} `json:"items"`
			NextCursor json.RawMessage `json:"nextCursor"`
		}
		if err := t.rest.Do(ctx, http.MethodGet, "/issues/"+strconv.Itoa(id)+"/feed?"+q.Encode(), nil, &page); err != nil {
			return "", err
		}
		for _, it := range page.Items {
			c := Comment{ID: strconv.Itoa(it.ID), Body: it.Body, Author: it.ActorName}
			c.CreatedAt, _ = time.Parse(time.RFC3339, it.CreatedAt)
			out = append(out, c)
		}
		if next := strings.TrimSpace(string(page.NextCursor)); next != "null" {
			return next, nil
		}
		return "", nil
	})
	return out, err
}

// List reads the board (every active issue) and, unless only open issues are
// wanted, the archived ones, once each: neither route takes a limit or pages.
// The result is ordered by number like Jira's keys, then cut to the limit.
func (t *Itsaplan) List(ctx context.Context, f ListFilter) ([]Issue, error) {
	if f.Milestone != "" {
		return nil, errItsaplanNoMilestones()
	}
	m, err := t.projectMeta(ctx)
	if err != nil {
		return nil, err
	}
	var me string
	if f.Mine {
		if me, err = t.viewer(ctx); err != nil {
			return nil, err
		}
	}
	var board struct {
		Issues []itsaplanIssue `json:"issues"`
	}
	if err := t.rest.Do(ctx, http.MethodGet, t.projectPath("/issues/board"), nil, &board); err != nil {
		return nil, err
	}
	got := board.Issues
	if f.State == "closed" || f.State == "all" {
		var archived []itsaplanIssue
		if err := t.rest.Do(ctx, http.MethodGet, t.projectPath("/issues/archived"), nil, &archived); err != nil {
			return nil, err
		}
		got = append(got, archived...)
	}
	wantLabels := uniq(f.Labels)
	out := make([]Issue, 0, len(got))
	for _, d := range got {
		is := t.norm(m, d)
		if f.State != "all" && (is.State == "closed") != (f.State == "closed") {
			continue
		}
		if f.Mine && (d.AssigneeUserID == nil || *d.AssigneeUserID != me) {
			continue
		}
		if !hasAllLabels(is.Labels, wantLabels) {
			continue
		}
		out = append(out, is)
	}
	slices.SortStableFunc(out, func(a, b Issue) int { return CompareKeys(a.Key, b.Key) })
	return firstN(out, f.Limit), nil
}

// hasAllLabels reports whether have holds every name in want, ignoring case.
func hasAllLabels(have, want []string) bool {
	for _, w := range want {
		if !slices.ContainsFunc(have, func(h string) bool { return strings.EqualFold(h, w) }) {
			return false
		}
	}
	return true
}

// ---- not built yet --------------------------------------------------------------

// errItsaplanSlice refuses a call whose slice of the It's a Plan source has not
// landed; the plan on #1100 builds claims and the gate close (T3) and migrate
// (T4) after the connection, read and write slices.
func errItsaplanSlice(what string) error {
	return failed("issues.source itsaplan does not support %s yet (not implemented)", what)
}

func errItsaplanNoMilestones() error {
	return failed("issues.source itsaplan has no milestones")
}

func (t *Itsaplan) AssignSelf(context.Context, IssueKey) error {
	return errItsaplanSlice("assigning issues")
}

// ClosesIssue: a key-based tracker, so the key in the branch or the PR title.
func (t *Itsaplan) ClosesIssue(key IssueKey, c ClosingSource) bool { return ClosesByKey(key, c) }

func (t *Itsaplan) Milestones(context.Context, string) ([]Milestone, error) {
	return nil, errItsaplanNoMilestones()
}
func (t *Itsaplan) FindMilestone(context.Context, string) (string, bool, error) {
	return "", false, nil
}
func (t *Itsaplan) CreateMilestone(context.Context, string, string) (int, error) {
	return 0, errItsaplanNoMilestones()
}
func (t *Itsaplan) EditMilestone(context.Context, int, MilestoneEdit) error {
	return errItsaplanNoMilestones()
}
func (t *Itsaplan) IssuesInMilestone(context.Context, string, string) ([]Issue, error) {
	return nil, errItsaplanNoMilestones()
}
