package cli

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog/trackertest"
)

func lastBody(fake *trackertest.Fake) string { return fake.Issues[len(fake.Issues)-1].Body }

func TestIssueCreateNumbersAcceptance(t *testing.T) {
	root := trackerProject(t, issuesConfig)
	fake := issueFixture()
	deps := withTracker(t, fake)
	body := "Why.\n\n## Acceptance\n\n- [ ] first\n- [ ] AC-4: kept\n- [ ] second\n"
	code, _, stderr := issueRunWith(t, deps, root, "item", "create", "--kind", "features", "--title", "T",
		"--tag", "Minor", "--body-file", bodyFile(t, body))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	got := lastBody(fake)
	for _, want := range []string{"- [ ] AC-5: first", "- [ ] AC-4: kept", "- [ ] AC-6: second"} {
		if !strings.Contains(got, want) {
			t.Errorf("body lacks %q:\n%s", want, got)
		}
	}
}
