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
		s := Load(root)
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
			// A legacy reset only cleared its own spelling. Put the cleared
			// prefixes first, then each still-active tail, rather than allowing
			// a reset from one alias to erase another alias's failed attempts.
			var cleared, active []Record
			for _, list := range histories {
				at := 0
				for i, r := range list {
					if r.Kind == DebugReset {
						at = i + 1
					}
				}
				cleared = append(cleared, list[:at]...)
				active = append(active, list[at:]...)
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
	start := 0
	for i, record := range list {
		if record.Kind == DebugReset {
			start = i + 1
		}
	}
	for i := start; i < cut; i++ {
		if list[i].Kind == DebugFix && list[i].Verdict == Fail {
			cut = i
			break
		}
	}
	return list[cut:]
}
