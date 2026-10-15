package jiratest

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// call sends an authenticated request and decodes the JSON reply.
func call(t *testing.T, s *Server, method, path, body string) (int, http.Header, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, s.URL+s.prefix()+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", s.auth())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, resp.Header, out
}

func want(t *testing.T, got, code int) {
	t.Helper()
	if got != code {
		t.Fatalf("status = %d, want %d", got, code)
	}
}

func mk(t *testing.T, s *Server, fields string) string {
	t.Helper()
	code, _, out := call(t, s, "POST", "/issue", `{"fields":{"project":{"key":"PROJ"},"issuetype":{"name":"Task"},`+fields+`}}`)
	want(t, code, 201)
	return out["key"].(string)
}

func TestAuthAndPrefix(t *testing.T) {
	for _, v := range []int{2, 3} {
		s := New(t, Options{Version: v})
		req, _ := http.NewRequest("GET", s.URL+s.prefix()+"/myself", nil)
		req.Header.Set("Authorization", "Basic nope")
		resp, _ := http.DefaultClient.Do(req)
		want(t, resp.StatusCode, 401)
		code, _, me := call(t, s, "GET", "/myself", "")
		want(t, code, 200)
		if me["emailAddress"] != "me@example.com" {
			t.Fatalf("v%d myself = %v", v, me)
		}
		resp, _ = http.DefaultClient.Do(func() *http.Request {
			r, _ := http.NewRequest("GET", s.URL+"/rest/api/9/myself", nil)
			r.Header.Set("Authorization", s.auth())
			return r
		}())
		want(t, resp.StatusCode, 404)
	}
}

func TestSearchPagingAndJQL(t *testing.T) {
	s := New(t, Options{PageSize: 2})
	for i := 0; i < 5; i++ {
		f := `"summary":"s"`
		if i%2 == 0 {
			f += `,"labels":["odd"]`
		}
		mk(t, s, f)
	}
	var keys []string
	token, pages := "", 0
	for {
		body := `{"jql":"project = \"PROJ\" AND statusCategory != Done ORDER BY key ASC","fields":["summary"],"maxResults":10`
		if token != "" {
			body += `,"nextPageToken":"` + token + `"`
		}
		code, _, out := call(t, s, "POST", "/search/jql", body+`}`)
		want(t, code, 200)
		pages++
		for _, is := range out["issues"].([]any) {
			keys = append(keys, is.(map[string]any)["key"].(string))
		}
		if out["isLast"] == true {
			if _, ok := out["nextPageToken"]; ok {
				t.Fatal("last page carries a token")
			}
			break
		}
		token = out["nextPageToken"].(string)
	}
	if pages != 3 || strings.Join(keys, ",") != "PROJ-1,PROJ-2,PROJ-3,PROJ-4,PROJ-5" {
		t.Fatalf("pages=%d keys=%v", pages, keys)
	}
	code, _, out := call(t, s, "POST", "/search/jql", `{"jql":"labels = \"odd\" AND key in (\"PROJ-1\", \"PROJ-2\")"}`)
	want(t, code, 200)
	if n := len(out["issues"].([]any)); n != 1 {
		t.Fatalf("filtered issues = %d", n)
	}
	code, _, out = call(t, s, "POST", "/search/jql", `{"jql":"summary ~ foo"}`)
	want(t, code, 400)
	if !strings.Contains(out["errorMessages"].([]any)[0].(string), "unsupported JQL") {
		t.Fatalf("got %v", out)
	}
	if code, _, _ = call(t, s, "POST", "/search", `{"jql":"project = PROJ"}`); code != 404 {
		t.Fatalf("v2 search on v3 = %d", code)
	}
}

func TestV2SearchStartAt(t *testing.T) {
	s := New(t, Options{Version: 2, PageSize: 2})
	for i := 0; i < 3; i++ {
		mk(t, s, `"summary":"s"`)
	}
	code, _, out := call(t, s, "POST", "/search", `{"jql":"project = PROJ","startAt":2}`)
	want(t, code, 200)
	if out["total"].(float64) != 3 || len(out["issues"].([]any)) != 1 {
		t.Fatalf("got %v", out)
	}
}

func TestTransitions(t *testing.T) {
	s := New(t, Options{})
	k := mk(t, s, `"summary":"s"`)
	_, _, out := call(t, s, "GET", "/issue/"+k+"/transitions", "")
	if n := len(out["transitions"].([]any)); n != 2 {
		t.Fatalf("transitions = %d", n)
	}
	want(t, func() int {
		c, _, _ := call(t, s, "POST", "/issue/"+k+"/transitions", `{"transition":{"id":"9"}}`)
		return c
	}(), 400)
	c, _, _ := call(t, s, "POST", "/issue/"+k+"/transitions", `{"transition":{"id":"3"}}`)
	want(t, c, 204)
	_, _, out = call(t, s, "GET", "/issue/"+k, "")
	f := out["fields"].(map[string]any)
	if s.StatusOf(k) != "Done" || f["resolutiondate"] == nil || f["resolution"] == nil {
		t.Fatalf("fields = %v", f)
	}
	_, _, out = call(t, s, "POST", "/search/jql", `{"jql":"statusCategory = Done"}`)
	if len(out["issues"].([]any)) != 1 {
		t.Fatalf("done search = %v", out)
	}
}

