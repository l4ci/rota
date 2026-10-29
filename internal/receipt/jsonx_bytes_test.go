package receipt

import (
	"testing"

	"github.com/l4ci/rota/internal/fsio/fsiotest"
)

func TestRecordWritesJsonxBytes(t *testing.T) {
	s := New(t.TempDir())
	if err := s.record(&Entry{ID: "r1", Verb: "v", Status: Failed, Message: fsiotest.Hostile}); err != nil {
		t.Fatal(err)
	}
	fsiotest.RequireJsonxBytes(t, path(s.Root))
}
