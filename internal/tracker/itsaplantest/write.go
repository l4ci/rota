package itsaplantest

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

func (s *Server) newComment(body string) *comment {
	s.nextComment++
	return &comment{id: s.nextComment, body: body}
}

// Labels is the names of the labels on issue key (MKT-1), in label id order.
func (s *Server) Labels(key string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.byKey(key)
	var out []string
	for _, n := range s.sortedLabelIDs(i) {
		out = append(out, s.labels[n-1])
	}
	return out
}

// SetLabels replaces the labels on key, as another writer's PATCH would.
func (s *Server) SetLabels(key string, names ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byKey(key).Labels = names
	for _, n := range names {
		s.labelID(n)
	}
}

// Column is the id of the column key sits in.
func (s *Server) Column(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byKey(key).Column
}

// Description is the description of key.
func (s *Server) Description(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byKey(key).Description
}

// Field is the rota custom field's value on key.
func (s *Server) Field(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byKey(key).Fields
}

// HasField reports whether the project has the rota custom field.
func (s *Server) HasField() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hasField
}

// CommentBodies is the comment bodies on key, oldest first.
func (s *Server) CommentBodies(key string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, c := range s.byKey(key).comments {
		out = append(out, c.body)
	}
	return out
}

// Exists reports whether key is still an issue of the project.
func (s *Server) Exists(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byKey(key) != nil
}

// byKey is the issue named key, nil when there is none; the caller holds mu.
func (s *Server) byKey(key string) *issue {
	n, ok := strings.CutPrefix(key, s.o.Project+"-")
	num, err := strconv.Atoi(n)
	if !ok || err != nil || num < 1 || num > len(s.issues) || s.issues[num-1].gone {
		s.t.Fatalf("itsaplantest: no issue %s", key)
		return &issue{}
	}
	return s.issues[num-1]
}

func (s *Server) sortedLabelIDs(i *issue) []int {
	var ids []int
	for _, l := range i.Labels {
		ids = append(ids, s.labelID(l))
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

// write serves the mutating routes the tracker uses; the caller holds mu.
func (s *Server) write(w http.ResponseWriter, r *http.Request) {
	var in map[string]json.RawMessage
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			reply(w, 400, obj{"error": "bad json"})
			return
		}
	}
	proj := "/projects/" + s.o.Project
	p := r.URL.Path
	switch {
	case r.Method == http.MethodPost && p == proj+"/issues":
		s.createIssue(w, in)
	case r.Method == http.MethodPost && p == proj+"/issues/bulk/labels":
		s.bulkLabels(w, in)
	case r.Method == http.MethodPost && p == proj+"/labels":
		var name string
		_ = json.Unmarshal(in["name"], &name)
		if name == "" {
			reply(w, 400, obj{"error": "Property 'name' is missing"})
			return
		}
		for _, l := range s.labels {
			if strings.EqualFold(l, name) {
				reply(w, 409, obj{"error": "Label already exists"})
				return
			}
		}
		reply(w, 201, obj{"id": s.labelID(name), "name": name})
	case r.Method == http.MethodPost && p == proj+"/custom-fields":
		if s.o.NoFieldRight {
			reply(w, 403, obj{"error": "Forbidden"})
			return
		}
		var name, typ string
		_ = json.Unmarshal(in["name"], &name)
		_ = json.Unmarshal(in["fieldType"], &typ)
		if name == "" || typ == "" {
			reply(w, 400, obj{"error": "Property 'fieldType' is missing"})
			return
		}
		if name != "rota" || s.hasField {
			s.t.Errorf("itsaplantest: unexpected custom field %q (field exists: %v)", name, s.hasField)
		}
		s.hasField = true
		reply(w, 201, obj{"id": fieldID, "name": name, "fieldType": typ, "options": []any{}})
	case strings.HasPrefix(p, "/issues/"):
		s.issueRoute(w, r, strings.TrimPrefix(p, "/issues/"), in)
	case strings.HasPrefix(p, "/comments/"):
		s.commentRoute(w, r, strings.TrimPrefix(p, "/comments/"), in)
	default:
		s.t.Errorf("itsaplantest: unsupported request %s %s", r.Method, p)
		reply(w, 501, obj{"error": "not in the fake's slice of the api"})
	}
}

func (s *Server) createIssue(w http.ResponseWriter, in map[string]json.RawMessage) {
	var i Issue
	var col int
	_ = json.Unmarshal(in["title"], &i.Title)
	_ = json.Unmarshal(in["description"], &i.Description)
	if err := json.Unmarshal(in["columnId"], &col); err != nil || i.Title == "" {
		reply(w, 400, obj{"error": "Property 'columnId' is missing"})
		return
	}
	i.Column = col
	var ids []int
	_ = json.Unmarshal(in["labelIds"], &ids)
	names, ok := s.labelNames(ids)
	if !ok {
		reply(w, 400, obj{"error": "Labels must belong to this project"})
		return
	}
	i.Labels = names
	n := len(s.issues) + 1
	s.issues = append(s.issues, &issue{Issue: i, num: n})
	reply(w, 201, s.issueJSON(s.issues[n-1], false))
}

