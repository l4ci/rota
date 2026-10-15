package tracker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/proc"
	"github.com/l4ci/rota/internal/projects"
)

// Jira is the issue tracker of issues.source jira, over the REST API of a
// `rota connect` connection: Cloud (REST v3, email plus API token, ADF
// bodies) or Data Center (REST v2, personal access token, plain-text bodies),
// as the connection's deployment says.
//
// rota's hidden state never goes into a body, since Jira keeps no HTML
// comment through ADF: a comment rota writes with a marker is posted with the
// markers stripped, and its full body lives in the issue property
// rota.comment.<comment id>; the fields block that ends an issue body lives in
// the property rota.fields. Reads put both back, so callers see the same
// bodies they wrote, as on GitHub. Status changes go through the transition
// whose target has the wanted status category, never a status name.
type Jira struct {
	rest       *REST
	site       string
	project    string
	cloud      bool
	notPlanned string

	mu        sync.Mutex
	me        *jiraUser
	issueType string
}

var _ IssueTracker = (*Jira)(nil)

// ConnResolver finds a named connection and its secret. Tests swap it in
// with WithConnections; the default reads the machine's connections.
type ConnResolver func(ctx context.Context, name string) (connections.Connection, string, error)

// WithConnections replaces the connection lookup of an HTTP tracker.
func WithConnections(r ConnResolver) Option {
	return func(c *CLI) { c.Conn = r }
}

// machineConn is the connection lookup of `rota connect`: connections.json
// under the global config dir, the secret from ROTA_<NAME>_TOKEN, the
// keychain or the file store.
func machineConn(ctx context.Context, name string) (connections.Connection, string, error) {
	dir, err := projects.Dir()
	if err != nil {
		return connections.Connection{}, "", err
	}
	c, ok, err := connections.Get(dir, name)
	if err != nil {
		return connections.Connection{}, "", err
	}
	if !ok {
		return connections.Connection{}, "", fmt.Errorf("connection %q is not defined on this machine (run: rota connect add %s --kind jira --site <url>)", name, name)
	}
	sec := &connections.Secrets{Getenv: os.Getenv, Keychain: connections.OSKeychain(proc.Run, runtime.GOOS), File: connections.FileStore(dir)}
	secret, _, err := sec.Resolve(ctx, name)
	if err != nil {
		return connections.Connection{}, "", fmt.Errorf("reading the secret of connection %q: %v", name, err)
	}
	return c, secret, nil
}

// newJiraTracker is NewIssueTracker for issues.source jira.
func newJiraTracker(ctx context.Context, c *CLI, s Settings) (*Jira, error) {
	if s.Connection == "" {
		return nil, unavailable("issues.source is jira but issues.connection is not set (run: rota connect add <name> --kind jira ..., then rota config set issues.connection <name>)")
	}
	if s.JiraProject == "" {
		return nil, unavailable("issues.source is jira but issues.jira.project is not set (run: rota config set issues.jira.project <KEY>)")
	}
	resolve := c.Conn
	if resolve == nil {
		resolve = machineConn
	}
	conn, secret, err := resolve(ctx, s.Connection)
	if err != nil {
		return nil, unavailable("%v", err)
	}
	if conn.Kind != connections.KindJira {
		return nil, unavailable("connection %q is kind %s, not jira", conn.Name, conn.Kind)
	}
	if err := conn.Validate(); err != nil {
		return nil, unavailable("%v (run: rota connect add %s --replace ...)", err, conn.Name)
	}
	if secret == "" {
		return nil, unavailable("connection %q has no secret (run: rota connect add %s --replace, or set %s)", conn.Name, conn.Name, connections.EnvVar(conn.Name))
	}
	j := NewJira(conn, secret, s.JiraProject, s.NotPlannedLabel)
	if sleep := c.Sleep; sleep != nil {
		j.rest.Sleep = func(ctx context.Context, d time.Duration) error {
			sleep(d)
			return ctx.Err()
		}
	}
	return j, nil
}