func TestProperties(t *testing.T) {
	s := New(t, Options{})
	k := mk(t, s, `"summary":"s"`)
	c, _, _ := call(t, s, "PUT", "/issue/"+k+"/properties/rota.x", `{"a":1}`)
	want(t, c, 201)
	c, _, _ = call(t, s, "PUT", "/issue/"+k+"/properties/rota.x", `{"a":2}`)
	want(t, c, 200)
	c, _, _ = call(t, s, "PUT", "/issue/"+k+"/properties/rota.big", `"`+strings.Repeat("x", maxPropSz)+`"`)
	want(t, c, 400)
	_, _, out := call(t, s, "GET", "/issue/"+k+"?properties=*all", "")
	if p := out["properties"].(map[string]any); p["rota.x"].(map[string]any)["a"] != float64(2) {
		t.Fatalf("properties = %v", p)
	}
	// top-level properties on edit
	c, _, _ = call(t, s, "PUT", "/issue/"+k, `{"properties":[{"key":"rota.fields","value":{"b":true}}]}`)
	want(t, c, 204)
	if v, ok := s.Property(k, "rota.fields"); !ok || !strings.Contains(string(v), "true") {
		t.Fatalf("rota.fields = %s, %v", v, ok)
	}
	c, _, _ = call(t, s, "DELETE", "/issue/"+k+"/properties/rota.x", "")
	want(t, c, 204)
	c, _, _ = call(t, s, "GET", "/issue/"+k+"/properties/rota.x", "")
	want(t, c, 404)
}

func TestCommentsAndDocs(t *testing.T) {
	s := New(t, Options{PageSize: 2})
	k := mk(t, s, `"summary":"s"`)
	doc := `{"type":"doc","version":1,"content":[]}`
	c, _, _ := call(t, s, "POST", "/issue/"+k+"/comment", `{"body":"plain"}`)
	want(t, c, 400)
	for i := 0; i < 3; i++ {
		c, _, _ = call(t, s, "POST", "/issue/"+k+"/comment", `{"body":`+doc+`}`)
		want(t, c, 201)
	}
	id := s.AddHumanComment(k, json.RawMessage(doc), "bob")
	if id != "20004" {
		t.Fatalf("id = %s", id)
	}
	_, _, out := call(t, s, "GET", "/issue/"+k+"/comment?startAt=2&maxResults=50", "")
	if out["total"].(float64) != 4 || len(out["comments"].([]any)) != 2 {
		t.Fatalf("got %v", out)
	}
	c, _, _ = call(t, s, "DELETE", "/issue/"+k+"/comment/20001", "")
	want(t, c, 204)
	if n := len(s.CommentBodies(k)); n != 3 {
		t.Fatalf("bodies = %d", n)
	}
	c, _, _ = call(t, s, "PUT", "/issue/"+k, `{"fields":{"description":"str"}}`)
	want(t, c, 400)
}

func TestRateLimit(t *testing.T) {
	s := New(t, Options{})
	s.RateLimitNext(1, "7")
	c, h, _ := call(t, s, "GET", "/myself", "")
	want(t, c, 429)
	if h.Get("Retry-After") != "7" {
		t.Fatalf("Retry-After = %q", h.Get("Retry-After"))
	}
	c, _, _ = call(t, s, "GET", "/myself", "")
	want(t, c, 200)
	if r := s.Requests(); len(r) != 2 || r[0] != "GET /rest/api/3/myself" {
		t.Fatalf("requests = %v", r)
	}
}

func TestFailNextRefusesBeforeHandling(t *testing.T) {
	s := New(t, Options{})
	s.FailNext(1, 503)
	c, _, _ := call(t, s, "POST", "/version", `{"name":"1.0","project":"PROJ"}`)
	want(t, c, 503)
	c, _, _ = call(t, s, "GET", "/project/PROJ/versions", "")
	want(t, c, 200)
	c, _, _ = call(t, s, "POST", "/version", `{"name":"1.0","project":"PROJ"}`)
	want(t, c, 201) // the failed POST created nothing, so the name is free
}

func TestVersionsAndLabels(t *testing.T) {
	s := New(t, Options{})
	c, _, v := call(t, s, "POST", "/version", `{"name":"1.0","project":"PROJ"}`)
	want(t, c, 201)
	if v["id"] != "30001" {
		t.Fatalf("version = %v", v)
	}
	c, _, _ = call(t, s, "POST", "/version", `{"name":"1.0","projectId":"10000"}`)
	want(t, c, 400)
	k := mk(t, s, `"summary":"s","fixVersions":[{"name":"1.0"}]`)
	_, _, out := call(t, s, "POST", "/search/jql", `{"jql":"fixVersion = \"1.0\""}`)
	if len(out["issues"].([]any)) != 1 {
		t.Fatalf("got %v", out)
	}
	c, _, _ = call(t, s, "PUT", "/issue/"+k, `{"update":{"labels":[{"add":"has space"}]}}`)
	want(t, c, 400)
	c, _, _ = call(t, s, "PUT", "/issue/"+k, `{"update":{"labels":[{"add":"x"}],"fixVersions":[{"set":[]}]}}`)
	want(t, c, 204)
	_, _, out = call(t, s, "GET", "/issue/"+k, "")
	f := out["fields"].(map[string]any)
	if len(f["labels"].([]any)) != 1 || len(f["fixVersions"].([]any)) != 0 {
		t.Fatalf("fields = %v", f)
	}
}
