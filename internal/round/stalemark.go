package round

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/rotastate"
)

// staleMarkerName is the once-per-round marker under <git-common-dir>/rota/,
// next to the lease. It holds the round the stale-binary warning last spoke in.
const staleMarkerName = "stale-binary-round"

// ClaimStaleWarning reports whether the stale-binary warning is still due in
// round: true the first time it is asked in a round, false after, so a watch
// that wakes every ten minutes does not repeat it. A marker that cannot be read
// or written is returned as err; the warning is still due then (first is true),
// since losing the marker only risks a repeat, never a silent miss.
func ClaimStaleWarning(cd string, round int) (first bool, err error) {
	path := rotastate.File(cd, staleMarkerName)
	b, rerr := os.ReadFile(path)
	switch {
	case rerr == nil:
		if strings.TrimSpace(string(b)) == strconv.Itoa(round) {
			return false, nil
		}
	case !errors.Is(rerr, fs.ErrNotExist):
		err = fmt.Errorf("read %s: %w", path, rerr)
	}
	if werr := os.MkdirAll(filepath.Dir(path), 0o755); werr != nil {
		return true, errors.Join(err, fmt.Errorf("write %s: %w", path, werr))
	}
	if werr := fsio.WriteFileAtomic(path, []byte(strconv.Itoa(round)+"\n")); werr != nil {
		return true, errors.Join(err, fmt.Errorf("write %s: %w", path, werr))
	}
	return true, err
}
