package extknowledge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

const editGo = "# Go\n\n" +
	"- **Table tests** — Use t.Run per case. <!-- 2026-01-02 source:rota -->\n" +
	"- **Tween leak** — Kill tweens on exit. <!-- 2026-09-01 source:game seen:godot@4.6,swift@6 -->\n" +
	"- **Legacy** — Old one. <!-- 2026-08-02 source:kluft+Upracked -->\n" +
	"- **Bare** — No stamp at all.\n"

// editFixture seeds the Go topic with editGo and pushes it.
func editFixture(t *testing.T, policy string) *fixture {
	t.Helper()
	f := newFixture(t, policy)
	gittest.Commit(t, f.a, "seed bullets", "topics/go.md", editGo)
	gittest.Run(t, f.a, "push", "-q")
	return f
}

func goFile(t *testing.T, f *fixture) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.a, "topics/go.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func commitCount(t *testing.T, f *fixture) string {
	return gittest.Run(t, f.a, "rev-list", "--count", "HEAD")
}

func TestRemoveRefusesDirtyClone(t *testing.T) {
	f := editFixture(t, "")
	gittest.Write(t, f.a, "topics/go.md", editGo+"- local\n")
	before, n := goFile(t, f), commitCount(t, f)
	_, err := f.clone().Remove(context.Background(), RmIn{Topic: "Go", Title: "Legacy"})
	if !errorsIs(err, ErrDirty) {
		t.Fatalf("err = %v", err)
	}
	if goFile(t, f) != before || commitCount(t, f) != n {
		t.Fatal("a refused remove wrote something")
	}
}

func TestRemoveRefusesDivergedClone(t *testing.T) {
	f := editFixture(t, "")
	gittest.Commit(t, f.a, "local", "extra.md", "x")
	f.remoteAdds("rust")
	_, err := f.clone().Remove(context.Background(), RmIn{Topic: "Go", Title: "Legacy"})
	if !errorsIs(err, ErrDiverged) {
		t.Fatalf("err = %v", err)
	}
}

