package tracker

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/tracker/lineartest"
)

// Only a label that does not exist means "nothing to remove": a failed label
// read during RemoveLabels is returned, and the label stays on the issue.
func TestLinearRemoveLabelsReturnsAFailedLabelRead(t *testing.T) {
	ctx := context.Background()
	l, s := fakeLinear(t, lineartest.Options{})
	k := linearCreate(t, l, "t", "", "in-progress")
	fresh := linearAt(s, linearKey, "ENG") // no labels read yet
	s.ErrorOn("Labels", "RATELIMITED", "slow down")
	if err := fresh.RemoveLabels(ctx, k, []string{"in-progress"}); !IsKind(err, KindRateLimited) {
		t.Fatalf("RemoveLabels during a rate limit = %v, want it returned", err)
	}
	if got := s.Labels(string(k)); !slices.Contains(got, "in-progress") {
		t.Fatalf("labels %v: the label went although the call failed", got)
	}
	if err := fresh.RemoveLabels(ctx, k, []string{"never-made"}); err != nil {
		t.Fatalf("removing a label no one made = %v, want nil", err)
	}
}

// An oversized state body fails before any write: no issue, no visible
// comment stub, no edited text that its state no longer matches.
func TestLinearCapIsCheckedBeforeWriting(t *testing.T) {
	ctx := context.Background()
	l, s := fakeLinear(t, lineartest.Options{})
	k := linearCreate(t, l, "t", "")
	cid, err := l.AddComment(ctx, k, "<!-- rota:proof -->\nsmall")
	if err != nil {
		t.Fatal(err)
	}
	huge := strings.Repeat("x", 64<<10)
	n := len(s.Requests())
	if _, err := l.Create(ctx, "big", "text\n\n"+marker.FieldsBlock([]string{"Related: " + huge}), nil, ""); err == nil {
		t.Error("Create with an oversized fields block succeeded")
	}
	if _, err := l.AddComment(ctx, k, "<!-- rota:proof -->\n"+huge); err == nil {
		t.Error("AddComment with an oversized state succeeded")
	}
	if err := l.EditComment(ctx, k, cid, "<!-- rota:proof -->\n"+huge); err == nil {
		t.Error("EditComment with an oversized state succeeded")
	}
	for _, op := range since(s, n) {
		if op == "IssueCreate" || op == "CommentCreate" || op == "CommentUpdate" || op == "AttachmentCreate" {
			t.Errorf("an oversized body still wrote %s: %v", op, since(s, n))
		}
	}
	if got := s.CommentBodies(string(k)); len(got) != 1 || got[0] != "small" {
		t.Errorf("visible comments %q, want the one small note unchanged", got)
	}
}

// A rollback that fails too names what it left behind, so it can be cleaned up.
func TestLinearRollbackFailureNamesTheOrphan(t *testing.T) {
	ctx := context.Background()
	l, s := fakeLinear(t, lineartest.Options{})
	k := linearCreate(t, l, "t", "")

	s.ErrorOn("AttachmentCreate", "INTERNAL", "boom")
	if _, err := l.Create(ctx, "half", "text\n\n"+marker.FieldsBlock([]string{"Priority: P1"}), nil, ""); err == nil || strings.Contains(err.Error(), "ENG-2") {
		t.Fatalf("a rolled-back Create = %v, want the attachment error alone", err)
	}
	if _, err := l.Get(ctx, "ENG-2", false); !IsKind(err, KindNotFound) {
		t.Fatalf("the half-made issue survived its rollback: %v", err)
	}

	s.ErrorOn("AttachmentCreate", "INTERNAL", "boom")
	s.ErrorOn("IssueDelete", "INTERNAL", "no delete")
	_, err := l.Create(ctx, "orphan", "text\n\n"+marker.FieldsBlock([]string{"Priority: P1"}), nil, "")
	if err == nil || !strings.Contains(err.Error(), "ENG-3") || !strings.Contains(err.Error(), "no delete") {
		t.Fatalf("a failed rollback = %v, want it to name the orphan ENG-3", err)
	}

	s.ErrorOn("AttachmentCreate", "INTERNAL", "boom")
	s.ErrorOn("CommentDelete", "INTERNAL", "no delete")
	_, err = l.AddComment(ctx, k, "<!-- rota:proof -->\nnote")
	if got := s.CommentBodies(string(k)); len(got) != 1 {
		t.Fatalf("comments %q, want the orphan stub", got)
	}
	if err == nil || !strings.Contains(err.Error(), "ENG-1") || !strings.Contains(err.Error(), "comment") {
		t.Fatalf("a failed comment rollback = %v, want it to name the comment on ENG-1", err)
	}
}

// The state format is versioned from the start, so a later change can be told apart.
func TestLinearStateIsVersioned(t *testing.T) {
	l, s := fakeLinear(t, lineartest.Options{})
	linearCreate(t, l, "t", "text\n\n"+marker.FieldsBlock([]string{"Priority: P1"}))
	md, _ := input(shape(t, s, "AttachmentCreate"))["metadata"].(map[string]any)
	if md["v"] != float64(1) {
		t.Fatalf("metadata = %v, want v 1", md)
	}
}

// A limit cuts after every page is read and sorted, never at an early page.
func TestLinearListLimitReadsEveryPage(t *testing.T) {
	ctx := context.Background()
	l, s := fakeLinear(t, lineartest.Options{PageSize: 2})
	for i := 0; i < 5; i++ {
		linearCreate(t, l, "t", "")
	}
	n := len(s.Requests())
	got, err := l.List(ctx, ListFilter{Limit: 1})
	if err != nil || len(got) != 1 || got[0].Key != "ENG-1" {
		t.Fatalf("List limit 1 = %v, %v", got, err)
	}
	if pages := strings.Count(strings.Join(since(s, n), " "), "Issues"); pages != 3 {
		t.Fatalf("read %d pages of 5 issues at 2 a page, want all 3", pages)
	}
}
