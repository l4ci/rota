package lineartest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// post sends one GraphQL request and returns the status and the decoded body.
func post(t *testing.T, s *Server, key, op, query string, vars map[string]any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"operationName": op, "query": query, "variables": vars})
	req, _ := http.NewRequest(http.MethodPost, s.URL+"/graphql", bytes.NewReader(b))
	req.Header.Set("Authorization", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, out
}

func firstError(out map[string]any) string {
	errs, _ := out["errors"].([]any)
	if len(errs) == 0 {
		return ""
	}
	e, _ := errs[0].(map[string]any)
	m, _ := e["message"].(string)
	return m
}

// The fake refuses what Linear would: a bad key, an operation of the wrong
// kind, a field outside its slice of the schema, a missing issue.
func TestFakeRefusesWhatLinearWould(t *testing.T) {
	s := New(t, Options{})
	viewer := "query Viewer { viewer { id name displayName } }"
	if code, out := post(t, s, "lin_api_test", "Viewer", viewer, nil); code != 200 || out["data"] == nil {
		t.Fatalf("viewer: %d %v", code, out)
	}
	for _, c := range []struct{ name, key, op, query, want string }{
		{"wrong key", "nope", "Viewer", viewer, "Authentication required"},
		{"mutation sent as a query", "lin_api_test", "IssueCreate", "query IssueCreate { x }", "not a mutation"},
		{"unknown field", "lin_api_test", "Viewer", "query Viewer { viewer { id emailAddress } }", "emailAddress"},
	} {
		if code, out := post(t, s, c.key, c.op, c.query, nil); code != 400 || !strings.Contains(firstError(out), c.want) {
			t.Errorf("%s: %d %v, want 400 naming %q", c.name, code, out, c.want)
		}
	}
	code, out := post(t, s, "lin_api_test", "Issue", "query Issue($id: String!) { issue(id: $id) { id } }", map[string]any{"id": "ENG-9"})
	if code != 200 || !strings.Contains(firstError(out), "Entity not found") {
		t.Errorf("missing issue: %d %v, want 200 with Entity not found", code, out)
	}
}

// attachmentCreate with a URL the issue already has replaces that
// attachment, as Linear documents.
func TestFakeAttachmentCreateUpsertsByURL(t *testing.T) {
	s := New(t, Options{})
	_, out := post(t, s, "lin_api_test", "IssueCreate", "mutation IssueCreate($input: IssueCreateInput!) { issueCreate(input: $input) { success issue { id identifier } } }",
		map[string]any{"input": map[string]any{"teamId": "team-eng", "title": "Widen"}})
	if firstError(out) != "" {
		t.Fatalf("create: %v", out)
	}
	put := "mutation AttachmentCreate($input: AttachmentCreateInput!) { attachmentCreate(input: $input) { success } }"
	for _, body := range []string{"one", "two"} {
		in := map[string]any{"issueId": "ENG-1", "url": "https://rota.invalid/state/fields", "title": "rota state", "metadata": map[string]any{"body": body}}
		if _, out := post(t, s, "lin_api_test", "AttachmentCreate", put, map[string]any{"input": in}); firstError(out) != "" {
			t.Fatalf("attach %s: %v", body, out)
		}
	}
	if got := s.Attachments("ENG-1"); len(got) != 1 || got["https://rota.invalid/state/fields"] != "two" {
		t.Errorf("attachments %v, want the one URL holding the second body", got)
	}
}
