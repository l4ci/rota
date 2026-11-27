package extknowledge

import (
	"context"
	"testing"
)

// Replace refuses a title another bullet of the topic holds and writes nothing;
// a case change in place stays allowed.
func TestReplaceRefusesATakenTitle(t *testing.T) {
	f := editFixture(t, "")
	c := f.clone()
	ctx := context.Background()
	before, n := goFile(t, f), commitCount(t, f)
	for _, title := range []string{"**Tween leak**", "**TWEEN LEAK**"} {
		if _, err := c.Replace(ctx, ReplaceIn{Topic: "Go", Old: "**Legacy**", New: title}); !errorsIs(err, ErrExists) {
			t.Errorf("%s: err = %v, want ErrExists", title, err)
		}
	}
	if goFile(t, f) != before || commitCount(t, f) != n {
		t.Fatal("a refused replace wrote something")
	}
	res, err := c.Replace(ctx, ReplaceIn{Topic: "Go", Old: "**Legacy**", New: "**LEGACY**"})
	if err != nil || !res.Changed {
		t.Fatalf("case change = %+v, %v", res, err)
	}
}
