package tracker

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/l4ci/rota/internal/strutil"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/marker"
)

// Linear is the issue tracker of issues.source linear: one team of a Linear
// workspace, over the GraphQL API of a `rota connect` connection (a personal
// API key). Every call is one POST through REST; a query is retried on a 5xx,
// a mutation never is.
//
// Linear keeps no HTML comment in Markdown and an issue has no properties, so
// rota's hidden state lives in issue attachments, which carry a JSON metadata
// object: a comment rota writes with a marker is posted without its markers
// and kept whole in the attachment at linearStateURL+"comment/<comment id>",
// and the fields block that ends an issue body in the one at
// linearStateURL+"fields". Reads put both back, as Jira's properties do.
// Status changes pick the team's workflow state by type (completed,
// canceled, unstarted, started), never by name. Projects and cycles are not
// rota milestones (#586): the milestone calls fail and FindMilestone finds none.
type Linear struct {
	rest *REST
	team string

	mu     sync.Mutex
	me     *linearUser
	teamID string
	states []linearState
	labels map[string]linearLabel // lower-case name -> label usable by the team
	ids    map[IssueKey]string    // identifier -> issue id
}

var (
	_ IssueTracker = (*Linear)(nil)
	_ ClaimStore   = (*Linear)(nil)
	_ MergeCloser  = (*Linear)(nil)
)

// LinearAPI is the API a linear connection without a --site talks to.
const LinearAPI = "https://api.linear.app"

// linearStateURL prefixes the attachments rota keeps its state in. The
// .invalid name never resolves, so no one follows it anywhere.
const linearStateURL = "https://rota.invalid/state/"

// linearStateMax caps the body one state attachment holds. Linear's schema
// documents no limit on attachment metadata, so rota sets its own and fails
// above it rather than split a body across attachments.
const linearStateMax = 64 << 10

// linearPage is the page size of every list read; with nested lists of the
// same size a page stays far under Linear's query complexity limit.
const linearPage = 50

// newLinearTracker is NewIssueTracker for issues.source linear.
func newLinearTracker(ctx context.Context, c *CLI, s Settings) (*Linear, error) {
	if s.Connection == "" {
		return nil, unavailable("issues.source is linear but issues.connection is not set (run: rota connect add <name> --kind linear, then rota config set issues.connection <name>)")
	}
	if s.LinearTeam == "" {
		return nil, unavailable("issues.source is linear but issues.linear.team is not set (run: rota config set issues.linear.team <KEY>)")
	}
	conn, secret, err := c.resolver()(ctx, s.Connection)
	if err != nil {
		return nil, unavailable("%v", err)
	}
	if conn.Kind != connections.KindLinear {
		return nil, unavailable("connection %q is kind %s, not linear", conn.Name, conn.Kind)
	}
	if secret == "" {
		return nil, unavailable("connection %q has no secret (run: rota connect add %s --replace, or set %s)", conn.Name, conn.Name, connections.EnvVar(conn.Name))
	}
	l := NewLinear(conn, secret, s.LinearTeam)
	if sleep := c.Sleep; sleep != nil {
		l.rest.Sleep = func(ctx context.Context, d time.Duration) error {
			sleep(d)
			return ctx.Err()
		}
	}
	return l, nil
}

// NewLinear is the adapter for team (its key, such as ENG) on connection c,
// authenticated by a personal API key. A connection with no site talks to
// LinearAPI.
func NewLinear(c connections.Connection, apiKey, team string) *Linear {
	site := strings.TrimRight(c.Site, "/")
	if site == "" {
		site = LinearAPI
	}
	l := &Linear{team: strings.ToUpper(strings.TrimSpace(team)), ids: map[IssueKey]string{}}
	l.rest = &REST{Base: site + "/graphql", Auth: func(r *http.Request) { r.Header.Set("Authorization", apiKey) },
		Classify: func(_, _ string, status int, raw []byte) *Error {
			var out struct {
				Errors []gqlError `json:"errors"`
			}
			if json.Unmarshal(raw, &out) != nil || len(out.Errors) == 0 {
				return nil
			}
			return gqlFailure(fmt.Sprintf("HTTP %d", status), out.Errors)
		}}
	return l
}

// LinearTester is the connections.Tester of kind linear: one viewer query.
type LinearTester struct{}

func (LinearTester) Test(ctx context.Context, c connections.Connection, secret string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	_, err := NewLinear(c, secret, "").viewer(ctx)
	return err
}

func (l *Linear) Provider() string { return "linear" }

// CheckAuth reads the authenticated user and the team; a bad key is
// KindUnavailable, and so is a team the key cannot see.
func (l *Linear) CheckAuth(ctx context.Context) error {
	if _, err := l.viewer(ctx); err != nil {
		return err
	}
	_, err := l.teamInfo(ctx)
	return err
}

// ---- GraphQL -----------------------------------------------------------------

type gqlError struct {
	Message    string `json:"message"`
	Extensions struct {
		Code                   string `json:"code"`
		Type                   string `json:"type"`
		UserPresentableMessage string `json:"userPresentableMessage"`
	} `json:"extensions"`
}

