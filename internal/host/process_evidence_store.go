package host

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"time"
)

var errEvidenceCorrupt = errors.New("process evidence corrupt")

// evidenceLockWait bounds how long a store waits for its file lock.
const evidenceLockWait = 30 * time.Second

// evidenceFormat is the version of the envelope Save writes.
const evidenceFormat = 1

// evidenceFile is what a store read found.
type evidenceFile struct {
	Procs processEvidence
	// Legacy holds bare pids from before start times. They have no historical
	// identity and are never upgraded from today's process table; each backend
	// decides, from its own trusted tree, what a legacy pid may prove.
	Legacy []int
	Found  bool
	Bare   bool // a pre-envelope file; an empty one proves nothing
}

// evidenceRecord is the one on-disk format: the envelope distinguishes a
// proved empty tree from an invalid empty legacy list.
type evidenceRecord struct {
	Version   int              `json:"version"`
	Processes *processEvidence `json:"processes"`
}

// evidenceStore persists one backend's process evidence under a file path the
// backend chooses. Locking, format and legacy decoding live here only.
type evidenceStore struct {
	fs   FileSystem
	path string
}

// Locked serializes read-modify-write cycles on this store's file.
func (s evidenceStore) Locked(fn func() error) error {
	return s.fs.Locked(s.path, evidenceLockWait, fn)
}

// Load reads the file. A missing file is Found=false with no error. Both the
// envelope and the older bare arrays (Core's and Orca's) are accepted.
func (s evidenceStore) Load() (evidenceFile, error) {
	b, err := s.fs.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return evidenceFile{}, nil
	}
	if err != nil {
		return evidenceFile{}, err
	}
	f, err := decodeEvidence(b)
	if err != nil {
		return evidenceFile{}, errEvidenceCorrupt
	}
	f.Found = true
	for _, p := range f.Procs {
		if p.PID <= 0 {
			return evidenceFile{}, errEvidenceCorrupt
		}
	}
	for _, n := range f.Legacy {
		if n <= 0 {
			return evidenceFile{}, errEvidenceCorrupt
		}
	}
	return f, nil
}

// Save writes the envelope atomically.
func (s evidenceStore) Save(procs processEvidence) error {
	if procs == nil {
		procs = processEvidence{}
	}
	b, err := json.Marshal(evidenceRecord{Version: evidenceFormat, Processes: &procs})
	if err != nil {
		return err
	}
	return s.fs.WriteFileAtomic(s.path, b)
}

func decodeEvidence(b []byte) (evidenceFile, error) {
	if bytes.HasPrefix(bytes.TrimSpace(b), []byte("{")) {
		var r evidenceRecord
		if err := json.Unmarshal(b, &r); err != nil || r.Version != evidenceFormat || r.Processes == nil {
			return evidenceFile{}, errEvidenceCorrupt
		}
		return evidenceFile{Procs: *r.Processes}, nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil || raw == nil {
		return evidenceFile{}, errEvidenceCorrupt
	}
	f := evidenceFile{Bare: true}
	for _, r := range raw {
		var n int
		if json.Unmarshal(r, &n) == nil {
			f.Legacy = append(f.Legacy, n)
			continue
		}
		var p processIdentity
		if err := json.Unmarshal(r, &p); err != nil {
			return evidenceFile{}, errEvidenceCorrupt
		}
		f.Procs = append(f.Procs, p)
	}
	return f, nil
}
