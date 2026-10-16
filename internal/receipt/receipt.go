// Package receipt records one entry per state-writing round verb call, keyed
// by a request id, so a retry returns the first answer instead of applying
// the mutation again. The file is per-developer runtime state, gitignored,
// and pruned when the round winds down.
//
// The receipt is a second guard beside the .lock sidecars: those serialise
// concurrent writers, this one recognises a repeated caller.
package receipt

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/rotatree"
)

// File is the receipts file, relative to the project root.
const File = ".rota/" + rotatree.ReceiptsFile

const lockWait = 10 * time.Second

// Status of a recorded call.
const (
	Done   = "done"   // the verb finished; a retry replays Data and Text
	Failed = "failed" // the verb stopped part way; a retry runs again
)

// Entry is one recorded call.
type Entry struct {
	ID          string          `json:"id"`
	Verb        string          `json:"verb"`
	Fingerprint string          `json:"fingerprint"`
	Status      string          `json:"status"`
	Attempts    int             `json:"attempts"`
	Exit        int             `json:"exit,omitempty"`
	Message     string          `json:"message,omitempty"`
	Text        string          `json:"text,omitempty"`
	Data        json.RawMessage `json:"data,omitempty"`
	FailedStage string          `json:"failedStage,omitempty"`
	Residual    []string        `json:"residualResources,omitempty"`
	At          string          `json:"at"`
}

// Mismatch is the error for an id already used by a different call.
type Mismatch struct {
	ID, Verb string
}

func (m *Mismatch) Error() string {
	return fmt.Sprintf("request id %s was already used for a different call (%s) with other arguments", m.ID, m.Verb)
}

type fileData struct {
	Receipts []Entry `json:"receipts"`
}

func path(root string) string { return filepath.Join(root, filepath.FromSlash(File)) }

func load(p string) (fileData, error) {
	var d fileData
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	if err := json.Unmarshal(b, &d); err != nil {
		return d, fmt.Errorf("%s: %w: %v", File, fsio.ErrUnreadable, err)
	}
	return d, nil
}

// Lookup returns the entry recorded for id. found is false when none is.
// An entry whose verb or fingerprint differs is a *Mismatch.
func Lookup(root, id, verb, fingerprint string) (e Entry, found bool, err error) {
	d, err := load(path(root))
	if err != nil {
		return Entry{}, false, err
	}
	for _, r := range d.Receipts {
		if r.ID != id {
			continue
		}
		if r.Verb != verb || r.Fingerprint != fingerprint {
			return Entry{}, false, &Mismatch{ID: id, Verb: r.Verb}
		}
		return r, true, nil
	}
	return Entry{}, false, nil
}

// Record stores e, replacing an earlier entry with the same id. Attempts
// counts the calls under the id.
func Record(root string, e Entry) error {
	p := path(root)
	e.At = time.Now().UTC().Format(time.RFC3339)
	return fsio.Locked(p, lockWait, func() error {
		d, err := load(p)
		if err != nil {
			return err
		}
		out := d.Receipts[:0:0]
		e.Attempts = 1
		for _, r := range d.Receipts {
			if r.ID == e.ID {
				e.Attempts = r.Attempts + 1
				continue
			}
			out = append(out, r)
		}
		d.Receipts = capPerVerb(append(out, e))
		b, err := json.MarshalIndent(d, "", "  ")
		if err != nil {
			return err
		}
		return fsio.WriteFileAtomic(p, append(b, '\n'))
	})
}

// KeepPerVerb is how many receipts the file keeps for each verb. A retry comes
// right after the call it repeats, so the newest few are the ones that matter;
// older ones go on the next write, which keeps an autopilot's gate ticks from
// growing the file for the whole round.
const KeepPerVerb = 20

func capPerVerb(rs []Entry) []Entry {
	seen := map[string]int{}
	keep := make([]bool, len(rs))
	for i := len(rs) - 1; i >= 0; i-- {
		if seen[rs[i].Verb]++; seen[rs[i].Verb] <= KeepPerVerb {
			keep[i] = true
		}
	}
	out := rs[:0:0]
	for i, r := range rs {
		if keep[i] {
			out = append(out, r)
		}
	}
	return out
}

// Prune drops every receipt: the round is over, so no retry can reach them.
func Prune(root string) error {
	p := path(root)
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fsio.Locked(p, lockWait, func() error {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	})
}