// gqlFailure maps Linear's GraphQL errors to a tracker *Error by the first
// error's code or type: rate limits, authentication and missing entities get
// their kinds, anything else fails.
func gqlFailure(op string, errs []gqlError) *Error {
	e := errs[0]
	msg := "Linear " + op + ": " + e.Message
	if u := e.Extensions.UserPresentableMessage; u != "" && u != e.Message {
		msg += " (" + u + ")"
	}
	code, typ := strings.ToUpper(e.Extensions.Code), strings.ToLower(e.Extensions.Type)
	switch {
	case code == "RATELIMITED" || typ == "ratelimited":
		return rateLimited("%s", msg)
	case code == "AUTHENTICATION_ERROR" || typ == "authentication error":
		return unavailable("%s (check the connection's API key: rota connect test)", msg)
	case code == "FORBIDDEN" || typ == "forbidden":
		return unavailable("%s (check the API key's access to this team: rota connect test)", msg)
	case strings.Contains(strings.ToLower(e.Message), "not found"):
		return &Error{Kind: KindNotFound, Code: 1, Message: msg}
	}
	return failed("%s", msg)
}

// query runs a read-only operation and decodes its data into out.
func (l *Linear) query(ctx context.Context, op, doc string, vars map[string]any, out any) error {
	return l.gql(ctx, op, "query", doc, vars, out)
}

// mutate runs a mutation; it is never resent after a 5xx.
func (l *Linear) mutate(ctx context.Context, op, doc string, vars map[string]any, out any) error {
	return l.gql(ctx, op, "mutation", doc, vars, out)
}

// gql sends one operation. kind is the single decision: it opens the document
// and picks the transport, so a mutation cannot take the retrying read path.
func (l *Linear) gql(ctx context.Context, op, kind, doc string, vars map[string]any, out any) error {
	in := map[string]any{"operationName": op, "query": kind + " " + op + doc, "variables": vars}
	var resp struct {
		Data   json.RawMessage `json:"data"`
		Errors []gqlError      `json:"errors"`
	}
	var err error
	if kind == "query" {
		err = l.rest.ReadPost(ctx, "", in, &resp)
	} else {
		err = l.rest.Do(ctx, http.MethodPost, "", in, &resp)
	}
	if err != nil {
		return err
	}
	if len(resp.Errors) > 0 {
		return gqlFailure(op, resp.Errors)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(resp.Data, out); err != nil {
		return failed("Linear %s: unparseable data: %v", op, err)
	}
	return nil
}

// ok fails a mutation whose payload says it did not succeed.
func succeeded(op string, success bool) error {
	if !success {
		return failed("Linear %s: the mutation did not succeed", op)
	}
	return nil
}

type pageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

func (p pageInfo) next() string {
	if p.HasNextPage {
		return p.EndCursor
	}
	return ""
}

// after is a page cursor as a GraphQL variable: null on the first page.
func after(cursor string) any {
	if cursor == "" {
		return nil
	}
	return cursor
}

// ---- viewer, team, states, labels ---------------------------------------------

type linearUser struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

func (u *linearUser) label() string {
	if u == nil {
		return ""
	}
	return strutil.FirstNonEmpty(u.DisplayName, u.Name, u.ID)
}

// viewer is the authenticated user, read once; a failure is retried next call.
func (l *Linear) viewer(ctx context.Context) (*linearUser, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.me != nil {
		return l.me, nil
	}
	var d struct {
		Viewer linearUser `json:"viewer"`
	}
	if err := l.query(ctx, "Viewer", ` { viewer { id name displayName } }`, nil, &d); err != nil {
		return nil, err
	}
	l.me = &d.Viewer
	return l.me, nil
}

// linearState is a workflow state; Type is triage, backlog, unstarted,
// started, completed or canceled.
type linearState struct {
	ID       string  `json:"id"`
	Type     string  `json:"type"`
	Position float64 `json:"position"`
}

// teamInfo is the team's id and workflow states, read once.
func (l *Linear) teamInfo(ctx context.Context) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.teamID != "" {
		return l.teamID, nil
	}
	var d struct {
		Teams struct {
			Nodes []struct {
				ID     string `json:"id"`
				Key    string `json:"key"`
				States struct {
					Nodes []linearState `json:"nodes"`
				} `json:"states"`
			} `json:"nodes"`
		} `json:"teams"`
	}
	err := l.query(ctx, "Team", `($key: String!) { teams(filter: {key: {eq: $key}}) { nodes { id key states(first: 250) { nodes { id type position } } } } }`,
		map[string]any{"key": l.team}, &d)
	if err != nil {
		return "", err
	}
	for _, t := range d.Teams.Nodes {
		if strings.EqualFold(t.Key, l.team) {
			l.teamID, l.states = t.ID, t.States.Nodes
			return l.teamID, nil
		}
	}
	return "", unavailable("Linear team %s is not visible to this API key (check issues.linear.team, or the key's access: rota connect test)", l.team)
}

