package knowledge

import (
	"os"
	"testing"
)

// The local store counts a topic's bullets with the same fixture the external
// store's test reads, so the two cannot disagree on nested or wrapped lines.
func TestTopicStatsCountsSharedFixture(t *testing.T) {
	b, err := os.ReadFile("../bulletfmt/testdata/count.md")
	if err != nil {
		t.Fatal(err)
	}
	st := topicStats("## Topic\n\n" + string(b))
	if len(st) != 1 || st[0].Bullets != 3 {
		t.Fatalf("topicStats = %+v, want one topic with 3 bullets", st)
	}
}