// labelNames maps label ids to names; ok is false for an id that is no label
// of the project.
func (s *Server) labelNames(ids []int) ([]string, bool) {
	var out []string
	for _, id := range ids {
		if id < 1 || id > len(s.labels) {
			return nil, false
		}
		out = append(out, s.labels[id-1])
	}
	return out, true
}

func (s *Server) bulkLabels(w http.ResponseWriter, in map[string]json.RawMessage) {
	var ids, add []int
	_ = json.Unmarshal(in["ids"], &ids)
	_ = json.Unmarshal(in["add"], &add)
	names, ok := s.labelNames(add)
	if !ok || len(ids) == 0 || len(add) == 0 {
		reply(w, 400, obj{"error": "Labels must belong to this project"})
		return
	}
	for _, id := range ids {
		if id < 1 || id > len(s.issues) {
			reply(w, 404, obj{"error": "Issue not found"})
			return
		}
		is := s.issues[id-1]
		for _, n := range names {
			if !slices.ContainsFunc(is.Labels, func(l string) bool { return strings.EqualFold(l, n) }) {
				is.Labels = append(is.Labels, n)
			}
		}
	}
	reply(w, 200, obj{"updated": len(ids)})
}

// issueRoute serves /issues/:id (PATCH, DELETE) and /issues/:id/{comments,fields/:f}.
func (s *Server) issueRoute(w http.ResponseWriter, r *http.Request, rest string, in map[string]json.RawMessage) {
	idText, tail, _ := strings.Cut(rest, "/")
	id, err := strconv.Atoi(idText)
	if err != nil || id < 1 || id > len(s.issues) || s.issues[id-1].gone {
		reply(w, 404, obj{"error": "Issue not found"})
		return
	}
	is := s.issues[id-1]
	switch {
	case tail == "" && r.Method == http.MethodDelete:
		is.gone = true
		w.WriteHeader(204)
	case tail == "" && r.Method == http.MethodPatch:
		if v, ok := in["title"]; ok {
			_ = json.Unmarshal(v, &is.Title)
		}
		if v, ok := in["description"]; ok {
			_ = json.Unmarshal(v, &is.Description)
		}
		if v, ok := in["columnId"]; ok {
			var c int
			_ = json.Unmarshal(v, &c)
			if !slices.ContainsFunc(s.columns, func(col Column) bool { return col.ID == c }) {
				reply(w, 400, obj{"error": "Column not found"})
				return
			}
			is.Column = c
		}
		if v, ok := in["assigneeUserId"]; ok {
			var u *string
			_ = json.Unmarshal(v, &u)
			is.AssigneeMe = u != nil && *u == meID
		}
		if v, ok := in["labelIds"]; ok {
			var ids []int
			_ = json.Unmarshal(v, &ids)
			names, ok := s.labelNames(ids)
			if !ok {
				reply(w, 400, obj{"error": "Labels must belong to this project"})
				return
			}
			is.Labels = names
		}
		reply(w, 200, s.issueJSON(is, false))
	case tail == "comments" && r.Method == http.MethodPost:
		var body string
		_ = json.Unmarshal(in["body"], &body)
		if body == "" {
			reply(w, 400, obj{"error": "Property 'body' is missing"})
			return
		}
		c := s.newComment(body)
		is.comments = append(is.comments, c)
		reply(w, 201, obj{"id": c.id, "issueId": is.num, "kind": "comment", "body": body, "actorName": "Rota"})
	case strings.HasPrefix(tail, "fields/") && r.Method == http.MethodPut:
		if !s.hasField || tail != "fields/"+strconv.Itoa(fieldID) {
			reply(w, 404, obj{"error": "Custom field not found"})
			return
		}
		var v *string
		_ = json.Unmarshal(in["value"], &v)
		is.Fields = ""
		if v != nil {
			is.Fields = *v
		}
		reply(w, 200, obj{"ok": true})
	default:
		s.t.Errorf("itsaplantest: unsupported request %s /issues/%s", r.Method, rest)
		reply(w, 501, obj{"error": "not in the fake's slice of the api"})
	}
}

func (s *Server) commentRoute(w http.ResponseWriter, r *http.Request, idText string, in map[string]json.RawMessage) {
	id, _ := strconv.Atoi(idText)
	for _, is := range s.issues {
		for i, c := range is.comments {
			if c.id != id {
				continue
			}
			switch r.Method {
			case http.MethodPatch:
				_ = json.Unmarshal(in["body"], &c.body)
				reply(w, 200, obj{"id": c.id, "kind": "comment", "body": c.body})
			case http.MethodDelete:
				is.comments = slices.Delete(is.comments, i, i+1)
				w.WriteHeader(204)
			default:
				reply(w, 405, obj{"error": "method not allowed"})
			}
			return
		}
	}
	reply(w, 404, obj{"error": "Comment not found"})
}

// SetDescription replaces the description of key, as a human's edit in the web
// editor would, leaving the custom fields alone.
func (s *Server) SetDescription(key, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byKey(key).Description = text
}

// Issues is how many issues the project holds, deleted ones excluded.
func (s *Server) Issues() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, i := range s.issues {
		if !i.gone {
			n++
		}
	}
	return n
}