// stateOf is the team's first state of the first type in want that it has,
// by position.
func (l *Linear) stateOf(ctx context.Context, want ...string) (string, error) {
	if _, err := l.teamInfo(ctx); err != nil {
		return "", err
	}
	l.mu.Lock()
	states := l.states
	l.mu.Unlock()
	for _, typ := range want {
		var best *linearState
		for i, s := range states {
			if s.Type == typ && (best == nil || s.Position < best.Position) {
				best = &states[i]
			}
		}
		if best != nil {
			return best.ID, nil
		}
	}
	return "", failed("Linear team %s has no workflow state of type %s", l.team, strings.Join(want, " or "))
}

type linearLabel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Team *struct {
		ID string `json:"id"`
	} `json:"team"`
}

// loadLabels reads the workspace labels and the team's own; another team's
// label cannot go on this team's issues.
func (l *Linear) loadLabels(ctx context.Context) error {
	teamID, err := l.teamInfo(ctx)
	if err != nil {
		return err
	}
	got := map[string]linearLabel{}
	err = Pages(ctx, func(cursor string) (string, error) {
		var d struct {
			IssueLabels struct {
				Nodes    []linearLabel `json:"nodes"`
				PageInfo pageInfo      `json:"pageInfo"`
			} `json:"issueLabels"`
		}
		if err := l.query(ctx, "Labels", `($after: String) { issueLabels(first: 250, after: $after) { nodes { id name team { id } } pageInfo { hasNextPage endCursor } } }`,
			map[string]any{"after": after(cursor)}, &d); err != nil {
			return "", err
		}
		for _, lb := range d.IssueLabels.Nodes {
			if lb.Team == nil || lb.Team.ID == teamID {
				got[strings.ToLower(lb.Name)] = lb
			}
		}
		return d.IssueLabels.PageInfo.next(), nil
	})
	if err != nil {
		return err
	}
	l.mu.Lock()
	l.labels = got
	l.mu.Unlock()
	return nil
}

// labelIDs is the ids of names; a missing label is created on the team when
// create is set, else KindNotFound. Labels are read once and again when a
// name is missing, since another writer may have made it.
func (l *Linear) labelIDs(ctx context.Context, names []string, create bool) ([]string, error) {
	names = uniq(names)
	if len(names) == 0 {
		return nil, nil
	}
	missing := func() []string {
		l.mu.Lock()
		defer l.mu.Unlock()
		var out []string
		for _, n := range names {
			if _, ok := l.labels[strings.ToLower(n)]; !ok {
				out = append(out, n)
			}
		}
		return out
	}
	l.mu.Lock()
	loaded := l.labels != nil
	l.mu.Unlock()
	if !loaded || len(missing()) > 0 {
		if err := l.loadLabels(ctx); err != nil {
			return nil, err
		}
	}
	for _, n := range missing() {
		if !create {
			return nil, &Error{Kind: KindNotFound, Code: 1, Message: fmt.Sprintf("Linear label %q does not exist for team %s", n, l.team)}
		}
		var d struct {
			IssueLabelCreate struct {
				Success    bool        `json:"success"`
				IssueLabel linearLabel `json:"issueLabel"`
			} `json:"issueLabelCreate"`
		}
		if err := l.mutate(ctx, "LabelCreate", `($input: IssueLabelCreateInput!) { issueLabelCreate(input: $input) { success issueLabel { id name } } }`,
			map[string]any{"input": map[string]any{"name": n, "teamId": l.teamID}}, &d); err != nil {
			return nil, err
		}
		if err := succeeded("LabelCreate", d.IssueLabelCreate.Success); err != nil {
			return nil, err
		}
		l.mu.Lock()
		l.labels[strings.ToLower(n)] = d.IssueLabelCreate.IssueLabel
		l.mu.Unlock()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, l.labels[strings.ToLower(n)].ID)
	}
	return out, nil
}

// ---- issues ------------------------------------------------------------------

// linearIssueFields is the selection every issue read asks for; the first
// page of attachments usually holds the fields block.
const linearIssueFields = `id identifier title description url completedAt canceledAt state { type } ` +
	`labels(first: 50) { nodes { name } } assignee { id name displayName } creator { id name displayName } ` +
	`attachments(first: 50) { nodes { id url metadata } pageInfo { hasNextPage endCursor } }`

type linearAttachment struct {
	ID       string       `json:"id"`
	URL      string       `json:"url"`
	Metadata linearState0 `json:"metadata"`
}

// linearState0 is the metadata of a state attachment: format version V
// (missing reads as 1) and the body. A version this rota does not know is
// not read, so a newer format is never misread as the body.
type linearState0 struct {
	V    int    `json:"v"`
	Body string `json:"body"`
}

// linearStateVersion is the state attachment format rota writes.
const linearStateVersion = 1

// body is the stored body; ok is false for an unknown format version.
func (m linearState0) body() (string, bool) {
	return m.Body, m.V <= linearStateVersion
}

type linearAttachments struct {
	Nodes    []linearAttachment `json:"nodes"`
	PageInfo pageInfo           `json:"pageInfo"`
}

