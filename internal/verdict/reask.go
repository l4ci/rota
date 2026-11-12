package verdict

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/l4ci/rota/internal/fsio"
)

// MaxReasks is how many times a reviewer session is asked to resend a block
// `verdict add` rejected. The block failing to parse is a formatting slip: the
// same session still holds the review, so a short error re-emits it without
// redoing the work. A second malformed block means the session cannot fix it.
const MaxReasks = 1

// ReaskPath is the per-developer count of rejected blocks, keyed by branch and
// kind. It sits under .rota/review/, which is gitignored.
func ReaskPath(root string) string {
	return filepath.Join(root, ".rota", "review", "reask.json")
}

func reaskKey(key, kind string) string { return key + "|" + kind }

func updateReasks(root string, mutate func(map[string]int)) error {
	p := ReaskPath(root)
	if err := os.MkdirAll(filepath.Dir(p), 0o777); err != nil {
		return err
	}
	return fsio.Locked(p, fsio.LockTimeout, func() error {
		m := map[string]int{}
		if b, err := os.ReadFile(p); err == nil {
			// A count file that does not parse is a lost count, not a refusal.
			_ = json.Unmarshal(b, &m)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		mutate(m)
		if len(m) == 0 {
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return nil
		}
		b, err := json.Marshal(m)
		if err != nil {
			return err
		}
		return fsio.WriteFileAtomic(p, b)
	})
}

// NoteMalformed counts one more rejected block for the branch and kind and
// returns the count including it.
func NoteMalformed(root, key, kind string) (int, error) {
	var n int
	err := updateReasks(root, func(m map[string]int) {
		m[reaskKey(key, kind)]++
		n = m[reaskKey(key, kind)]
	})
	return n, err
}

// ClearMalformed drops the count once a block for the branch and kind records.
func ClearMalformed(root, key, kind string) error {
	return updateReasks(root, func(m map[string]int) { delete(m, reaskKey(key, kind)) })
}

