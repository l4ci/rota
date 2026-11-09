// Package gate is the manual-gate registry (B1, #54): the steps no autonomy
// level may pass on its own, the check a gated verb runs before it acts, and
// the audit log of every human approval it was given.
package gate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/pathglob"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/stamp"
)

// Gate names.
const (
	TagPush        = "tag-push"
	ReleasePublish = "release-publish"
	PublicFiling   = "public-filing"
	MergeApproval  = "merge-approval"
	DebugReset     = "debug-reset"
	BugreportFile  = "bugreport-file"
	FreezeClear    = "freeze-clear"
)

// Gate is one registry row. A gate with Verbs is enforced: those verbs refuse
// without --confirm. A gate without is skill judgment, listed so the inventory
// is complete in one place.
type Gate struct {
	Name    string
	Verbs   []string // verb paths, e.g. "release push"
	Skills  []string // skills whose text routes through the gate
	Creates string   // the externally-visible state it creates
}

// Enforced reports whether a verb refuses this gate without --confirm.
func (g Gate) Enforced() bool { return len(g.Verbs) > 0 }

// Registry is every manual gate the skills describe, in contract order.
var Registry = []Gate{
	{TagPush, []string{"release push"}, []string{"rota-release"}, "the release tag (and branch) on the remote"},
	{ReleasePublish, []string{"release publish"}, []string{"rota-release"}, "a GitHub or GitLab release page"},
	{PublicFiling, []string{"tracker suggest-upstream"}, nil, "a public issue on the rota repo"},
	{MergeApproval, []string{"ship merge", "ship pr-merge", "worker gate"}, []string{"rota-ship", "rota-review", "rota-work"}, "a merge into the base branch"},
	{DebugReset, []string{"debug reset", "debug work-fix reset"}, []string{"rota-debug", "rota-work"}, "a fresh failed-fix count for an item the Iron Law halted"},
	{BugreportFile, []string{"bugreport file"}, []string{"rota-bugreport"}, "a public issue or comment on the rota repo"},
	{FreezeClear, nil, []string{"rota-orchestrate"}, "a cleared merge-train freeze (confirm pre-given; audited, not refused)"},
	{"pr-open", nil, []string{"rota-ship"}, "a public PR or MR"},
	{"issue-close", nil, []string{"rota-ship", "rota-release"}, "closed upstream issues"},
	{"decision-write", nil, []string{"rota-decide"}, "a hard boundary in DECISIONS.md"},
}

// AuditFile is the audit log, relative to the project root.
const AuditFile = rotatree.RelPrefix + rotatree.GateAuditFile

// Confirm is the human approval a gated verb was given: --confirm and the
// quoted answer from --confirm-note. Escalation is the id of the C5 escalation
// whose answer approved the step (--approval), recorded in the audit line.
type Confirm struct {
	Given      bool
	Note       string
	Escalation string
}

// ErrBadConfirm is a confirmation flag given without the other.
var ErrBadConfirm = errors.New("--confirm and a non-empty --confirm-note go together")

// Validate rejects --confirm without a note, and a note without --confirm.
func (c Confirm) Validate() error {
	if c.Given != (strings.TrimSpace(c.Note) != "") {
		return ErrBadConfirm
	}
	return nil
}

// Refused is a gate crossed without --confirm. Paths lists the changed files
// that put a merge under approval (merge-approval only).
type Refused struct {
	Gate  string
	Paths []string
}

func (r *Refused) Error() string {
	return fmt.Sprintf("manual gate '%s' is not cleared", r.Gate)
}

// Hint is what the caller does about a refusal.
func (r *Refused) Hint() string {
	return `ask the user, then re-run with --confirm --confirm-note "<their answer>"`
}

// Clear lets a gated action through or refuses it. Malformed confirmation
// pairs return ErrBadConfirm before any audit write. Without --confirm it
// returns *Refused, whatever autonomy.level says. With a valid confirmation,
// it appends the approval, stamped with autonomy (the autonomy.level in
// force), to the audit log before the caller acts. paths travels into the
// refusal for merge-approval.
func Clear(root, autonomy string, now time.Time, name, verb, target string, c Confirm, paths []string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if !c.Given {
		if paths == nil {
			paths = []string{}
		}
		return &Refused{Gate: name, Paths: paths}
	}
	return AppendAudit(root, Record{TS: now, Gate: name, Verb: verb, Target: target, Note: c.Note,
		Autonomy: autonomy, Escalation: c.Escalation})
}

// Autopilot appends an autopilot action to the audit log, one line per assign,
// merge or repair. These are not human approvals: gate is "autopilot" and the
// note says what was done.
func Autopilot(root string, now time.Time, verb, target, note string) error {
	return AppendAudit(root, Record{TS: now, Gate: "autopilot", Verb: verb, Target: target, Note: note})
}