type linearIssue struct {
	ID          string `json:"id"`
	Identifier  string `json:"identifier"`
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
	CompletedAt string `json:"completedAt"`
	CanceledAt  string `json:"canceledAt"`
	State       struct {
		Type string `json:"type"`
	} `json:"state"`
	Labels struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	Assignee    *linearUser       `json:"assignee"`
	Creator     *linearUser       `json:"creator"`
	Attachments linearAttachments `json:"attachments"`
}

func (d linearIssue) closed() bool { return d.State.Type == "completed" || d.State.Type == "canceled" }

// state is rota's attachments of the issue by URL.
func (d linearIssue) state() map[string]linearAttachment {
	out := map[string]linearAttachment{}
	for _, a := range d.Attachments.Nodes {
		if strings.HasPrefix(a.URL, linearStateURL) {
			out[a.URL] = a
		}
	}
	return out
}

func (l *Linear) norm(d linearIssue) Issue {
	body := d.Description
	if f, ok := d.state()[linearStateURL+"fields"]; ok {
		if block, known := f.Metadata.body(); known && block != "" {
			body = strings.TrimRight(body, "\n")
			if body != "" {
				body += "\n\n"
			}
			body += block
		}
	}
	is := Issue{Key: IssueKey(d.Identifier), Title: d.Title, Body: body, Labels: []string{}, State: "open",
		URL: d.URL, Assignees: []string{}, Author: d.Creator.label()}
	for _, lb := range d.Labels.Nodes {
		is.Labels = append(is.Labels, lb.Name)
	}
	switch d.State.Type {
	case "completed":
		is.State, is.StateReason, is.ClosedAt = "closed", CloseCompleted, d.CompletedAt
	case "canceled":
		is.State, is.StateReason, is.ClosedAt = "closed", CloseNotPlanned, d.CanceledAt
	}
	if d.Assignee != nil {
		is.Assignees = append(is.Assignees, d.Assignee.label())
	}
	return is
}

// ident is key as Linear's issue(id:) takes it; the key is checked first, so
// a number or a stray string never reaches the API.
func ident(key IssueKey) (string, error) {
	k, ok := ParseKey(string(key))
	if _, numbered := k.Number(); !ok || numbered {
		return "", &Error{Kind: KindNotFound, Code: 1, Message: "not a Linear issue identifier: " + fmt.Sprintf("%q", string(key))}
	}
	return string(k), nil
}

// issue reads key with every attachment of rota's state.
func (l *Linear) issue(ctx context.Context, key IssueKey) (linearIssue, error) {
	var d struct {
		Issue *linearIssue `json:"issue"`
	}
	id, err := ident(key)
	if err != nil {
		return linearIssue{}, err
	}
	if err := l.query(ctx, "Issue", `($id: String!) { issue(id: $id) { `+linearIssueFields+` } }`, map[string]any{"id": id}, &d); err != nil {
		return linearIssue{}, err
	}
	if d.Issue == nil {
		return linearIssue{}, &Error{Kind: KindNotFound, Code: 1, Message: "Linear issue " + id + " not found"}
	}
	if err := l.moreAttachments(ctx, d.Issue); err != nil {
		return linearIssue{}, err
	}
	l.mu.Lock()
	l.ids[IssueKey(d.Issue.Identifier)] = d.Issue.ID
	l.mu.Unlock()
	return *d.Issue, nil
}

// moreAttachments reads the attachment pages after the first one.
func (l *Linear) moreAttachments(ctx context.Context, d *linearIssue) error {
	cursor := d.Attachments.PageInfo.next()
	if cursor == "" {
		return nil
	}
	return Pages(ctx, func(string) (string, error) {
		var p struct {
			Issue struct {
				Attachments linearAttachments `json:"attachments"`
			} `json:"issue"`
		}
		if err := l.query(ctx, "Attachments", `($id: String!, $after: String) { issue(id: $id) { attachments(first: 50, after: $after) { nodes { id url metadata } pageInfo { hasNextPage endCursor } } } }`,
			map[string]any{"id": d.ID, "after": cursor}, &p); err != nil {
			return "", err
		}
		d.Attachments.Nodes = append(d.Attachments.Nodes, p.Issue.Attachments.Nodes...)
		cursor = p.Issue.Attachments.PageInfo.next()
		return cursor, nil
	})
}

// issueID is key's Linear id, read once per adapter.
func (l *Linear) issueID(ctx context.Context, key IssueKey) (string, error) {
	k, err := ident(key)
	if err != nil {
		return "", err
	}
	l.mu.Lock()
	id := l.ids[IssueKey(k)]
	l.mu.Unlock()
	if id != "" {
		return id, nil
	}
	d, err := l.issue(ctx, IssueKey(k))
	return d.ID, err
}

func (l *Linear) Get(ctx context.Context, key IssueKey, withComments bool) (Issue, error) {
	d, err := l.issue(ctx, key)
	if err != nil {
		return Issue{}, err
	}
	is := l.norm(d)
	if withComments {
		if is.Comments, err = l.comments(ctx, d.ID, d.state()); err != nil {
			return Issue{}, err
		}
	}
	return is, nil
}

