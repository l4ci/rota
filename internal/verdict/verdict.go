// Package verdict is the typed verdict store behind review, qa, ship and
// debug (B2, #55). Skills record each verdict through a verb instead of
// parsing a report's last line; the routing from verdict to next step lives
// here as plain functions. The schema is docs/contributing/contract/,
// "B2: verdicts".
package verdict

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/stamp"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
)

// Kinds.
const (
	ReviewSpec    = "review-spec"
	ReviewQuality = "review-quality"
	// ReviewCross is the cross-harness review of a size:Major or risk:high
	// issue's PR (#1612): one read-only run on a harness other than the
	// author's. Its record names that harness and model.
	ReviewCross = "review-cross"
	// ReviewSecurity, ReviewPerf and ReviewUX are the focused lens reviews
	// (#2064) a branch triggers by the paths it touches or its issue's labels
	// (review.lenses). Each is one read-only reviewer on one axis.
	ReviewSecurity = "review-security"
	ReviewPerf     = "review-perf"
	ReviewUX       = "review-ux"
	SecondOpinion  = "second-opinion"
	QA             = "qa"
	DebugFix       = "debug-fix"
	// Behaviour is the merge gate's run of an item's "Try it" steps on the
	// merged tree (#899): PASS, or FAIL on a non-zero exit or a missing
	// expected line. Its record carries the transcript's path.
	Behaviour = "behaviour"
	// DebugReset is the human reset of an item's failed-fix count (B3). It is
	// written by `debug reset` only: no verb takes it as --kind.
	DebugReset = "debug-reset"
	// Look is the maintainer's approval of how a batch looks, recorded against
	// an integration branch at its tip. A push after it voids it like any
	// sha-bound verdict. `worker refs` runs only behind a PASS at the tip.
	Look = "look"
	// WorkFix is one /rota-work Step 7 fix dispatch (FAIL), or the PASS that
	// ends the run of them. Its count is separate from the debug-fix one, so
	// the cap that hands off to /rota-debug never halts /rota-debug itself.
	WorkFix = "work-fix"
	// WorkFixReset is the human reset of an item's work-fix count. It is
	// written by `debug work-fix reset` only: no verb takes it as --kind.
	WorkFixReset = "work-fix-reset"
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
var BranchKinds = append(slices.Clone(readinessReviewKinds), QA, Behaviour, Look)

// LensKinds are the lens review kinds, one per lens name.
var LensKinds = lensKinds(config.LensNames)

func lensKinds(names []string) []string {
	kinds := make([]string, len(names))
	for i, n := range names {
		kinds[i] = LensKind(n)
	}
	return kinds
}

// LensKind is the verdict kind of the lens called name: review-<name>.
func LensKind(name string) string { return "review-" + name }

var kindVerdicts = map[string][]string{
	ReviewSpec:    {Pass, Concerns, Fail},
	ReviewQuality: {Pass, Concerns, Fail},
	ReviewCross:   {Pass, Concerns, Fail},
	SecondOpinion: {Pass, Concerns, Fail},
	QA:            {Pass, Concerns, Fail, InfraFail},
	DebugFix:      {Pass, Fail},
	Behaviour:     {Pass, Fail},
	Look:          {Pass, Fail},
}

func init() {
	for _, k := range LensKinds {
		kindVerdicts[k] = []string{Pass, Concerns, Fail}
	}
}

var rank = map[string]int{Pass: 0, Concerns: 1, Fail: 2, InfraFail: 3}

var severities = []string{"blocker", "major", "minor", "info"}

// IronLaw is the number of failed fixes that halts a debug session.
const IronLaw = 3

// WorkFixCap is the number of Step 7 fix attempts after which /rota-work
// dispatches no more and hands the item to /rota-debug.
const WorkFixCap = 3

// keep is the history limit, extended for items with older active failures.
const keep = 20

// KnownKind reports whether kind is a branch kind or debug-fix.
func KnownKind(kind string) bool { _, ok := kindVerdicts[kind]; return ok }

// Takes reports whether kind accepts verdict v, and lists what it accepts.
func Takes(kind, v string) (bool, []string) {
	allowed := kindVerdicts[kind]
	return slices.Contains(allowed, v), allowed
}

// Worst is the worse of two verdicts.
func Worst(a, b string) string {
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// Finding is one surfaced problem.
type Finding struct {
	Severity string `json:"severity"`
	Title    string `json:"title"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Detail   string `json:"detail,omitempty"`
	// Fix is the expected behaviour: the acceptance criterion of the item a
	// deferred finding is filed as. Optional.
	Fix string `json:"fix,omitempty"`
	// Patch is an optional unified diff for a small, mechanical fix. The
	// reviewer only attaches it; the orchestrator applies it as its own commit.
	Patch string `json:"patch,omitempty"`
	// Blocking is whether a bounce relays this finding. ParseBody always sets
	// it (explicit value, else from the severity); a record stored before the
	// field existed has none and IsBlocking falls back to the severity.
	Blocking *bool `json:"blocking,omitempty"`
}

// SeverityBlocks is the default of a finding's blocking flag: blocker and
// major findings block, minor and info ones are deferred.
func SeverityBlocks(severity string) bool { return severity == "blocker" || severity == "major" }

// IsBlocking reports whether a bounce relays and counts f.
func (f Finding) IsBlocking() bool {
	if f.Blocking != nil {
		return *f.Blocking
	}
	return SeverityBlocks(f.Severity)
}

// IsDeferred reports whether f is filed as an item instead of relayed: it does
// not block and is more than an info note.
func (f Finding) IsDeferred() bool { return !f.IsBlocking() && f.Severity != "info" }

// HasBlocking reports whether any finding in list blocks.
func HasBlocking(list []Finding) bool {
	for _, f := range list {
		if f.IsBlocking() {
			return true
		}
	}
	return false
}

// Declined is something the reviewer could not judge from the diff. It
// carries no severity and never feeds a verdict or a route.
type Declined struct {
	Title  string `json:"title"`
	File   string `json:"file,omitempty"`
	Line   int    `json:"line,omitempty"`
	Detail string `json:"detail,omitempty"`
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
	Declined []Declined    `json:"declined,omitempty"`
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
		Fix      *string `json:"fix"`
		Patch    *string `json:"patch"`
		Blocking *bool   `json:"blocking"`
	} `json:"findings"`
	Items []struct {
		ID      *string `json:"id"`
		Verdict *string `json:"verdict"`
	} `json:"items"`
	Declined []struct {
		Title  *string `json:"title"`
		File   *string `json:"file"`
		Line   *int    `json:"line"`
		Detail *string `json:"detail"`
	} `json:"declined"`
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
		if !slices.Contains([]string{Pass, Concerns, Fail, InfraFail}, *raw.Verdict) {
			return b, usage("invalid verdict body: verdict must be PASS, CONCERNS, FAIL or INFRA-FAIL")
		}
		b.Verdict = *raw.Verdict
	}
	if raw.Summary != nil {
		b.Summary = strings.TrimSpace(*raw.Summary)
	}
	for i, f := range raw.Findings {
		at := fmt.Sprintf("findings[%d]", i)
		if f.Severity == nil || !slices.Contains(severities, *f.Severity) {
			return b, usage("invalid verdict body: %s.severity must be one of %s", at, strings.Join(severities, ", "))
		}
		if f.Title == nil || strings.TrimSpace(*f.Title) == "" {
			return b, usage("invalid verdict body: %s.title is required", at)
		}
		blocking := SeverityBlocks(*f.Severity)
		if f.Blocking != nil {
			blocking = *f.Blocking
		}
		out := Finding{Severity: *f.Severity, Title: strings.TrimSpace(*f.Title), Blocking: &blocking}
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
		if f.Fix != nil {
			out.Fix = strings.TrimSpace(*f.Fix)
		}
		if f.Patch != nil {
			out.Patch = *f.Patch
		}
		b.Findings = append(b.Findings, out)
	}
	for i, it := range raw.Items {
		at := fmt.Sprintf("items[%d]", i)
		if it.ID == nil || strings.TrimSpace(*it.ID) == "" {
			return b, usage("invalid verdict body: %s.id is required", at)
		}
		if it.Verdict == nil || !slices.Contains([]string{Pass, Concerns, Fail}, *it.Verdict) {
			return b, usage("invalid verdict body: %s.verdict must be PASS, CONCERNS or FAIL", at)
		}
		b.Items = append(b.Items, ItemVerdict{ID: strings.TrimSpace(*it.ID), Verdict: *it.Verdict})
	}
	for i, d := range raw.Declined {
		at := fmt.Sprintf("declined[%d]", i)
		if d.Title == nil || strings.TrimSpace(*d.Title) == "" {
			return b, usage("invalid verdict body: %s.title is required", at)
		}
		out := Declined{Title: strings.TrimSpace(*d.Title)}
		if d.File != nil {
			out.File = *d.File
		}
		if d.Line != nil {
			if *d.Line < 1 {
				return b, usage("invalid verdict body: %s.line must be 1 or more", at)
			}
			out.Line = *d.Line
		}
		if d.Detail != nil {
			out.Detail = *d.Detail
		}
		b.Declined = append(b.Declined, out)
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
	// FromReport distinguishes cache entries from locally recorded verdicts.
	// A recovered report only applies at its reviewed head.
	FromReport bool          `json:"fromReport,omitempty"`
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
	Declined   []Declined    `json:"declined,omitempty"`
	// Transcript is the path of a behaviour run's transcript.
	Transcript string `json:"transcript,omitempty"`
	// Harness and Model are the reviewer's harness and model, when the
	// recorder named them (a review-cross record always does).
	Harness string `json:"harness,omitempty"`
	Model   string `json:"model,omitempty"`
}

// NewRecord stamps a record with now in UTC.
func NewRecord(kind, v, sha string, body Body, now time.Time) Record {
	findings := body.Findings
	if findings == nil {
		findings = []Finding{}
	}
	return Record{
		Kind: kind, Verdict: v, Sha: sha,
		RecordedAt: stamp.Format(now),
		Summary:    body.Summary, Findings: findings, Items: body.Items, Declined: body.Declined,
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
func Path(root string) string { return rotatree.Verdicts(root) }

// BranchKey keys a branch, qualified by its sub-repo in umbrella mode.
func BranchKey(repo, branch string) string {
	if repo == "" {
		return branch
	}
	return repo + ":" + branch
}

// Branch is the records of branch under the store's key for repo (the sub-repo
// name in an umbrella, else ""): the one place a reader picks the key.
func (s Store) Branch(repo, branch string) []Record {
	return s.Branches[BranchKey(repo, branch)]
}

// Load reads the store for a display reader; a missing or corrupt file is an
// empty store. Writers and merge decisions use LoadStrict so they never
// replace or pass over what they cannot parse.
func Load(root string) Store {
	s, err := loadStrict(root)
	if err != nil {
		return emptyStore()
	}
	return s
}

func emptyStore() Store {
	return Store{Branches: map[string][]Record{}, Items: map[string][]Record{}}
}

// LoadStrict reads the store for a decision, not a display: only a missing
// file is empty, an unreadable or unparseable one is an exit-4 refusal. The
// merge gate and ship checks feed it to Check.StoreErr so a corrupt store
// cannot read as "no failing verdict".
func LoadStrict(root string) (Store, error) { return loadStrict(root) }

// loadStrict reads the store, treating only a missing file as empty. An
// unreadable or unparseable file is an error, so a read-modify-write refuses
// to overwrite it.
func loadStrict(root string) (Store, error) {
	raw, err := os.ReadFile(Path(root))
	if errors.Is(err, os.ErrNotExist) {
		return emptyStore(), nil
	}
	if err != nil {
		return Store{}, storeRefusal(root, "unreadable", err)
	}
	v, err := jsonx.Decode(raw)
	if err != nil {
		return Store{}, storeRefusal(root, "unparseable", err)
	}
	compact, _ := jsonx.MarshalCompact(v)
	s := emptyStore()
	if err := json.Unmarshal(compact, &s); err != nil {
		return Store{}, storeRefusal(root, "unparseable", err)
	}
	if s.Branches == nil {
		s.Branches = map[string][]Record{}
	}
	if s.Items == nil {
		s.Items = map[string][]Record{}
	}
	return s, nil
}

// storeRefusal is the exit-4 refusal a writer returns over a store it cannot
// read, so a corrupt file is the user's to repair, not a rota bug.
func storeRefusal(root, why string, cause error) error {
	e := exitcode.Errf(exitcode.ExitRefused, "verdict store %s is %s, refusing to act on it: %v", Path(root), why, cause)
	e.Cause = cause
	return e.WithHint("repair the file or move it aside; moving it aside drops the recorded verdicts")
}

// update is a locked read-modify-write of the store.
func update(root string, mutate func(*Store)) error {
	return fsio.Locked(Path(root), fsio.LockTimeout, func() error {
		s, err := loadStrict(root)
		if err != nil {
			return err
		}
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

// combine sets r.Combined against the records already stored before it. A
// review-spec record is its own combined verdict (the branch's review verdict
// when the Standards axis was not run, light depth). A review-quality record
// is the worst of its own verdict and the latest review-spec in list recorded
// at the same sha, else its own verdict; records of other kinds between the
// two do not matter. Every writer of Combined goes through here.
func combine(list []Record, r Record) Record {
	switch r.Kind {
	case ReviewSpec:
		r.Combined = r.Verdict
	case ReviewQuality:
		r.Combined = r.Verdict
		if spec, ok := Latest(list, ReviewSpec); ok && AtHead(spec, r.Sha) {
			r.Combined = Worst(spec.Verdict, r.Verdict)
		}
	}
	return r
}

// AddBranch appends r to a branch's list, with its combined verdict set by
// combine.
func AddBranch(root, key string, r Record) (Record, error) {
	err := update(root, func(s *Store) {
		list := s.Branches[key]
		r = combine(list, r)
		s.Branches[key] = appendCapped(list, r)
	})
	return r, err
}

// AddItem appends a debug record to an item's list and returns the item's
// failed-fix count after it.
func AddItem(root, id string, r Record) (failed int, err error) {
	err = update(root, func(s *Store) {
		s.Items[id] = appendItem(s.Items[id], r)
		failed = FailedFixes(s.Items[id])
	})
	return
}

// counter is one failed-attempt rule: failKind FAIL records count, from the
// position after the latest record isReset accepts. debugCounter and
// workCounter are the one place that names the failure and reset kinds;
// FailedFixes, WorkFixAttempts, the reset verbs and appendItem's retention cut
// all read them.
type counter struct {
	failKind string
	isReset  func(Record) bool
}

var (
	debugCounter = counter{DebugFix, func(r Record) bool { return r.Kind == DebugReset }}
	workCounter  = counter{WorkFix, func(r Record) bool {
		return r.Kind == WorkFixReset || r.Kind == WorkFix && r.Verdict == Pass
	}}
)

// failed reports whether r is a failed attempt of this counter.
func (c counter) failed(r Record) bool { return r.Kind == c.failKind && r.Verdict == Fail }

// start is the index in list from which the counter counts: the position
// after its latest reset record.
func (c counter) start(list []Record) int {
	at := 0
	for i, r := range list {
		if c.isReset(r) {
			at = i + 1
		}
	}
	return at
}

// count is the failed attempts since the counter's latest reset.
func (c counter) count(list []Record) int {
	n := 0
	for _, r := range list[c.start(list):] {
		if c.failed(r) {
			n++
		}
	}
	return n
}

// FailedFixes counts an item's failed debug-fix records after its latest
// debug-reset record.
func FailedFixes(list []Record) int { return debugCounter.count(list) }

// WorkFixAttempts counts an item's work-fix attempts since the latest
// work-fix PASS or work-fix-reset record.
func WorkFixAttempts(list []Record) int { return workCounter.count(list) }

// AddWorkFix appends r, a work-fix record, to an item's list and returns the
// item's attempt count after it. A FAIL record is refused (ok false, nothing
// written) when the count already stands at WorkFixCap; the check and the
// write share one lock.
func AddWorkFix(root, id string, r Record) (attempts int, ok bool, err error) {
	err = update(root, func(s *Store) {
		attempts = WorkFixAttempts(s.Items[id])
		if r.Verdict == Fail && attempts >= WorkFixCap {
			return
		}
		s.Items[id] = appendItem(s.Items[id], r)
		attempts = WorkFixAttempts(s.Items[id])
		ok = true
	})
	return
}

// resetCounter appends the reset record r to an item's list when c has
// attempts to clear, and returns that count. With none it appends nothing.
func resetCounter(root, id string, r Record, c counter) (cleared int, err error) {
	err = update(root, func(s *Store) {
		if cleared = c.count(s.Items[id]); cleared > 0 {
			s.Items[id] = appendItem(s.Items[id], r)
		}
	})
	return
}

// ResetWorkFix appends the work-fix-reset record r when the item has attempts
// to clear, and returns that count. With none it appends nothing.
func ResetWorkFix(root, id string, r Record) (int, error) {
	return resetCounter(root, id, r, workCounter)
}

// ResetItem appends the reset record r to an item's list when the item has
// failed fixes to clear, and returns that count. With none it appends
// nothing and returns 0.
func ResetItem(root, id string, r Record) (int, error) {
	return resetCounter(root, id, r, debugCounter)
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

// SameHead reports whether two commit ids name the same commit. Ids may be
// short or full, so either may be the prefix of the other. An empty id never
// matches.
func SameHead(a, b string) bool {
	return a != "" && b != "" && (strings.HasPrefix(a, b) || strings.HasPrefix(b, a))
}

// AtHead reports whether rec was recorded at head. A record with no sha, or an
// unknown head, is never at head.
func AtHead(rec Record, head string) bool {
	return SameHead(rec.Sha, head)
}

// Recorded lists the review kinds (ReviewSpec, ReviewQuality) with a verdict
// recorded for branch at head. A record on another commit is stale and does
// not count, as /rota-review treats it.
func Recorded(list []Record, head string) []string {
	var kinds []string
	for _, r := range CoreReviewsAtHead(list, head) {
		kinds = append(kinds, r.Kind)
	}
	return kinds
}

// CrossAt is the branch's newest review-cross record when it was recorded at
// head; a record on another commit is stale and does not count.
func CrossAt(list []Record, head string) (Record, bool) {
	r, ok := Latest(ForHead(list, head), ReviewCross)
	return r, ok && AtHead(r, head)
}

// LensAt is the branch's newest record of the lens kind when it was recorded at
// head; a record on another commit is stale and does not count.
func LensAt(list []Record, kind, head string) (Record, bool) {
	r, ok := Latest(ForHead(list, head), kind)
	return r, ok && AtHead(r, head)
}

// LensFails are the lens reviews whose newest record is FAIL, in LensKinds
// order: lens reviews that block and are relayed like a review FAIL.
func LensFails(list []Record) []Record {
	var out []Record
	for _, k := range LensKinds {
		if r, ok := Latest(list, k); ok && r.Verdict == Fail {
			out = append(out, r)
		}
	}
	return out
}

// CrossFail is the branch's newest review-cross record when it is FAIL: a
// cross-harness review that blocks and is relayed like a review FAIL.
func CrossFail(list []Record) (Record, bool) {
	r, ok := Latest(list, ReviewCross)
	return r, ok && r.Verdict == Fail
}

// LastReviewSet lists the review kinds recorded at the commit of the branch's
// newest review verdict, whatever head that was: the review set the branch last
// had, which a later push has made stale. It is nil when no review was recorded.
func LastReviewSet(list []Record) []string {
	var newest Record
	for i := len(list) - 1; i >= 0; i-- {
		if k := list[i].Kind; k == ReviewSpec || k == ReviewQuality {
			newest = list[i]
			break
		}
	}
	if newest.Sha == "" {
		return nil
	}
	return Recorded(list, newest.Sha)
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

// ItemFailed reports a per-item FAIL in r, a spec failure carried by items[]
// alone, which the findings do not show.
func (r Record) ItemFailed() bool {
	for _, it := range r.Items {
		if it.Verdict == Fail {
			return true
		}
	}
	return false
}

// Failing reports whether r gives a bounce something to relay: a FAIL
// verdict, a blocking finding or a failed item. It is the bounce rule, wider
// than the narrow FailingRecords on purpose: a CONCERNS review can still carry a blocking
// finding or a failed item, and a bounce relays those, while a relay or ship
// block follows the effective verdict alone.
func (r Record) Failing() bool {
	return r.Verdict == Fail || HasBlocking(r.Findings) || r.ItemFailed()
}

// FailScope selects which records FailingRecords judges.
type FailScope struct {
	// Head, when AtHead is set, limits the set to records at Head. Recovered
	// reports then obey ForHead, the rule the gate applies.
	Head   string
	AtHead bool
	// Wide counts Failing records (a blocking finding or a failed item, even
	// under CONCERNS) as the bounce rule does; narrow counts a FAIL verdict only.
	Wide bool
	// SecondOpinion adds the second-opinion record, whatever runner made it.
	SecondOpinion bool
}

// FailingRecords is the one rule for which records fail a branch, in blocking
// order: the review (spec, quality), cross, then each lens, then second opinion
// when asked. Across heads the review is the effective one (EffectiveReview)
// and a stale record still counts, so only a newer record of the same kind
// clears it. At a head each of spec and quality is its own latest record at that
// head. CONCERNS and QA never fail unless Wide finds a blocking finding. Blocking,
// BlockingAtHead, BlockingAnyHead, the relay and the bounce derive from it.
func FailingRecords(list []Record, s FailScope) []Record {
	var cands []Record
	if s.AtHead {
		kinds := bounceReviewKinds
		if s.SecondOpinion {
			kinds = readinessReviewKinds
		}
		cands = latestAtHead(ForHead(list, s.Head), kinds, s.Head)
	} else {
		if r, ok := EffectiveReview(list); ok {
			cands = append(cands, r)
		}
		kinds := bounceReviewKinds[len(coreReviewKinds):]
		if s.SecondOpinion {
			kinds = readinessReviewKinds[len(coreReviewKinds):]
		}
		cands = append(cands, latestRecords(list, kinds)...)
	}
	return slices.DeleteFunc(cands, func(r Record) bool {
		if s.Wide {
			return !r.Failing()
		}
		return r.Verdict != Fail
	})
}

func firstFailing(list []Record, s FailScope) (Record, bool) {
	if fails := FailingRecords(list, s); len(fails) > 0 {
		return fails[0], true
	}
	return Record{}, false
}

// Blocking is the record that blocks a ship (B3): the first of the effective
// review, cross, lens and second-opinion records whose verdict is FAIL,
// whatever head or runner. CONCERNS and QA never block.
func Blocking(list []Record) (Record, bool) {
	return firstFailing(list, FailScope{SecondOpinion: true})
}

// BlockingAtHead is the first review record (spec, quality, cross, then the
// lenses) recorded at head that is Failing: a FAIL, a blocking finding or a
// failed item. It is the bounce rule, which the merge gate applies so a PR a
// bounce would relay is not merged. Unlike Blocking it ignores a record at
// another commit, and a CONCERNS review can trip it. Ship does not use it.
func BlockingAtHead(list []Record, head string) (Record, bool) {
	return firstFailing(list, FailScope{Head: head, AtHead: true, Wide: true})
}

// BlockingAnyHead is the bounce rule without head scoping, the set a nudged
// merge train keeps out: Blocking, else any review, cross or lens record that
// is Failing. A record at another commit still counts, and a CONCERNS review
// with a blocking finding trips it.
func BlockingAnyHead(list []Record) (Record, bool) {
	if r, ok := Blocking(list); ok {
		return r, true
	}
	return firstFailing(list, FailScope{Wide: true})
}
