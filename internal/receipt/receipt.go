// Package receipt records one entry per state-writing round verb call, keyed
// by a request id, so a retry returns the first answer instead of applying
// the mutation again. The file is per-developer runtime state, gitignored,
// and pruned when the round winds down.
//
// The receipt is a second guard beside the .lock sidecars: those serialise
// concurrent writers, this one recognises a repeated caller.
package receipt

import (
	"crypto/sha256"
	"encoding/hex"
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

// claimWait is how long a second call under one id waits for the first to
// finish: a verb's whole run, which for a gate or a train is minutes.
const claimWait = 10 * time.Minute

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

// Store is the receipts of one project. Now stamps each entry; tests set it.
type Store struct {
	Root string
	Now  func() time.Time
}

// New is the store of the project at root, stamping with the wall clock.
func New(root string) *Store { return &Store{Root: root, Now: time.Now} }

// Call is the identity of one verb call: the request id the caller retries
// with, the verb, and a fingerprint of everything the call judges.
type Call struct {
	ID, Verb, Fingerprint string
}

// Outcome is what Do reports once the call is settled.
type Outcome struct {
	// Replayed is true when a done receipt answered and run never ran.
	Replayed bool
	// Entry is the receipt that answered, or the one recorded for this run
	// (zero when the run wrote nothing).
	Entry Entry
	// RecordErr is why a finished run left no receipt. The mutation happened;
	// only the retry guard is missing.
	RecordErr error
}

// Do is the replay protocol: claim the id, look it up, run the mutation once,
// record what it did. The claim is a lock on the id held across the run, so
// two concurrent calls under one id cannot both miss and both mutate: the
// second waits, then finds the first's receipt and replays it.
//
// An id used before for another verb or fingerprint is a *Mismatch and run
// does not run. A done receipt replays and run does not run. Otherwise run gets
// the failed receipt of an earlier attempt (zero when there is none) and
// returns the entry to record; false means the call wrote nothing and leaves
// no receipt. Do fills in ID, Verb, Fingerprint, At and Attempts.
func (s *Store) Do(call Call, run func(prior Entry) (Entry, bool)) (Outcome, error) {
	var out Outcome
	err := fsio.Locked(s.claimPath(call.ID), claimWait, func() error {
		// Read the file before mutating, so a corrupt receipts file is
		// refused rather than written over (#712).
		prior, found, err := s.Lookup(call)
		if err != nil {
			return err
		}
		if found && prior.Status == Done {
			out = Outcome{Replayed: true, Entry: prior}
			return nil
		}
		e, record := run(prior)
		if !record {
			return nil
		}
		e.ID, e.Verb, e.Fingerprint = call.ID, call.Verb, call.Fingerprint
		out.Entry = e
		out.RecordErr = s.record(&out.Entry)
		return nil
	})
	return out, err
}

// Lookup returns the entry recorded for the call's id. found is false when none
// is. An entry whose verb or fingerprint differs is a *Mismatch.
func (s *Store) Lookup(call Call) (e Entry, found bool, err error) {
	d, err := load(path(s.Root))
	if err != nil {
		return Entry{}, false, err
	}
	for _, r := range d.Receipts {
		if r.ID != call.ID {
			continue
		}
		if r.Verb != call.Verb || r.Fingerprint != call.Fingerprint {
			return Entry{}, false, &Mismatch{ID: call.ID, Verb: r.Verb}
		}
		return r, true, nil
	}
	return Entry{}, false, nil
}

// record stores e, replacing an earlier entry with the same id. Attempts
// counts the calls under the id.
func (s *Store) record(e *Entry) error {
	p := path(s.Root)
	e.At = s.Now().UTC().Format(time.RFC3339)
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
		d.Receipts = capPerVerb(append(out, *e))
		b, err := json.MarshalIndent(d, "", "  ")
		if err != nil {
			return err
		}
		return fsio.WriteFileAtomic(p, append(b, '\n'))
	})
}

// claimPath is the file whose lock is the claim on an id. The id is hashed: it
// is caller text and must not shape a path.
func (s *Store) claimPath(id string) string {
	sum := sha256.Sum256([]byte(id))
	return path(s.Root) + "." + hex.EncodeToString(sum[:6])
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
	if claims, _ := filepath.Glob(p + ".*.lock"); len(claims) > 0 {
		for _, c := range claims {
			os.Remove(c)
		}
	}
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