// errNoMilestones refuses a milestone on Linear: projects and cycles are not
// rota milestones (#586).
func errNoMilestones() error {
	return failed("issues.source linear has no milestones: Linear projects and cycles are not rota milestones")
}

func (l *Linear) Create(ctx context.Context, title, body string, labels []string, milestone string) (IssueKey, error) {
	if milestone != "" {
		return "", errNoMilestones()
	}
	teamID, err := l.teamInfo(ctx)
	if err != nil {
		return "", err
	}
	ids, err := l.labelIDs(ctx, labels, true)
	if err != nil {
		return "", err
	}
	text, fields := splitBody(body)
	if err := stateFits("fields", fields.Block); err != nil {
		return "", err
	}
	in := map[string]any{"teamId": teamID, "title": title, "description": text}
	if len(ids) > 0 {
		in["labelIds"] = ids
	}
	var d struct {
		IssueCreate struct {
			Success bool `json:"success"`
			Issue   struct {
				ID         string `json:"id"`
				Identifier string `json:"identifier"`
			} `json:"issue"`
		} `json:"issueCreate"`
	}
	if err := l.mutate(ctx, "IssueCreate", `($input: IssueCreateInput!) { issueCreate(input: $input) { success issue { id identifier } } }`,
		map[string]any{"input": in}, &d); err != nil {
		return "", err
	}
	c := d.IssueCreate
	if err := succeeded("IssueCreate", c.Success && c.Issue.ID != ""); err != nil {
		return "", err
	}
	if fields.Block != "" {
		if err := l.putState(ctx, c.Issue.ID, "fields", fields.Block); err != nil {
			if derr := l.mutate(ctx, "IssueDelete", `($id: String!) { issueDelete(id: $id) { success } }`, map[string]any{"id": c.Issue.ID}, nil); derr != nil {
				return "", failed("%v; deleting the half-made issue %s also failed (%v): delete it in Linear", err, c.Issue.Identifier, derr)
			}
			return "", err
		}
	}
	key := IssueKey(c.Issue.Identifier)
	l.mu.Lock()
	l.ids[key] = c.Issue.ID
	l.mu.Unlock()
	return key, nil
}

func (l *Linear) Edit(ctx context.Context, key IssueKey, e IssueEdit) error {
	if e.Milestone != "" {
		return errNoMilestones()
	}
	id, err := l.issueID(ctx, key)
	if err != nil {
		return err
	}
	in := map[string]any{}
	if e.Title != nil {
		in["title"] = *e.Title
	}
	var fields fieldsProp
	if e.Body != nil {
		var text string
		text, fields = splitBody(*e.Body)
		in["description"] = text
	}
	add, err := l.labelIDs(ctx, e.AddLabels, true)
	if err != nil {
		return err
	}
	if len(add) > 0 {
		in["addedLabelIds"] = add
	}
	drop, err := l.knownLabelIDs(ctx, e.RemoveLabels)
	if err != nil {
		return err
	}
	if len(drop) > 0 {
		in["removedLabelIds"] = drop
	}
	if len(in) > 0 {
		if err := l.update(ctx, id, in); err != nil {
			return err
		}
	}
	if e.Body == nil {
		return nil
	}
	if fields.Block == "" {
		return l.dropState(ctx, key, "fields")
	}
	return l.putState(ctx, id, "fields", fields.Block)
}

// knownLabelIDs is the ids of the names that exist; removing a label no one
// made changes nothing. Any other failure to read the labels is returned,
// never taken for "nothing to remove".
func (l *Linear) knownLabelIDs(ctx context.Context, names []string) ([]string, error) {
	var out []string
	for _, n := range uniq(names) {
		ids, err := l.labelIDs(ctx, []string{n}, false)
		if IsKind(err, KindNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, ids...)
	}
	return out, nil
}

func (l *Linear) update(ctx context.Context, id string, in map[string]any) error {
	var d struct {
		IssueUpdate struct {
			Success bool `json:"success"`
		} `json:"issueUpdate"`
	}
	if err := l.mutate(ctx, "IssueUpdate", `($id: String!, $input: IssueUpdateInput!) { issueUpdate(id: $id, input: $input) { success } }`,
		map[string]any{"id": id, "input": in}, &d); err != nil {
		return err
	}
	return succeeded("IssueUpdate", d.IssueUpdate.Success)
}

// EnsureLabels creates the missing labels on the team when autoCreate is set;
// without it a missing label is KindNotFound.
func (l *Linear) EnsureLabels(ctx context.Context, names []string, autoCreate bool) error {
	_, err := l.labelIDs(ctx, names, autoCreate)
	return err
}

func (l *Linear) AddLabels(ctx context.Context, key IssueKey, labels []string, autoCreate bool) error {
	if len(uniq(labels)) == 0 {
		return nil
	}
	if !autoCreate {
		if _, err := l.labelIDs(ctx, labels, false); err != nil {
			return err
		}
	}
	return l.Edit(ctx, key, IssueEdit{AddLabels: labels})
}

