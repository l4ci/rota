package migrate

import (
	"context"
	"errors"
	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/tracker"
	"slices"
	"testing"
)

func labelFake() *trackertest.Fake {
	return &trackertest.Fake{Issues: []tracker.Issue{
		{Key: tracker.NumKey(1), State: "open", Labels: []string{"remote"}},
		{Key: tracker.NumKey(2), State: "closed", Labels: []string{"remote"}},
		{Key: tracker.NumKey(3), State: "open", Labels: []string{"machine:mac"}},
		{Key: tracker.NumKey(4), State: "open", Labels: []string{"other"}},
	}}
}
func TestRelabelSupportPreviewWritesNothing(t *testing.T) {
	f := labelFake()
	rows, err := RelabelSupport(context.Background(), f, "remote", "machine:mac", false)
	if err != nil || len(rows) != 1 || rows[0].Key != tracker.NumKey(1) || !rows[0].Changed {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	if !slices.Equal(f.Issues[0].Labels, []string{"remote"}) {
		t.Fatal("preview changed labels")
	}
	for _, c := range f.Calls {
		if c.Method != "list" {
			t.Fatalf("preview wrote: %v", c)
		}
	}
}
func TestRelabelSupportApplyIsIdempotent(t *testing.T) {
	f := labelFake()
	rows, err := RelabelSupport(context.Background(), f, "remote", "machine:mac", true)
	if err != nil || len(rows) != 1 || !rows[0].Changed {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	if !slices.Equal(f.Issues[0].Labels, []string{"machine:mac"}) {
		t.Fatalf("labels=%v", f.Issues[0].Labels)
	}
	if !slices.Equal(f.Issues[1].Labels, []string{"remote"}) {
		t.Fatal("changed closed issue")
	}
	f.Calls = nil
	rows, err = RelabelSupport(context.Background(), f, "remote", "machine:mac", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Changed {
			t.Fatalf("second apply changed %v", r)
		}
	}
	for _, c := range f.Calls {
		if c.Method != "list" {
			t.Fatalf("second apply wrote: %v", c)
		}
	}
}
func TestRelabelSupportConflict(t *testing.T) {
	f := labelFake()
	f.Issues[0].Labels = []string{"remote", "machine:mac", "machine:main"}
	_, err := RelabelSupport(context.Background(), f, "remote", "machine:mac", true)
	if err == nil {
		t.Fatal("conflicting pin accepted")
	}
	if !slices.Equal(f.Issues[0].Labels, []string{"remote", "machine:mac", "machine:main"}) {
		t.Fatal("conflict changed")
	}
}
func TestRelabelSupportResumesAfterError(t *testing.T) {
	f := labelFake()
	f.Fail = map[string]error{"remove_labels": errors.New("offline")}
	_, err := RelabelSupport(context.Background(), f, "remote", "machine:mac", true)
	if err == nil {
		t.Fatal("tracker error ignored")
	}
	if !slices.Contains(f.Issues[0].Labels, "remote") {
		t.Fatal("old label lost")
	}
	f.Fail = nil
	rows, err := RelabelSupport(context.Background(), f, "remote", "machine:mac", true)
	if err != nil || len(rows) != 1 || !rows[0].Changed || !slices.Equal(f.Issues[0].Labels, []string{"machine:mac"}) {
		t.Fatalf("resume rows=%v err=%v labels=%v", rows, err, f.Issues[0].Labels)
	}
}

func TestRelabelSupportReportsPartialWrite(t *testing.T) {
	f := labelFake()
	f.Fail = map[string]error{"remove_labels": errors.New("offline")}
	rows, err := RelabelSupport(context.Background(), f, "remote", "machine:mac", true)
	if err == nil || len(rows) != 1 || !rows[0].Changed {
		t.Fatalf("partial write rows=%v err=%v", rows, err)
	}
}
