package extknowledge

import (
	"context"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

// The bullet-selection rules Replace, Amend and Remove share with the local
// store (bulletfmt owns them). internal/knowledge/bullet_rules_test.go runs the
// same cases through its own store: a case added here goes there too.

func rulesFixture(t *testing.T, content string) *fixture {
	t.Helper()
	f := newFixture(t, "")
	gittest.Commit(t, f.a, "seed", "topics/go.md", content)
	gittest.Run(t, f.a, "push", "-q")
	return f
}

func TestReplaceNeverSearchesTheStamp(t *testing.T) {
	const in = "# Go\n\n- **One** — body <!-- 2026-01-01 source:rota -->\n"
	for _, old := range []string{"2026-01-01", "source:rota", "body <!--"} {
		f := rulesFixture(t, in)
		if _, err := f.clone().Replace(context.Background(), ReplaceIn{Topic: "Go", Old: old, New: "x"}); !errorsIs(err, ErrNotFound) {
			t.Errorf("old %q: err = %v, want ErrNotFound", old, err)
		}
		if got := goFile(t, f); got != in {
			t.Errorf("old %q: file changed: %q", old, got)
		}
	}
	f := rulesFixture(t, in)
	if _, err := f.clone().Replace(context.Background(), ReplaceIn{Topic: "Go", Old: "body", New: "text"}); err != nil {
		t.Fatal(err)
	}
	if got, want := goFile(t, f), "# Go\n\n- **One** — text <!-- 2026-01-01 source:rota -->\n"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestAmendTreatsAnIndentedDashLineAsContinuation(t *testing.T) {
	f := rulesFixture(t, "# Go\n\n- **One** — body <!-- 2026-01-01 -->\n  - nested note\n")
	if _, err := f.clone().Amend(context.Background(), AmendIn{Topic: "Go", Fragment: "nested note", Text: "more"}); err != nil {
		t.Fatal(err)
	}
	// The text lands on the bullet the nested line belongs to.
	if got, want := goFile(t, f), "# Go\n\n- **One** — body more <!-- 2026-01-01 -->\n  - nested note\n"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestAmendRefusesTwoBulletsInOneFile(t *testing.T) {
	const in = "# Go\n\n- **One** — has X <!-- 2026-01-01 -->\n- **Two** — has X <!-- 2026-01-01 -->\n"
	f := rulesFixture(t, in)
	if _, err := f.clone().Amend(context.Background(), AmendIn{Topic: "Go", Fragment: "X", Text: "more"}); !errorsIs(err, ErrMultiMatch) {
		t.Fatalf("err = %v, want ErrMultiMatch", err)
	}
	if got := goFile(t, f); got != in {
		t.Errorf("file changed: %q", got)
	}
}

func TestAmendNeverSearchesTheStamp(t *testing.T) {
	const in = "# Go\n\n- **One** — body <!-- 2026-01-01 -->\n"
	f := rulesFixture(t, in)
	if _, err := f.clone().Amend(context.Background(), AmendIn{Topic: "Go", Fragment: "2026-01-01", Text: "more"}); !errorsIs(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if got := goFile(t, f); got != in {
		t.Errorf("file changed: %q", got)
	}
}

func TestRemoveCollapsesBlankLines(t *testing.T) {
	cases := []struct{ name, in, title, want string }{
		{"between blanks", "# Go\n\n- **One** — a\n\n- **Two** — b\n\n## B\n", "One", "# Go\n\n- **Two** — b\n\n## B\n"},
		{"last of topic", "# Go\n\n- **Two** — b\n\n## B\n", "Two", "# Go\n\n## B\n"},
		{"wrapped bullet goes whole", "# Go\n\n- **One** — a\n  wrapped\n\n- **Two** — b\n", "One", "# Go\n\n- **Two** — b\n"},
		{"next line is not blank", "# Go\n\n- **One** — a\n- **Two** — b\n", "One", "# Go\n\n- **Two** — b\n"},
	}
	for _, c := range cases {
		f := rulesFixture(t, c.in)
		if _, err := f.clone().Remove(context.Background(), RmIn{Topic: "Go", Title: c.title}); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := goFile(t, f); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestHitCountsUnderTheStoredTitle(t *testing.T) {
	f := rulesFixture(t, "# Go\n\n- **Foo** — body <!-- 2026-01-01 -->\n")
	c := f.clone()
	for i, typed := range []string{"foo", "FOO"} {
		title, n, err := c.Hit("Go", typed, "p")
		if err != nil || title != "Foo" || n != i+1 {
			t.Fatalf("Hit(%q) = %q, %d, %v; want Foo, %d", typed, title, n, err, i+1)
		}
	}
	rows, _, err := c.readHits()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Title != "Foo" {
			t.Errorf("hit row keyed %q, want Foo", r.Title)
		}
	}
}

func TestHitRefusesCaseTwins(t *testing.T) {
	f := rulesFixture(t, "# Go\n\n- **Foo** — a <!-- 2026-01-01 -->\n- **FOO** — b <!-- 2026-01-01 -->\n")
	c := f.clone()
	_, _, err := c.Hit("Go", "foo", "p")
	if !errorsIs(err, ErrMultiMatch) {
		t.Fatalf("err = %v, want ErrMultiMatch", err)
	}
	if rows, _, _ := c.readHits(); len(rows) != 0 {
		t.Errorf("a refused hit was logged: %v", rows)
	}
}