// NewJira is the adapter for project on connection c, authenticated by secret.
// The connection's deployment (cloud unless it says datacenter) picks the API.
func NewJira(c connections.Connection, secret, project, notPlannedLabel string) *Jira {
	site := strings.TrimRight(c.Site, "/")
	cloud := c.WithDefaults().Deployment != connections.DeploymentDataCenter
	j := &Jira{site: site, project: strings.ToUpper(project), cloud: cloud, notPlanned: notPlannedLabel}
	if j.notPlanned == "" {
		j.notPlanned = "not-planned"
	}
	version, auth := "2", "Bearer "+secret
	if j.cloud {
		version = "3"
		auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(c.User+":"+secret))
	}
	j.rest = &REST{Base: site + "/rest/api/" + version, Auth: func(r *http.Request) { r.Header.Set("Authorization", auth) }}
	return j
}

// JiraTester is the connections.Tester of kind jira: one GET /myself.
type JiraTester struct{}

func (JiraTester) Test(ctx context.Context, c connections.Connection, secret string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	return NewJira(c, secret, "", "").CheckAuth(ctx)
}

func (j *Jira) Provider() string { return "jira" }

// CheckAuth reads the authenticated user; a bad token is KindUnavailable.
func (j *Jira) CheckAuth(ctx context.Context) error {
	_, err := j.myself(ctx)
	return err
}

