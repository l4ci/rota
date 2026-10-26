package extknowledge

import (
	"context"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

const migrateGo = "# Go\n\n" +
	"- **Godot** — Kill tweens. (seen on Godot 4.6) <!-- 2026-09-01 source:game -->\n" +
	"- **Two tools** — Build it. (seen on Swift 6, macOS 26) <!-- 2026-09-02 source:app -->\n" +
	"- **Dotted** — Texture limits. (seen on three.js r186) <!-- 2026-09-03 source:web -->\n" +
	"- **Next** — Cache it (seen on Next.js 16). <!-- 2026-09-04 source:web -->\n" +
	"- **Hyphen** — Plugin order. (seen on tailwind-merge 3) <!-- 2026-09-05 source:web -->\n" +
	"- **Plus** — Legacy source. (seen on SQLite 3.49.2) <!-- 2026-08-02 source:kluft+Upracked -->\n" +
	"- **Plain** — Nothing to move. <!-- 2026-01-02 source:rota -->\n"

const migrateGoWant = "# Go\n\n" +
	"- **Godot** — Kill tweens. <!-- 2026-09-01 source:game seen:godot@4.6 -->\n" +
	"- **Two tools** — Build it. <!-- 2026-09-02 source:app seen:swift@6,macos@26 -->\n" +
	"- **Dotted** — Texture limits. <!-- 2026-09-03 source:web seen:three.js@r186 -->\n" +
	"- **Next** — Cache it. <!-- 2026-09-04 source:web seen:next.js@16 -->\n" +
	"- **Hyphen** — Plugin order. <!-- 2026-09-05 source:web seen:tailwind-merge@3 -->\n" +
	"- **Plus** — Legacy source. <!-- 2026-08-02 source:kluft+Upracked seen:sqlite@3.49.2 -->\n" +
	"- **Plain** — Nothing to move. <!-- 2026-01-02 source:rota -->\n"

// migrateFixture seeds the Go topic with body and an Xcode topic, and pushes.
func migrateFixture(t *testing.T, policy, body string) *fixture {
	t.Helper()
	f := newFixture(t, policy)
	gittest.Commit(t, f.a, "seed go", "topics/go.md", body)
	gittest.Commit(t, f.a, "seed xcode", "topics/xcode.md", "# Xcode\n\n- **Build number** — Bump it. (seen on 26.5) <!-- 2026-10-01 source:app -->\n")
	gittest.Commit(t, f.a, "index", IndexFile, seedIndex+"- [Xcode](topics/xcode.md) — build tooling\n")
	gittest.Run(t, f.a, "push", "-q")
	return f
}

func TestMigrateSeenMovesTrailingSuffixes(t *testing.T) {
	f := migrateFixture(t, "", migrateGo)
	n := commitCount(t, f)
	rep, err := f.clone().MigrateSeen(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Changed || !rep.Write.Pushed || len(rep.Changes) != 7 {
		t.Fatalf("rep = %+v", rep)
	}
	if got := goFile(t, f); got != migrateGoWant {
		t.Fatalf("file = %q", got)
	}
	if commitCount(t, f) == n {
		t.Fatal("no commit")
	}
	if msg := gittest.Run(t, f.a, "log", "-1", "--format=%B"); strings.TrimSpace(msg) != "knowledge: move (seen on …) into seen: stamps" {
		t.Fatalf("message = %q", msg)
	}
	if got := gittest.Run(t, f.remote, "show", "main:topics/go.md"); got+"\n" != migrateGoWant {
		t.Fatalf("remote = %q", got)
	}
	want := map[string]string{"Godot": "godot@4.6", "Two tools": "swift@6,macos@26", "Dotted": "three.js@r186", "Next": "next.js@16", "Hyphen": "tailwind-merge@3", "Plus": "sqlite@3.49.2", "Build number": "xcode@26.5"}
	for _, ch := range rep.Changes {
		if want[ch.Title] != ch.Seen {
			t.Errorf("%s: seen %q, want %q", ch.Title, ch.Seen, want[ch.Title])
		}
	}
}

func TestMigrateSeenRoundTripsStamp(t *testing.T) {
	f := migrateFixture(t, "", migrateGo)
	if _, err := f.clone().MigrateSeen(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, b := range parseBullets(goFile(t, f)) {
		if strings.Contains(b.Stamp, "seen:") {
			n++
			if _, err := ParseSeen(b.Seen.String()); err != nil || len(b.Seen) == 0 {
				t.Errorf("%s: seen %v does not re-parse: %v", b.Title, b.Seen, err)
			}
		}
		if b.Title == "Plus" && b.Source != "kluft+Upracked" {
			t.Errorf("source = %q", b.Source)
		}
		if strings.Contains(b.Body, "(seen on") {
			t.Errorf("%s: suffix left in %q", b.Title, b.Body)
		}
	}
	if n != 6 {
		t.Fatalf("stamps with seen: = %d", n)
	}
}

func TestMigrateSeenIsIdempotent(t *testing.T) {
	f := migrateFixture(t, "", migrateGo)
	if _, err := f.clone().MigrateSeen(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	n, file := commitCount(t, f), goFile(t, f)
	rep, err := f.clone().MigrateSeen(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Changed || len(rep.Changes) != 0 || rep.Stranded {
		t.Fatalf("rep = %+v", rep)
	}
	if commitCount(t, f) != n || goFile(t, f) != file {
		t.Fatal("second apply wrote something")
	}
}

func TestMigrateSeenPreviewWritesNothing(t *testing.T) {
	f := migrateFixture(t, "", migrateGo)
	gittest.Write(t, f.a, "topics/go.md", migrateGo+"- **Dirty** — local edit. (seen on Godot 4.7) <!-- 2026-10-01 -->\n")
	before, n := goFile(t, f), commitCount(t, f)
	rep, err := f.clone().MigrateSeen(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Changed || len(rep.Changes) != 8 {
		t.Fatalf("rep = %+v", rep)
	}
	if goFile(t, f) != before || commitCount(t, f) != n {
		t.Fatal("preview wrote")
	}
	if _, err := f.clone().MigrateSeen(context.Background(), true); !errorsIs(err, ErrDirty) {
		t.Fatalf("apply on a dirty clone: err = %v", err)
	}
}

func TestMigrateSeenManualReview(t *testing.T) {
	body := "# Go\n\n" +
		"- **Mid** — Pinned (seen on 3.49.2). Use the new API. <!-- 2026-10-01 source:db -->\n" +
		"- **Cont** — First line. <!-- 2026-10-01 source:db -->\n" +
		"  More text (seen on Godot 4.6)\n" +
		"- **No version** — Mac only. (seen on macOS) <!-- 2026-10-01 source:db -->\n" +
		"- **Both** — Early (seen on Godot 4.5). Late. (seen on Godot 4.6) <!-- 2026-10-01 source:db -->\n"
	f := migrateFixture(t, "", body)
	rep, err := f.clone().MigrateSeen(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, m := range rep.ManualReview {
		got[m.Title] = m.Text
	}
	for _, title := range []string{"Mid", "Cont", "No version", "Both"} {
		if got[title] == "" {
			t.Errorf("%q not in manualReview: %+v", title, rep.ManualReview)
		}
	}
	if len(rep.Changes) != 2 || rep.Changes[0].Title != "Both" {
		t.Fatalf("changes = %+v", rep.Changes)
	}
	want := strings.Replace(body, " (seen on Godot 4.6) <!-- 2026-10-01 source:db -->\n", " <!-- 2026-10-01 source:db seen:godot@4.6 -->\n", 1)
	if g := goFile(t, f); g != want {
		t.Fatalf("file = %q\nwant %q", g, want)
	}
}

func TestMigrateSeenMergesExistingStamp(t *testing.T) {
	conflict := "- **Conflict** — B. (seen on Godot 4.7, Swift 6) <!-- 2026-10-01 source:x seen:godot@4.6 -->\n"
	body := "# Go\n\n" +
		"- **Adds** — A. (seen on Godot 4.6, Swift 6) <!-- 2026-10-01 source:x seen:godot@4.6 -->\n" + conflict
	f := migrateFixture(t, "", body)
	rep, err := f.clone().MigrateSeen(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	want := "# Go\n\n" +
		"- **Adds** — A. <!-- 2026-10-01 source:x seen:godot@4.6,swift@6 -->\n" + conflict
	if g := goFile(t, f); g != want {
		t.Fatalf("file = %q", g)
	}
	review := func(r MigrateReport) {
		t.Helper()
		if len(r.ManualReview) != 1 || r.ManualReview[0].Title != "Conflict" || r.ManualReview[0].Text != "stamp says godot@4.6, suffix says godot@4.7" {
			t.Fatalf("manualReview = %+v", r.ManualReview)
		}
	}
	review(rep)
	n := commitCount(t, f)
	rep, err = f.clone().MigrateSeen(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	review(rep)
	if rep.Changed || len(rep.Changes) != 0 || commitCount(t, f) != n || goFile(t, f) != want {
		t.Fatalf("re-run changed something: %+v", rep)
	}
}

func TestMigrateSeenInfersToolFromTopic(t *testing.T) {
	f := migrateFixture(t, "", migrateGo)
	rep, err := f.clone().MigrateSeen(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range rep.Changes {
		if (ch.Title == "Build number") != ch.Inferred {
			t.Errorf("%s: inferred = %v", ch.Title, ch.Inferred)
		}
		if ch.Title == "Build number" && (ch.Seen != "xcode@26.5" || ch.Topic != "Xcode") {
			t.Errorf("change = %+v", ch)
		}
	}
}

func TestMigrateSeenReportsStrandedLocalCommit(t *testing.T) {
	f := migrateFixture(t, "", migrateGo)
	if _, err := f.clone().MigrateSeen(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	// A previous apply whose push failed leaves its commit local.
	gittest.Commit(t, f.a, "stranded", "note.md", "x")
	rep, err := f.clone().MigrateSeen(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Changed || !rep.Stranded {
		t.Fatalf("rep = %+v", rep)
	}
}

func TestMigrateSeenPRMode(t *testing.T) {
	f := migrateFixture(t, `{"write":"pr","branch":"main"}`, migrateGo)
	rep, err := f.clone().MigrateSeen(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Write.PR == "" || rep.Write.Branch == "" || rep.Write.Pushed || len(f.gh) != 1 {
		t.Fatalf("rep = %+v gh = %v", rep, f.gh)
	}
	if got := gittest.Run(t, f.a, "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
		t.Fatalf("clone left on %q", got)
	}
	if got := gittest.Run(t, f.remote, "show", rep.Write.Branch+":topics/go.md"); got+"\n" != migrateGoWant {
		t.Fatalf("branch file = %q", got)
	}
	if goFile(t, f) != migrateGo {
		t.Fatal("main checkout changed")
	}
}
