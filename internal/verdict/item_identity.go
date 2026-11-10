package verdict

import (
	"slices"

	"github.com/l4ci/rota/internal/fsio"
)

// CanonicalizeItems moves legacy keys under the store lock. targets maps an old
// key to its canonical identities; nil leaves it alone. Several targets copy
// history whose original repository is unknown, so no possible owner loses it.
// The operation is idempotent and preserves every record, including resets.
func CanonicalizeItems(root string, targets func(string) []string) error {
	return fsio.Locked(Path(root), fsio.LockTimeout, func() error {
		s, err := loadStrict(root)
		if err != nil {
			// A corrupt store reads as empty here: nothing to move, nothing written.
			return nil
		}
		keys := make([]string, 0, len(s.Items))
		for key := range s.Items {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		groups := map[string][][]Record{}
		changed := false
		for _, key := range keys {
			ids := targets(key)
			if len(ids) == 0 {
				ids = []string{key}
			}
			changed = changed || len(ids) != 1 || ids[0] != key
			for _, id := range ids {
				groups[id] = append(groups[id], s.Items[key])
			}
		}
		if !changed {
			return nil
		}
		s.Items = map[string][]Record{}
		for id, histories := range groups {
			if len(histories) == 1 {
				s.Items[id] = histories[0]
				continue
			}
			// A legacy reset only cleared its own spelling. Put everything that
			// is not an active failure first, then each still-active failure,
			// rather than allowing a reset from one alias to erase another
			// alias's failed attempts.
			var cleared, active []Record
			for _, list := range histories {
				live := activeFailures(list)
				for i, r := range list {
					if live[i] {
						active = append(active, r)
					} else {
						cleared = append(cleared, r)
					}
				}
			}
			s.Items[id] = append(cleared, active...)
		}
		return fsio.WriteJSONAtomic(Path(root), toJSONX(s))
	})
}

// appendItem retains active failures even when consolidated alias histories
// exceed the usual history limit. Only an explicit reset may clear them.
func appendItem(list []Record, r Record) []Record {
	list = append(list, r)
	cut := len(list) - keep
	if cut <= 0 {
		return list
	}
	live := activeFailures(list)
	for i := 0; i < cut; i++ {
		if live[i] {
			cut = i
			break
		}
	}
	return list[cut:]
}

// activeFailures marks the records that still count: a failed attempt of a
// counter at or after that counter's latest reset. CanonicalizeItems and
// appendItem share it, so both agree on what is active.
func activeFailures(list []Record) []bool {
	debugStart, workStart := debugCounter.start(list), workCounter.start(list)
	live := make([]bool, len(list))
	for i, r := range list {
		live[i] = (i >= debugStart && debugCounter.failed(r)) ||
			(i >= workStart && workCounter.failed(r))
	}
	return live
}
