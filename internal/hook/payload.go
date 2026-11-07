package hook

import (
	"encoding/json"
	"fmt"
)

// Payload is a hook or statusline JSON input, decoded once. Every verb parses
// its stdin into one and passes it down, so no decider decodes the bytes again
// and cwd has one rule.
type Payload struct {
	// Raw is the input exactly as read.
	Raw []byte
	// Fields is the decoded top-level object, for the fields beyond the two
	// below (tool_name, context_window, ...).
	Fields map[string]any
	// SessionID is the payload's session_id, "" when absent.
	SessionID string
	// Cwd is the directory the payload names: the cwd key, else
	// workspace.current_dir. "" when it names none; callers pick their own
	// fallback.
	Cwd string
}

// ParsePayload decodes input, which must be a JSON object.
func ParsePayload(input []byte) (Payload, error) {
	var m map[string]any
	if json.Unmarshal(input, &m) != nil || m == nil {
		return Payload{}, fmt.Errorf("input is not a JSON object")
	}
	p := Payload{Raw: input, Fields: m}
	p.SessionID, _ = m["session_id"].(string)
	if p.Cwd, _ = m["cwd"].(string); p.Cwd == "" {
		p.Cwd = p.String("workspace", "current_dir")
	}
	return p, nil
}

// String is the string at the nested key path, "" when any step is missing or
// not the expected type.
func (p Payload) String(path ...string) string {
	var cur any = p.Fields
	for _, k := range path {
		m, _ := cur.(map[string]any)
		cur = m[k]
	}
	s, _ := cur.(string)
	return s
}

// CwdOr is Cwd, else def.
func (p Payload) CwdOr(def string) string {
	if p.Cwd != "" {
		return p.Cwd
	}
	return def
}
