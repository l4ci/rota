// Package flakeledger stores observed recoveries, separate from the tracked
// known-red exclusion ledger. One line is one command that passed its retry.
package flakeledger

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/rotatree"
)

const File = "flakes.jsonl"

// Run preserves the exit status and output of either observation.
type Run struct {
	Output      string  `json:"output"`
	ExitCode    int     `json:"exitCode"`
	WallSeconds float64 `json:"wallSeconds,omitempty"`
}

// Entry is one observed fail/pass pair on the same tree. PRs contains every
// member of a train, or the gate's single PR; local branches have none.
type Entry struct {
	Check string    `json:"check"`
	Tests []string  `json:"tests"`
	Tree  string    `json:"tree"`
	PRs   []string  `json:"prs"`
	Round int       `json:"round"`
	TS    time.Time `json:"ts"`
	First Run       `json:"first"`
	Retry Run       `json:"retry"`
}

func Path(root string) string { return rotatree.File(root, File) }

// complete is the validity predicate shared by Append and Load: a record
// Load would reject is never written.
func (e Entry) complete() bool {
	return e.Check != "" && len(e.Tests) > 0 && e.Tree != "" && !e.TS.IsZero() && e.First.ExitCode == 1 && e.Retry.ExitCode == 0
}

// Append serializes writers and syncs the complete JSON line before returning.
// An incomplete entry is refused without touching the file.
func Append(root string, e Entry) error {
	if !e.complete() {
		return errors.New("flake ledger: refusing incomplete recovery record")
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return fsio.AppendLine(Path(root), b, 0o644)
}

// Load preserves os.ErrNotExist: a summary must distinguish no observations
// from missing evidence. Malformed lines are errors, never a lower count.
func Load(root string) ([]Entry, error) {
	var out []Entry
	_, err := fsio.ReadLines(Path(root), fsio.Strict, func(line []byte) error {
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			return err
		}
		if !e.complete() {
			return errors.New("incomplete recovery record")
		}
		out = append(out, e)
		return nil
	})
	var le *fsio.LineError
	if errors.As(err, &le) {
		return nil, fmt.Errorf("flake ledger line %d: %w", le.Line, le.Err)
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Ranked counts a command/test pair across all recorded recoveries.
type Ranked struct {
	Check    string    `json:"check"`
	Test     string    `json:"test"`
	Count    int       `json:"count"`
	LastSeen time.Time `json:"lastSeen"`
}

// Rank orders repeat offenders first, breaking ties by command and test name.
func Rank(entries []Entry) []Ranked {
	by := map[[2]string]*Ranked{}
	for _, e := range entries {
		seen := map[string]bool{}
		for _, test := range e.Tests {
			if seen[test] {
				continue
			}
			seen[test] = true
			key := [2]string{e.Check, test}
			r := by[key]
			if r == nil {
				r = &Ranked{Check: e.Check, Test: test}
				by[key] = r
			}
			r.Count++
			if e.TS.After(r.LastSeen) {
				r.LastSeen = e.TS
			}
		}
	}
	out := make([]Ranked, 0, len(by))
	for _, r := range by {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		if a.Check != b.Check {
			return a.Check < b.Check
		}
		return a.Test < b.Test
	})
	return out
}
