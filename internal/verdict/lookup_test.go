package verdict

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/tracker"
)

func TestParseReport(t *testing.T) {
	report := RenderReport(NewRecord(ReviewSpec, Fail, "abcdef1234567", Body{Summary: "summary", Findings: []Finding{{Severity: "major", Title: "broken", Patch: "```\n@@ -1 +1 @@\n-old\n+new"}}, Items: []ItemVerdict{{ID: "2133", Verdict: Fail}}, Declined: []Declined{{Title: "not a bug"}}}, recAt), nil)
	r, ok := ParseReport(report)
	if !ok || r.Verdict != Fail || r.Sha != "abcdef1234567" || len(r.Findings) != 1 || r.Findings[0].Patch != "```\n@@ -1 +1 @@\n-old\n+new" || len(r.Declined) != 1 || len(r.Items) != 1 {
		t.Fatalf("round trip: %+v %v", r, ok)
	}
	for name, text := range map[string]string{
		"prose before marker":     "Copied report\n" + report,
		"embedded marker":         "```\n" + report + "```\n",
		"bad kind":                strings.ReplaceAll(report, "review-spec", "gate"),
		"bad sha":                 strings.ReplaceAll(report, "abcdef1234567", "not-a-sha"),
		"partial marker":          strings.Replace(report, " -->", "", 1),
		"header kind mismatch":    strings.Replace(report, "Kind: `review-spec`", "Kind: `qa`", 1),
		"header sha mismatch":     strings.Replace(report, "Reviewed SHA: `abcdef1234567`", "Reviewed SHA: `1234567`", 1),
		"header verdict mismatch": strings.Replace(report, "Verdict: **FAIL**", "Verdict: **PASS**", 1),
		"bad body":                strings.Replace(report, `"severity": "major"`, `"severity": "huge"`, 1),
		"missing body":            strings.Split(report, "### Verdict body")[0],
		"unclosed body":           strings.TrimSuffix(report, "````\n"),
		"trailing prose":          report + "extra",
	} {
		t.Run(name, func(t *testing.T) {
			if r, ok := ParseReport(text); ok {
				t.Fatalf("accepted invalid report: %+v", r)
			}
		})
	}
}

type lookupSource struct {
	notes            []tracker.Comment
	err              error
	self             string
	writers          map[string]bool
	permissionErrors map[string]error
}

func (f *lookupSource) MRNotes(context.Context, int) ([]tracker.Comment, error) {
	return f.notes, f.err
}
func (f *lookupSource) AuthenticatedUser(context.Context) (string, error) { return f.self, nil }
func (f *lookupSource) CanPush(_ context.Context, author string) (bool, error) {
	if err := f.permissionErrors[author]; err != nil {
		return false, err
	}
	return f.writers[author], nil
}

func TestRecoverPR(t *testing.T) {
	const head = "abcdef1234567890123456789012345678901234"
	note := func(kind, v, sha, author string) tracker.Comment {
		return tracker.Comment{Body: RenderReport(NewRecord(kind, v, sha, Body{}, recAt), nil), Author: author, CreatedAt: recAt}
	}
	t.Run("only current trusted reports", func(t *testing.T) {
		root := t.TempDir()
		source := &lookupSource{self: "me", writers: map[string]bool{"colleague": true}, notes: []tracker.Comment{
			note(ReviewSpec, Fail, "1111111", "me"),
			note(ReviewSpec, Concerns, "abcdef1", "colleague"),
			note(ReviewQuality, Fail, head, "outsider"),
			note(ReviewQuality, Pass, head, "me"),
			note(QA, Fail, head, ""),
		}}
		if err := RecoverPR(context.Background(), root, "sub:feature", head, 20, source); err != nil {
			t.Fatal(err)
		}
		list := Load(root).Branches["sub:feature"]
		if len(list) != 2 {
			t.Fatalf("records: %+v", list)
		}
		r, ok := EffectiveReview(list)
		if !ok || r.Verdict != Concerns {
			t.Fatalf("combined review: %+v %v", r, ok)
		}
		if _, ok := Load(root).Branches["feature"]; ok {
			t.Fatal("lost umbrella key")
		}
		// A second read is idempotent; a local head verdict wins over a remote update.
		if _, err := AddBranch(root, "sub:feature", NewRecord(ReviewSpec, Pass, head, Body{}, recAt)); err != nil {
			t.Fatal(err)
		}
		if err := RecoverPR(context.Background(), root, "sub:feature", head, 20, source); err != nil {
			t.Fatal(err)
		}
		list = Load(root).Branches["sub:feature"]
		if len(list) != 3 {
			t.Fatalf("duplicated cache: %+v", list)
		}
		if r, _ := Latest(list, ReviewSpec); r.Verdict != Pass {
			t.Fatalf("overwrote local: %+v", r)
		}
	})
	t.Run("newest valid report wins", func(t *testing.T) {
		root := t.TempDir()
		source := &lookupSource{self: "me", notes: []tracker.Comment{note(ReviewSpec, Fail, head, "me"), note(ReviewSpec, Pass, head, "me")}}
		if err := RecoverPR(context.Background(), root, "feature", head, 20, source); err != nil {
			t.Fatal(err)
		}
		if r, _ := Latest(Load(root).Branches["feature"], ReviewSpec); r.Verdict != Pass {
			t.Fatal(r)
		}
	})
	t.Run("offline preserves cache", func(t *testing.T) {
		root := t.TempDir()
		cached := NewRecord(QA, Pass, head, Body{}, recAt)
		cached.FromReport = true
		_, _ = AddBranch(root, "feature", cached)
		if err := RecoverPR(context.Background(), root, "feature", head, 20, &lookupSource{err: errors.New("offline")}); err == nil {
			t.Fatal("missing read error")
		}
		if r, _ := Latest(Load(root).Branches["feature"], QA); r.Verdict != Pass {
			t.Fatal(r)
		}
	})
}

