package ledger

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/stamp"
)

// Unavailable is what a row says when a session fact could not be read. It is
// never a 0: a missing transcript is unknown, not free.
const Unavailable = "unavailable"

// Tokens are the token totals of one session.
type Tokens = harness.Tokens

// Session is what a worker's harness reported about its session: the id and
// transcript path come from the harness's own hook payload, never from a scan
// of the harness's session directories.
type Session struct {
	Slot, ID, Transcript string
	UpdatedAt            time.Time
}

var slotNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func sessionFile(commonDir, slot string) (string, error) {
	if !slotNameRe.MatchString(slot) || strings.Contains(slot, "..") {
		return "", fmt.Errorf("unusable slot name %q", slot)
	}
	return filepath.Join(rotastate.Dir(commonDir), "slot-session", slot+".json"), nil
}

// RecordSession stores the session a slot's harness reported, replacing the
// slot's earlier record. It is written by the Claude statusline and the Codex
// prompt hook, which both run inside the worker's session.
func RecordSession(commonDir string, s Session) error {
	p, err := sessionFile(commonDir, s.Slot)
	if err != nil {
		return err
	}
	if s.ID == "" {
		return errors.New("session has no id")
	}
	b, err := json.Marshal(map[string]string{"id": s.ID, "transcript": s.Transcript, "updatedAt": stamp.Format(s.UpdatedAt)})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o777); err != nil {
		return err
	}
	return fsio.WriteFileAtomic(p, b)
}

// LoadSession reads the slot's reported session; ok is false when none.
func LoadSession(commonDir, slot string) (Session, bool) {
	p, err := sessionFile(commonDir, slot)
	if err != nil {
		return Session{}, false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return Session{}, false
	}
	var m map[string]string
	if json.Unmarshal(b, &m) != nil || m["id"] == "" {
		return Session{}, false
	}
	ts, _ := stamp.Parse(m["updatedAt"])
	return Session{Slot: slot, ID: m["id"], Transcript: m["transcript"], UpdatedAt: ts}, true
}

func tokensObject(t Tokens) *jsonx.Object {
	o := jsonx.NewObject()
	o.Set("input", int(t.Input))
	o.Set("output", int(t.Output))
	o.Set("cacheRead", int(t.CacheRead))
	o.Set("cacheWrite", int(t.CacheWrite))
	return o
}

// DetailTokens reads the entry's token totals; ok is false for a row without
// them or one that says unavailable.
func (e Entry) DetailTokens() (Tokens, bool) {
	if e.Detail == nil {
		return Tokens{}, false
	}
	v, _ := e.Detail.Get("tokens")
	o, ok := v.(*jsonx.Object)
	if !ok {
		return Tokens{}, false
	}
	n := func(k string) int64 { x, _ := jsonx.Int(mustGet(o, k)); return int64(x) }
	return Tokens{Input: n("input"), Output: n("output"), CacheRead: n("cacheRead"), CacheWrite: n("cacheWrite")}, true
}

func mustGet(o *jsonx.Object, k string) any { v, _ := o.Get(k); return v }

// SessionDetail is the session part of a done or park row for slot: session
// id, transcript path, subagent transcripts and token totals. A session the
// harness reported before since belongs to an earlier assignment and counts as
// none. Whatever cannot be read says "unavailable". The transcript is copied
// into copyDir only when it is non-empty (the opt-in config key); the path is
// recorded either way.
func SessionDetail(commonDir, slot, kind string, since time.Time, copyDir string) *jsonx.Object {
	d := jsonx.NewObject()
	s, ok := LoadSession(commonDir, slot)
	if !ok || s.UpdatedAt.Before(since) {
		d.Set("session", Unavailable)
		d.Set("transcript", Unavailable)
		d.Set("tokens", Unavailable)
		return d
	}
	d.Set("session", s.ID)
	d.Set("transcript", cmpOr(s.Transcript, Unavailable))
	tok, subs, err := harness.TranscriptUsage(kind, s.Transcript)
	if err != nil {
		d.Set("tokens", Unavailable)
	} else {
		d.Set("tokens", tokensObject(tok))
	}
	if len(subs) > 0 {
		l := make([]any, len(subs))
		for i, p := range subs {
			l[i] = p
		}
		d.Set("subagents", l)
	}
	if copyDir != "" && s.Transcript != "" {
		if cp, err := copyTranscript(s, copyDir); err == nil {
			d.Set("transcriptCopy", cp)
		}
	}
	return d
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func copyTranscript(s Session, dir string) (string, error) {
	if !slotNameRe.MatchString(s.ID) {
		return "", errors.New("unusable session id")
	}
	b, err := os.ReadFile(s.Transcript)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, s.ID+".jsonl")
	return dst, fsio.WriteFileAtomic(dst, b)
}
