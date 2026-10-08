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

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/overlap"
	"github.com/l4ci/rota/internal/rotatree"
)

// Gate names.
const (
	TagPush        = "tag-push"
	ReleasePublish = "release-publish"
	PublicFiling   = "public-filing"
	MergeApproval  = "merge-approval"
	DebugReset     = "debug-reset"
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
	{DebugReset, []string{"debug reset"}, []string{"rota-debug"}, "a fresh failed-fix count for an item the Iron Law halted"},
	{"pr-open", nil, []string{"rota-ship"}, "a public PR or MR"},
	{"issue-close", nil, []string{"rota-ship", "rota-release"}, "closed upstream issues"},
	{"decision-write", nil, []string{"rota-decide"}, "a hard boundary in DECISIONS.md"},
}

// AuditFile is the audit log, relative to the project root.
const AuditFile = ".rota/gate-audit.jsonl"

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

// now is the audit clock; tests replace it.
var now = time.Now

// Clear lets a gated action through or refuses it. Without --confirm it
// returns *Refused, whatever autonomy.level says. With it, it appends the
// approval to the audit log before the caller acts. paths travels into the
// refusal for merge-approval.
func Clear(root, name, verb, target string, c Confirm, paths []string) error {
	if !c.Given {
		if paths == nil {
			paths = []string{}
		}
		return &Refused{Gate: name, Paths: paths}
	}
	autonomy := "off"
	if v, err := config.Value(config.Load(rotatree.Config(root)), "autonomy.level"); err == nil {
		if s, ok := v.(string); ok {
			autonomy = s
		}
	}
	rec := jsonx.NewObject()
	rec.Set("ts", now().UTC().Format(time.RFC3339))
	rec.Set("gate", name)
	rec.Set("verb", verb)
	rec.Set("target", target)
	rec.Set("note", c.Note)
	rec.Set("autonomy", autonomy)
	if c.Escalation != "" {
		rec.Set("escalation", c.Escalation)
	}
	return appendAudit(root, rec)
}

// Autopilot appends an autopilot action to the audit log, one line per assign,
// merge or repair. These are not human approvals: gate is "autopilot" and the
// note says what was done.
func Autopilot(root, verb, target, note string) error {
	rec := jsonx.NewObject()
	rec.Set("ts", now().UTC().Format(time.RFC3339))
	rec.Set("gate", "autopilot")
	rec.Set("verb", verb)
	rec.Set("target", target)
	rec.Set("note", note)
	return appendAudit(root, rec)
}

// ReadAudit returns the audit log's lines in order. A missing log is no lines;
// a line that is not a JSON object is skipped.
func ReadAudit(root string) ([]*jsonx.Object, error) {
	data, err := os.ReadFile(filepath.Join(root, AuditFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*jsonx.Object
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if v, err := jsonx.Decode([]byte(line)); err == nil {
			if o, ok := v.(*jsonx.Object); ok {
				out = append(out, o)
			}
		}
	}
	return out, nil
}

// appendAudit writes one line to the audit log under its lock and syncs it.
func appendAudit(root string, rec *jsonx.Object) error {
	line, err := jsonx.MarshalCompact(rec)
	if err != nil {
		return err
	}
	p := filepath.Join(root, AuditFile)
	return fsio.Locked(p, fsio.LockTimeout, func() error {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			f.Close()
			return err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return err
		}
		return f.Close()
	})
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

// LoadMergePolicy reads the merge policy from the project config.
func LoadMergePolicy(root string) (MergePolicy, error) {
	cfg := config.Load(rotatree.Config(root))
	var p MergePolicy
	v, err := config.Value(cfg, "ship.mergeApproval")
	if err != nil {
		return p, err
	}
	mode, _ := v.(string)
	switch mode {
	case MergeNone, MergeAll, MergePaths:
		p.Mode = mode
	default:
		return p, fmt.Errorf("%w (got %v)", ErrBadMergeMode, v)
	}
	if v, err = config.Value(cfg, "ship.mergeApprovalPaths"); err != nil {
		return p, err
	}
	list, _ := v.([]any)
	for _, e := range list {
		if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
			p.Paths = append(p.Paths, strings.TrimSpace(s))
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
				if overlap.MatchPath(e, f) {
					hit = append(hit, f)
					break
				}
			}
		}
		return len(hit) > 0, hit
	}
	return false, nil
}
