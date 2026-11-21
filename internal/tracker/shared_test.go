package tracker

import (
	"context"
	"reflect"
	"testing"
)

func TestEditLabelsSkipsEmptyAndDedups(t *testing.T) {
	var got []IssueEdit
	edit := func(_ context.Context, _ IssueKey, e IssueEdit) error { got = append(got, e); return nil }
	var key IssueKey
	if err := editLabels(context.Background(), edit, key, IssueEdit{AddLabels: []string{"", ""}}); err != nil || len(got) != 0 {
		t.Fatalf("empty add wrote: %v %v", got, err)
	}
	if err := editLabels(context.Background(), edit, key, IssueEdit{RemoveLabels: []string{"a", "a", ""}}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0].RemoveLabels, []string{"a"}) {
		t.Fatalf("got %+v", got)
	}
}