func TestParseReportGitObjectFormats(t *testing.T) {
	for _, sha := range []string{"abcd", strings.Repeat("a", 64)} {
		report := RenderReport(NewRecord(QA, InfraFail, sha, Body{Summary: "runner unavailable"}, recAt), nil)
		r, ok := ParseReport(report)
		if !ok || r.Sha != sha || r.Verdict != InfraFail {
			t.Fatalf("valid git object name %q was lost: %+v %v", sha, r, ok)
		}
	}
}

func TestRecoveredCacheDefersToLocalBranch(t *testing.T) {
	const head = "abcdef1"
	root := t.TempDir()
	if _, err := AddBranch(root, "feature", NewRecord(ReviewQuality, Fail, head, Body{}, recAt)); err != nil {
		t.Fatal(err)
	}
	cached := NewRecord(ReviewSpec, Pass, head, Body{}, recAt)
	cached.FromReport = true
	if _, err := AddBranch(root, "feature", cached); err != nil {
		t.Fatal(err)
	}
	list := ForHead(Load(root).Branches["feature"], head)
	if r, ok := Blocking(list); !ok || r.Kind != ReviewQuality || r.Verdict != Fail {
		t.Fatalf("cached PASS masked local FAIL: %+v %v", r, ok)
	}
	if kinds := Recorded(root, "feature", head); len(kinds) != 1 || kinds[0] != ReviewQuality {
		t.Fatalf("cached record supplemented authoritative local review: %v", kinds)
	}
}

func TestRecoverPRRefreshesCachedAtHead(t *testing.T) {
	const head = "abcdef1"
	root := t.TempDir()
	report := func(kind, v string) tracker.Comment {
		return tracker.Comment{Author: "me", Body: RenderReport(NewRecord(kind, v, head, Body{}, recAt), nil)}
	}
	source := &lookupSource{self: "me", notes: []tracker.Comment{report(ReviewSpec, Fail), report(ReviewQuality, Pass)}}
	if err := RecoverPR(context.Background(), root, "feature", head, 20, source); err != nil {
		t.Fatal(err)
	}
	if r, ok := EffectiveReview(Load(root).Branches["feature"]); !ok || r.Verdict != Fail {
		t.Fatalf("initial FAIL not cached: %+v %v", r, ok)
	}
	// The posting verb upserts a comment: its position and SHA stay the same.
	source.notes[0] = report(ReviewSpec, Pass)
	for i := 0; i < 2; i++ {
		if err := RecoverPR(context.Background(), root, "feature", head, 20, source); err != nil {
			t.Fatal(err)
		}
		list := Load(root).Branches["feature"]
		if r, ok := EffectiveReview(list); !ok || r.Verdict != Pass || !r.FromReport {
			t.Fatalf("new PASS did not refresh cached FAIL/combined: %+v %v", r, ok)
		}
		if len(list) != 2 {
			t.Fatalf("refresh duplicated cache entries: %+v", list)
		}
	}
	source.notes[1] = report(ReviewQuality, Fail)
	if err := RecoverPR(context.Background(), root, "feature", head, 20, source); err != nil {
		t.Fatal(err)
	}
	if r, ok := Blocking(Load(root).Branches["feature"]); !ok || r.Verdict != Fail {
		t.Fatalf("new FAIL did not replace cached PASS: %+v %v", r, ok)
	}
}

func TestRecoverPRSkipsAuthorPermissionError(t *testing.T) {
	const head = "abcdef1"
	note := func(v, author string) tracker.Comment {
		return tracker.Comment{Author: author, Body: RenderReport(NewRecord(ReviewSpec, v, head, Body{}, recAt), nil)}
	}
	for _, badFirst := range []bool{true, false} {
		t.Run(fmt.Sprint("bad author first: ", badFirst), func(t *testing.T) {
			root := t.TempDir()
			notes := []tracker.Comment{note(Pass, "unverified"), note(Fail, "me")}
			if !badFirst {
				notes[0], notes[1] = notes[1], notes[0]
			}
			source := &lookupSource{self: "me", notes: notes, permissionErrors: map[string]error{"unverified": errors.New("permission lookup unavailable")}}
			if err := RecoverPR(context.Background(), root, "feature", head, 20, source); err != nil {
				t.Fatalf("one author aborted recovery: %v", err)
			}
			if r, ok := Blocking(ForHead(Load(root).Branches["feature"], head)); !ok || r.Verdict != Fail || !r.FromReport {
				t.Fatalf("lost trusted FAIL: %+v %v", r, ok)
			}
		})
	}
}

func TestRecoverPRStampsRecordedAtWithRegistryLayout(t *testing.T) {
	const head = "abcdef1234567890123456789012345678901234"
	created := time.Date(2026, 3, 4, 10, 5, 6, 987654321, time.FixedZone("CET", 60*60))
	source := &lookupSource{self: "me", notes: []tracker.Comment{{
		Body:      RenderReport(NewRecord(ReviewSpec, Pass, head, Body{}, recAt), nil),
		Author:    "me",
		CreatedAt: created,
	}}}
	root := t.TempDir()
	if err := RecoverPR(context.Background(), root, "feature", head, 20, source); err != nil {
		t.Fatal(err)
	}
	r, ok := Latest(Load(root).Branches["feature"], ReviewSpec)
	if !ok {
		t.Fatal("no recovered record")
	}
	if want := "2026-03-04T09:05:06Z"; r.RecordedAt != want {
		t.Fatalf("RecordedAt = %q, want %q", r.RecordedAt, want)
	}
}
