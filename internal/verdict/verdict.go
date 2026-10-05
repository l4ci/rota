// Package verdict is the typed verdict store behind review, qa, ship and
// debug (B2, #55). Skills record each verdict through a verb instead of
// parsing a report's last line; the routing from verdict to next step lives
// here as plain functions. The schema is docs/design/contract/,
// "B2: verdicts".
package verdict

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
)

// Kinds.
const (
	ReviewSpec    = "review-spec"
	ReviewQuality = "review-quality"
	SecondOpinion = "second-opinion"
	QA            = "qa"
	DebugFix      = "debug-fix"
	// DebugReset is the human reset of an item's failed-fix count (B3). It is
	// written by `debug reset` only: no verb takes it as --kind.
	DebugReset = "debug-reset"
)

// Verdicts, in worst-of order.
const (
	Pass      = "PASS"
	Concerns  = "CONCERNS"
	Fail      = "FAIL"
	InfraFail = "INFRA-FAIL"
	// Reset is the verdict of a debug-reset record.
	Reset = "RESET"
)

// BranchKinds are the kinds recorded against a branch, in the order
// `verdict show` lists them.
var BranchKinds = []string{ReviewSpec, ReviewQuality, SecondOpinion, QA}

var kindVerdicts = map[string][]string{
	ReviewSpec:    {Pass, Concerns, Fail},
	ReviewQuality: {Pass, Concerns, Fail},
	SecondOpinion: {Pass, Concerns, Fail},
	QA:            {Pass, Concerns, Fail, InfraFail},
	DebugFix:      {Pass, Fail},
}

var rank = map[string]int{Pass: 0, Concerns: 1, Fail: 2, InfraFail: 3}

var severities = []string{"blocker", "major", "minor", "info"}

// IronLaw is the number of failed fixes that halts a debug session.
const IronLaw = 3

// keep is how many records each branch or item list holds.
const keep = 20

// KnownKind reports whether kind is a branch kind or debug-fix.
func KnownKind(kind string) bool { _, ok := kindVerdicts[kind]; return ok }

// Takes reports whether kind accepts verdict v, and lists what it accepts.
func Takes(kind, v string) (bool, []string) {
	allowed := kindVerdicts[kind]
	return contains(allowed, v), allowed
}