// Record is one line of the audit log. Autonomy and Escalation are omitted
// from the line when empty.
type Record struct {
	TS                       time.Time
	Gate, Verb, Target, Note string
	Autonomy, Escalation     string
}

func (r Record) object() *jsonx.Object {
	o := jsonx.NewObject()
	o.Set("ts", stamp.Format(r.TS))
	o.Set("gate", r.Gate)
	o.Set("verb", r.Verb)
	o.Set("target", r.Target)
	o.Set("note", r.Note)
	if r.Autonomy != "" {
		o.Set("autonomy", r.Autonomy)
	}
	if r.Escalation != "" {
		o.Set("escalation", r.Escalation)
	}
	return o
}

// recordOf reads the fields it knows; one that is missing or not a string is
// empty, and an unparseable ts is the zero time.
func recordOf(o *jsonx.Object) Record {
	ts, _ := stamp.Parse(jsonx.Str(o, "ts"))
	return Record{TS: ts, Gate: jsonx.Str(o, "gate"), Verb: jsonx.Str(o, "verb"), Target: jsonx.Str(o, "target"),
		Note: jsonx.Str(o, "note"), Autonomy: jsonx.Str(o, "autonomy"), Escalation: jsonx.Str(o, "escalation")}
}

// AppendAudit writes r as one line to the audit log under its lock and syncs it.
func AppendAudit(root string, r Record) error {
	line, err := jsonx.MarshalCompact(r.object())
	if err != nil {
		return err
	}
	return fsio.AppendLine(filepath.Join(root, AuditFile), line, 0o644)
}

// ReadAudit returns the audit log's records in order. A missing log is no
// records; a line that is not a JSON object is skipped.
func ReadAudit(root string) ([]Record, error) {
	out, err := readAudit(root, fsio.Lenient)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return out, err
}

// ReadAuditStrict is ReadAudit that also stops, with a *fsio.LineError, at a
// line that is not a JSON object or lacks gate or ts. A missing log returns
// the os.ErrNotExist error, so a caller can tell no log from an empty one.
func ReadAuditStrict(root string) ([]Record, error) {
	return readAudit(root, fsio.Strict)
}

func readAudit(root string, policy fsio.LinePolicy) ([]Record, error) {
	var out []Record
	_, err := fsio.ReadLines(filepath.Join(root, AuditFile), policy, func(line []byte) error {
		v, err := jsonx.Decode(line)
		if err != nil {
			return err
		}
		o, ok := v.(*jsonx.Object)
		if !ok {
			return errors.New("not a JSON object")
		}
		if policy == fsio.Strict {
			for _, k := range []string{"gate", "ts"} {
				if v, _ := o.Get(k); v == nil {
					return errors.New("incomplete approval record")
				}
			}
		}
		out = append(out, recordOf(o))
		return nil
	})
	return out, err
}

// Merge approval modes (ship.mergeApproval).
const (
	MergeNone  = "none"
	MergeAll   = "all"
	MergePaths = "paths"
)

// MergePolicy is ship.mergeApproval and ship.mergeApprovalPaths.
type MergePolicy struct {
	Mode  string
	Paths []string
}

// ErrBadMergeMode is a ship.mergeApproval value other than none, all, paths.
var ErrBadMergeMode = errors.New("ship.mergeApproval must be none, all or paths")

// NewMergePolicy builds the policy from ship.mergeApproval and
// ship.mergeApprovalPaths. A mode other than none, all or paths is
// ErrBadMergeMode; paths are trimmed and blank ones dropped.
func NewMergePolicy(mode string, paths []string) (MergePolicy, error) {
	var p MergePolicy
	switch mode {
	case MergeNone, MergeAll, MergePaths:
		p.Mode = mode
	default:
		return p, fmt.Errorf("%w (got %v)", ErrBadMergeMode, mode)
	}
	for _, s := range paths {
		if s = strings.TrimSpace(s); s != "" {
			p.Paths = append(p.Paths, s)
		}
	}
	return p, nil
}

// NeedsFiles reports whether Covers reads the changed files, so callers can
// skip listing them otherwise.
func (p MergePolicy) NeedsFiles() bool { return p.Mode == MergePaths }

// Covers reports whether a merge of the changed files needs approval, and
// which files put it there (empty under all).
func (p MergePolicy) Covers(changed []string) (bool, []string) {
	switch p.Mode {
	case MergeAll:
		return true, []string{}
	case MergePaths:
		hit := []string{}
		for _, f := range changed {
			for _, e := range p.Paths {
				if pathglob.MatchEntry(e, f) {
					hit = append(hit, f)
					break
				}
			}
		}
		return len(hit) > 0, hit
	}
	return false, nil
}
