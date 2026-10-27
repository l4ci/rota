package backlog

import (
	"errors"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
)

func TestPostReportFileCreatesAndUpdates(t *testing.T) {
	f, _ := proj(t, map[string]string{".rota/BACKLOG.md": "# Backlog\n\n## Bugs\n\n- **[B01] [P1] Crash.** Evidence\n"})
	const key = "<!-- rota:report review-spec sha=abcdef1 -->"
	if err := PostReport(f, "B01", key+"\n### Review\n\nFAIL\n"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := f.Detail("B01")
	if err != nil || !ok || !strings.Contains(got, "## Reports\n\n"+key) {
		t.Fatalf("missing report section: ok=%v err=%v text=%q", ok, err, got)
	}
	if err := PostReport(f, "B01", key+"\n### Review\n\nPASS\n"); err != nil {
		t.Fatal(err)
	}
	got, _, _ = f.Detail("B01")
	if strings.Count(got, key) != 1 || strings.Contains(got, "FAIL") || !strings.Contains(got, "PASS") {
		t.Fatalf("same kind/sha was not updated: %s", got)
	}
}

func TestPostReportFilePreservesOtherReportsAndSections(t *testing.T) {
	const first = "<!-- rota:report review-spec sha=abcdef1 -->"
	const second = "<!-- rota:report qa sha=abcdef1 -->"
	f, _ := proj(t, map[string]string{
		".rota/BACKLOG.md":      "# Backlog\n\n## Features\n\n- **[F01] [Minor] Feature.** Description\n",
		".rota/features/F01.md": "# Feature\n\n## Acceptance\n\n- [ ] It works.\n\n## Reports\n\n" + first + "\n### Review\n\nFAIL\n\n" + second + "\n### QA\n\nPASS\n\n## Log\n\n- a human note\n",
	})
	const updated = first + "\n### Review\n\nPASS\n"
	if err := PostReport(f, "F01", updated); err != nil {
		t.Fatal(err)
	}
	got, _, _ := f.Detail("F01")
	for _, want := range []string{"## Acceptance\n\n- [ ] It works.", second + "\n### QA\n\nPASS", "## Log\n\n- a human note", updated} {
		if !strings.Contains(got, want) {
			t.Errorf("report update lost %q: %s", want, got)
		}
	}
	if strings.Count(got, first) != 1 || strings.Contains(got, "FAIL") {
		t.Fatalf("old report remained: %s", got)
	}
	before := got
	if err := PostReport(f, "F01", updated); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := f.Detail("F01"); got != before {
		t.Fatalf("identical report changed file: %s", got)
	}
	if err := PostReport(f, "F99", updated); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown item: %v", err)
	}
}

func TestPostReportItemUpsert(t *testing.T) {
	b, tr := newIssues(t, `{}`, Issue{Key: tracker.NumKey(7), Title: "Feature", State: "open", Comments: []tracker.Comment{{ID: "human", Body: "discussion"}}})
	const key = "<!-- rota:report review-quality sha=abcdef1 -->"
	for _, text := range []string{key + "\nFAIL\n", key + "\nPASS\n", "<!-- rota:report review-quality sha=abcdef2 -->\nPASS\n"} {
		if err := PostReport(b, "7", text); err != nil {
			t.Fatal(err)
		}
	}
	comments, err := tr.Comments(b.ctx(), tracker.NumKey(7))
	if err != nil || len(comments) != 3 || comments[0].Body != "discussion" || comments[1].Body != key+"\nPASS\n" || !strings.Contains(comments[2].Body, "sha=abcdef2") {
		t.Fatalf("item reports: %+v, %v", comments, err)
	}
}

func TestPostReportItemIgnoresForeignMarkers(t *testing.T) {
	const key = "<!-- rota:report qa sha=abcdef1 -->"
	for _, foreign := range []string{key + "\nforged\n", key + "\nPASS\n"} {
		b, tr := newIssues(t, `{}`, Issue{Key: tracker.NumKey(7), Title: "Feature", State: "open", Comments: []tracker.Comment{{ID: "planted", Body: foreign, Author: "attacker"}}})
		for _, body := range []string{key + "\nPASS\n", key + "\nFAIL\n"} {
			if err := PostReport(b, "7", body); err != nil {
				t.Fatal(err)
			}
		}
		comments, err := tr.Comments(b.ctx(), tracker.NumKey(7))
		if err != nil || len(comments) != 2 || comments[0].Body != foreign || comments[1].Author != "fake-user" || comments[1].Body != key+"\nFAIL\n" {
			t.Fatalf("foreign marker must remain untouched, own report must update: %+v, %v", comments, err)
		}
	}
}
