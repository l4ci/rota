package knowledge

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
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
func bumpHit(sc *Sidecar, h pendingHit, threshold int, contradicted map[[2]string]bool) (e Entry, promoted, blocked bool, err error) {
	e, err = sc.Bump(h.Topic, h.Title, h.Date)
	if err != nil {
		return e, false, false, err
	}
	if e.Tier == Provisional && e.Hits >= threshold {
		if contradicted[[2]string{h.Topic, h.Title}] {
			return e, false, true, nil
		}
		if err := sc.Promote(h.Topic, h.Title); err != nil {
			return e, false, false, err
		}
		e.Tier = Confirmed
		promoted = true
	}
	return e, promoted, false, nil
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

// foldedKey is the receipt for a claimed buffer whose safe hits are applied.
// A string names a fully applied file; an object also preserves deferred hits
// until the claimed file is rewritten. Reads and recovery never replay safe hits.
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
	if o, ok := v.(*jsonx.Object); ok {
		v, _ = o.Get("name")
	}
	name, _ := v.(string)
	return name
}

// foldedPending is the remainder of a partially applied claimed buffer.
// Keeping it in the same atomic write as the hit counts makes rewriting that
// buffer recoverable: reads use this remainder until the receipt is cleared.
func foldedPending(sc *Sidecar) ([]pendingHit, error) {
	v, _ := sc.obj.Get(foldedKey)
	o, ok := v.(*jsonx.Object)
	if !ok {
		// The legacy string receipt applied the whole file.
		return nil, nil
	}
	v, found := o.Get("pending")
	if !found {
		return nil, fmt.Errorf("%s: foldedBuffer receipt has no pending hits", sc.path)
	}
	raw, err := jsonx.MarshalCompact(v)
	if err != nil {
		return nil, err
	}
	var hits []pendingHit
	if err := json.Unmarshal(raw, &hits); err != nil {
		return nil, fmt.Errorf("%s: invalid foldedBuffer pending hits: %w", sc.path, err)
	}
	for _, h := range hits {
		if h.Topic == "" || h.Title == "" {
			return nil, fmt.Errorf("%s: invalid foldedBuffer pending owner", sc.path)
		}
	}
	return hits, nil
}

func retainPending(path string, hits []pendingHit) error {
	if len(hits) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	var b strings.Builder
	for _, h := range hits {
		raw, err := json.Marshal(h)
		if err != nil {
			return err
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	return fsio.WriteFileAtomic(path, []byte(b.String()))
}

// finishFold rewrites the claimed buffer before clearing its receipt. A crash
// between the two leaves an idempotent rewrite, not a replay of applied hits.
func finishFold(path, claimed string, pending []pendingHit) error {
	if err := retainPending(claimed, pending); err != nil {
		return err
	}
	return Update(path, func(sc *Sidecar) (bool, error) {
		if foldedMark(sc) != filepath.Base(claimed) {
			return false, fmt.Errorf("%s: foldedBuffer receipt changed during recovery", path)
		}
		sc.obj.Delete(foldedKey)
		return true, nil
	})
}

// unfoldedHits is every buffered hit the sidecar does not yet hold: claimed
// buffers it has not marked folded, then the live buffer.
func unfoldedHits(sc *Sidecar, hp string) ([]pendingHit, error) {
	claimed, err := claimedBuffers(hp)
	if err != nil {
		return nil, err
	}
	out, err := foldedPending(sc)
	if err != nil {
		return nil, err
	}
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
	if len(hits) > 0 {
		cm, err := s.contradicted()
		if err != nil {
			return nil, err
		}
		for _, h := range hits {
			if err := sc.checkIdentity(h.Topic, h.Title); err != nil {
				// Leave this hit buffered; a read of its owner still refuses through Get.
				// Replaying it must not prevent an unrelated key from being read.
				continue
			}
			if _, _, _, err := bumpHit(sc, h, s.PromoteThreshold(), cm); err != nil {
				return nil, err
			}
		}
	}
	return sc, nil
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

// FoldHits applies hits whose owners are unambiguous and keeps the others
// buffered for repair. A receipt atomically records applied counts and any
// remainder before the claimed file is rewritten; recovery never replays the
// applied hits or loses the remainder.
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
		sc, err := LoadSidecar(p)
		if err != nil {
			return err
		}
		if name := foldedMark(sc); name != "" {
			// A receipt is a basename in this buffer's directory, never an arbitrary path.
			prefix := filepath.Base(hp) + "."
			if filepath.Base(name) != name || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, claimedSuffix) || len(name) <= len(prefix)+len(claimedSuffix) {
				return fmt.Errorf("%s: invalid foldedBuffer name %q", p, name)
			}
			remaining, err := foldedPending(sc)
			if err != nil {
				return err
			}
			if err := finishFold(p, filepath.Join(filepath.Dir(hp), name), remaining); err != nil {
				return err
			}
			res.Changed = true
		}
		// Recovery may have removed a completed file or retained only its remainder.
		pending, err := claimedBuffers(hp)
		if err != nil {
			return err
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
			var remaining []pendingHit
			if err := Update(p, func(sc *Sidecar) (bool, error) {
				for _, h := range hits {
					if err := sc.checkIdentity(h.Topic, h.Title); err != nil {
						remaining = append(remaining, h)
						continue
					}
					if _, _, _, err := bumpHit(sc, h, th, cm); err != nil {
						return false, err
					}
					applied++
				}
				if applied == 0 {
					return false, nil
				}
				if len(remaining) == 0 {
					sc.obj.Set(foldedKey, name)
				} else {
					receipt := jsonx.NewObject()
					receipt.Set("name", name)
					receipt.Set("pending", remaining)
					sc.obj.Set(foldedKey, receipt)
				}
				return true, nil
			}); err != nil {
				return err
			}
			if applied == 0 {
				// No counts changed, so retaining this subset needs no sidecar receipt.
				if err := retainPending(c, remaining); err != nil {
					return err
				}
				continue
			}
			res.Hits += applied
			res.Changed = true
			if err := finishFold(p, c, remaining); err != nil {
				return err
			}
		}
		return nil
	})
	return res, err
}
