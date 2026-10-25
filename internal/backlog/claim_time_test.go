package backlog

import (
	"github.com/l4ci/rota/internal/tracker"
	"testing"
	"time"
)

func TestStatusClaimedAt(t *testing.T) {
	b, tr := newIssues(t, `{}`, open(4))
	at := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	tr.Issues[0].Comments = []tracker.Comment{{Body: "<!-- rota:claim dana@3 -->", CreatedAt: at}, {Body: "<!-- rota:release dana@3 -->"}, {Body: "<!-- rota:claim dana@3 -->", CreatedAt: at.Add(time.Hour)}}
	st, err := b.Status("4")
	if err != nil {
		t.Fatal(err)
	}
	if !st.ClaimedAt["dana@3"].Equal(at.Add(time.Hour)) {
		t.Fatalf("ClaimedAt = %v, want renewed claim time", st.ClaimedAt)
	}
}
