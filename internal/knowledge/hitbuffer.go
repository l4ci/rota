package knowledge

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

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

// readPending returns the buffered hits in order and how many lines it
// skipped. A missing file is empty and a non-blank line that does not parse is
// skipped, so one torn append loses one hit; the count lets the fold say so.
func readPending(path string) ([]pendingHit, int, error) {
	return fsio.ReadJSONL(path, func(h *pendingHit) bool { return h.Topic != "" && h.Title != "" })
}

func appendPending(path string, h pendingHit) error {
	return fsio.AppendJSONL(path, h)
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

// foldedKey is the sidecar field naming the claimed buffer whose hits the
// sidecar already holds. A fold that dies after the sidecar write leaves that
// buffer behind; the mark is how the next fold or read knows not to replay it.
const foldedKey = "foldedBuffer"

const claimedSuffix = ".folding"

// claimedBuffers lists the buffers a fold claimed and has not yet removed. It
// lists the directory and matches names as strings: a glob on the absolute
// path would read [, * or ? in the repo path as a pattern and miss the buffer.
func claimedBuffers(hp string) ([]string, error) {
	dir, prefix := filepath.Dir(hp), filepath.Base(hp)+"."
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if len(n) > len(prefix)+len(claimedSuffix) && strings.HasPrefix(n, prefix) && strings.HasSuffix(n, claimedSuffix) {
			out = append(out, filepath.Join(dir, n))
		}
	}
	return out, nil
}

func foldedMark(sc *Sidecar) string {
	v, _ := sc.obj.Get(foldedKey)
	name, _ := v.(string)
	return name
}

// unfoldedHits is every buffered hit the sidecar does not yet hold: claimed
// buffers it has not marked folded, then the live buffer.
func unfoldedHits(sc *Sidecar, hp string) ([]pendingHit, error) {
	claimed, err := claimedBuffers(hp)
	if err != nil {
		return nil, err
	}
	var out []pendingHit
	for _, c := range claimed {
		if filepath.Base(c) == foldedMark(sc) {
			continue
		}
		hits, _, err := readPending(c)
		if err != nil {
			return nil, err
		}
		out = append(out, hits...)
	}
	live, _, err := readPending(hp)
	if err != nil {
		return nil, err
	}
	return append(out, live...), nil
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
	hits, err := unfoldedHits(sc, hp)
	if err != nil {
		return nil, err
	}
	return sc, s.applyPending(sc, hits)
}

// FoldResult reports FoldHits.
type FoldResult struct {
	Hits    int  // buffered hits applied
	Changed bool // the tracked sidecar was rewritten
	Skipped int  // buffer lines dropped as malformed
}

// claim renames the live buffer to a uniquely named claimed file, so hits
// appended from here on land in a fresh buffer instead of being removed with
// the folded one. It returns "" when there is no live buffer.
func claim(hp string) (string, error) {
	var nonce [6]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	dst := hp + "." + hex.EncodeToString(nonce[:]) + claimedSuffix
	if err := os.Rename(hp, dst); os.IsNotExist(err) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	return dst, nil
}

// FoldHits applies the buffered hits of scope to its tracked sidecar and
// empties the buffer. It is the one verb that writes the sidecar for hits, so
// the caller commits the result.
//
// A fold is crash-safe: the buffer is claimed by rename, the sidecar write
// records the claimed file's name, and only then is the file removed. A fold
// that dies, or fails to remove, after the sidecar write leaves a claimed file
// the sidecar already marks, which the next fold removes without replaying.
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
		if left, _ := claimedBuffers(hp); len(left) == 0 {
			return res, nil // nothing buffered: take no lock, leave no lockfile
		}
	}
	err = fsio.Locked(hp, fsio.LockTimeout, func() error {
		if _, err := claim(hp); err != nil {
			return err
		}
		claimed, err := claimedBuffers(hp)
		if err != nil || len(claimed) == 0 {
			return err
		}
		// Drop the already-folded leftover before anything overwrites the mark.
		sc, err := LoadSidecar(p)
		if err != nil {
			return err
		}
		recovered := foldedMark(sc) != "" // a crashed fold left its mark in the tracked sidecar
		pending := claimed[:0:0]
		for _, c := range claimed {
			if filepath.Base(c) != foldedMark(sc) {
				pending = append(pending, c)
			} else if err := os.Remove(c); err != nil {
				return err
			}
		}
		cm, err := s.contradicted()
		if err != nil {
			return err
		}
		th := s.PromoteThreshold()
		for _, c := range pending {
			name := filepath.Base(c)
			hits, skipped, err := readPending(c)
			if err != nil {
				return err
			}
			res.Skipped += skipped
			var applied int
			if err := Update(p, func(sc *Sidecar) (bool, error) {
				for _, h := range hits {
					bumpHit(sc, h, th, cm)
				}
				sc.obj.Set(foldedKey, name)
				applied = len(hits)
				return true, nil
			}); err != nil {
				return err
			}
			if applied > 0 {
				res.Hits += applied
				res.Changed = true
			}
			if err := os.Remove(c); err != nil {
				return err
			}
		}
		// The mark has done its job; clear it so the tracked sidecar carries no
		// per-fold noise. Names are unique, so a failure here leaves it harmless.
		return Update(p, func(sc *Sidecar) (bool, error) {
			if foldedMark(sc) == "" {
				return false, nil
			}
			sc.obj.Delete(foldedKey)
			// A mark this fold set and cleared nets out; one it found was a rewrite.
			if recovered {
				res.Changed = true
			}
			return true, nil
		})
	})
	return res, err
}