// Worst is the worse of two verdicts.
func Worst(a, b string) string {
	if rank[b] > rank[a] {
		return b
	}
	return a
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Finding is one surfaced problem.
type Finding struct {
	Severity string `json:"severity"`
	Title    string `json:"title"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// ItemVerdict is a per-item verdict inside a review.
type ItemVerdict struct {
	ID      string `json:"id"`
	Verdict string `json:"verdict"`
}

// Body is the optional --body-file payload. Verdict, when the body
// carries one, must equal --verdict; the record always takes --verdict.
type Body struct {
	Verdict  string        `json:"-"`
	Summary  string        `json:"summary,omitempty"`
	Findings []Finding     `json:"findings"`
	Items    []ItemVerdict `json:"items,omitempty"`
}

// rawBody mirrors Body with pointers, so validation can tell a missing
// field from a zero one.
type rawBody struct {
	Verdict  *string `json:"verdict"`
	Summary  *string `json:"summary"`
	Findings []struct {
		Severity *string `json:"severity"`
		Title    *string `json:"title"`
		File     *string `json:"file"`
		Line     *int    `json:"line"`
		Detail   *string `json:"detail"`
	} `json:"findings"`
	Items []struct {
		ID      *string `json:"id"`
		Verdict *string `json:"verdict"`
	} `json:"items"`
}

func usage(format string, a ...any) error {
	return exitcode.Errf(exitcode.ExitUsage, format, a...)
}

// ParseBody validates a body strictly: one JSON object, no unknown keys,
// right types, non-empty titles and IDs, known severities and verdicts.
// Every failure is exit 2 and names the field.
func ParseBody(text string) (Body, error) {
	b := Body{Findings: []Finding{}}
	if !strings.HasPrefix(strings.TrimSpace(text), "{") {
		return b, usage("invalid verdict body: not a JSON object")
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.DisallowUnknownFields()
	var raw rawBody
	if err := dec.Decode(&raw); err != nil {
		return b, usage("invalid verdict body: %s", bodyErr(err))
	}
	if _, err := dec.Token(); err != io.EOF {
		return b, usage("invalid verdict body: text after the JSON object")
	}
	if raw.Verdict != nil {
		if !contains([]string{Pass, Concerns, Fail, InfraFail}, *raw.Verdict) {
			return b, usage("invalid verdict body: verdict must be PASS, CONCERNS, FAIL or INFRA-FAIL")
		}
		b.Verdict = *raw.Verdict
	}
	if raw.Summary != nil {
		b.Summary = strings.TrimSpace(*raw.Summary)
	}
	for i, f := range raw.Findings {
		at := fmt.Sprintf("findings[%d]", i)
		if f.Severity == nil || !contains(severities, *f.Severity) {
			return b, usage("invalid verdict body: %s.severity must be one of %s", at, strings.Join(severities, ", "))
		}
		if f.Title == nil || strings.TrimSpace(*f.Title) == "" {
			return b, usage("invalid verdict body: %s.title is required", at)
		}
		out := Finding{Severity: *f.Severity, Title: strings.TrimSpace(*f.Title)}
		if f.File != nil {
			out.File = *f.File
		}
		if f.Line != nil {
			if *f.Line < 1 {
				return b, usage("invalid verdict body: %s.line must be 1 or more", at)
			}
			out.Line = *f.Line
		}
		if f.Detail != nil {
			out.Detail = *f.Detail
		}
		b.Findings = append(b.Findings, out)
	}
	for i, it := range raw.Items {
		at := fmt.Sprintf("items[%d]", i)
		if it.ID == nil || strings.TrimSpace(*it.ID) == "" {
			return b, usage("invalid verdict body: %s.id is required", at)
		}
		if it.Verdict == nil || !contains([]string{Pass, Concerns, Fail}, *it.Verdict) {
			return b, usage("invalid verdict body: %s.verdict must be PASS, CONCERNS or FAIL", at)
		}
		b.Items = append(b.Items, ItemVerdict{ID: strings.TrimSpace(*it.ID), Verdict: *it.Verdict})
	}
	return b, nil
}

// bodyErr turns a decoder error into a message naming the field.
func bodyErr(err error) string {
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) {
		return fmt.Sprintf("%s must be %s, got %s", te.Field, jsonType(te.Type.Kind().String()), te.Value)
	}
	msg := err.Error()
	if f, ok := strings.CutPrefix(msg, "json: unknown field "); ok {
		return "unknown field " + f
	}
	return msg
}

func jsonType(kind string) string {
	switch kind {
	case "string", "ptr":
		return "a string"
	case "int":
		return "a whole number"
	case "slice":
		return "a list"
	case "struct":
		return "an object"
	}
	return kind
}

// Record is one stored verdict.
type Record struct {
	Kind       string        `json:"kind"`
	Verdict    string        `json:"verdict"`
	Combined   string        `json:"combined,omitempty"`
	Branch     string        `json:"branch,omitempty"`
	Repo       string        `json:"repo,omitempty"`
	Sha        string        `json:"sha"`
	RecordedAt string        `json:"recordedAt"`
	Summary    string        `json:"summary,omitempty"`
	Findings   []Finding     `json:"findings"`
	Items      []ItemVerdict `json:"items,omitempty"`
}

// NewRecord stamps a record with the current UTC time.
func NewRecord(kind, v, sha string, body Body) Record {
	findings := body.Findings
	if findings == nil {
		findings = []Finding{}
	}
	return Record{
		Kind: kind, Verdict: v, Sha: sha,
		RecordedAt: time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		Summary:    body.Summary, Findings: findings, Items: body.Items,
	}
}

// Object is the record as an ordered JSON object, for a verb's data.
func (r Record) Object() *jsonx.Object {
	o, _ := toJSONX(r).(*jsonx.Object)
	return o
}

// Store is .rota/verdicts.json.
type Store struct {
	Branches map[string][]Record `json:"branches"`
	Items    map[string][]Record `json:"items"`
}

// Path is the store file under root.
func Path(root string) string { return filepath.Join(root, ".rota", "verdicts.json") }

// BranchKey keys a branch, qualified by its sub-repo in umbrella mode.
func BranchKey(repo, branch string) string {
	if repo == "" {
		return branch
	}
	return repo + ":" + branch
}

// Load reads the store; a missing or corrupt file is an empty store.
func Load(root string) Store {
	s := Store{Branches: map[string][]Record{}, Items: map[string][]Record{}}
	raw, _ := jsonx.MarshalCompact(fsio.LoadJSON(Path(root), nil))
	if err := json.Unmarshal(raw, &s); err != nil {
		return Store{Branches: map[string][]Record{}, Items: map[string][]Record{}}
	}
	if s.Branches == nil {
		s.Branches = map[string][]Record{}
	}
	if s.Items == nil {
		s.Items = map[string][]Record{}
	}
	return s
}

// update is a locked read-modify-write of the store.
func update(root string, mutate func(*Store)) error {
	return fsio.Locked(Path(root), fsio.LockTimeout, func() error {
		s := Load(root)
		mutate(&s)
		return fsio.WriteJSONAtomic(Path(root), toJSONX(s))
	})
}

// toJSONX round-trips a struct into jsonx values, so the file is written
// in struct key order with the conventions' formatting.
func toJSONX(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err) // only plain structs of strings, ints and slices reach here
	}
	out, err := jsonx.Decode(raw)
	if err != nil {
		panic(err)
	}
	return out
}

func appendCapped(list []Record, r Record) []Record {
	list = append(list, r)
	if len(list) > keep {
		list = list[len(list)-keep:]
	}
	return list
}

// AddBranch appends r to a branch's list. A review-quality record gets its
// combined verdict first: the worst of r and the branch's previous record
// when that is a review-spec at the same sha, else r's own verdict.
func AddBranch(root, key string, r Record) (Record, error) {
	err := update(root, func(s *Store) {
		list := s.Branches[key]
		if r.Kind == ReviewQuality {
			r.Combined = r.Verdict
			if n := len(list); n > 0 && list[n-1].Kind == ReviewSpec && list[n-1].Sha == r.Sha {
				r.Combined = Worst(list[n-1].Verdict, r.Verdict)
			}
		}
		s.Branches[key] = appendCapped(list, r)
	})
	return r, err
}

// AddItem appends a debug record to an item's list and returns the item's
// failed-fix count after it.
func AddItem(root, id string, r Record) (failed int, err error) {
	err = update(root, func(s *Store) {
		s.Items[id] = appendCapped(s.Items[id], r)
		failed = FailedFixes(s.Items[id])
	})
	return
}

// FailedFixes counts an item's failed debug-fix records after its latest
// debug-reset record.
func FailedFixes(list []Record) int {
	n := 0
	for _, r := range list {
		switch {
		case r.Kind == DebugReset:
			n = 0
		case r.Kind == DebugFix && r.Verdict == Fail:
			n++
		}
	}
	return n
}

// ResetItem appends the reset record r to an item's list when the item has
// failed fixes to clear, and returns that count. With none it appends
// nothing and returns 0.
func ResetItem(root, id string, r Record) (cleared int, err error) {
	err = update(root, func(s *Store) {
		if cleared = FailedFixes(s.Items[id]); cleared > 0 {
			s.Items[id] = appendCapped(s.Items[id], r)
		}
	})
	return
}

// Latest is the newest record of kind in list.
func Latest(list []Record, kind string) (Record, bool) {
	for i := len(list) - 1; i >= 0; i-- {
		if list[i].Kind == kind {
			return list[i], true
		}
	}
	return Record{}, false
}

// EffectiveReview is a branch's review verdict: the latest review-quality
// record's combined verdict, unless a review-spec record is newer (a
// spec-only review, or a spec FAIL that skipped Stage 2). The record
// returned has Verdict set to the effective verdict.
func EffectiveReview(list []Record) (Record, bool) {
	for i := len(list) - 1; i >= 0; i-- {
		switch r := list[i]; r.Kind {
		case ReviewSpec:
			return r, true
		case ReviewQuality:
			if r.Combined != "" {
				r.Verdict = r.Combined
			}
			return r, true
		}
	}
	return Record{}, false
}

// Blocking is the record that blocks a ship (B3): the effective review
// verdict when it is FAIL, else the latest second-opinion record when it is
// FAIL and the runner is not the advisory codex fallback. A stale record
// still blocks; only a newer record of the same kind clears it. CONCERNS and
// QA never block.
func Blocking(list []Record, s Settings) (Record, bool) {
	if r, ok := EffectiveReview(list); ok && r.Verdict == Fail {
		return r, true
	}
	if r, ok := Latest(list, SecondOpinion); ok && r.Verdict == Fail && s.Runner != "codex" {
		return r, true
	}
	return Record{}, false
}