func (l *Linear) RemoveLabels(ctx context.Context, key IssueKey, labels []string) error {
	if len(uniq(labels)) == 0 {
		return nil
	}
	return l.Edit(ctx, key, IssueEdit{RemoveLabels: labels})
}

// filter is the IssueFilter of f on the team.
func (l *Linear) filter(f ListFilter) map[string]any {
	closed := []string{"completed", "canceled"}
	flt := map[string]any{"team": map[string]any{"key": map[string]any{"eq": l.team}}}
	switch f.State {
	case "", "open":
		flt["state"] = map[string]any{"type": map[string]any{"nin": closed}}
	case "closed":
		flt["state"] = map[string]any{"type": map[string]any{"in": closed}}
	}
	if f.Mine {
		flt["assignee"] = map[string]any{"isMe": map[string]any{"eq": true}}
	}
	var and []any
	for _, lb := range uniq(f.Labels) {
		and = append(and, map[string]any{"labels": map[string]any{"some": map[string]any{"name": map[string]any{"eqIgnoreCase": lb}}}})
	}
	if len(and) > 0 {
		flt["and"] = and
	}
	return flt
}

// List reads every page of the team's issues, orders them by number like
// Jira's keys, then cuts to the limit, so the result never depends on the
// order Linear pages in.
func (l *Linear) List(ctx context.Context, f ListFilter) ([]Issue, error) {
	if f.Milestone != "" {
		return nil, errNoMilestones()
	}
	var got []linearIssue
	err := Pages(ctx, func(cursor string) (string, error) {
		var d struct {
			Issues struct {
				Nodes    []linearIssue `json:"nodes"`
				PageInfo pageInfo      `json:"pageInfo"`
			} `json:"issues"`
		}
		if err := l.query(ctx, "Issues", `($filter: IssueFilter, $after: String) { issues(filter: $filter, first: 50, after: $after) { nodes { `+linearIssueFields+` } pageInfo { hasNextPage endCursor } } }`,
			map[string]any{"filter": l.filter(f), "after": after(cursor)}, &d); err != nil {
			return "", err
		}
		got = append(got, d.Issues.Nodes...)
		return d.Issues.PageInfo.next(), nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]Issue, 0, len(got))
	for i := range got {
		if err := l.moreAttachments(ctx, &got[i]); err != nil {
			return nil, err
		}
		out = append(out, l.norm(got[i]))
	}
	slices.SortStableFunc(out, func(a, b Issue) int { return CompareKeys(a.Key, b.Key) })
	return firstN(out, f.Limit), nil
}

// ---- status ------------------------------------------------------------------

// moveTo puts the issue in the team's state of the first type in want; an
// issue already in a state of any of them is left alone.
func (l *Linear) moveTo(ctx context.Context, d linearIssue, want ...string) error {
	if slices.Contains(want, d.State.Type) {
		return nil
	}
	state, err := l.stateOf(ctx, want...)
	if err != nil {
		return err
	}
	return l.update(ctx, d.ID, map[string]any{"stateId": state})
}

// Close moves the issue to a completed state, or a canceled one for
// not_planned, then posts comment. The move comes first, so a retry after it
// failed posts no second comment.
func (l *Linear) Close(ctx context.Context, key IssueKey, reason, comment string) error {
	d, err := l.issue(ctx, key)
	if err != nil {
		return err
	}
	want := CloseCompleted
	if reason == CloseNotPlanned {
		want = "canceled"
	}
	if err := l.moveTo(ctx, d, want); err != nil {
		return err
	}
	if comment != "" {
		if _, err := l.AddComment(ctx, key, comment); err != nil {
			return err
		}
	}
	return nil
}

// CloseAfterMerge closes the issue a merged PR names: rota relies on no
// Linear integration to do it.
func (l *Linear) CloseAfterMerge(ctx context.Context, key IssueKey, pr string) error {
	d, err := l.issue(ctx, key)
	if err != nil {
		return err
	}
	if d.closed() {
		return nil
	}
	return l.Close(ctx, key, CloseCompleted, "Closed by rota: "+pr+" merged.")
}

// Reopen moves a closed issue to an unstarted state, else a started one.
func (l *Linear) Reopen(ctx context.Context, key IssueKey) error {
	d, err := l.issue(ctx, key)
	if err != nil || !d.closed() {
		return err
	}
	return l.moveTo(ctx, d, "unstarted", "started")
}

// AssignSelf makes the authenticated user the assignee; Linear has one.
func (l *Linear) AssignSelf(ctx context.Context, key IssueKey) error {
	me, err := l.viewer(ctx)
	if err != nil {
		return err
	}
	id, err := l.issueID(ctx, key)
	if err != nil {
		return err
	}
	return l.update(ctx, id, map[string]any{"assigneeId": me.ID})
}

// ClosesIssue: a PR closes a Linear issue by naming its identifier in the
// head branch or the title, as Linear's own integrations link them.
func (l *Linear) ClosesIssue(key IssueKey, c ClosingSource) bool { return ClosesByKey(key, c) }