func TestRemovePushes(t *testing.T) {
	f := editFixture(t, "")
	res, err := f.clone().Remove(context.Background(), RmIn{Topic: "Go", Title: "tween LEAK"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || !res.Pushed || res.File != "topics/go.md" {
		t.Fatalf("res = %+v", res)
	}
	want := strings.Replace(editGo, "- **Tween leak** — Kill tweens on exit. <!-- 2026-09-01 source:game seen:godot@4.6,swift@6 -->\n", "", 1)
	if got := goFile(t, f); got != want {
		t.Fatalf("file = %q", got)
	}
	if got := gittest.Run(t, f.remote, "show", "main:topics/go.md"); got+"\n" != want {
		t.Fatalf("remote = %q", got)
	}
	if msg := gittest.Run(t, f.a, "log", "-1", "--format=%B"); strings.Contains(msg, "Co-Authored") || strings.Contains(msg, "Generated") {
		t.Fatalf("commit message has a trailer: %q", msg)
	}
	c := f.clone()
	if _, err := c.Remove(context.Background(), RmIn{Topic: "Go", Title: "Tween leak"}); !errorsIs(err, ErrNotFound) {
		t.Fatalf("second remove err = %v", err)
	}
	if _, err := c.Remove(context.Background(), RmIn{Topic: "Nope", Title: "x"}); !errorsIs(err, ErrNotFound) {
		t.Fatalf("unknown topic err = %v", err)
	}
}

func TestRemoveSeveralWithTitleIsMultiMatch(t *testing.T) {
	f := editFixture(t, "")
	gittest.Commit(t, f.a, "dup", "topics/go.md", editGo+"- **Legacy** — Again. <!-- 2026-08-03 -->\n")
	gittest.Run(t, f.a, "push", "-q")
	before := goFile(t, f)
	if _, err := f.clone().Remove(context.Background(), RmIn{Topic: "Go", Title: "Legacy"}); !errorsIs(err, ErrMultiMatch) {
		t.Fatalf("err = %v", err)
	}
	if goFile(t, f) != before {
		t.Fatal("file changed")
	}
}

func TestRemoveRetriesRejectedPush(t *testing.T) {
	f := editFixture(t, `{"write":"push"}`)
	c := f.clone()
	pushed := false
	f.hook = func(args []string) {
		if len(args) > 0 && args[0] == "push" && !pushed {
			pushed = true
			f.remoteAdds("rust")
		}
	}
	res, err := c.Remove(context.Background(), RmIn{Topic: "Go", Title: "Legacy"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pushed || !res.Retried {
		t.Fatalf("res = %+v", res)
	}
	gittest.Run(t, f.b, "pull", "-q", "--ff-only")
	if _, err := os.Stat(filepath.Join(f.b, "topics/rust.md")); err != nil {
		t.Fatal("the other writer's topic was lost")
	}
	if b, _ := os.ReadFile(filepath.Join(f.b, "topics/go.md")); strings.Contains(string(b), "**Legacy**") {
		t.Fatal("removal missing on the remote")
	}
}

func TestReplacePRMode(t *testing.T) {
	f := editFixture(t, `{"write":"pr","branch":"main"}`)
	res, err := f.clone().Replace(context.Background(), ReplaceIn{Topic: "Go", Old: "Use t.Run per case.", New: "Use t.Run."})
	if err != nil {
		t.Fatal(err)
	}
	if res.PR == "" || res.Branch == "" || res.Pushed {
		t.Fatalf("res = %+v", res)
	}
	if got := gittest.Run(t, f.remote, "show", res.Branch+":topics/go.md"); !strings.Contains(got, "**Table tests** — Use t.Run. <!-- 2026-01-02 source:rota -->") {
		t.Fatalf("branch file = %q", got)
	}
	if strings.Contains(gittest.Run(t, f.remote, "show", "main:topics/go.md"), "Use t.Run.") {
		t.Fatal("main must not change in pr mode")
	}
	if gittest.Run(t, f.a, "rev-parse", "--abbrev-ref", "HEAD") != "main" || gittest.Run(t, f.a, "status", "--porcelain") != "" {
		t.Fatal("clone must be back on main and clean")
	}
}

func TestReplaceNeverTouchesStamp(t *testing.T) {
	f := editFixture(t, "")
	c := f.clone()
	ctx := context.Background()
	before, n := goFile(t, f), commitCount(t, f)
	for _, tc := range []struct {
		name, old, new string
		want           error
	}{
		{"stamp only", "source:rota", "source:x", ErrNotFound},
		{"date only", "2026-01-02", "2027-01-01", ErrNotFound},
		{"spans body and stamp", "per case. <!--", "per case. <!-- x", ErrNotFound},
		{"spans legacy plus sign", "Old one. <!-- 2026-08-02", "Old.", ErrNotFound},
		{"no match", "nothing like this", "x", ErrNotFound},
		{"several bullets", "—", "-", ErrMultiMatch},
		{"closes the stamp", "Use t.Run", "x -->", ErrContent},
		{"line break", "Use t.Run", "a\nb", ErrContent},
	} {
		if _, err := c.Replace(ctx, ReplaceIn{Topic: "Go", Old: tc.old, New: tc.new}); !errorsIs(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
	if goFile(t, f) != before || commitCount(t, f) != n {
		t.Fatal("a refused replace wrote something")
	}
	// A real edit keeps the odd stamp byte for byte.
	if _, err := c.Replace(ctx, ReplaceIn{Topic: "Go", Old: "Old one.", New: "Older."}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(goFile(t, f), "- **Legacy** — Older. <!-- 2026-08-02 source:kluft+Upracked -->\n") {
		t.Fatalf("file = %q", goFile(t, f))
	}
	// Same text is no change and no commit.
	n = commitCount(t, f)
	res, err := c.Replace(ctx, ReplaceIn{Topic: "Go", Old: "Older.", New: "Older."})
	if err != nil || res.Changed || commitCount(t, f) != n {
		t.Fatalf("noop: %+v %v", res, err)
	}
}

func TestReplaceRefusesDirtyClone(t *testing.T) {
	f := editFixture(t, "")
	gittest.Write(t, f.a, "topics/go.md", editGo+"- local\n")
	if _, err := f.clone().Replace(context.Background(), ReplaceIn{Topic: "Go", Old: "Old one.", New: "x"}); !errorsIs(err, ErrDirty) {
		t.Fatalf("err = %v", err)
	}
}

func seenOf(t *testing.T, s string) *Seen {
	t.Helper()
	v, err := ParseSeen(s)
	if err != nil {
		t.Fatal(err)
	}
	return &v
}

func TestAmendSeenReplacesList(t *testing.T) {
	f := editFixture(t, "")
	res, err := f.clone().Amend(context.Background(), AmendIn{Topic: "Go", Fragment: "Tween leak", Seen: seenOf(t, "godot@4.8")})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || !res.Pushed {
		t.Fatalf("res = %+v", res)
	}
	if !strings.Contains(goFile(t, f), "Kill tweens on exit. <!-- 2026-09-01 source:game seen:godot@4.8 -->\n") {
		t.Fatalf("file = %q", goFile(t, f))
	}
	// A stamp with a plus-sign source and no seen: gains the field, source untouched.
	if _, err := f.clone().Amend(context.Background(), AmendIn{Topic: "Go", Fragment: "Legacy", Seen: seenOf(t, "go@1.23")}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(goFile(t, f), "<!-- 2026-08-02 source:kluft+Upracked seen:go@1.23 -->\n") {
		t.Fatalf("file = %q", goFile(t, f))
	}
}

func TestAmendSeenEmptyDropsField(t *testing.T) {
	f := editFixture(t, "")
	if _, err := f.clone().Amend(context.Background(), AmendIn{Topic: "Go", Fragment: "Tween leak", Seen: seenOf(t, "")}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(goFile(t, f), "Kill tweens on exit. <!-- 2026-09-01 source:game -->\n") {
		t.Fatalf("file = %q", goFile(t, f))
	}
}

func TestAmendDateOnly(t *testing.T) {
	f := editFixture(t, "")
	c := f.clone()
	if _, err := c.Amend(context.Background(), AmendIn{Topic: "Go", Fragment: "Legacy", Date: "2026-10-26"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(goFile(t, f), "Old one. <!-- 2026-10-26 source:kluft+Upracked -->\n") {
		t.Fatalf("file = %q", goFile(t, f))
	}
	// Body and stamp edits together: text lands before the stamp.
	if _, err := c.Amend(context.Background(), AmendIn{Topic: "Go", Fragment: "Table tests", Text: "More.", Date: "2026-10-27", Seen: seenOf(t, "go@1.23")}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(goFile(t, f), "Use t.Run per case. More. <!-- 2026-10-27 source:rota seen:go@1.23 -->\n") {
		t.Fatalf("file = %q", goFile(t, f))
	}
}

func TestAmendDateInvalid(t *testing.T) {
	f := editFixture(t, "")
	before, n := goFile(t, f), commitCount(t, f)
	for _, d := range []string{"2026-13-40", "26-10-26", "yesterday"} {
		if _, err := f.clone().Amend(context.Background(), AmendIn{Topic: "Go", Fragment: "Legacy", Date: d}); !errorsIs(err, ErrUsage) {
			t.Errorf("%q: err = %v", d, err)
		}
	}
	if goFile(t, f) != before || commitCount(t, f) != n {
		t.Fatal("a refused amend wrote something")
	}
}

func TestAmendSeenOnUnstampedBullet(t *testing.T) {
	f := editFixture(t, "")
	c := f.clone()
	before := goFile(t, f)
	_, err := c.Amend(context.Background(), AmendIn{Topic: "Go", Fragment: "Bare", Seen: seenOf(t, "go@1.23")})
	if !errorsIs(err, ErrUsage) || !strings.Contains(err.Error(), "--date") {
		t.Fatalf("err = %v", err)
	}
	if goFile(t, f) != before {
		t.Fatal("file changed")
	}
	if _, err := c.Amend(context.Background(), AmendIn{Topic: "Go", Fragment: "Bare", Seen: seenOf(t, "go@1.23"), Date: "2026-10-26"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(goFile(t, f), "- **Bare** — No stamp at all. <!-- 2026-10-26 seen:go@1.23 -->\n") {
		t.Fatalf("file = %q", goFile(t, f))
	}
}

func TestAmendNeedsSomethingToChange(t *testing.T) {
	f := editFixture(t, "")
	if _, err := f.clone().Amend(context.Background(), AmendIn{Topic: "Go", Fragment: "Legacy"}); !errorsIs(err, ErrUsage) {
		t.Fatalf("err = %v", err)
	}
}

func TestAmendStampEditRefusesDirtyClone(t *testing.T) {
	f := editFixture(t, "")
	gittest.Write(t, f.a, "topics/go.md", editGo+"- local\n")
	if _, err := f.clone().Amend(context.Background(), AmendIn{Topic: "Go", Fragment: "Legacy", Date: "2026-10-26"}); !errorsIs(err, ErrDirty) {
		t.Fatalf("err = %v", err)
	}
}
