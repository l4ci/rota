// Package landfreeze owns the repository-wide landing freeze. Its state is
// shared by all worktrees, survives a failed gate, and is never source-controlled.
// Writers hold the worker landing lock; readers see an atomic snapshot.
package landfreeze

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/tracker"
)

// Record retains the cause and the latest clearance even when inactive.
type Record struct {
	Active      bool     `json:"active"`
	Base        string   `json:"base"`
	SHA         string   `json:"sha"`
	PRs         []string `json:"prs"`
	Checks      []string `json:"checks"`
	CreatedAt   string   `json:"createdAt"`
	RepairIssue string   `json:"repairIssue,omitempty"`
	RepairPR    string   `json:"repairPR,omitempty"`
	ClearedBy   string   `json:"clearedBy,omitempty"`
	ClearedSHA  string   `json:"clearedSHA,omitempty"`
	ClearedAt   string   `json:"clearedAt,omitempty"`
	Reason      string   `json:"reason,omitempty"`
}

// Path resolves through the caller's git runner, including linked worktrees.
func Path(ctx context.Context, run git.Runner, root string) (string, error) {
	if run == nil {
		run = git.Exec
	}
	common, err := rotastate.CommonDirVia(ctx, run, root)
	if err != nil {
		return "", err
	}
	return rotastate.File(common, "freeze.json"), nil
}

func Read(path string) (*Record, error) {
	r, found, err := fsio.ReadJSON[Record](path)
	if errors.Is(err, fsio.ErrUnreadable) {
		return nil, fmt.Errorf("invalid landing freeze: %w", err)
	}
	if err != nil {
		return nil, fmt.Errorf("read landing freeze %s: %w", path, err)
	}
	if !found {
		return nil, nil
	}
	if r.Base == "" || r.SHA == "" {
		return nil, fmt.Errorf("invalid landing freeze %s: missing base or SHA", path)
	}
	if !validSHA(r.SHA) || (r.ClearedSHA != "" && !validSHA(r.ClearedSHA)) {
		return nil, fmt.Errorf("invalid landing freeze %s: SHA must be a full hexadecimal object ID", path)
	}
	if err := ValidateRepair(r.RepairIssue, r.RepairPR); err != nil {
		return nil, fmt.Errorf("invalid landing freeze %s: %w", path, err)
	}
	return &r, nil
}

func Load(ctx context.Context, run git.Runner, root string) (*Record, error) {
	p, err := Path(ctx, run, root)
	if err != nil {
		return nil, err
	}
	return Read(p)
}

// Frozen is the one answer to "may a landing be blocked right now": true when
// any freeze is active, and also when the record cannot be read, since an
// unreadable freeze must not look like a clear one. Outside a repo there is no
// freeze. The git runner resolves the shared state dir, so a fake works.
func Frozen(ctx context.Context, run git.Runner, root string) bool {
	r, err := Load(ctx, run, root)
	if err != nil {
		return !errors.Is(err, rotastate.ErrNotRepo)
	}
	return r.frozen()
}

func Save(path string, r *Record) error {
	return fsio.WriteJSONAtomic(path, r)
}

func issueID(s string) string { return strings.TrimPrefix(strings.TrimSpace(s), "#") }

// prID is the PR number of a ref in the one grammar (tracker.PRNumber), as
// text; "" for anything else.
func prID(s string) string {
	if n, ok := tracker.PRNumber(s); ok {
		return strconv.Itoa(n)
	}
	return ""
}

func samePR(a, b string) bool {
	ar, aURL := tracker.ParsePRURL(a)
	br, bURL := tracker.ParsePRURL(b)
	if aURL && bURL {
		au, _ := url.Parse(a)
		bu, _ := url.Parse(b)
		if ar.Provider != br.Provider || ar.Repo != br.Repo || !strings.EqualFold(au.Host, bu.Host) {
			return false
		}
	}
	id := prID(a)
	return id != "" && id == prID(b)
}

// Binds reports whether the freeze is active and holds the given base.
func (r *Record) Binds(base string) bool {
	return r != nil && r.Active && r.Base == base
}

// frozen reports whether any freeze is active, whatever its base.
func (r *Record) frozen() bool { return r != nil && r.Active }

