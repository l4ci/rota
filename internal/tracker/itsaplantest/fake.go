// Package itsaplantest is an in-memory fake It's a Plan REST server for tests.
// It speaks the slice of the api the tracker adapter uses, with the shapes and
// the quirks of a real instance (checked against api 1.6.0): an unknown key is
// answered 200 with authenticated false on /me and 401 elsewhere, the board
// lists a custom field's values as fieldValues while a single issue read lists
// them, named, as fields. It rejects anything outside that slice and imports
// only the standard library so internal/tracker tests can use it without an
// import cycle.
package itsaplantest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Column is one workflow column. StateType is backlog, unstarted, started,
// completed or canceled.
type Column struct {
	ID        int
	Name      string
	StateType string
}

// Options configures New; zero fields take the documented defaults.
type Options struct {
	Project string // project key, default "MKT"
	Key     string // the api key the fake accepts, default "itp_key"
	// Columns default to Backlog, Todo, In Progress, Done and Canceled, ids 1 to 5.
	Columns []Column
	Labels  []string
	// NoField leaves the project without the rota custom field. NoFieldRight
	// also takes the key's right to create one.
	NoField, NoFieldRight bool
	// FeedPage caps the items one feed page holds, below the api's 100, so a
	// test can make a feed span pages. 0 is no cap.
	FeedPage int
}

// Issue seeds one issue. Labels are names (created in the project when new);
// Column is a column id, default the first unstarted one.
type Issue struct {
	Title, Description string
	Labels             []string
	Column             int
	Archived           bool
	Fields             string // the rota custom field's value
	AssigneeMe         bool
	Comments           []string // comment bodies, oldest first
}

type issue struct {
	Issue
	num int
}

// Server is the fake. Its embedded httptest.Server exposes URL.
type Server struct {
	*httptest.Server
	t  testing.TB
	o  Options
	mu sync.Mutex

	columns []Column
	labels  []string // label id is index+1
	issues  []*issue
	reqs    []string
}

type obj = map[string]any

const (
	fieldID = 1
	meID    = "user-1"
)

// New starts a fake and registers its cleanup with t.
func New(t testing.TB, o Options) *Server {
	if o.Project == "" {
		o.Project = "MKT"
	}
	if o.Key == "" {
		o.Key = "itp_key"
	}
	if o.Columns == nil {
		o.Columns = []Column{{1, "Backlog", "backlog"}, {2, "Todo", "unstarted"}, {3, "In Progress", "started"}, {4, "Done", "completed"}, {5, "Canceled", "canceled"}}
	}
	s := &Server{t: t, o: o, columns: o.Columns, labels: append([]string(nil), o.Labels...)}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// Key is the api key the fake accepts.
func (s *Server) Key() string { return s.o.Key }

// Add seeds an issue and returns its key, such as MKT-1.
func (s *Server) Add(i Issue) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i.Column == 0 {
		i.Column = s.firstOfType("unstarted")
	}
	for _, l := range i.Labels {
		s.labelID(l)
	}
	s.issues = append(s.issues, &issue{Issue: i, num: len(s.issues) + 1})
	return s.o.Project + "-" + strconv.Itoa(len(s.issues))
}

// Requests is every request the fake served, as "METHOD path", oldest first.
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.reqs...)
}

// Count is how many requests to path (method and query left out) the fake served.
func (s *Server) Count(path string) int {
	n := 0
	for _, r := range s.Requests() {
		if _, p, _ := strings.Cut(r, " "); p == path {
			n++
		}
	}
	return n
}

func (s *Server) firstOfType(typ string) int {
	for _, c := range s.columns {
		if c.StateType == typ {
			return c.ID
		}
	}
	return 0
}

// labelID is the id of label name, creating it; the caller holds mu.
func (s *Server) labelID(name string) int {
	for i, l := range s.labels {
		if strings.EqualFold(l, name) {
			return i + 1
		}
	}
	s.labels = append(s.labels, name)
	return len(s.labels)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs = append(s.reqs, r.Method+" "+r.URL.Path)
	authed := r.Header.Get("x-api-key") == s.o.Key
	if r.URL.Path == "/me" && r.Method == http.MethodGet {
		if !authed {
			reply(w, 200, obj{"authenticated": false})
			return
		}
		reply(w, 200, obj{"authenticated": true, "user": obj{"id": meID, "name": "Rota", "email": "rota@example.com"}})
		return
	}
	if !authed {
		reply(w, 401, obj{"error": "Authentication required"})
		return
	}
	proj := "/projects/" + s.o.Project
	if rest, ok := strings.CutPrefix(r.URL.Path, "/projects/"); ok {
		if key, _, _ := strings.Cut(rest, "/"); key != s.o.Project {
			reply(w, 404, obj{"error": fmt.Sprintf("Project '%s' not found", key)})
			return
		}
	}
	if r.Method != http.MethodGet {
		reply(w, 405, obj{"error": "the fake serves reads only"})
		return
	}
	if rest, ok := strings.CutPrefix(r.URL.Path, "/issues/"); ok {
		s.feed(w, r.URL.Query(), strings.TrimSuffix(rest, "/feed"), strings.HasSuffix(rest, "/feed"))
		return
	}
	switch r.URL.Path {
	case proj:
		reply(w, 200, s.project())
		return
	case proj + "/issues/board":
		out := []obj{}
		for _, i := range s.issues {
			if !i.Archived {
				out = append(out, s.issueJSON(i, false))
			}
		}
		reply(w, 200, obj{"issues": out})
		return
	case proj + "/issues/archived":
		out := []obj{}
		for _, i := range s.issues {
			if i.Archived {
				out = append(out, s.issueJSON(i, false))
			}
		}
		reply(w, 200, out)
		return
	}
	if n, ok := strings.CutPrefix(r.URL.Path, proj+"/issues/"); ok {
		num, err := strconv.Atoi(n)
		if err == nil && num >= 1 && num <= len(s.issues) {
			reply(w, 200, s.issueJSON(s.issues[num-1], true))
			return
		}
		reply(w, 404, obj{"error": "Issue not found"})
		return
	}
	s.t.Errorf("itsaplantest: unsupported request %s %s", r.Method, r.URL.Path)
	reply(w, 501, obj{"error": "not in the fake's slice of the api"})
}

