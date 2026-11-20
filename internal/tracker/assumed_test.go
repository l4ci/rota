package tracker

import (
	"errors"
	"reflect"
	"slices"
	"testing"
)

// TestAssumedForgeRefusesEveryWrite walks IssueTracker by reflection: each
// method that is not in IssueReader is a write, and the assumed forge must
// refuse it without a CLI call. A method added to either interface shows up
// here without a test edit.
func TestAssumedForgeRefusesEveryWrite(t *testing.T) {
	s := &scripted{answer: func(string, []string) (string, string, int) { return `[]`, "", 0 }}
	a, err := newAssumedForge(newAdapter(t, "github", s), Resolution{Origin: "https://unknown.example/o/r"})
	if err != nil {
		t.Fatal(err)
	}
	tracker := reflect.TypeOf((*IssueTracker)(nil)).Elem()
	reader := reflect.TypeOf((*IssueReader)(nil)).Elem()
	var reads, writes []string
	for i := 0; i < tracker.NumMethod(); i++ {
		m := tracker.Method(i)
		if _, isRead := reader.MethodByName(m.Name); isRead {
			reads = append(reads, m.Name)
			continue
		}
		writes = append(writes, m.Name)
		method := reflect.ValueOf(a).MethodByName(m.Name)
		args := make([]reflect.Value, m.Type.NumIn())
		for j := range args {
			args[j] = reflect.Zero(m.Type.In(j))
		}
		out := method.Call(args)
		err, _ := out[len(out)-1].Interface().(error)
		if !errors.Is(err, ErrUnknownForge) {
			t.Errorf("%s = %v, want ErrUnknownForge", m.Name, err)
		}
	}
	if len(s.calls) != 0 {
		t.Fatalf("a write reached the wrapped adapter: %q", s.calls)
	}
	if len(writes) == 0 {
		t.Fatal("no write methods found: the reflection walk is broken")
	}
	// The read set is a decision: adding to IssueReader must be deliberate.
	slices.Sort(reads)
	want := []string{"CheckAuth", "ClosesIssue", "CommentURL", "Comments", "FindMilestone", "Get", "IssuesInMilestone", "List", "Milestones", "Provider"}
	if !slices.Equal(reads, want) {
		t.Fatalf("IssueReader methods = %v, want %v", reads, want)
	}
}
