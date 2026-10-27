// Package flakeledger stores observed recoveries, separate from the tracked
// known-red exclusion ledger. One line is one command that passed its retry.
package flakeledger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/rotatree"
)

const File = "flakes.jsonl"

// Run preserves the exit status and output of either observation.
type Run struct {
	Output   string `json:"output"`
	ExitCode int    `json:"exitCode"`
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

// Append serializes writers and syncs the complete JSON line before returning.
func Append(root string, e Entry) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	p := Path(root)
	return fsio.Locked(p, fsio.LockTimeout, func() error {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := f.Write(append(b, '\n')); err != nil {
			return err
		}
		return f.Sync()
	})
}

// Load preserves os.ErrNotExist: a summary must distinguish no observations
// from missing evidence. Malformed lines are errors, never a lower count.
func Load(root string) ([]Entry, error) {
	b, err := os.ReadFile(Path(root))
	if err != nil {
		return nil, err
	}
	var out []Entry
	for i, line := range bytes.Split(b, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("flake ledger line %d: %w", i+1, err)
		}
		if e.Check == "" || len(e.Tests) == 0 || e.Tree == "" || e.TS.IsZero() || e.First.ExitCode != 1 || e.Retry.ExitCode != 0 {
			return nil, fmt.Errorf("flake ledger line %d: incomplete recovery record", i+1)
		}
		out = append(out, e)
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
