package round

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
)

// notedRemote is a fakeRemote whose items carry a design note.
type notedRemote struct{ *fakeRemote }

func (notedRemote) NoteGet(_, kind string) (string, bool, error) {
	if kind == "design" {
		return "the design", true, nil
	}
	return "", false, nil
}

// #2570: the autopilot holds epics and unplanned Major items; plain items,
// dependency lists and planned Major items pass.
func TestAutoHold(t *testing.T) {
	fr := &fakeRemote{}
	fr.add("1", "Epic: rework the board", "", false, "")
	fr.add("2", "Board rework", "", false, "## Children\n- [ ] #19 first\n- [x] #20 second\n")
	fr.add("3", "Fix the label flag", "", false, "## Acceptance\n- [ ] works\n\n## Depends on\n- #12\n- #13\n")
	fr.add("4", "Big feature", "", false, "## Acceptance\n- [ ] works\n")
	fr.items["4"].Tag = "Major"
	fr.add("5", "Labelled epic", "", false, "")
	fr.items["5"].Labels = []string{"Epic"}
	cases := []struct {
		be   backlog.Backend
		id   string
		want string
	}{
		{fr, "1", "epic"},
		{fr, "2", "umbrella"},
		{fr, "3", ""},
		{fr, "4", "Major"},
		{notedRemote{fr}, "4", ""},
		{fr, "5", "labelled epic"},
	}
	for _, tc := range cases {
		got := autoHold(tc.be, *fr.items[tc.id])
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("item %s: hold %q, want one mentioning %q", tc.id, got, tc.want)
		}
	}
}
