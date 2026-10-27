// Package landfreeze owns the repository-wide landing freeze. Its state is
// shared by all worktrees, survives a failed gate, and is never source-controlled.
// Writers hold the worker landing lock; readers see an atomic snapshot.
package landfreeze

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/rotastate"
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
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read landing freeze %s: %w", path, err)
	}
	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("invalid landing freeze %s: %w", path, err)
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

func Save(path string, r *Record) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return fsio.WriteFileAtomic(path, append(b, '\n'))
}

func issueID(s string) string { return strings.TrimPrefix(strings.TrimSpace(s), "#") }
func prID(s string) string {
	s = strings.TrimSpace(s)
	if ref, ok := tracker.ParsePRURL(s); ok {
		return strconv.Itoa(ref.Number)
	}
	n, err := strconv.Atoi(strings.TrimLeft(s, "#!"))
	if err != nil || n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
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

// Allows grants an exception only to the explicitly designated repair.
func (r *Record) Allows(issue, pr string) bool {
	return r == nil || !r.Active || (r.RepairIssue != "" && issueID(issue) == issueID(r.RepairIssue)) || (r.RepairPR != "" && samePR(pr, r.RepairPR))
}

// Line is safe to include in a single-line prompt digest, including when a
// caller constructs a record without reading the validated on-disk format.
func (r *Record) Line() string {
	repair := "none designated"
	var fields []string
	if r.RepairIssue != "" {
		fields = append(fields, "issue="+display(r.RepairIssue, 100))
	}
	if r.RepairPR != "" {
		fields = append(fields, "PR="+display(r.RepairPR, 160))
	}
	if len(fields) > 0 {
		repair = strings.Join(fields, " ")
	}
	return fmt.Sprintf("FROZEN %s at %s: %s; repair: %s (rota round freeze)", display(r.Base, 160), display(r.SHA, 64), display(strings.Join(r.Checks, ", "), 320), repair)
}

func validSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) })
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
	if issue != "" && (hasControl(issue) || (!positiveID(id) && !backlog.ValidID(id, backlog.FileIDDigits))) {
		return fmt.Errorf("--repair-issue requires a positive issue number or backlog item ID")
	}
	if pr != "" {
		if hasControl(pr) {
			return fmt.Errorf("--repair-pr requires a positive PR number or PR URL")
		}
		s := strings.TrimSpace(pr)
		ref, url := tracker.ParsePRURL(s)
		if (!url || ref.Number <= 0) && !positiveID(strings.TrimPrefix(strings.TrimPrefix(s, "#"), "!")) {
			return fmt.Errorf("--repair-pr requires a positive PR number or PR URL")
		}
	}
	return nil
}

// display strips control and formatting characters and bounds untrusted text.
func display(s string, limit int) string {
	out := make([]rune, 0, limit)
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		if len(out) == limit {
			out[limit-1] = '…'
			break
		}
		out = append(out, r)
	}
	return string(out)
}

// ClearHint is the audited escape hatch for an unreadable freeze record.
const ClearHint = "rota round freeze --clear --reason '<why it is safe to clear>'"

func UnreadableLine(err error) string {
	return "landing freeze unreadable: " + display(err.Error(), 600) + "; recover with " + ClearHint
}