// allows grants an exception only to the explicitly designated repair.
func (r *Record) allows(issue, pr string) bool {
	return !r.frozen() || (r.RepairIssue != "" && issueID(issue) == issueID(r.RepairIssue)) || (r.RepairPR != "" && samePR(pr, r.RepairPR))
}

// Subject is what a landing or assignment asks to do while a freeze may hold.
type Subject struct {
	// Base is the branch the work lands on. "" means the base is not known yet
	// (round assign resolves it after the claim), so the base is not judged.
	Base string
	// Issue and PR name the work; either may be "".
	Issue, PR string
	// Queue judges the base alone, for a train's queue-wide refusal before any
	// member is resolved. Issue and PR are ignored.
	Queue bool
}

// Decision is the freeze's answer to a Subject. Line and Hint are set when
// Admit is false.
type Decision struct {
	Admit bool
	// Frozen is true when a freeze is active (or unreadable); Admit may still
	// be true for the designated repair. A train skips member checks without it.
	Frozen bool
	// Unreadable marks a refusal caused by an unreadable record.
	Unreadable bool
	Line, Hint string
}

const (
	baseHint   = "repairs must target the frozen base"
	repairHint = "designate a repair with rota round freeze --repair-issue <id> or --repair-pr <pr>, then verify recovery"
)

// Decide is the one admission rule every caller shares: no active freeze
// admits; an unreadable record refuses; a known base other than the frozen one
// refuses; otherwise only the designated repair (by issue or PR) is admitted.
// Assign passes no Base, so it admits by repair designation alone: the item's
// base is resolved only after the claim, and a freeze blocks every unrelated
// assignment whatever branch it targets.
func Decide(ctx context.Context, run git.Runner, root string, s Subject) Decision {
	r, err := Load(ctx, run, root)
	if err != nil {
		return Decision{Frozen: true, Unreadable: true, Line: UnreadableLine(err), Hint: ClearHint}
	}
	return r.decide(s)
}

func (r *Record) decide(s Subject) Decision {
	if !r.frozen() {
		return Decision{Admit: true}
	}
	if s.Base != "" && !r.Binds(s.Base) {
		return Decision{Frozen: true, Line: r.Line(), Hint: baseHint}
	}
	if s.Queue || r.allows(s.Issue, s.PR) {
		return Decision{Admit: true, Frozen: true}
	}
	return Decision{Frozen: true, Line: r.Line(), Hint: repairHint}
}

// Line is safe to include in a single-line prompt digest, including when a
// caller constructs a record without reading the validated on-disk format.
func (r *Record) Line() string {
	repair := "none designated"
	var fields []string
	if r.RepairIssue != "" {
		fields = append(fields, "issue="+strutil.Display(r.RepairIssue, 100))
	}
	if r.RepairPR != "" {
		fields = append(fields, "PR="+strutil.Display(r.RepairPR, 160))
	}
	if len(fields) > 0 {
		repair = strings.Join(fields, " ")
	}
	return fmt.Sprintf("FROZEN %s at %s: %s; repair: %s (rota round freeze)", strutil.Display(r.Base, 160), strutil.Display(r.SHA, 64), strutil.Display(strings.Join(r.Checks, ", "), 320), repair)
}

func validSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func positiveID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	n, err := strconv.Atoi(s)
	return err == nil && n > 0
}

// ValidateRepair accepts tracker numbers or file-backend item IDs for issues,
// and positive numbers or URLs for PRs. Issue whitespace is never normalized.
func ValidateRepair(issue, pr string) error {
	id := strings.TrimPrefix(issue, "#")
	if issue != "" && (strutil.HasControl(issue) || (!positiveID(id) && !backlog.ValidID(id, backlog.FileIDDigits))) {
		return fmt.Errorf("--repair-issue requires a positive issue number or backlog item ID")
	}
	if pr != "" {
		if strutil.HasControl(pr) {
			return fmt.Errorf("--repair-pr requires a positive PR number or PR URL")
		}
		if prID(pr) == "" {
			return fmt.Errorf("--repair-pr requires a positive PR number or PR URL")
		}
	}
	return nil
}

// ClearHint is the audited escape hatch for an unreadable freeze record.
const ClearHint = "rota round freeze --clear --reason '<why it is safe to clear>'"

func UnreadableLine(err error) string {
	return "landing freeze unreadable: " + strutil.Display(err.Error(), 600) + "; recover with " + ClearHint
}
