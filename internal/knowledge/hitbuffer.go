package knowledge

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/l4ci/rota/internal/fsio"
)

// HitsFile is the gitignored, per-developer buffer of hits not yet folded into
// the tracked sidecar. A hit appends one JSON line here instead of rewriting
// knowledge-tier.json, so consulting a bullet never dirties the tree (#1290);
// `rota knowledge tier fold` applies the buffer to the sidecar.
const HitsFile = "knowledge-hits.jsonl"

type pendingHit struct {
	Topic string `json:"topic"`
	Title string `json:"title"`
	Date  string `json:"date"`
}

// HitsPath is the hit buffer of scope.
func (s Store) HitsPath(scope string) (string, error) {
	if err := s.check(scope); err != nil {
		return "", err
	}
	return filepath.Join(s.dir(scope), HitsFile), nil
}

// readPending returns the buffered hits in order. A missing file is empty and
// a line that does not parse is skipped, so one torn append loses one hit.
func readPending(path string) ([]pendingHit, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []pendingHit
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		var h pendingHit
		if json.Unmarshal(sc.Bytes(), &h) == nil && h.Topic != "" && h.Title != "" {
			out = append(out, h)
		}
	}
	return out, sc.Err()
}

func appendPending(path string, h pendingHit) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return err
	}
	line, err := json.Marshal(h)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// bumpHit registers one hit on sc in memory and promotes a provisional bullet
// at the threshold unless the pair is queued as a contradiction.
func bumpHit(sc *Sidecar, h pendingHit, threshold int, contradicted map[[2]string]bool) (e Entry, promoted, blocked bool) {
	e = sc.Bump(h.Topic, h.Title, h.Date)
	if e.Tier == Provisional && e.Hits >= threshold {
		if contradicted[[2]string{h.Topic, h.Title}] {
			return e, false, true
		}
		sc.Promote(h.Topic, h.Title)
		e.Tier = Confirmed
		promoted = true
	}
	return e, promoted, false
}

func (s Store) contradicted() (map[[2]string]bool, error) {
	list, err := s.Contradictions()
	if err != nil {
		return nil, err
	}
	m := map[[2]string]bool{}
	for _, c := range list {
		m[[2]string{c.Topic, c.Title}] = true
	}
	return m, nil
}

// applyPending replays the buffered hits onto sc in memory.
func (s Store) applyPending(sc *Sidecar, hits []pendingHit) error {
	if len(hits) == 0 {
		return nil
	}
	cm, err := s.contradicted()
	if err != nil {
		return err
	}
	th := s.PromoteThreshold()
	for _, h := range hits {
		bumpHit(sc, h, th, cm)
	}
	return nil
}

// effective is scope's sidecar as it reads once the buffered hits are folded
// in: nothing is written, so reads stay clean.
func (s Store) effective(scope string) (*Sidecar, error) {
	p, err := s.TierPath(scope)
	if err != nil {
		return nil, err
	}
	sc, err := LoadSidecar(p)
	if err != nil {
		return nil, err
	}
	hp, err := s.HitsPath(scope)
	if err != nil {
		return nil, err
	}
	hits, err := readPending(hp)
	if err != nil {
		return nil, err
	}
	return sc, s.applyPending(sc, hits)
}

// FoldResult reports FoldHits.
type FoldResult struct {
	Hits    int  // buffered hits applied
	Changed bool // the tracked sidecar was rewritten
}

// FoldHits applies the buffered hits of scope to its tracked sidecar and
// empties the buffer. It is the one verb that writes the sidecar for hits, so
// the caller commits the result.
func (s Store) FoldHits(scope string) (FoldResult, error) {
	var res FoldResult
	p, err := s.TierPath(scope)
	if err != nil {
		return res, err
	}
	hp, err := s.HitsPath(scope)
	if err != nil {
		return res, err
	}
	if _, err := os.Stat(hp); os.IsNotExist(err) {
		return res, nil // nothing buffered: take no lock, leave no lockfile
	}
	err = fsio.Locked(hp, fsio.LockTimeout, func() error {
		hits, err := readPending(hp)
		if err != nil || len(hits) == 0 {
			return err
		}
		cm, err := s.contradicted()
		if err != nil {
			return err
		}
		th := s.PromoteThreshold()
		if err := Update(p, func(sc *Sidecar) (bool, error) {
			for _, h := range hits {
				bumpHit(sc, h, th, cm)
			}
			return true, nil
		}); err != nil {
			return err
		}
		res = FoldResult{Hits: len(hits), Changed: true}
		return os.Remove(hp)
	})
	return res, err
}
