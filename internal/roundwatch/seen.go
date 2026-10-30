package roundwatch

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/rotastate"
)

// SeenFile keeps what the support entries said when a watch last returned
// them (Env.Seen); it lives beside the watch marker.
const SeenFile = "round-watch-seen.json"

// SeenStore is the record behind Env.Seen and Env.Mark, kept under a common dir.
type SeenStore struct{ path string }

// NewSeenStore returns the store for the common dir cd.
func NewSeenStore(cd string) *SeenStore { return &SeenStore{path: rotastate.File(cd, SeenFile)} }

// Seen returns the recorded entries; a missing or unreadable record is empty.
func (s *SeenStore) Seen() map[string]string {
	out := map[string]string{}
	if b, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

// Mark merges m into the record. It never drops an entry: one that vanishes
// for a poll and comes back unchanged is not news.
func (s *SeenStore) Mark(m map[string]string) {
	cur := s.Seen()
	for k, v := range m {
		cur[k] = v
	}
	// A failed write only repeats a wake.
	if b, err := json.Marshal(cur); err == nil && os.MkdirAll(filepath.Dir(s.path), 0o755) == nil {
		_ = fsio.WriteFileAtomic(s.path, append(b, '\n'))
	}
}
