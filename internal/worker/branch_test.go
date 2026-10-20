package worker

import "testing"

func TestBranchName(t *testing.T) {
	for _, c := range []struct{ agent, id, title, want string }{
		{"ben", "59", "C3: rota round start / assign / wind-down with autonomy levels", "ben/59-c3-rota-round-start-assign"},
		{"dana", "B07", "Fix it!", "dana/b07-fix-it"},
		{"kit", "1", "", "kit/1"},
		{"kit", "2", "Supercalifragilisticexpialidocious-averyveryverylongwordhere", "kit/2-supercalifragilisticexpialidocious-avery"},
		{"ben", "PROJ-123", "Widen issue identifiers to tracker keys", "ben/PROJ-123-widen-issue-identifiers-to-tracker"},
	} {
		if got := BranchName(c.agent, c.id, c.title); got != c.want {
			t.Errorf("%q: %q want %q", c.title, got, c.want)
		}
	}
}

// The writer and the parser are one grammar: what BranchName writes, the
// readers get the same issue back from, for numeric ids, tracker keys and
// truncated slugs.
func TestBranchNameRoundTrips(t *testing.T) {
	long := "Supercalifragilisticexpialidocious averyveryverylongwordhere and then some more words"
	for _, c := range []struct{ id, title, want string }{
		{"12", "Add a thing", "12"},
		{"1037", long, "1037"},
		{"PROJ-123", "Widen issue identifiers to tracker keys", "PROJ-123"},
		{"PROJ-9", long, "PROJ-9"},
		{"ABC_D-7", "Underscore project key", "ABC_D-7"},
	} {
		br := BranchName("ben", c.id, c.title)
		if !RoundBranch(br) {
			t.Errorf("%q: not a round branch", br)
		}
		if got := IssueFromBranch(br); got != c.want {
			t.Errorf("%q: IssueFromBranch = %q, want %q", br, got, c.want)
		}
		if got := HeldID("", br, "ben"); got != c.want {
			t.Errorf("%q: HeldID = %q, want %q", br, got, c.want)
		}
		if k, ok := BranchIssue(br); ok && string(k) != c.want {
			t.Errorf("%q: BranchIssue = %q, want %q", br, k, c.want)
		}
	}
	// A lower-case slug that merely looks like a key is not an issue.
	if RoundBranch("dana/add-12-things") {
		t.Error("add-12-things is a slug, not a key")
	}
}