func (s *Server) project() obj {
	cols := []obj{}
	for i, c := range s.columns {
		cols = append(cols, obj{"id": c.ID, "projectId": 1, "name": c.Name, "stateType": c.StateType, "position": i})
	}
	lbs := []obj{}
	for i, l := range s.labels {
		lbs = append(lbs, obj{"id": i + 1, "projectId": 1, "name": l})
	}
	fields := []obj{}
	if !s.o.NoField {
		fields = append(fields, obj{"id": fieldID, "name": "rota", "fieldType": "text", "options": []any{}})
	}
	perm := func(ok bool) obj { return obj{"create": ok, "edit": ok, "read": true, "delete": ok} }
	return obj{
		"project":      obj{"id": 1, "key": s.o.Project, "name": s.o.Project},
		"columns":      cols,
		"labels":       lbs,
		"customFields": fields,
		"assignees":    []obj{{"userId": meID, "name": "Rota", "username": "rota", "kind": "member"}},
		"permissions":  obj{"work_items": perm(true), "custom_fields": perm(!s.o.NoFieldRight)},
		"viewer":       obj{"role": "owner"},
	}
}

// issueJSON is an issue as the board lists it; single is the shape of a read
// of one issue, which names its field values under fields instead.
func (s *Server) issueJSON(i *issue, single bool) obj {
	ids := []int{}
	for _, l := range i.Labels {
		ids = append(ids, s.labelID(l))
	}
	var assignee any
	if i.AssigneeMe {
		assignee = meID
	}
	o := obj{
		"id": i.num, "projectId": 1, "sequenceNumber": i.num, "identifier": s.o.Project + "-" + strconv.Itoa(i.num),
		"title": i.Title, "description": i.Description, "columnId": i.Column, "assigneeUserId": assignee,
		"labelIds": ids, "statusSince": "2026-01-01T00:00:00.000Z", "archivedAt": nil,
		"fieldValues": []obj{}, "links": []any{},
	}
	if i.Fields != "" && !s.o.NoField {
		v := obj{"fieldId": fieldID, "value": i.Fields, "valueEnd": nil, "optionIds": []any{}}
		if single {
			v["name"], v["fieldType"] = "rota", "text"
			o["fields"] = []obj{v}
		} else {
			o["fieldValues"] = []obj{v}
		}
	}
	return o
}

// commentIDBase keeps comment ids apart from issue ids, as a real instance's
// shared change-log ids are.
const commentIDBase = 1000

// feed serves /issues/:id/feed?filter=comments: the comments of one issue,
// newest first unless order=asc, cursor-paged. The cursor is the JSON object
// {"id": <last comment id served>} that nextCursor holds.
func (s *Server) feed(w http.ResponseWriter, q url.Values, id string, isFeed bool) {
	num, err := strconv.Atoi(id)
	if !isFeed || err != nil || num < 1 || num > len(s.issues) {
		reply(w, 404, obj{"error": "Issue not found"})
		return
	}
	if q.Get("filter") != "comments" {
		s.t.Errorf("itsaplantest: the feed is served for filter=comments only, got %q", q.Get("filter"))
	}
	var items []obj
	for i, body := range s.issues[num-1].Comments {
		cid := commentIDBase + num*100 + i
		items = append(items, obj{"id": cid, "issueId": num, "kind": "comment", "body": body, "actorName": "Rota",
			"createdAt": fmt.Sprintf("2026-01-01T00:%02d:00.000Z", i)})
	}
	if q.Get("order") != "asc" {
		for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
			items[i], items[j] = items[j], items[i]
		}
	}
	if c := q.Get("cursor"); c != "" {
		var cur struct{ ID int }
		if err := json.Unmarshal([]byte(c), &cur); err != nil {
			reply(w, 400, obj{"error": "bad cursor"})
			return
		}
		for len(items) > 0 {
			past := items[0]["id"].(int) == cur.ID
			items = items[1:]
			if past {
				break
			}
		}
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 25
	}
	if s.o.FeedPage > 0 {
		limit = min(limit, s.o.FeedPage)
	}
	var next any
	if len(items) > limit {
		items = items[:limit]
		next = obj{"id": items[len(items)-1]["id"]}
	}
	reply(w, 200, obj{"items": append([]obj{}, items...), "nextCursor": next})
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
