package skills

import "testing"

func TestClassify(t *testing.T) {
	const rec, want, other = "rec", "want", "other"
	cases := []struct {
		name string
		in   pathState
		want decision
	}{
		{"absent, wanted: create", pathState{Want: want, Wanted: true}, decision{actCreate, Created, false}},
		{"absent, managed, wanted: create", pathState{Recorded: rec, Managed: true, Want: want, Wanted: true}, decision{actCreate, Created, false}},
		{"identical unmanaged: adopt", pathState{Want: want, Wanted: true, Have: want}, decision{actAdopt, Unchanged, false}},
		{"identical managed: adopt", pathState{Recorded: rec, Managed: true, Want: want, Wanted: true, Have: want}, decision{actAdopt, Unchanged, false}},
		{"managed, untouched, new content: update", pathState{Recorded: rec, Managed: true, Want: want, Wanted: true, Have: rec}, decision{actUpdate, Updated, false}},
		{"managed, edited: keep as edited", pathState{Recorded: rec, Managed: true, Want: want, Wanted: true, Have: other}, decision{actKeep, Edited, true}},
		{"managed, edited, overwrite: replace", pathState{Recorded: rec, Managed: true, Want: want, Wanted: true, Have: other, Overwrite: true}, decision{actReplace, Replaced, false}},
		{"unmanaged differing: keep as unmanaged", pathState{Want: want, Wanted: true, Have: other}, decision{actKeep, Unmanaged, false}},
		{"unmanaged differing, overwrite: replace", pathState{Want: want, Wanted: true, Have: other, Overwrite: true}, decision{actReplace, Replaced, false}},
		{"not a regular file, wanted: keep as unmanaged", pathState{Want: want, Wanted: true, Have: "?"}, decision{actKeep, Unmanaged, false}},
		{"not a regular file, overwrite: replace", pathState{Want: want, Wanted: true, Have: "?", Overwrite: true}, decision{actReplace, Replaced, false}},
		{"unsafe, wanted, unmanaged: keep as unmanaged", pathState{Want: want, Wanted: true, Unsafe: true}, decision{actKeep, Unmanaged, false}},
		{"unsafe, wanted, managed: keep, dropped from the manifest", pathState{Recorded: rec, Managed: true, Want: want, Wanted: true, Unsafe: true, Overwrite: true}, decision{actKeep, Unmanaged, false}},
		{"unsafe, not wanted: keep as edited, stays recorded", pathState{Recorded: rec, Managed: true, Unsafe: true, Overwrite: true}, decision{actKeep, Edited, true}},
		{"recorded, not wanted, gone: forget", pathState{Recorded: rec, Managed: true}, decision{actForget, "", false}},
		{"recorded, not wanted, untouched: remove", pathState{Recorded: rec, Managed: true, Have: rec}, decision{actRemove, Removed, false}},
		{"recorded, not wanted, edited: keep as edited", pathState{Recorded: rec, Managed: true, Have: other}, decision{actKeep, Edited, true}},
		{"recorded, not wanted, edited, overwrite: remove", pathState{Recorded: rec, Managed: true, Have: other, Overwrite: true}, decision{actRemove, Removed, false}},
	}
	for _, c := range cases {
		if got := classify(c.in); got != c.want {
			t.Errorf("%s: classify(%+v) = %+v, want %+v", c.name, c.in, got, c.want)
		}
	}
}
