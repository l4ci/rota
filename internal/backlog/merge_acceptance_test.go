package backlog

import (
	"errors"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/tracker"
)

func TestMergePRUnmetAcceptance(t *testing.T) {
	fake := &rrFake{MS: &trackertest.MS{Fake: &trackertest.Fake{Issues: []Issue{{Key: tracker.NumKey(1), Title: "Task", State: "open", Labels: []string{"type:task"}, Body: "## Acceptance\n- [ ] AC-1: ready\n", Comments: []tracker.Comment{{ID: "1", Body: "<!-- rota:proof -->\n## Proof\n- test · PASS · ok\n"}}}}}}, HostCloses: true, PRs: []tracker.PR{{Number: 12, Body: "Closes #1", Branch: "task"}}}
	b := &Issues{Cfg: mustDecode(t, `{}`), Tracker: fake, CountProof: stubCountProof}
	_, err := b.MergePRGated(12, nil, nil, stubLander)
	var refusal *RefusedError
	if !errors.As(err, &refusal) || refusal.BlockedBy != "unmet acceptance" || !strings.Contains(err.Error(), "AC-1: ready") {
		t.Fatalf("merge error: %v", err)
	}
	if len(fake.merged) != 0 || fake.Issues[0].State != "open" {
		t.Fatal("unmet item merged and closed")
	}
}