type jiraUser struct {
	AccountID   string `json:"accountId"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

func (u *jiraUser) label() string {
	if u == nil {
		return ""
	}
	return firstOf(u.DisplayName, u.Name, u.AccountID)
}

func firstOf(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// myself is the authenticated user, read once; a failure is retried next call.
func (j *Jira) myself(ctx context.Context) (*jiraUser, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.me != nil {
		return j.me, nil
	}
	var u jiraUser
	if err := j.rest.Do(ctx, "GET", "/myself", nil, &u); err != nil {
		return nil, err
	}
	j.me = &u
	return j.me, nil
}

// ---- bodies ------------------------------------------------------------------

// wire is a Markdown body as the API takes it: ADF on Cloud, the text itself
// on Data Center.
func (j *Jira) wire(md string) any {
	if j.cloud {
		return markdownToADF(md)
	}
	return md
}

// text reads a body back as Markdown.
func (j *Jira) text(raw json.RawMessage) string {
	return adfToMarkdown(raw)
}

// The issue properties rota keeps its state in.
const (
	propFields  = "rota.fields"
	propComment = "rota.comment."
	// propChunk keeps one property value under Jira's 32 KB limit with room
	// for the JSON around it; a longer body spans several properties.
	propChunk = 30000
	// visibleMax keeps a posted comment under Jira's 32767-character limit.
	visibleMax = 32000
)

// reMarker is one rota (or legacy hv) marker on a line.
var reMarker = regexp.MustCompile(`<!-- (?:rota|hv):([^\n]*?) -->`)

// visible is what a rota comment shows people: the body without its
// markers, or a short note naming the first marker when nothing else is left.
func visible(body string) string {
	first := reMarker.FindStringSubmatch(body)
	text := reMarker.ReplaceAllString(body, "")
	var lines []string
	blank := false
	for _, l := range strings.Split(strings.TrimSpace(text), "\n") {
		if strings.TrimSpace(l) == "" {
			if !blank && len(lines) > 0 {
				lines = append(lines, "")
			}
			blank = true
			continue
		}
		blank = false
		lines = append(lines, strings.TrimRightFunc(l, unicode.IsSpace))
	}
	text = strings.Join(lines, "\n")
	if text == "" && first != nil {
		text = "(rota " + first[1] + ")"
	}
	if utf8.RuneCountInString(text) > visibleMax {
		text = string([]rune(text)[:visibleMax]) + "\n\n…"
	}
	return text
}

// chunks splits s into pieces of at most propChunk bytes on rune boundaries.
func chunks(s string) []string {
	var out []string
	for len(s) > propChunk {
		cut := propChunk
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		if cut == 0 { // no rune start: invalid UTF-8, cut anywhere
			cut = propChunk
		}
		out = append(out, s[:cut])
		s = s[cut:]
	}
	return append(out, s)
}

type propPart struct {
	Body  string `json:"body"`
	Parts int    `json:"parts,omitempty"`
}

func commentProp(id string, part int) string {
	if part == 0 {
		return propComment + id
	}
	return propComment + id + "." + strconv.Itoa(part)
}

// stateBody is the full body rota stored for comment id, from the issue's
// properties; ok is false for a comment rota wrote without a marker, or a
// person's.
func stateBody(props map[string]json.RawMessage, id string) (string, bool) {
	raw, ok := props[commentProp(id, 0)]
	if !ok {
		return "", false
	}
	var head propPart
	if json.Unmarshal(raw, &head) != nil {
		return "", false
	}
	var b strings.Builder
	b.WriteString(head.Body)
	for i := 1; i < head.Parts; i++ {
		var p propPart
		if json.Unmarshal(props[commentProp(id, i)], &p) != nil {
			return "", false
		}
		b.WriteString(p.Body)
	}
	return b.String(), true
}

type fieldsProp struct {
	Block string `json:"block"`
}

// splitBody is the description and the fields-block property of a body.
func splitBody(body string) (string, fieldsProp) {
	text, lines, ok := marker.SplitFields(strings.ReplaceAll(body, "\r\n", "\n"))
	if !ok {
		return body, fieldsProp{}
	}
	return text, fieldsProp{Block: marker.FieldsOpen + "\n" + lines + "\n-->"}
}

// joinBody puts the stored fields block back after the description.
func joinBody(text string, props map[string]json.RawMessage) string {
	var f fieldsProp
	if raw, ok := props[propFields]; !ok || json.Unmarshal(raw, &f) != nil || f.Block == "" {
		return text
	}
	if text == "" {
		return f.Block
	}
	return text + "\n\n" + f.Block
}

type jiraProperty struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

// ---- issues ------------------------------------------------------------------

const jiraFields = "summary,description,labels,fixVersions,status,assignee,reporter,resolutiondate"

type jiraIssue struct {
	Key    string `json:"key"`
	Fields struct {
		Summary     string          `json:"summary"`
		Description json.RawMessage `json:"description"`
		Labels      []string        `json:"labels"`
		FixVersions []struct {
			Name string `json:"name"`
		} `json:"fixVersions"`
		Status struct {
			StatusCategory struct {
				Key string `json:"key"`
			} `json:"statusCategory"`
		} `json:"status"`
		Assignee       *jiraUser `json:"assignee"`
		Reporter       *jiraUser `json:"reporter"`
		ResolutionDate string    `json:"resolutiondate"`
	} `json:"fields"`
	Properties map[string]json.RawMessage `json:"properties"`
}

func (j *Jira) norm(d jiraIssue) Issue {
	f := d.Fields
	is := Issue{Key: IssueKey(d.Key), Title: f.Summary, Body: joinBody(j.text(f.Description), d.Properties),
		Labels: append([]string{}, f.Labels...), State: "open", URL: j.site + "/browse/" + d.Key,
		Assignees: []string{}, Author: f.Reporter.label()}
	if len(f.FixVersions) > 0 {
		is.Milestone = f.FixVersions[0].Name
	}
	if f.Status.StatusCategory.Key == "done" {
		is.State, is.ClosedAt, is.StateReason = "closed", f.ResolutionDate, "completed"
		if slices.Contains(f.Labels, j.notPlanned) {
			is.StateReason = "not_planned"
		}
	}
	if f.Assignee != nil {
		is.Assignees = append(is.Assignees, f.Assignee.label())
	}
	return is
}

// path is an issue's API path; the key is checked first, so a number or a
// stray string never reaches the URL.
func (j *Jira) path(key IssueKey, rest string) (string, error) {
	k, ok := ParseKey(string(key))
	if _, numbered := k.Number(); !ok || numbered {
		return "", &Error{Kind: KindNotFound, Code: 1, Message: "not a Jira issue key: " + strconv.Quote(string(key))}
	}
	return "/issue/" + url.PathEscape(string(k)) + rest, nil
}

func (j *Jira) issue(ctx context.Context, key IssueKey) (jiraIssue, error) {
	p, err := j.path(key, "?fields="+jiraFields+"&properties=*all")
	var d jiraIssue
	if err != nil {
		return d, err
	}
	err = j.rest.Do(ctx, "GET", p, nil, &d)
	return d, err
}

func (j *Jira) Get(ctx context.Context, key IssueKey, withComments bool) (Issue, error) {
	d, err := j.issue(ctx, key)
	if err != nil {
		return Issue{}, err
	}
	is := j.norm(d)
	if withComments {
		if is.Comments, err = j.comments(ctx, key, d.Properties); err != nil {
			return Issue{}, err
		}
	}
	return is, nil
}

// issueTypeID is the type new issues get: Task when the project has one by
// that name, else its first non-subtask type (names are translated).
func (j *Jira) issueTypeID(ctx context.Context) (string, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.issueType != "" {
		return j.issueType, nil
	}
	var p struct {
		IssueTypes []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Subtask bool   `json:"subtask"`
		} `json:"issueTypes"`
	}
	if err := j.rest.Do(ctx, "GET", "/project/"+url.PathEscape(j.project), nil, &p); err != nil {
		return "", err
	}
	for _, t := range p.IssueTypes {
		if !t.Subtask && (j.issueType == "" || strings.EqualFold(t.Name, "task")) {
			j.issueType = t.ID
		}
	}
	if j.issueType == "" {
		return "", failed("Jira project %s has no issue type that is not a subtask", j.project)
	}
	return j.issueType, nil
}

func (j *Jira) Create(ctx context.Context, title, body string, labels []string, milestone string) (IssueKey, error) {
	if err := jiraLabels(labels); err != nil {
		return "", err
	}
	typ, err := j.issueTypeID(ctx)
	if err != nil {
		return "", err
	}
	text, fields := splitBody(body)
	f := map[string]any{"project": map[string]string{"key": j.project}, "summary": title,
		"issuetype": map[string]string{"id": typ}, "labels": uniqOrEmpty(labels)}
	if text != "" {
		f["description"] = j.wire(text)
	}
	if milestone != "" {
		f["fixVersions"] = []map[string]string{{"name": milestone}}
	}
	var out struct {
		Key string `json:"key"`
	}
	in := map[string]any{"fields": f, "properties": []jiraProperty{{propFields, fields}}}
	if err := j.rest.Do(ctx, "POST", "/issue", in, &out); err != nil {
		return "", err
	}
	return IssueKey(out.Key), nil
}

func uniqOrEmpty(names []string) []string {
	if u := uniq(names); u != nil {
		return u
	}
	return []string{}
}

// jiraLabels refuses a label Jira cannot hold: labels are single words.
func jiraLabels(labels []string) error {
	for _, l := range labels {
		if strings.IndexFunc(l, unicode.IsSpace) >= 0 {
			return failed("Jira labels cannot contain spaces: %q (rename it under issues.labels)", l)
		}
	}
	return nil
}

func (j *Jira) Edit(ctx context.Context, key IssueKey, e IssueEdit) error {
	if err := jiraLabels(e.AddLabels); err != nil {
		return err
	}
	p, err := j.path(key, "")
	if err != nil {
		return err
	}
	in := map[string]any{}
	f := map[string]any{}
	if e.Title != nil {
		f["summary"] = *e.Title
	}
	if e.Body != nil {
		text, fields := splitBody(*e.Body)
		f["description"] = nil
		if text != "" {
			f["description"] = j.wire(text)
		}
		in["properties"] = []jiraProperty{{propFields, fields}}
	}
	if e.Milestone != "" {
		f["fixVersions"] = []map[string]string{{"name": e.Milestone}}
	} else if e.RemoveMilestone {
		f["fixVersions"] = []any{}
	}
	var ops []map[string]string
	for _, l := range uniq(e.AddLabels) {
		ops = append(ops, map[string]string{"add": l})
	}
	for _, l := range uniq(e.RemoveLabels) {
		ops = append(ops, map[string]string{"remove": l})
	}
	if len(f) > 0 {
		in["fields"] = f
	}
	if len(ops) > 0 {
		in["update"] = map[string]any{"labels": ops}
	}
	if len(in) == 0 {
		return nil
	}
	return j.rest.Do(ctx, "PUT", p, in, nil)
}

// EnsureLabels only checks the names: Jira creates a label on first use.
func (j *Jira) EnsureLabels(_ context.Context, names []string, _ bool) error {
	return jiraLabels(names)
}

func (j *Jira) AddLabels(ctx context.Context, key IssueKey, labels []string, _ bool) error {
	if len(uniq(labels)) == 0 {
		return nil
	}
	return j.Edit(ctx, key, IssueEdit{AddLabels: labels})
}

func (j *Jira) RemoveLabels(ctx context.Context, key IssueKey, labels []string) error {
	if len(uniq(labels)) == 0 {
		return nil
	}
	return j.Edit(ctx, key, IssueEdit{RemoveLabels: labels})
}

// jqlQuote is a JQL string literal.
func jqlQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func (j *Jira) jql(f ListFilter) string {
	cl := []string{"project = " + jqlQuote(j.project)}
	switch f.State {
	case "", "open":
		cl = append(cl, "statusCategory != Done")
	case "closed":
		cl = append(cl, "statusCategory = Done")
	}
	for _, l := range f.Labels {
		cl = append(cl, "labels = "+jqlQuote(l))
	}
	if f.Milestone != "" {
		cl = append(cl, "fixVersion = "+jqlQuote(f.Milestone))
	}
	if f.Mine {
		cl = append(cl, "assignee = currentUser()")
	}
	return strings.Join(cl, " AND ") + " ORDER BY key ASC"
}

// jiraPage is the search page size asked for; Jira may cap it lower.
const jiraPage = 100

// List searches the project. Cloud pages by token (search/jql), Data Center
// by offset (search).
func (j *Jira) List(ctx context.Context, f ListFilter) ([]Issue, error) {
	in := map[string]any{"jql": j.jql(f), "fields": strings.Split(jiraFields, ","), "maxResults": jiraPage, "properties": []string{propFields}}
	out := []Issue{}
	err := Pages(ctx, func(cursor string) (string, error) {
		var page struct {
			Issues        []jiraIssue `json:"issues"`
			NextPageToken string      `json:"nextPageToken"`
			StartAt       int         `json:"startAt"`
			Total         int         `json:"total"`
		}
		path := "/search/jql"
		if j.cloud {
			delete(in, "nextPageToken")
			if cursor != "" {
				in["nextPageToken"] = cursor
			}
		} else {
			path = "/search"
			in["startAt"], _ = strconv.Atoi(cursor)
		}
		if err := j.rest.Do(ctx, "POST", path, in, &page); err != nil {
			return "", err
		}
		for _, d := range page.Issues {
			out = append(out, j.norm(d))
		}
		if f.Limit > 0 && len(out) >= f.Limit {
			return "", nil
		}
		if j.cloud {
			return page.NextPageToken, nil
		}
		if next := page.StartAt + len(page.Issues); len(page.Issues) > 0 && next < page.Total {
			return strconv.Itoa(next), nil
		}
		return "", nil
	})
	if err != nil {
		return nil, err
	}
	return firstN(out, f.Limit), nil
}

// ---- status ------------------------------------------------------------------

// transition moves the issue to a status of the first category in want that a
// transition reaches; already in one of them is a no-op.
func (j *Jira) transition(ctx context.Context, key IssueKey, want ...string) error {
	d, err := j.issue(ctx, key)
	if err != nil {
		return err
	}
	if slices.Contains(want, d.Fields.Status.StatusCategory.Key) {
		return nil
	}
	p, _ := j.path(key, "/transitions")
	var ts struct {
		Transitions []struct {
			ID string `json:"id"`
			To struct {
				Name           string `json:"name"`
				StatusCategory struct {
					Key string `json:"key"`
				} `json:"statusCategory"`
			} `json:"to"`
		} `json:"transitions"`
	}
	if err := j.rest.Do(ctx, "GET", p, nil, &ts); err != nil {
		return err
	}
	for _, cat := range want {
		for _, t := range ts.Transitions {
			if t.To.StatusCategory.Key == cat {
				return j.rest.Do(ctx, "POST", p, map[string]any{"transition": map[string]string{"id": t.ID}}, nil)
			}
		}
	}
	return failed("%s has no transition to a status in category %s from its current status (check the project's workflow)", key, strings.Join(want, " or "))
}

// Close moves the issue to a done-category status, then labels a not-planned
// close and posts comment. The transition comes first, so a retry after it
// failed posts no second comment.
func (j *Jira) Close(ctx context.Context, key IssueKey, reason, comment string) error {
	if err := j.transition(ctx, key, "done"); err != nil {
		return err
	}
	if reason == "not_planned" {
		if err := j.AddLabels(ctx, key, []string{j.notPlanned}, true); err != nil {
			return err
		}
	}
	if comment != "" {
		if _, err := j.AddComment(ctx, key, comment); err != nil {
			return err
		}
	}
	return nil
}

// Reopen moves the issue back to a to-do status (else an in-progress one) and
// drops the not-planned label.
func (j *Jira) Reopen(ctx context.Context, key IssueKey) error {
	if err := j.transition(ctx, key, "new", "indeterminate"); err != nil {
		return err
	}
	return j.RemoveLabels(ctx, key, []string{j.notPlanned})
}

// AssignSelf makes the authenticated user the assignee; Jira has one.
func (j *Jira) AssignSelf(ctx context.Context, key IssueKey) error {
	me, err := j.myself(ctx)
	if err != nil {
		return err
	}
	p, err := j.path(key, "/assignee")
	if err != nil {
		return err
	}
	in := map[string]string{"name": me.Name}
	if j.cloud {
		in = map[string]string{"accountId": me.AccountID}
	}
	return j.rest.Do(ctx, "PUT", p, in, nil)
}

// ---- comments ----------------------------------------------------------------

type jiraComment struct {
	ID      string          `json:"id"`
	Body    json.RawMessage `json:"body"`
	Author  *jiraUser       `json:"author"`
	Created string          `json:"created"`
}

// jiraTime reads Jira's timestamp (2026-01-02T15:04:05.000+0000).
func jiraTime(s string) time.Time {
	t, err := time.Parse("2006-01-02T15:04:05.000-0700", s)
	if err != nil {
		return parseTime(s)
	}
	return t
}

func (j *Jira) Comments(ctx context.Context, key IssueKey) ([]Comment, error) {
	d, err := j.issue(ctx, key)
	if err != nil {
		return nil, err
	}
	return j.comments(ctx, key, d.Properties)
}

// comments pages the issue's comments oldest first, each rota comment with
// the body its property holds.
func (j *Jira) comments(ctx context.Context, key IssueKey, props map[string]json.RawMessage) ([]Comment, error) {
	out := []Comment{}
	err := Pages(ctx, func(cursor string) (string, error) {
		start, _ := strconv.Atoi(cursor)
		p, err := j.path(key, fmt.Sprintf("/comment?startAt=%d&maxResults=%d&orderBy=created", start, jiraPage))
		if err != nil {
			return "", err
		}
		var page struct {
			StartAt  int           `json:"startAt"`
			Total    int           `json:"total"`
			Comments []jiraComment `json:"comments"`
		}
		if err := j.rest.Do(ctx, "GET", p, nil, &page); err != nil {
			return "", err
		}
		for _, c := range page.Comments {
			body, ok := stateBody(props, c.ID)
			if !ok {
				body = j.text(c.Body)
			}
			out = append(out, Comment{ID: c.ID, Body: body, Author: c.Author.label(), CreatedAt: jiraTime(c.Created)})
		}
		if next := page.StartAt + len(page.Comments); len(page.Comments) > 0 && next < page.Total {
			return strconv.Itoa(next), nil
		}
		return "", nil
	})
	return out, err
}

// AddComment posts body; a body with a rota marker is posted without it and
// kept whole in its comment's property. A property that cannot be written
// takes the comment back down, so no half-written state is left.
func (j *Jira) AddComment(ctx context.Context, key IssueKey, body string) (string, error) {
	p, err := j.path(key, "/comment")
	if err != nil {
		return "", err
	}
	shown := body
	if marker.Has(body) {
		shown = visible(body)
	}
	var c jiraComment
	if err := j.rest.Do(ctx, "POST", p, map[string]any{"body": j.wire(shown)}, &c); err != nil {
		return "", err
	}
	if c.ID == "" {
		return "", failed("Jira returned no id for the new comment on %s", key)
	}
	if marker.Has(body) {
		if err := j.putState(ctx, key, c.ID, body, 0); err != nil {
			_ = j.rest.Do(ctx, "DELETE", p+"/"+url.PathEscape(c.ID), nil, nil)
			return "", err
		}
	}
	return c.ID, nil
}

func (j *Jira) EditComment(ctx context.Context, key IssueKey, commentID, body string) error {
	p, err := j.path(key, "/comment/"+url.PathEscape(commentID))
	if err != nil {
		return err
	}
	shown := body
	if marker.Has(body) {
		shown = visible(body)
	}
	if err := j.rest.Do(ctx, "PUT", p, map[string]any{"body": j.wire(shown)}, nil); err != nil {
		return err
	}
	old, err := j.stateParts(ctx, key, commentID)
	if err != nil {
		return err
	}
	if !marker.Has(body) {
		return j.dropState(ctx, key, commentID, 0, old)
	}
	return j.putState(ctx, key, commentID, body, old)
}

func (j *Jira) DeleteComment(ctx context.Context, key IssueKey, commentID string) error {
	p, err := j.path(key, "/comment/"+url.PathEscape(commentID))
	if err != nil {
		return err
	}
	old, err := j.stateParts(ctx, key, commentID)
	if err != nil {
		return err
	}
	if err := j.rest.Do(ctx, "DELETE", p, nil, nil); err != nil {
		return err
	}
	return j.dropState(ctx, key, commentID, 0, old)
}

// CommentURL is the browse link that focuses the comment; it costs no call.
func (j *Jira) CommentURL(_ context.Context, key IssueKey, commentID string) (string, error) {
	if _, err := j.path(key, ""); err != nil {
		return "", err
	}
	k, _ := ParseKey(string(key))
	return j.site + "/browse/" + url.PathEscape(string(k)) + "?focusedCommentId=" + url.QueryEscape(commentID), nil
}

// putState writes body into the comment's properties, then drops parts an
// older, longer body left beyond them.
func (j *Jira) putState(ctx context.Context, key IssueKey, id, body string, old int) error {
	parts := chunks(body)
	for i := len(parts) - 1; i >= 0; i-- {
		v := propPart{Body: parts[i]}
		if i == 0 {
			v.Parts = len(parts)
		}
		p, _ := j.path(key, "/properties/"+url.PathEscape(commentProp(id, i)))
		if err := j.rest.Do(ctx, "PUT", p, v, nil); err != nil {
			return err
		}
	}
	return j.dropState(ctx, key, id, len(parts), old)
}

// stateParts is how many properties the comment's state spans; 0 for none.
func (j *Jira) stateParts(ctx context.Context, key IssueKey, id string) (int, error) {
	p, _ := j.path(key, "/properties/"+url.PathEscape(commentProp(id, 0)))
	var got struct {
		Value propPart `json:"value"`
	}
	if err := j.rest.Do(ctx, "GET", p, nil, &got); err != nil {
		if IsKind(err, KindNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return max(1, got.Value.Parts), nil
}

// dropState deletes the comment's state properties from part from to part to.
func (j *Jira) dropState(ctx context.Context, key IssueKey, id string, from, to int) error {
	for i := from; i < to; i++ {
		p, _ := j.path(key, "/properties/"+url.PathEscape(commentProp(id, i)))
		if err := j.rest.Do(ctx, "DELETE", p, nil, nil); err != nil && !IsKind(err, KindNotFound) {
			return err
		}
	}
	return nil
}

// ClosesIssue: a PR closes a Jira issue by naming its key in the head branch
// or the title, as Jira's own development panel links them.
func (j *Jira) ClosesIssue(key IssueKey, c ClosingSource) bool { return ClosesByKey(key, c) }

// ---- milestones (project versions) -----------------------------------------

type jiraVersion struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Released    bool   `json:"released"`
	Archived    bool   `json:"archived"`
}

func (v jiraVersion) milestone() Milestone {
	n, _ := strconv.Atoi(v.ID)
	m := Milestone{Number: n, Title: v.Name, Description: v.Description, State: "open"}
	if v.Released || v.Archived {
		m.State = "closed"
	}
	return m
}

// Milestones lists the project's versions; a released or archived one is closed.
func (j *Jira) Milestones(ctx context.Context, state string) ([]Milestone, error) {
	var vs []jiraVersion
	if err := j.rest.Do(ctx, "GET", "/project/"+url.PathEscape(j.project)+"/versions", nil, &vs); err != nil {
		return nil, err
	}
	out := []Milestone{}
	for _, v := range vs {
		if m := v.milestone(); state == "" || state == "all" || m.State == state {
			out = append(out, m)
		}
	}
	return out, nil
}

func (j *Jira) FindMilestone(ctx context.Context, rotaID string) (string, bool, error) {
	ms, err := j.Milestones(ctx, "all")
	if err != nil {
		return "", false, err
	}
	t, ok := matchMilestone(ms, rotaID)
	return t, ok, nil
}

// CreateMilestone makes a project version and returns its id.
func (j *Jira) CreateMilestone(ctx context.Context, title, description string) (int, error) {
	var v jiraVersion
	if err := j.rest.Do(ctx, "POST", "/version", map[string]string{"name": title, "description": description, "project": j.project}, &v); err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(v.ID)
	if err != nil {
		return 0, failed("cannot parse version id from: %q", v.ID)
	}
	return n, nil
}

// EditMilestone renames or re-describes a version; closing releases it.
func (j *Jira) EditMilestone(ctx context.Context, number int, e MilestoneEdit) error {
	in := map[string]any{}
	if e.Title != nil {
		in["name"] = *e.Title
	}
	if e.Description != nil {
		in["description"] = *e.Description
	}
	if e.State != nil {
		in["released"] = *e.State == "closed"
	}
	if len(in) == 0 {
		return nil
	}
	return j.rest.Do(ctx, "PUT", "/version/"+strconv.Itoa(number), in, nil)
}

func (j *Jira) IssuesInMilestone(ctx context.Context, title, state string) ([]Issue, error) {
	if state == "" {
		state = "all"
	}
	return j.List(ctx, ListFilter{State: state, Milestone: title})
}

// ---- claims (plain comments) -----------------------------------------------

var _ ClaimStore = (*Jira)(nil)

// claimText opens a claim comment: "Claimed by <claim id>", with no marker,
// so people read it as written. Claims order by the server's created time,
// then comment id, never the claimer's clock: a write that a retry delays
// lands, and counts, after one the server took in between.
const claimText = "Claimed by "

// claimOf is the claim id of a claim comment; ok is false for any other.
func claimOf(body string) (string, bool) {
	id, ok := strings.CutPrefix(strings.TrimSpace(body), claimText)
	if !ok || id == "" || strings.IndexFunc(id, unicode.IsSpace) >= 0 {
		return "", false
	}
	return id, true
}

// claimComments is the issue's claim comments, earliest first.
func (j *Jira) claimComments(ctx context.Context, key IssueKey) ([]Comment, error) {
	cs, err := j.comments(ctx, key, nil)
	if err != nil {
		return nil, err
	}
	out := slices.DeleteFunc(cs, func(c Comment) bool { _, ok := claimOf(c.Body); return !ok })
	slices.SortStableFunc(out, func(a, b Comment) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		if c := len(a.ID) - len(b.ID); c != 0 { // numeric ids: shorter is smaller
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

// Claims lists the claim ids on key, earliest first, each once.
func (j *Jira) Claims(ctx context.Context, key IssueKey) ([]string, error) {
	cs, err := j.claimComments(ctx, key)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, c := range cs {
		if id, _ := claimOf(c.Body); !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out, nil
}

// PutClaim posts id's claim comment. A second one changes nothing: the
// earliest comment of an id holds its place.
func (j *Jira) PutClaim(ctx context.Context, key IssueKey, id string) error {
	_, err := j.AddComment(ctx, key, claimText+id)
	return err
}

// DropClaim deletes every claim comment of id; none is a no-op.
func (j *Jira) DropClaim(ctx context.Context, key IssueKey, id string) error {
	cs, err := j.claimComments(ctx, key)
	if err != nil {
		return err
	}
	for _, c := range cs {
		if got, _ := claimOf(c.Body); got == id {
			if err := j.DeleteComment(ctx, key, c.ID); err != nil && !IsKind(err, KindNotFound) {
				return err
			}
		}
	}
	return nil
}
