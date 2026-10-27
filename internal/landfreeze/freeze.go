// Package landfreeze owns the repository-wide landing freeze. Its state is
// shared by all worktrees, survives a failed gate, and is never source-controlled.
// Writers hold the worker landing lock; readers see an atomic snapshot.
package landfreeze

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/tracker"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/rotastate"
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
		return nil, err
	}
	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("invalid landing freeze: %w", err)
	}
	if r.Base == "" || r.SHA == "" {
		return nil, fmt.Errorf("invalid landing freeze: missing base or SHA")
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

func (r *Record) Line() string {
	return fmt.Sprintf("FROZEN %s at %s: %s; repair issue=%s PR=%s (rota round freeze)", r.Base, r.SHA, strings.Join(r.Checks, ", "), r.RepairIssue, r.RepairPR)
}
