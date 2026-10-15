// Package lineartest is an in-memory fake Linear GraphQL server for tests. It
// answers the operations the tracker adapter sends (by operationName, checking
// the query keyword), rejects anything outside that slice, and imports only the
// standard library so internal/tracker tests can use it without an import cycle.
// It does not parse selection sets: each operation returns its full object.
package lineartest

import (
	"encoding/json"
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

// Options configures New; zero fields take the documented defaults.
type Options struct {
	Team     string // team key, default "ENG"
	Key      string // API key the Authorization header must equal, default "lin_api_test"
	PageSize int    // cap on every `first:`, default 50
}

const tsFmt = "2006-01-02T15:04:05.000Z"

type obj = map[string]any

type user struct{ id, name string }

type state struct {
	id, name, typ string
	pos           int
}

type label struct{ id, name, team string } // team "" is a workspace label

type attachment struct {
	id, url  string
	metadata obj
}

type comment struct {
	id, body string
	created  time.Time
	author   user
}

type issue struct {
	id, ident, title, desc string
	num                    int
	state                  *state
	labels                 []string // label ids
	assignee               string   // user id
	completed, canceled    string
	atts                   []*attachment
	comments               []*comment
}

// Server is the fake. Its embedded httptest.Server exposes URL.
type Server struct {
	*httptest.Server
	t  testing.TB
	o  Options
	mu sync.Mutex

	states  []*state
	labels  []*label
	users   map[string]user
	issues  []*issue
	seq     int
	nextNum int
	clock   time.Time
	reqs    []string
	last    map[string]sent
	nextAt  time.Time // SetNextCommentTime
	nextN   int
	limitN  int
	limitSt int
	failN   int
	failSt  int
	errs    []queued
}

var me = user{"user-me", "Me"}

// New starts a fake and registers its cleanup with t.
func New(t testing.TB, o Options) *Server {
	if o.Team == "" {
		o.Team = "ENG"
	}
	if o.Key == "" {
		o.Key = "lin_api_test"
	}
	if o.PageSize == 0 {
		o.PageSize = 50
	}
	s := &Server{t: t, o: o, users: map[string]user{me.id: me}, last: map[string]sent{}, clock: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	for i, st := range [][2]string{{"Triage", "triage"}, {"Backlog", "backlog"}, {"Todo", "unstarted"}, {"In Progress", "started"},
		{"Done", "completed"}, {"Canceled", "canceled"}, {"Duplicate", "canceled"}} {
		s.states = append(s.states, &state{"state-" + strconv.Itoa(i), st[0], st[1], i})
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

func (s *Server) teamID() string { return "team-" + strings.ToLower(s.o.Team) }

// RateLimitNext makes the next n requests answer status (400 or 429) with a
// RATELIMITED error; a 429 also carries Retry-After: 0.
func (s *Server) RateLimitNext(n, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limitN, s.limitSt = n, status
}

// FailNext makes the next n requests answer status (a 5xx) before they reach
// any handler, so nothing they ask for happens.
func (s *Server) FailNext(n, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failN, s.failSt = n, status
}

type queued struct{ op, code, msg string }

// ErrorOn makes the next request for op (any op when "") answer HTTP 200 with
// one GraphQL error, before it changes anything. Calls queue: each error
// answers one request.
func (s *Server) ErrorOn(op, code, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errs = append(s.errs, queued{op, code, message})
}

// queuedError takes the first queued error for op, if any.
func (s *Server) queuedError(op string) (queued, bool) {
	for i, q := range s.errs {
		if q.op == "" || q.op == op {
			s.errs = slices.Delete(s.errs, i, i+1)
			return q, true
		}
	}
	return queued{}, false
}

// SetNextCommentTime gives the next n comments createdAt t, to test tie-breaks.
func (s *Server) SetNextCommentTime(t time.Time, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextAt, s.nextN = t, n
}

type sent struct {
	doc  string
	vars obj
}

// Last is the document and variables of the latest request for op, the
// document with its whitespace collapsed to single spaces, so a test can
// assert the query shape the fake does not parse; "" if op was never sent.
func (s *Server) Last(op string) (string, map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.last[op]
	return strings.Join(strings.Fields(l.doc), " "), l.vars
}

// Requests is the operationName of every request, in order.
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.reqs)
}

// AddLabel seeds a label; team "" is a workspace label, else a team key. It
// returns the label id.
func (s *Server) AddLabel(name, team string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := &label{s.uid("lbl"), name, team}
	s.labels = append(s.labels, l)
	return l.id
}

// AddHumanComment adds a comment by a named person and returns its id.
func (s *Server) AddHumanComment(key, body, author string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.find(key)
	if i == nil {
		s.t.Fatalf("lineartest: no issue %s", key)
	}
	u := user{"user-" + author, author}
	s.users[u.id] = u
	return s.addComment(i, body, u).id
}

// read runs f on the issue under the lock; a missing issue gives zero values.
func (s *Server) read(key string, f func(*issue)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.find(key); i != nil {
		f(i)
	}
}

func (s *Server) StateType(key string) (v string) {
	s.read(key, func(i *issue) { v = i.state.typ })
	return
}

func (s *Server) Description(key string) (v string) {
	s.read(key, func(i *issue) { v = i.desc })
	return
}

func (s *Server) Assignee(key string) (v string) {
	s.read(key, func(i *issue) { v = s.users[i.assignee].name })
	return
}

// Labels are the label names on the issue, in the order they were added.
func (s *Server) Labels(key string) (v []string) {
	s.read(key, func(i *issue) {
		for _, id := range i.labels {
			v = append(v, s.label(id).name)
		}
	})
	return
}

// CommentBodies are the visible comment bodies, oldest first.
func (s *Server) CommentBodies(key string) (v []string) {
	s.read(key, func(i *issue) {
		for _, c := range sortedComments(i) {
			v = append(v, c.body)
		}
	})
	return
}

// Attachments maps each attachment URL to its metadata["body"].
func (s *Server) Attachments(key string) map[string]string {
	v := map[string]string{}
	s.read(key, func(i *issue) {
		for _, a := range i.atts {
			b, _ := a.metadata["body"].(string)
			v[a.url] = b
		}
	})
	return v
}

// ---- helpers (call with s.mu held) ---------------------------------------------

func (s *Server) uid(p string) string {
	s.seq++
	return fmt.Sprintf("%s%05d-0000-4000-8000-000000000000", p, s.seq)
}

func (s *Server) tick() time.Time {
	s.clock = s.clock.Add(time.Second)
	return s.clock
}

func (s *Server) find(id string) *issue {
	for _, i := range s.issues {
		if i.id == id || i.ident == id {
			return i
		}
	}
	return nil
}

func (s *Server) label(id string) *label {
	for _, l := range s.labels {
		if l.id == id {
			return l
		}
	}
	return nil
}

// usable is whether the label can go on this team's issues.
func (s *Server) usable(id string) bool {
	l := s.label(id)
	return l != nil && (l.team == "" || strings.EqualFold(l.team, s.o.Team))
}

func (s *Server) stateBy(id string) *state {
	for _, st := range s.states {
		if st.id == id {
			return st
		}
	}
	return nil
}

func (s *Server) addComment(i *issue, body string, u user) *comment {
	t := s.tick()
	if s.nextN > 0 {
		t, s.nextN = s.nextAt, s.nextN-1
	}
	c := &comment{s.uid("cmt"), body, t, u}
	i.comments = append(i.comments, c)
	return c
}

// sortedComments is oldest first; equal times come back highest id first, so
// an adapter that trusts the server's order for ties is caught.
func sortedComments(i *issue) []*comment {
	cs := slices.Clone(i.comments)
	sort.SliceStable(cs, func(a, b int) bool {
		if !cs[a].created.Equal(cs[b].created) {
			return cs[a].created.Before(cs[b].created)
		}
		return cs[a].id > cs[b].id
	})
	return cs
}

// page is the [lo,hi) window of an n-item list at cursor, and its pageInfo.
func (s *Server) page(n int, cursor any) (int, int, obj) {
	lo, _ := strconv.Atoi(str(cursor)) // nil or empty: the first page
	hi := min(lo+s.o.PageSize, n)
	return lo, hi, obj{"hasNextPage": hi < n, "endCursor": strconv.Itoa(hi)}
}

func userJSON(u user) any {
	if u.id == "" {
		return nil
	}
	return obj{"id": u.id, "name": strings.ToLower(u.name), "displayName": u.name}
}

func (s *Server) issueJSON(i *issue) obj {
	lbs := []obj{}
	for _, id := range i.labels {
		lbs = append(lbs, obj{"name": s.label(id).name})
	}
	lo, hi, info := s.page(len(i.atts), nil)
	return obj{"id": i.id, "identifier": i.ident, "title": i.title, "description": i.desc,
		"url": "https://linear.app/acme/issue/" + i.ident, "completedAt": orNil(i.completed), "canceledAt": orNil(i.canceled),
		"state": obj{"type": i.state.typ}, "labels": obj{"nodes": lbs}, "assignee": userJSON(s.users[i.assignee]),
		"creator": userJSON(me), "attachments": obj{"nodes": attJSON(i.atts[lo:hi]), "pageInfo": info}}
}

func attJSON(as []*attachment) []obj {
	out := []obj{}
	for _, a := range as {
		out = append(out, obj{"id": a.id, "url": a.url, "metadata": a.metadata})
	}
	return out
}

func orNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ---- transport ---------------------------------------------------------------

type fault struct {
	status               int
	data                 any
	code, typ, msg, user string
}

func (f *fault) body() obj {
	ext := obj{"code": f.code, "type": f.typ}
	if f.user != "" {
		ext["userPresentableMessage"] = f.user
	}
	b := obj{"errors": []obj{{"message": f.msg, "extensions": ext}}}
	if f.status == http.StatusOK {
		b["data"] = f.data
	}
	return b
}

func invalid(format string, a ...any) *fault {
	return &fault{status: 200, code: "INVALID_INPUT", typ: "invalid input", msg: fmt.Sprintf(format, a...)}
}

func missing(entity string, data any) *fault {
	return &fault{status: 200, data: data, code: "INVALID_INPUT", typ: "invalid input",
		msg: "Entity not found: " + entity, user: "Could not find referenced " + entity + "."}
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// kinds says whether each operation is a query or a mutation.
var kinds = map[string]string{"Viewer": "query", "Team": "query", "Labels": "query", "LabelCreate": "mutation", "Issue": "query",
	"Attachments": "query", "Comments": "query", "Issues": "query", "IssueCreate": "mutation", "IssueUpdate": "mutation",
	"IssueDelete": "mutation", "CommentCreate": "mutation", "CommentUpdate": "mutation", "CommentDelete": "mutation",
	"CommentURL": "query", "AttachmentCreate": "mutation", "AttachmentDelete": "mutation"}

// schemaNames are the operation, field, argument, type and comparator names
// of the slice of Linear's published schema (packages/sdk/src/schema.graphql
// in github.com/linear/linear) the adapter uses, each checked there. A query
// naming anything else is refused, as Linear refuses an unknown field.
var schemaNames = map[string]bool{}

func init() {
	for _, n := range strings.Fields(`query mutation String Boolean IssueFilter
		IssueCreateInput IssueUpdateInput IssueLabelCreateInput CommentCreateInput CommentUpdateInput AttachmentCreateInput
		viewer teams issueLabels issue issues comment
		issueCreate issueUpdate issueDelete issueLabelCreate commentCreate commentUpdate commentDelete attachmentCreate attachmentDelete
		id name displayName key states type position team identifier title description url completedAt canceledAt
		state labels assignee creator attachments metadata comments body createdAt user
		success issueLabel nodes pageInfo hasNextPage endCursor
		filter first after input eq`) {
		schemaNames[n] = true
	}
	for op := range kinds {
		schemaNames[op] = true
	}
}

// reName is a GraphQL name; a $variable is skipped by its leading $.
var reName = regexp.MustCompile(`\$?[A-Za-z_][A-Za-z0-9_]*`)

// unknownNames is the first name in q outside schemaNames, or "".
func unknownNames(q string) string {
	for _, n := range reName.FindAllString(q, -1) {
		if !strings.HasPrefix(n, "$") && !schemaNames[n] {
			return n
		}
	}
	return ""
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Method != http.MethodPost || r.URL.Path != "/graphql" {
		http.NotFound(w, r)
		return
	}
	raw, _ := io.ReadAll(r.Body)
	var req struct {
		Op    string `json:"operationName"`
		Query string `json:"query"`
		Vars  obj    `json:"variables"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		reply(w, 400, (&fault{status: 400, code: "GRAPHQL_PARSE_FAILED", typ: "graphql", msg: "bad JSON: " + err.Error()}).body())
		return
	}
	s.reqs = append(s.reqs, req.Op)
	s.last[req.Op] = sent{req.Query, req.Vars}
	switch {
	case s.failN > 0:
		s.failN--
		reply(w, s.failSt, obj{"errors": []obj{{"message": "upstream failure"}}})
		return
	case s.limitN > 0:
		s.limitN--
		if s.limitSt == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "0")
		}
		reply(w, s.limitSt, (&fault{status: s.limitSt, code: "RATELIMITED", typ: "ratelimited", msg: "Rate limit exceeded"}).body())
		return
	case r.Header.Get("Authorization") != s.o.Key:
		reply(w, 400, (&fault{status: 400, code: "AUTHENTICATION_ERROR", typ: "authentication error", msg: "Authentication required, not authenticated"}).body())
		return
	}
	if q, ok := s.queuedError(req.Op); ok {
		reply(w, 200, (&fault{status: 200, code: q.code, msg: q.msg}).body())
		return
	}
	kind, known := kinds[req.Op]
	if !known || !strings.HasPrefix(req.Query, kind+" "+req.Op) {
		reply(w, 400, (&fault{status: 400, code: "GRAPHQL_VALIDATION_FAILED", typ: "graphql", msg: fmt.Sprintf("operation %q is not a %s the fake serves", req.Op, kind)}).body())
		return
	}
	if bad := unknownNames(req.Query); bad != "" {
		reply(w, 400, (&fault{status: 400, code: "GRAPHQL_VALIDATION_FAILED", typ: "graphql", msg: fmt.Sprintf("Cannot query field %q: not in the fake's slice of Linear's schema", bad)}).body())
		return
	}
	data, f := s.dispatch(req.Op, req.Vars)
	if f != nil {
		reply(w, f.status, f.body())
		return
	}
	reply(w, 200, obj{"data": data})
}

// ---- operations ----------------------------------------------------------------

func str(v any) string { s, _ := v.(string); return s }

func (s *Server) dispatch(op string, v obj) (any, *fault) {
	in, _ := v["input"].(obj)
	switch op {
	case "Viewer":
		return obj{"viewer": userJSON(me)}, nil
	case "Team":
		nodes := []obj{}
		if strings.EqualFold(str(v["key"]), s.o.Team) {
			sts := []obj{}
			for _, st := range s.states {
				sts = append(sts, obj{"id": st.id, "type": st.typ, "position": st.pos})
			}
			nodes = append(nodes, obj{"id": s.teamID(), "key": s.o.Team, "states": obj{"nodes": sts}})
		}
		return obj{"teams": obj{"nodes": nodes}}, nil
	case "Labels":
		lo, hi, info := s.page(len(s.labels), v["after"])
		nodes := []obj{}
		for _, l := range s.labels[lo:hi] {
			var team any
			if l.team != "" {
				team = obj{"id": "team-" + strings.ToLower(l.team)}
			}
			nodes = append(nodes, obj{"id": l.id, "name": l.name, "team": team})
		}
		return obj{"issueLabels": obj{"nodes": nodes, "pageInfo": info}}, nil
	case "LabelCreate":
		name := str(in["name"])
		if str(in["teamId"]) != s.teamID() || name == "" {
			return nil, invalid("IssueLabelCreateInput needs this team's teamId and a name")
		}
		for _, l := range s.labels {
			if strings.EqualFold(l.name, name) && strings.EqualFold(l.team, s.o.Team) {
				return nil, invalid("Duplicate label name %q", name)
			}
		}
		l := &label{s.uid("lbl"), name, s.o.Team}
		s.labels = append(s.labels, l)
		return obj{"issueLabelCreate": obj{"success": true, "issueLabel": obj{"id": l.id, "name": l.name, "team": obj{"id": s.teamID()}}}}, nil
	case "Issue":
		i := s.find(str(v["id"]))
		if i == nil {
			return nil, missing("Issue", obj{"issue": nil})
		}
		return obj{"issue": s.issueJSON(i)}, nil
	case "Attachments":
		i := s.find(str(v["id"]))
		if i == nil {
			return nil, missing("Issue", obj{"issue": nil})
		}
		lo, hi, info := s.page(len(i.atts), v["after"])
		return obj{"issue": obj{"attachments": obj{"nodes": attJSON(i.atts[lo:hi]), "pageInfo": info}}}, nil
	case "Comments":
		i := s.find(str(v["id"]))
		if i == nil {
			return nil, missing("Issue", obj{"issue": nil})
		}
		cs := sortedComments(i)
		lo, hi, info := s.page(len(cs), v["after"])
		nodes := []obj{}
		for _, c := range cs[lo:hi] {
			nodes = append(nodes, obj{"id": c.id, "body": c.body, "createdAt": c.created.Format(tsFmt), "user": userJSON(c.author)})
		}
		return obj{"issue": obj{"comments": obj{"nodes": nodes, "pageInfo": info}}}, nil
	case "Issues":
		var got []*issue // s.issues is in number order
		for _, i := range s.issues {
			ok, err := s.match(v["filter"], i)
			if err != nil {
				return nil, invalid("%v", err)
			}
			if ok {
				got = append(got, i)
			}
		}
		lo, hi, info := s.page(len(got), v["after"])
		nodes := []obj{}
		for _, i := range got[lo:hi] {
			nodes = append(nodes, s.issueJSON(i))
		}
		return obj{"issues": obj{"nodes": nodes, "pageInfo": info}}, nil
	case "IssueCreate":
		return s.issueCreate(in)
	case "IssueUpdate":
		return s.issueUpdate(str(v["id"]), in)
	case "IssueDelete":
		i := s.find(str(v["id"]))
		if i == nil {
			return nil, missing("Issue", nil)
		}
		s.issues = slices.DeleteFunc(s.issues, func(x *issue) bool { return x == i })
		return obj{"issueDelete": obj{"success": true}}, nil
	case "CommentCreate":
		i := s.find(str(in["issueId"]))
		if i == nil {
			return nil, missing("Issue", nil)
		}
		c := s.addComment(i, str(in["body"]), me)
		return obj{"commentCreate": obj{"success": true, "comment": obj{"id": c.id}}}, nil
	case "CommentUpdate", "CommentDelete", "CommentURL":
		return s.commentOp(op, str(v["id"]), in)
	case "AttachmentCreate":
		return s.attachmentCreate(in)
	default: // AttachmentDelete
		for _, i := range s.issues {
			for n, a := range i.atts {
				if a.id == str(v["id"]) {
					i.atts = slices.Delete(i.atts, n, n+1)
					return obj{"attachmentDelete": obj{"success": true}}, nil
				}
			}
		}
		return nil, missing("Attachment", nil)
	}
}

func (s *Server) issueCreate(in obj) (any, *fault) {
	for k := range in {
		if !slices.Contains([]string{"teamId", "title", "description", "labelIds"}, k) {
			return nil, invalid("unknown IssueCreateInput field %s", k)
		}
	}
	if str(in["teamId"]) != s.teamID() || str(in["title"]) == "" {
		return nil, invalid("IssueCreateInput needs this team's teamId and a title")
	}
	ids, f := s.labelList(in["labelIds"])
	if f != nil {
		return nil, f
	}
	s.nextNum++
	n := s.nextNum
	todo := s.states[2]
	i := &issue{id: s.uid("iss"), num: n, ident: fmt.Sprintf("%s-%d", s.o.Team, n), title: str(in["title"]), desc: str(in["description"]), state: todo, labels: ids}
	s.issues = append(s.issues, i)
	s.tick()
	return obj{"issueCreate": obj{"success": true, "issue": obj{"id": i.id, "identifier": i.ident}}}, nil
}

// labelList validates a list of label ids usable by the team.
func (s *Server) labelList(v any) ([]string, *fault) {
	var out []string
	l, _ := v.([]any)
	for _, x := range l {
		if !s.usable(str(x)) {
			return nil, invalid("Invalid label id %v", x)
		}
		out = append(out, str(x))
	}
	return out, nil
}

func (s *Server) issueUpdate(id string, in obj) (any, *fault) {
	i := s.find(id)
	if i == nil {
		return nil, missing("Issue", nil)
	}
	for k := range in {
		if !slices.Contains([]string{"title", "description", "labelIds", "addedLabelIds", "removedLabelIds", "stateId", "assigneeId"}, k) {
			return nil, invalid("unknown IssueUpdateInput field %s", k)
		}
	}
	var set, add, drop []string
	var f *fault
	if _, ok := in["labelIds"]; ok {
		if set, f = s.labelList(in["labelIds"]); f != nil {
			return nil, f
		}
	}
	if add, f = s.labelList(in["addedLabelIds"]); f != nil {
		return nil, f
	}
	if drop, f = s.labelList(in["removedLabelIds"]); f != nil {
		return nil, f
	}
	st := i.state
	if _, ok := in["stateId"]; ok {
		if st = s.stateBy(str(in["stateId"])); st == nil {
			return nil, invalid("Invalid state id %v", in["stateId"])
		}
	}
	assignee := i.assignee
	if _, ok := in["assigneeId"]; ok {
		assignee = str(in["assigneeId"])
		if _, known := s.users[assignee]; !known && assignee != "" {
			return nil, invalid("Invalid assignee id %v", in["assigneeId"])
		}
	}
	if _, ok := in["title"]; ok {
		i.title = str(in["title"])
	}
	if _, ok := in["description"]; ok {
		i.desc = str(in["description"])
	}
	if _, ok := in["labelIds"]; ok {
		i.labels = set
	}
	for _, l := range add {
		if !slices.Contains(i.labels, l) {
			i.labels = append(i.labels, l)
		}
	}
	i.labels = slices.DeleteFunc(i.labels, func(l string) bool { return slices.Contains(drop, l) })
	i.assignee = assignee
	if st != i.state {
		i.state, i.completed, i.canceled = st, "", ""
		switch st.typ {
		case "completed":
			i.completed = s.tick().Format(tsFmt)
		case "canceled":
			i.canceled = s.tick().Format(tsFmt)
		}
	}
	return obj{"issueUpdate": obj{"success": true}}, nil
}

func (s *Server) commentOp(op, id string, in obj) (any, *fault) {
	for _, i := range s.issues {
		for n, c := range i.comments {
			if c.id != id {
				continue
			}
			switch op {
			case "CommentUpdate":
				c.body = str(in["body"])
				return obj{"commentUpdate": obj{"success": true}}, nil
			case "CommentDelete":
				i.comments = slices.Delete(i.comments, n, n+1)
				return obj{"commentDelete": obj{"success": true}}, nil
			}
			return obj{"comment": obj{"url": "https://linear.app/acme/issue/" + i.ident + "#comment-" + id[:8]}}, nil
		}
	}
	if op == "CommentURL" {
		return nil, missing("Comment", obj{"comment": nil})
	}
	return nil, missing("Comment", nil)
}

func (s *Server) attachmentCreate(in obj) (any, *fault) {
	i := s.find(str(in["issueId"]))
	if i == nil {
		return nil, missing("Issue", nil)
	}
	md, ok := in["metadata"].(obj)
	if !ok || str(in["url"]) == "" {
		return nil, invalid("AttachmentCreateInput needs a url and a metadata object")
	}
	for _, a := range i.atts {
		if a.url == str(in["url"]) { // same issue and url: replaced in place
			a.metadata = md
			return obj{"attachmentCreate": obj{"success": true, "attachment": obj{"id": a.id}}}, nil
		}
	}
	a := &attachment{s.uid("att"), str(in["url"]), md}
	i.atts = append(i.atts, a)
	return obj{"attachmentCreate": obj{"success": true, "attachment": obj{"id": a.id}}}, nil
}

// ---- issue filter ------------------------------------------------------------------

// single is the one key and value of a filter node; the adapter builds only
// single-key nodes, so anything else is drift.
func single(v any) (string, any, error) {
	m, ok := v.(obj)
	if !ok || len(m) != 1 {
		return "", nil, fmt.Errorf("filter node %v must be an object with one key", v)
	}
	for k, x := range m {
		return k, x, nil
	}
	return "", nil, nil
}

// chain walks single-key nodes named path and returns the value at the end.
func chain(v any, path ...string) (any, error) {
	for _, p := range path {
		k, x, err := single(v)
		if err != nil {
			return nil, err
		}
		if k != p {
			return nil, fmt.Errorf("unknown filter key %s (want %s)", k, p)
		}
		v = x
	}
	return v, nil
}

// match applies exactly the IssueFilter shape (*Linear).filter builds.
func (s *Server) match(f any, i *issue) (bool, error) {
	m, _ := f.(obj)
	ok := true
	for k, v := range m {
		switch k {
		case "team":
			x, err := chain(v, "key", "eq")
			if err != nil {
				return false, err
			}
			ok = ok && strings.EqualFold(str(x), s.o.Team)
		case "state":
			x, err := chain(v, "type")
			if err != nil {
				return false, err
			}
			op, list, err := single(x)
			if err != nil || (op != "in" && op != "nin") {
				return false, fmt.Errorf("unknown state filter %v", x)
			}
			has := false
			l, _ := list.([]any)
			for _, t := range l {
				has = has || str(t) == i.state.typ
			}
			ok = ok && has == (op == "in")
		case "assignee":
			x, err := chain(v, "isMe", "eq")
			if err != nil || x != true {
				return false, fmt.Errorf("unknown assignee filter %v", v)
			}
			ok = ok && i.assignee == me.id
		case "and":
			list, _ := v.([]any)
			for _, a := range list {
				x, err := chain(a, "labels", "some", "name", "eqIgnoreCase")
				if err != nil {
					return false, err
				}
				has := false
				for _, id := range i.labels {
					has = has || strings.EqualFold(s.label(id).name, str(x))
				}
				ok = ok && has
			}
		default:
			return false, fmt.Errorf("unknown filter key %s", k)
		}
	}
	return ok, nil
}