// ---- comments ----------------------------------------------------------------

type linearComment struct {
	ID        string      `json:"id"`
	Body      string      `json:"body"`
	CreatedAt string      `json:"createdAt"`
	User      *linearUser `json:"user"`
}

func (l *Linear) Comments(ctx context.Context, key IssueKey) ([]Comment, error) {
	d, err := l.issue(ctx, key)
	if err != nil {
		return nil, err
	}
	return l.comments(ctx, d.ID, d.state())
}

// comments pages the issue's comments, each rota comment with the body its
// attachment holds, oldest first by the server's time, then id.
func (l *Linear) comments(ctx context.Context, issueID string, state map[string]linearAttachment) ([]Comment, error) {
	out := []Comment{}
	err := Pages(ctx, func(cursor string) (string, error) {
		var d struct {
			Issue struct {
				Comments struct {
					Nodes    []linearComment `json:"nodes"`
					PageInfo pageInfo        `json:"pageInfo"`
				} `json:"comments"`
			} `json:"issue"`
		}
		if err := l.query(ctx, "Comments", `($id: String!, $after: String) { issue(id: $id) { comments(first: 50, after: $after) { nodes { id body createdAt user { id name displayName } } pageInfo { hasNextPage endCursor } } } }`,
			map[string]any{"id": issueID, "after": after(cursor)}, &d); err != nil {
			return "", err
		}
		for _, c := range d.Issue.Comments.Nodes {
			body := c.Body
			if a, ok := state[linearStateURL+"comment/"+c.ID]; ok {
				if full, known := a.Metadata.body(); known {
					body = full
				}
			}
			out = append(out, Comment{ID: c.ID, Body: body, Author: c.User.label(), CreatedAt: parseTime(c.CreatedAt)})
		}
		return d.Issue.Comments.PageInfo.next(), nil
	})
	slices.SortStableFunc(out, func(a, b Comment) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, err
}

// AddComment posts body; a body with a rota marker is posted without it and
// kept whole in its comment's attachment. An attachment that cannot be
// written takes the comment back down, so no half-written state is left.
func (l *Linear) AddComment(ctx context.Context, key IssueKey, body string) (string, error) {
	issueID, err := l.issueID(ctx, key)
	if err != nil {
		return "", err
	}
	shown := body
	if marker.Has(body) {
		if err := stateFits("a comment", body); err != nil {
			return "", err
		}
		shown = visible(body)
	}
	var d struct {
		CommentCreate struct {
			Success bool          `json:"success"`
			Comment linearComment `json:"comment"`
		} `json:"commentCreate"`
	}
	if err := l.mutate(ctx, "CommentCreate", `($input: CommentCreateInput!) { commentCreate(input: $input) { success comment { id } } }`,
		map[string]any{"input": map[string]any{"issueId": issueID, "body": shown}}, &d); err != nil {
		return "", err
	}
	id := d.CommentCreate.Comment.ID
	if err := succeeded("CommentCreate", d.CommentCreate.Success && id != ""); err != nil {
		return "", err
	}
	if marker.Has(body) {
		if err := l.putState(ctx, issueID, "comment/"+id, body); err != nil {
			if derr := l.deleteComment(ctx, id); derr != nil {
				return "", failed("%v; deleting the half-made comment %s on %s also failed (%v): delete it in Linear", err, id, key.Ref(), derr)
			}
			return "", err
		}
	}
	return id, nil
}

func (l *Linear) EditComment(ctx context.Context, key IssueKey, commentID, body string) error {
	issueID, err := l.issueID(ctx, key)
	if err != nil {
		return err
	}
	shown := body
	if marker.Has(body) {
		if err := stateFits("comment "+commentID, body); err != nil {
			return err
		}
		shown = visible(body)
	}
	var d struct {
		CommentUpdate struct {
			Success bool `json:"success"`
		} `json:"commentUpdate"`
	}
	if err := l.mutate(ctx, "CommentUpdate", `($id: String!, $input: CommentUpdateInput!) { commentUpdate(id: $id, input: $input) { success } }`,
		map[string]any{"id": commentID, "input": map[string]any{"body": shown}}, &d); err != nil {
		return err
	}
	if err := succeeded("CommentUpdate", d.CommentUpdate.Success); err != nil {
		return err
	}
	if !marker.Has(body) {
		return l.dropState(ctx, key, "comment/"+commentID)
	}
	return l.putState(ctx, issueID, "comment/"+commentID, body)
}

func (l *Linear) DeleteComment(ctx context.Context, key IssueKey, commentID string) error {
	if _, err := ident(key); err != nil {
		return err
	}
	if err := l.deleteComment(ctx, commentID); err != nil {
		return err
	}
	return l.dropState(ctx, key, "comment/"+commentID)
}

func (l *Linear) deleteComment(ctx context.Context, id string) error {
	var d struct {
		CommentDelete struct {
			Success bool `json:"success"`
		} `json:"commentDelete"`
	}
	if err := l.mutate(ctx, "CommentDelete", `($id: String!) { commentDelete(id: $id) { success } }`, map[string]any{"id": id}, &d); err != nil {
		return err
	}
	return succeeded("CommentDelete", d.CommentDelete.Success)
}

// CommentURL is the comment's own link, as Linear reports it.
func (l *Linear) CommentURL(ctx context.Context, key IssueKey, commentID string) (string, error) {
	if _, err := ident(key); err != nil {
		return "", err
	}
	var d struct {
		Comment *struct {
			URL string `json:"url"`
		} `json:"comment"`
	}
	if err := l.query(ctx, "CommentURL", `($id: String!) { comment(id: $id) { url } }`, map[string]any{"id": commentID}, &d); err != nil {
		return "", err
	}
	if d.Comment == nil {
		return "", &Error{Kind: KindNotFound, Code: 1, Message: "Linear comment " + commentID + " not found"}
	}
	return d.Comment.URL, nil
}

// stateFits refuses a state body over linearStateMax. Writers check it before
// their first write, so an oversized body leaves nothing behind.
func stateFits(name, body string) error {
	if len(body) > linearStateMax {
		return failed("rota state for %s is %d bytes, over the %d a Linear attachment takes here (Linear documents no metadata limit, so rota caps it): shorten the note or fields block", name, len(body), linearStateMax)
	}
	return nil
}

// putState writes body into the issue's state attachment of name (fields, or
// comment/<id>): attachmentCreate replaces an attachment with the same URL on
// the same issue.
func (l *Linear) putState(ctx context.Context, issueID, name, body string) error {
	if err := stateFits(name, body); err != nil {
		return err
	}
	var d struct {
		AttachmentCreate struct {
			Success bool `json:"success"`
		} `json:"attachmentCreate"`
	}
	in := map[string]any{"issueId": issueID, "url": linearStateURL + name, "title": "rota state",
		"subtitle": "Kept by rota; do not edit or remove.", "metadata": map[string]any{"v": linearStateVersion, "body": body}}
	if err := l.mutate(ctx, "AttachmentCreate", `($input: AttachmentCreateInput!) { attachmentCreate(input: $input) { success } }`,
		map[string]any{"input": in}, &d); err != nil {
		return err
	}
	return succeeded("AttachmentCreate", d.AttachmentCreate.Success)
}

// dropState deletes the issue's state attachment of name; none is a no-op.
func (l *Linear) dropState(ctx context.Context, key IssueKey, name string) error {
	d, err := l.issue(ctx, key)
	if err != nil {
		return err
	}
	a, found := d.state()[linearStateURL+name]
	if !found {
		return nil
	}
	var out struct {
		AttachmentDelete struct {
			Success bool `json:"success"`
		} `json:"attachmentDelete"`
	}
	if err := l.mutate(ctx, "AttachmentDelete", `($id: String!) { attachmentDelete(id: $id) { success } }`, map[string]any{"id": a.ID}, &out); err != nil {
		if IsKind(err, KindNotFound) {
			return nil
		}
		return err
	}
	return succeeded("AttachmentDelete", out.AttachmentDelete.Success)
}

// ---- milestones: none -----------------------------------------------------------

func (l *Linear) Milestones(context.Context, string) ([]Milestone, error) {
	return nil, errNoMilestones()
}

// FindMilestone finds none: Linear holds no rota milestones.
func (l *Linear) FindMilestone(context.Context, string) (string, bool, error) {
	return "", false, nil
}

func (l *Linear) CreateMilestone(context.Context, string, string) (int, error) {
	return 0, errNoMilestones()
}

func (l *Linear) EditMilestone(context.Context, int, MilestoneEdit) error { return errNoMilestones() }

func (l *Linear) IssuesInMilestone(context.Context, string, string) ([]Issue, error) {
	return nil, errNoMilestones()
}

// ---- claims (plain comments) ------------------------------------------------------

// claimStore is the shared claim-comment rule over Linear's comments.
func (l *Linear) claimStore() claimComments {
	return claimComments{
		list: func(ctx context.Context, key IssueKey) ([]Comment, error) {
			id, err := l.issueID(ctx, key)
			if err != nil {
				return nil, err
			}
			return l.comments(ctx, id, nil)
		},
		add: func(ctx context.Context, key IssueKey, body string) error {
			_, err := l.AddComment(ctx, key, body)
			return err
		},
		delete: func(ctx context.Context, _ IssueKey, commentID string) error { return l.deleteComment(ctx, commentID) },
	}
}

// Claims lists the claim ids on key, earliest first, each once.
func (l *Linear) Claims(ctx context.Context, key IssueKey) ([]string, error) {
	return l.claimStore().Claims(ctx, key)
}

// PutClaim posts id's plain claim comment; the earliest one of an id holds
// its place.
func (l *Linear) PutClaim(ctx context.Context, key IssueKey, id string) error {
	return l.claimStore().PutClaim(ctx, key, id)
}

// DropClaim deletes every claim comment of id; none is a no-op.
func (l *Linear) DropClaim(ctx context.Context, key IssueKey, id string) error {
	return l.claimStore().DropClaim(ctx, key, id)
}
