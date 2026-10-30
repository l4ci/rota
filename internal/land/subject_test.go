package land

import "testing"

func TestSubjectsRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		subject string
		want    string
		undo    bool
	}{
		{"merge", MergeSubject("ben/1-x", "main"), "merge: ben/1-x into main", true},
		{"train", TrainSubject("ben/1-x", "main"), "train: ben/1-x into main", false},
		{"sync", SyncSubject("main", "milestone/M01"), "sync main into milestone/M01", false},
	}
	for _, c := range cases {
		if c.subject != c.want {
			t.Errorf("%s: subject %q want %q", c.name, c.subject, c.want)
		}
		if got := IsCycleMerge(c.subject); got != c.undo {
			t.Errorf("%s: IsCycleMerge(%q) = %v want %v", c.name, c.subject, got, c.undo)
		}
	}
	if IsCycleMerge("wip") {
		t.Error("a plain subject is not a cycle merge")
	}
}
