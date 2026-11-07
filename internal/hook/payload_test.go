package hook

import "testing"

func mustPayload(t *testing.T, b []byte) Payload {
	t.Helper()
	p, err := ParsePayload(b)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParsePayload(t *testing.T) {
	tests := []struct{ name, in, sid, cwd string }{
		{"cwd key", `{"session_id":"s","cwd":"/a","workspace":{"current_dir":"/b"}}`, "s", "/a"},
		{"workspace only", `{"workspace":{"current_dir":"/b"}}`, "", "/b"},
		{"neither", `{"session_id":"s"}`, "s", ""},
		{"wrong types ignored", `{"session_id":1,"cwd":2,"workspace":"x"}`, "", ""},
	}
	for _, tc := range tests {
		p := mustPayload(t, []byte(tc.in))
		if p.SessionID != tc.sid || p.Cwd != tc.cwd {
			t.Errorf("%s: got sid %q cwd %q, want %q %q", tc.name, p.SessionID, p.Cwd, tc.sid, tc.cwd)
		}
	}
	for _, bad := range []string{"", "{", "null", "[]", `"x"`} {
		if _, err := ParsePayload([]byte(bad)); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}
