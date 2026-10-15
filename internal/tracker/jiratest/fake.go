// Package jiratest is an in-memory fake Jira REST server for tests. It speaks
// the slice of the Cloud (v3) and Data Center (v2) APIs the tracker adapter
// uses, rejects anything outside that slice, and imports only the standard
// library so internal/tracker tests can use it without an import cycle.
package jiratest

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Status is one workflow status. Category is "new", "indeterminate" or "done".
type Status struct{ ID, Name, Category string }

// IssueType is one project issue type.
type IssueType struct {
	ID, Name string
	Subtask  bool
}

// Options configures New; zero fields take the documented defaults.
type Options struct {
	Version      int    // 3 (Cloud, default) or 2 (Data Center)
	Project      string // project key, default "PROJ"; the project id is "10000"
	Email, Token string // defaults "me@example.com" and "tok"
	Statuses     []Status
	PageSize     int // cap on maxResults for search and comment pages, default 50
	IssueTypes   []IssueType
}

const (
	tsFmt     = "2006-01-02T15:04:05.000-0700"
	maxPropSz = 32768
	projectID = "10000"
)

type user struct{ id, name string }

type issue struct {
	num                        int
	summary                    string
	desc                       json.RawMessage
	labels, versions           []string // versions holds version ids
	status                     Status
	assigned                   bool
	created, updated, resolved string
	comments                   []*comment
	props                      map[string]json.RawMessage
}

type comment struct {
	id               string
	body             json.RawMessage
	author           user
	created, updated string
}

type version struct {
	id, name, desc string
	released       bool
	archived       bool
}

// Server is the fake. Its embedded httptest.Server exposes URL.
type Server struct {
	*httptest.Server
	t  testing.TB
	o  Options
	mu sync.Mutex

	issues      []*issue
	versions    []*version
	nextComment int
	nextVersion int
	clock       time.Time
	reqs        []string
	limitN      int
	retryAfter  string
	failN       int
	failStatus  int
}

type obj = map[string]any

// New starts a fake and registers its cleanup with t.
func New(t testing.TB, o Options) *Server {
	if o.Version == 0 {
		o.Version = 3
	}
	if o.Project == "" {
		o.Project = "PROJ"
	}
	if o.Email == "" {
		o.Email = "me@example.com"
	}
	if o.Token == "" {
		o.Token = "tok"
	}
	if o.Statuses == nil {
		o.Statuses = []Status{{"1", "To Do", "new"}, {"2", "In Progress", "indeterminate"}, {"3", "Done", "done"}}
	}
	if o.PageSize == 0 {
		o.PageSize = 50
	}
	if o.IssueTypes == nil {
		o.IssueTypes = []IssueType{{ID: "10001", Name: "Task"}, {ID: "10002", Name: "Sub-task", Subtask: true}}
	}
	s := &Server{t: t, o: o, nextComment: 20001, nextVersion: 30001,
		clock: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// RateLimitNext makes the next n requests answer 429 with the given Retry-After.
func (s *Server) RateLimitNext(n int, retryAfter string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limitN, s.retryAfter = n, retryAfter
}

// FailNext makes the next n requests answer status (a 5xx) before they reach
// any handler, so nothing they ask for happens.
func (s *Server) FailNext(n, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failN, s.failStatus = n, status
}

// Requests lists every request seen as "METHOD /path?query", in order.
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.reqs)
}

// CommentBodies returns the raw JSON of each stored comment body.
func (s *Server) CommentBodies(key string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	if is := s.find(key); is != nil {
		for _, c := range is.comments {
			out = append(out, string(c.body))
		}
	}
	return out
}

// Description returns the raw JSON of the stored description, "" if none.
func (s *Server) Description(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if is := s.find(key); is != nil {
		return string(is.desc)
	}
	return ""
}

// Property returns a stored issue property.
func (s *Server) Property(key, prop string) (json.RawMessage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if is := s.find(key); is != nil {
		v, ok := is.props[prop]
		return slices.Clone(v), ok
	}
	return nil, false
}

// AddHumanComment adds a comment by another user and returns its id.
func (s *Server) AddHumanComment(key string, body json.RawMessage, author string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	is := s.find(key)
	if is == nil {
		s.t.Fatalf("jiratest: AddHumanComment: no issue %s", key)
	}
	return s.addComment(is, body, user{author, author}).id
}

// StatusOf returns the issue's current status name.
func (s *Server) StatusOf(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if is := s.find(key); is != nil {
		return is.status.Name
	}
	return ""
}

// ---- plumbing ----

func (s *Server) prefix() string { return "/rest/api/" + strconv.Itoa(s.o.Version) }

func (s *Server) me() user {
	if s.o.Version == 2 {
		return user{"me", "Me Myself"}
	}
	return user{"acc-me", "Me Myself"}
}

func (s *Server) auth() string {
	if s.o.Version == 2 {
		return "Bearer " + s.o.Token
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(s.o.Email+":"+s.o.Token))
}

func (s *Server) tick() string {
	ts := s.clock.Format(tsFmt)
	s.clock = s.clock.Add(time.Second)
	return ts
}

func write(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func bad(w http.ResponseWriter, msg string) {
	write(w, 400, obj{"errorMessages": []string{msg}, "errors": obj{}})
}

func notFound(w http.ResponseWriter) {
	write(w, 404, obj{"errorMessages": []string{"Issue does not exist or you do not have permission to see it."}})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		bad(w, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func isNull(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || string(t) == "null"
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs = append(s.reqs, r.Method+" "+r.URL.RequestURI())
	if s.limitN > 0 {
		s.limitN--
		w.Header().Set("Retry-After", s.retryAfter)
		write(w, 429, obj{"errorMessages": []string{"rate limited"}})
		return
	}
	if s.failN > 0 {
		s.failN--
		write(w, s.failStatus, obj{"errorMessages": []string{"server error"}})
		return
	}
	if r.Header.Get("Authorization") != s.auth() {
		write(w, 401, obj{"errorMessages": []string{"unauthorized"}})
		return
	}
	rest, ok := strings.CutPrefix(r.URL.Path, s.prefix()+"/")
	if !ok {
		write(w, 404, obj{"errorMessages": []string{"no such endpoint"}})
		return
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	var key, arg string
	if len(parts) > 1 && (parts[0] == "project" || parts[0] == "issue" || parts[0] == "version") {
		key, parts[1] = parts[1], "{k}"
		if len(parts) > 3 && parts[0] == "issue" {
			arg, parts[3] = parts[3], "{a}"
		}
	}
	onIssue := func(f func(*issue)) {
		if is := s.find(key); is != nil {
			f(is)
		} else {
			notFound(w)
		}
	}
	switch r.Method + " /" + strings.Join(parts, "/") {
	case "GET /myself":
		s.myself(w)
	case "GET /project/{k}":
		s.project(w, key)
	case "GET /project/{k}/versions":
		s.listVersions(w, key)
	case "POST /issue":
		s.create(w, r)
	case "GET /issue/{k}":
		onIssue(func(is *issue) {
			q := r.URL.Query()
			write(w, 200, s.issueJSON(is, strings.Split(q.Get("fields"), ","), splitProps(q.Get("properties"))))
		})
	case "PUT /issue/{k}":
		onIssue(func(is *issue) { s.edit(w, r, is) })
	case "GET /issue/{k}/transitions":
		onIssue(func(is *issue) { s.transitions(w, is) })
	case "POST /issue/{k}/transitions":
		onIssue(func(is *issue) { s.transition(w, r, is) })
	case "PUT /issue/{k}/assignee":
		onIssue(func(is *issue) { s.assign(w, r, is) })
	case "GET /issue/{k}/comment":
		onIssue(func(is *issue) { s.listComments(w, r, is) })
	case "POST /issue/{k}/comment":
		onIssue(func(is *issue) { s.postComment(w, r, is) })
	case "PUT /issue/{k}/comment/{a}":
		onIssue(func(is *issue) { s.putComment(w, r, is, arg) })
	case "DELETE /issue/{k}/comment/{a}":
		onIssue(func(is *issue) { s.delComment(w, is, arg) })
	case "GET /issue/{k}/properties":
		onIssue(func(is *issue) { s.listProps(w, is) })
	case "GET /issue/{k}/properties/{a}":
		onIssue(func(is *issue) { s.getProp(w, is, arg) })
	case "PUT /issue/{k}/properties/{a}":
		onIssue(func(is *issue) { s.putProp(w, r, is, arg) })
	case "DELETE /issue/{k}/properties/{a}":
		onIssue(func(is *issue) { s.delProp(w, is, arg) })
	case "POST /search/jql":
		if s.o.Version == 3 {
			s.search(w, r)
		} else {
			notFound(w)
		}
	case "POST /search":
		if s.o.Version == 2 {
			s.search(w, r)
		} else {
			notFound(w)
		}
	case "POST /version":
		s.postVersion(w, r)
	case "PUT /version/{k}":
		s.putVersion(w, r, key)
	default:
		write(w, 404, obj{"errorMessages": []string{"no such endpoint"}})
	}
}

// ---- lookups and JSON shapes ----

func (s *Server) find(key string) *issue {
	n, err := strconv.Atoi(strings.TrimPrefix(key, s.o.Project+"-"))
	if err != nil || !strings.HasPrefix(key, s.o.Project+"-") || n < 1 || n > len(s.issues) {
		return nil
	}
	return s.issues[n-1]
}

func (s *Server) key(is *issue) string { return s.o.Project + "-" + strconv.Itoa(is.num) }

func (s *Server) versionBy(id, name string) *version {
	for _, v := range s.versions {
		if (id != "" && v.id == id) || (id == "" && v.name == name) {
			return v
		}
	}
	return nil
}

func (s *Server) userJSON(u user) obj {
	if s.o.Version == 2 {
		return obj{"name": u.id, "key": u.id, "displayName": u.name}
	}
	return obj{"accountId": u.id, "displayName": u.name}
}

func statusJSON(st Status) obj {
	return obj{"id": st.ID, "name": st.Name, "statusCategory": obj{"key": st.Category}}
}

func splitProps(p string) []string {
	if p == "" {
		return nil
	}
	return strings.Split(p, ",")
}

// issueJSON renders an issue; comments only when "comment" (or "*all") is in
// fields, properties only when props is non-nil.
func (s *Server) issueJSON(is *issue, fields, props []string) obj {
	vs := []obj{}
	for _, id := range is.versions {
		vs = append(vs, obj{"id": id, "name": s.versionBy(id, "").name})
	}
	var assignee, res, resDate any
	if is.assigned {
		assignee = s.userJSON(s.me())
	}
	if is.resolved != "" {
		res, resDate = obj{"name": "Done"}, is.resolved
	}
	f := obj{
		"summary": is.summary, "description": is.desc,
		"labels": append([]string{}, is.labels...), "fixVersions": vs,
		"status": statusJSON(is.status), "assignee": assignee, "reporter": s.userJSON(s.me()),
		"created": is.created, "updated": is.updated, "resolutiondate": resDate, "resolution": res,
	}
	if slices.Contains(fields, "comment") || slices.Contains(fields, "*all") {
		cs := []obj{}
		for _, c := range is.comments {
			cs = append(cs, s.commentJSON(c))
		}
		f["comment"] = obj{"comments": cs, "total": len(cs), "startAt": 0, "maxResults": len(cs)}
	}
	out := obj{"id": s.idOf(is), "key": s.key(is), "self": s.URL + s.prefix() + "/issue/" + s.idOf(is), "fields": f}
	if props != nil {
		p := obj{}
		for k, v := range is.props {
			if slices.Contains(props, "*all") || slices.Contains(props, k) {
				p[k] = v
			}
		}
		out["properties"] = p
	}
	return out
}

func (s *Server) idOf(is *issue) string { return fmt.Sprintf("100%02d", is.num) }

func (s *Server) commentJSON(c *comment) obj {
	return obj{"id": c.id, "body": c.body, "author": s.userJSON(c.author), "created": c.created, "updated": c.updated}
}

// validDoc applies the body rule: an ADF object (v3) or a string (v2).
func (s *Server) validDoc(raw json.RawMessage, nullOK bool) bool {
	if isNull(raw) {
		return nullOK && len(bytes.TrimSpace(raw)) > 0
	}
	if s.o.Version == 2 {
		var str string
		return json.Unmarshal(raw, &str) == nil
	}
	var d struct{ Type string }
	return json.Unmarshal(raw, &d) == nil && d.Type == "doc"
}

var errBadDoc = errors.New("body must be an ADF document (v3) or a string (v2)")

func docOrNil(raw json.RawMessage) json.RawMessage {
	if isNull(raw) {
		return nil
	}
	return slices.Clone(raw)
}

func checkLabels(ls []string) error {
	for _, l := range ls {
		if l == "" || strings.ContainsAny(l, " \t\n") {
			return fmt.Errorf("label %q is invalid", l)
		}
	}
	return nil
}

func (s *Server) versionIDs(raw json.RawMessage) ([]string, error) {
	if isNull(raw) {
		return nil, nil
	}
	var vs []struct{ ID, Name string }
	if err := json.Unmarshal(raw, &vs); err != nil {
		return nil, errors.New("fixVersions must be an array of objects")
	}
	ids := []string{}
	for _, x := range vs {
		v := s.versionBy(x.ID, x.Name)
		if v == nil {
			return nil, fmt.Errorf("version %q does not exist", x.Name+x.ID)
		}
		ids = append(ids, v.id)
	}
	return ids, nil
}

// propEntries are the top-level "properties" of create/edit bodies.
type propEntry struct {
	Key   string
	Value json.RawMessage
}

func checkProp(key string, v json.RawMessage) error {
	if key == "" || len(v) == 0 || !json.Valid(v) {
		return fmt.Errorf("property %q needs a key and a valid JSON value", key)
	}
	if len(v) > maxPropSz {
		return fmt.Errorf("property %q exceeds %d bytes", key, maxPropSz)
	}
	return nil
}

func checkProps(ps []propEntry) error {
	for _, p := range ps {
		if err := checkProp(p.Key, p.Value); err != nil {
			return err
		}
	}
	return nil
}

func setProps(is *issue, ps []propEntry) {
	for _, p := range ps {
		if is.props == nil {
			is.props = map[string]json.RawMessage{}
		}
		is.props[p.Key] = slices.Clone(p.Value)
	}
}

// ---- simple reads ----

func (s *Server) myself(w http.ResponseWriter) {
	u := s.userJSON(s.me())
	u["emailAddress"] = s.o.Email
	write(w, 200, u)
}

func (s *Server) project(w http.ResponseWriter, key string) {
	if key != s.o.Project {
		notFound(w)
		return
	}
	its := []obj{}
	for _, t := range s.o.IssueTypes {
		its = append(its, obj{"id": t.ID, "name": t.Name, "subtask": t.Subtask})
	}
	write(w, 200, obj{"id": projectID, "key": s.o.Project, "name": s.o.Project + " project", "issueTypes": its})
}

// ---- issues ----

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Fields struct {
			Project                  struct{ Key, ID string }
			Summary                  string
			Description, FixVersions json.RawMessage
			IssueType                struct{ ID, Name string }
			Labels                   []string
		}
		Properties []propEntry
	}
	if !decode(w, r, &in) {
		return
	}
	f := in.Fields
	err := func() error {
		if (f.Project.Key != "" && f.Project.Key != s.o.Project) || (f.Project.Key == "" && f.Project.ID != projectID) {
			return errors.New("project is invalid")
		}
		if f.Summary == "" {
			return errors.New("summary is required")
		}
		if !slices.ContainsFunc(s.o.IssueTypes, func(t IssueType) bool {
			return (f.IssueType.ID != "" && t.ID == f.IssueType.ID) || (f.IssueType.ID == "" && f.IssueType.Name != "" && t.Name == f.IssueType.Name)
		}) {
			return errors.New("issuetype is invalid")
		}
		if !isNull(f.Description) && !s.validDoc(f.Description, false) {
			return errBadDoc
		}
		if err := checkLabels(f.Labels); err != nil {
			return err
		}
		return checkProps(in.Properties)
	}()
	var ids []string
	if err == nil {
		ids, err = s.versionIDs(f.FixVersions)
	}
	if err != nil {
		bad(w, err.Error())
		return
	}
	ts := s.tick()
	is := &issue{num: len(s.issues) + 1, summary: f.Summary, desc: docOrNil(f.Description),
		labels: f.Labels, versions: ids, status: s.o.Statuses[0], created: ts, updated: ts}
	setProps(is, in.Properties)
	s.issues = append(s.issues, is)
	write(w, 201, obj{"id": s.idOf(is), "key": s.key(is), "self": s.URL + s.prefix() + "/issue/" + s.idOf(is)})
}

func (s *Server) edit(w http.ResponseWriter, r *http.Request, is *issue) {
	var in struct {
		Fields, Update map[string]json.RawMessage
		Properties     []propEntry
	}
	if !decode(w, r, &in) {
		return
	}
	c := *is
	c.labels = slices.Clone(is.labels)
	err := s.applyEdit(&c, in.Fields, in.Update)
	if err == nil {
		err = checkProps(in.Properties)
	}
	if err != nil {
		bad(w, err.Error())
		return
	}
	c.updated = s.tick()
	*is = c
	setProps(is, in.Properties)
	write(w, 204, nil)
}

func (s *Server) applyEdit(c *issue, fields, update map[string]json.RawMessage) error {
	for k, v := range fields {
		switch k {
		case "summary":
			var str string
			if json.Unmarshal(v, &str) != nil || str == "" {
				return errors.New("summary must be a non-empty string")
			}
			c.summary = str
		case "description":
			if !s.validDoc(v, true) {
				return errBadDoc
			}
			c.desc = docOrNil(v)
		case "labels":
			var l []string
			if json.Unmarshal(v, &l) != nil {
				return errors.New("labels must be a string array")
			}
			if err := checkLabels(l); err != nil {
				return err
			}
			c.labels = l
		case "fixVersions":
			ids, err := s.versionIDs(v)
			if err != nil {
				return err
			}
			c.versions = ids
		default:
			return fmt.Errorf("unsupported field %q", k)
		}
	}
	for k, v := range update {
		switch k {
		case "labels":
			var ops []map[string]string
			if json.Unmarshal(v, &ops) != nil {
				return errors.New("update.labels must be an array of operations")
			}
			for _, op := range ops {
				for verb, l := range op {
					switch verb {
					case "add":
						if err := checkLabels([]string{l}); err != nil {
							return err
						}
						if !slices.Contains(c.labels, l) {
							c.labels = append(c.labels, l)
						}
					case "remove":
						c.labels = slices.DeleteFunc(c.labels, func(x string) bool { return x == l })
					default:
						return fmt.Errorf("unsupported label operation %q", verb)
					}
				}
			}
		case "fixVersions":
			var ops []struct{ Set json.RawMessage }
			if json.Unmarshal(v, &ops) != nil {
				return errors.New("update.fixVersions must be an array of operations")
			}
			for _, op := range ops {
				ids, err := s.versionIDs(op.Set)
				if err != nil {
					return err
				}
				c.versions = ids
			}
		default:
			return fmt.Errorf("unsupported update %q", k)
		}
	}
	return nil
}

func (s *Server) transitions(w http.ResponseWriter, is *issue) {
	ts := []obj{}
	for _, st := range s.o.Statuses {
		if st.ID != is.status.ID {
			ts = append(ts, obj{"id": st.ID, "name": "→ " + st.Name, "to": statusJSON(st)})
		}
	}
	write(w, 200, obj{"transitions": ts})
}

func (s *Server) transition(w http.ResponseWriter, r *http.Request, is *issue) {
	var in struct{ Transition struct{ ID string } }
	if !decode(w, r, &in) {
		return
	}
	i := slices.IndexFunc(s.o.Statuses, func(st Status) bool { return st.ID == in.Transition.ID && st.ID != is.status.ID })
	if i < 0 {
		bad(w, "transition "+strconv.Quote(in.Transition.ID)+" is not valid")
		return
	}
	is.status, is.updated, is.resolved = s.o.Statuses[i], s.tick(), ""
	if is.status.Category == "done" {
		is.resolved = is.updated
	}
	write(w, 204, nil)
}

func (s *Server) assign(w http.ResponseWriter, r *http.Request, is *issue) {
	var in map[string]json.RawMessage
	if !decode(w, r, &in) {
		return
	}
	field := "accountId"
	if s.o.Version == 2 {
		field = "name"
	}
	raw, ok := in[field]
	var who string
	if !ok || (json.Unmarshal(raw, &who) != nil && !isNull(raw)) {
		bad(w, field+" is required")
		return
	}
	if !isNull(raw) && who != s.me().id {
		bad(w, "user "+strconv.Quote(who)+" does not exist")
		return
	}
	is.assigned, is.updated = !isNull(raw), s.tick()
	write(w, 204, nil)
}

// ---- search ----

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	var in struct {
		JQL                 string
		Fields, Properties  []string
		MaxResults, StartAt int
		NextPageToken       string
	}
	if !decode(w, r, &in) {
		return
	}
	match, err := s.parseJQL(in.JQL)
	if err != nil {
		bad(w, err.Error())
		return
	}
	var hits []*issue
	for _, is := range s.issues {
		if match(is) {
			hits = append(hits, is)
		}
	}
	start := in.StartAt
	if in.NextPageToken != "" {
		start, err = strconv.Atoi(in.NextPageToken)
	}
	if err != nil || start < 0 {
		bad(w, "invalid page token or startAt")
		return
	}
	start = min(start, len(hits))
	size := s.pageSize(in.MaxResults)
	end := min(start+size, len(hits))
	page := []obj{}
	for _, is := range hits[start:end] {
		page = append(page, s.issueJSON(is, in.Fields, in.Properties))
	}
	if s.o.Version == 2 {
		write(w, 200, obj{"startAt": start, "maxResults": size, "total": len(hits), "issues": page})
		return
	}
	out := obj{"issues": page, "isLast": end >= len(hits)}
	if end < len(hits) {
		out["nextPageToken"] = strconv.Itoa(end)
	}
	write(w, 200, out)
}

func (s *Server) pageSize(asked int) int {
	if asked <= 0 || asked > s.o.PageSize {
		return s.o.PageSize
	}
	return asked
}

const jqlVal = `"(?:[^"\\]|\\.)*"`

var (
	reOrder    = regexp.MustCompile(` ORDER BY (?:key|created) ASC$`)
	reVal      = regexp.MustCompile(jqlVal)
	reProject  = regexp.MustCompile(`^project\s*=\s*(` + jqlVal + `|[A-Za-z][A-Za-z0-9_]*)`)
	reStatCat  = regexp.MustCompile(`^statusCategory\s*(!=|=)\s*(?:Done|"Done")`)
	reLabels   = regexp.MustCompile(`^labels\s*=\s*(` + jqlVal + `)`)
	reFixV     = regexp.MustCompile(`^fixVersion\s*=\s*(` + jqlVal + `)`)
	reAssignee = regexp.MustCompile(`^assignee\s*=\s*currentUser\(\)`)
	reKeyIn    = regexp.MustCompile(`^key\s+in\s*\(\s*` + jqlVal + `(?:\s*,\s*` + jqlVal + `)*\s*\)`)
	unquote    = strings.NewReplacer(`\"`, `"`, `\\`, `\`)
)

func unq(v string) string {
	if strings.HasPrefix(v, `"`) {
		return unquote.Replace(v[1 : len(v)-1])
	}
	return v
}

// parseJQL compiles the supported JQL grammar; anything else is an error.
// Clauses are joined by ` AND `, and an optional trailing `ORDER BY key ASC`
// or `ORDER BY created ASC` is accepted (results are always in key order):
//
//	project = "PROJ"          (or unquoted PROJ)
//	statusCategory = Done     statusCategory != Done
//	labels = "x"
//	fixVersion = "name"
//	assignee = currentUser()
//	key in ("PROJ-1", "PROJ-2")
//
// Strings are double-quoted with \" and \\ escapes.
func (s *Server) parseJQL(q string) (func(*issue) bool, error) {
	unsupported := fmt.Errorf("unsupported JQL: %s", q)
	rest := strings.TrimSpace(reOrder.ReplaceAllString(q, ""))
	var ms []func(*issue) bool
	for {
		m, n := s.clause(rest)
		if m == nil {
			return nil, unsupported
		}
		ms, rest = append(ms, m), rest[n:]
		if rest == "" {
			break
		}
		var ok bool
		if rest, ok = strings.CutPrefix(rest, " AND "); !ok {
			return nil, unsupported
		}
	}
	return func(is *issue) bool {
		for _, m := range ms {
			if !m(is) {
				return false
			}
		}
		return true
	}, nil
}

// clause compiles the clause at the start of rest and returns how much it ate.
func (s *Server) clause(rest string) (func(*issue) bool, int) {
	if m := reProject.FindStringSubmatch(rest); m != nil {
		p := unq(m[1])
		return func(*issue) bool { return p == s.o.Project }, len(m[0])
	}
	if m := reStatCat.FindStringSubmatch(rest); m != nil {
		neg := m[1] == "!="
		return func(is *issue) bool { return (is.status.Category == "done") != neg }, len(m[0])
	}
	if m := reLabels.FindStringSubmatch(rest); m != nil {
		l := unq(m[1])
		return func(is *issue) bool { return slices.Contains(is.labels, l) }, len(m[0])
	}
	if m := reFixV.FindStringSubmatch(rest); m != nil {
		name := unq(m[1])
		return func(is *issue) bool {
			return slices.ContainsFunc(is.versions, func(id string) bool { return s.versionBy(id, "").name == name })
		}, len(m[0])
	}
	if m := reAssignee.FindString(rest); m != "" {
		return func(is *issue) bool { return is.assigned }, len(m)
	}
	if m := reKeyIn.FindString(rest); m != "" {
		keys := map[string]bool{}
		for _, v := range reVal.FindAllString(m, -1) {
			keys[unq(v)] = true
		}
		return func(is *issue) bool { return keys[s.key(is)] }, len(m)
	}
	return nil, 0
}

// ---- comments ----

func (s *Server) addComment(is *issue, body json.RawMessage, a user) *comment {
	ts := s.tick()
	c := &comment{id: strconv.Itoa(s.nextComment), body: slices.Clone(body), author: a, created: ts, updated: ts}
	s.nextComment++
	is.comments = append(is.comments, c)
	is.updated = ts
	return c
}

func (s *Server) listComments(w http.ResponseWriter, r *http.Request, is *issue) {
	q := r.URL.Query()
	start, _ := strconv.Atoi(q.Get("startAt"))
	asked, _ := strconv.Atoi(q.Get("maxResults"))
	start = min(max(start, 0), len(is.comments))
	size := s.pageSize(asked)
	cs := []obj{}
	for _, c := range is.comments[start:min(start+size, len(is.comments))] {
		cs = append(cs, s.commentJSON(c))
	}
	write(w, 200, obj{"startAt": start, "maxResults": size, "total": len(is.comments), "comments": cs})
}

func (s *Server) commentBody(w http.ResponseWriter, r *http.Request) (json.RawMessage, bool) {
	var in struct{ Body json.RawMessage }
	if !decode(w, r, &in) {
		return nil, false
	}
	if !s.validDoc(in.Body, false) {
		bad(w, errBadDoc.Error())
		return nil, false
	}
	return in.Body, true
}

func (s *Server) postComment(w http.ResponseWriter, r *http.Request, is *issue) {
	if body, ok := s.commentBody(w, r); ok {
		write(w, 201, s.commentJSON(s.addComment(is, body, s.me())))
	}
}

func (s *Server) putComment(w http.ResponseWriter, r *http.Request, is *issue, id string) {
	i := slices.IndexFunc(is.comments, func(c *comment) bool { return c.id == id })
	if i < 0 {
		notFound(w)
		return
	}
	if body, ok := s.commentBody(w, r); ok {
		c := is.comments[i]
		c.body, c.updated = slices.Clone(body), s.tick()
		is.updated = c.updated
		write(w, 200, s.commentJSON(c))
	}
}

func (s *Server) delComment(w http.ResponseWriter, is *issue, id string) {
	i := slices.IndexFunc(is.comments, func(c *comment) bool { return c.id == id })
	if i < 0 {
		notFound(w)
		return
	}
	is.comments = slices.Delete(is.comments, i, i+1)
	is.updated = s.tick()
	write(w, 204, nil)
}

// ---- properties ----

func (s *Server) propSelf(is *issue, k string) string {
	return s.URL + s.prefix() + "/issue/" + s.idOf(is) + "/properties/" + k
}

func (s *Server) listProps(w http.ResponseWriter, is *issue) {
	keys := []string{}
	for k := range is.props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []obj{}
	for _, k := range keys {
		out = append(out, obj{"key": k, "self": s.propSelf(is, k)})
	}
	write(w, 200, obj{"keys": out})
}

func (s *Server) getProp(w http.ResponseWriter, is *issue, k string) {
	v, ok := is.props[k]
	if !ok {
		write(w, 404, obj{"errorMessages": []string{"Issue property '" + k + "' does not exist."}})
		return
	}
	write(w, 200, obj{"key": k, "value": v})
}

func (s *Server) putProp(w http.ResponseWriter, r *http.Request, is *issue, k string) {
	v, err := io.ReadAll(r.Body)
	if err == nil {
		err = checkProp(k, v)
	}
	if err != nil {
		bad(w, err.Error())
		return
	}
	_, existed := is.props[k]
	setProps(is, []propEntry{{k, v}})
	s.tick()
	code := 201
	if existed {
		code = 200
	}
	write(w, code, nil)
}

func (s *Server) delProp(w http.ResponseWriter, is *issue, k string) {
	if _, ok := is.props[k]; !ok {
		write(w, 404, obj{"errorMessages": []string{"Issue property '" + k + "' does not exist."}})
		return
	}
	delete(is.props, k)
	s.tick()
	write(w, 204, nil)
}

// ---- versions ----

func (s *Server) versionJSON(v *version) obj {
	return obj{"id": v.id, "name": v.name, "description": v.desc, "released": v.released,
		"archived": v.archived, "projectId": 10000, "self": s.URL + s.prefix() + "/version/" + v.id}
}

func (s *Server) listVersions(w http.ResponseWriter, key string) {
	if key != s.o.Project {
		notFound(w)
		return
	}
	out := []obj{}
	for _, v := range s.versions {
		out = append(out, s.versionJSON(v))
	}
	write(w, 200, out)
}

func (s *Server) postVersion(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name, Description, Project string
		ProjectID                  json.RawMessage `json:"projectId"`
	}
	if !decode(w, r, &in) {
		return
	}
	switch {
	case in.Project != s.o.Project && strings.Trim(string(in.ProjectID), `"`) != projectID:
		bad(w, "project is invalid")
	case in.Name == "":
		bad(w, "version name is required")
	case s.versionBy("", in.Name) != nil:
		bad(w, "version "+strconv.Quote(in.Name)+" already exists")
	default:
		v := &version{id: strconv.Itoa(s.nextVersion), name: in.Name, desc: in.Description}
		s.nextVersion++
		s.versions = append(s.versions, v)
		s.tick()
		write(w, 201, s.versionJSON(v))
	}
}

func (s *Server) putVersion(w http.ResponseWriter, r *http.Request, id string) {
	v := s.versionBy(id, "")
	if v == nil {
		notFound(w)
		return
	}
	var in struct {
		Name, Description *string
		Released          *bool
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Name != nil {
		if o := s.versionBy("", *in.Name); *in.Name == "" || (o != nil && o != v) {
			bad(w, "version name is invalid or taken")
			return
		}
		v.name = *in.Name
	}
	if in.Description != nil {
		v.desc = *in.Description
	}
	if in.Released != nil {
		v.released = *in.Released
	}
	s.tick()
	write(w, 200, s.versionJSON(v))
}
