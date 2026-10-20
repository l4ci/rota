package tracker

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tracker/itsaplantest"
)

const fieldsBlock = "<!-- rota:fields\nSince: abc\n-->"

func TestItsaplanCreateGetEditRoundTrip(t *testing.T) {
	ctx := context.Background()
	it, s := fakeItsaplan(t, itsaplantest.Options{})
	key, err := it.Create(ctx, "Widen", "the text\n\n"+fieldsBlock, []string{"bug", "p1"}, "")
	if err != nil || key != "MKT-1" {
		t.Fatalf("%s %v", key, err)
	}
	if got := s.Description("MKT-1"); got != "the text" {
		t.Errorf("description %q: the fields block belongs in the custom field", got)
	}
	if got := s.Field("MKT-1"); got != fieldsBlock {
		t.Errorf("rota field %q", got)
	}
	if !s.HasField() || !slices.Equal(s.Labels("MKT-1"), []string{"bug", "p1"}) || s.Column("MKT-1") != 2 {
		t.Errorf("field %v labels %v column %d", s.HasField(), s.Labels("MKT-1"), s.Column("MKT-1"))
	}
	is, err := it.Get(ctx, key, false)
	if err != nil || !strings.HasSuffix(is.Body, fieldsBlock) || !strings.HasPrefix(is.Body, "the text") {
		t.Fatalf("body %q %v", is.Body, err)
	}
	title, body := "Widen more", "new text\n\n<!-- rota:fields\nSince: def\n-->"
	if err := it.Edit(ctx, key, IssueEdit{Title: &title, Body: &body, AddLabels: []string{"p2"}, RemoveLabels: []string{"p1"}}); err != nil {
		t.Fatal(err)
	}
	if is, _ = it.Get(ctx, key, false); is.Title != title || !strings.Contains(is.Body, "Since: def") || !slices.Equal(is.Labels, []string{"bug", "p2"}) {
		t.Errorf("after edit: %+v", is)
	}
	plain := "no block now"
	if err := it.Edit(ctx, key, IssueEdit{Body: &plain}); err != nil {
		t.Fatal(err)
	}
	if got := s.Field("MKT-1"); got != "" {
		t.Errorf("a body with no block must clear the field, left %q", got)
	}
	if _, err := it.Create(ctx, "x", "", nil, "M01"); err == nil {
		t.Error("a milestone was accepted")
	}
}

// A human edit in the web editor rewrites the description and may drop the
// HTML comment; the block lives in a custom field the editor leaves alone.
func TestItsaplanFieldsSurviveDescriptionEdit(t *testing.T) {
	ctx := context.Background()
	it, s := fakeItsaplan(t, itsaplantest.Options{})
	key, _ := it.Create(ctx, "Widen", "text\n\n"+fieldsBlock, nil, "")
	s.SetDescription("MKT-1", "<p>text, rewritten by a person</p>")
	is, err := it.Get(ctx, key, false)
	if err != nil || !strings.Contains(is.Body, "rewritten by a person") || !strings.HasSuffix(is.Body, fieldsBlock) {
		t.Errorf("%q %v", is.Body, err)
	}
}

// The project has no rota field: it is made on first need; where the key may
// not make it, Create fails before the issue exists.
func TestItsaplanCreatesTheFieldOrRefusesBeforeTheIssue(t *testing.T) {
	ctx := context.Background()
	it, s := fakeItsaplan(t, itsaplantest.Options{NoField: true})
	if _, err := it.Create(ctx, "plain", "no block", nil, ""); err != nil || s.HasField() {
		t.Fatalf("an issue with no block needs no field: %v %v", err, s.HasField())
	}
	if _, err := it.Create(ctx, "fielded", "x\n\n"+fieldsBlock, nil, ""); err != nil || !s.HasField() || s.Field("MKT-2") != fieldsBlock {
		t.Fatalf("%v field %v %q", err, s.HasField(), s.Field("MKT-2"))
	}
	it2, s2 := fakeItsaplan(t, itsaplantest.Options{NoField: true, NoFieldRight: true})
	if _, err := it2.Create(ctx, "fielded", "x\n\n"+fieldsBlock, nil, ""); !IsKind(err, KindUnavailable) || s2.Issues() != 0 {
		t.Errorf("%v, %d issues made", err, s2.Issues())
	}
}

// A column renamed by a project admin must not change which one is closed:
// Close and Reopen pick by stateType.
func TestItsaplanCloseByStateType(t *testing.T) {
	ctx := context.Background()
	it, s := fakeItsaplan(t, itsaplantest.Options{Columns: []itsaplantest.Column{
		{ID: 7, Name: "Done", StateType: "started"}, {ID: 8, Name: "Shipped", StateType: "completed"},
		{ID: 9, Name: "Dropped", StateType: "canceled"}, {ID: 10, Name: "Open", StateType: "unstarted"}}})
	key, _ := it.Create(ctx, "a", "", nil, "")
	if s.Column("MKT-1") != 10 {
		t.Fatalf("created in column %d", s.Column("MKT-1"))
	}
	if err := it.Close(ctx, key, CloseCompleted, "done in abc"); err != nil || s.Column("MKT-1") != 8 {
		t.Fatalf("close: %v column %d", err, s.Column("MKT-1"))
	}
	if got := s.CommentBodies("MKT-1"); !slices.Equal(got, []string{"done in abc"}) {
		t.Errorf("comments %v", got)
	}
	if err := it.Close(ctx, key, CloseCompleted, ""); err != nil || s.Column("MKT-1") != 8 {
		t.Errorf("an issue already completed is left alone: %v column %d", err, s.Column("MKT-1"))
	}
	if err := it.Reopen(ctx, key); err != nil || s.Column("MKT-1") != 10 {
		t.Errorf("reopen: %v column %d", err, s.Column("MKT-1"))
	}
	if err := it.Close(ctx, key, CloseNotPlanned, ""); err != nil || s.Column("MKT-1") != 9 {
		t.Errorf("not planned: %v column %d", err, s.Column("MKT-1"))
	}
	if err := it.Reopen(ctx, key); err != nil || s.Column("MKT-1") != 10 {
		t.Errorf("reopen canceled: %v column %d", err, s.Column("MKT-1"))
	}
	bare, _ := fakeItsaplan(t, itsaplantest.Options{Columns: []itsaplantest.Column{{ID: 1, Name: "Todo", StateType: "unstarted"}}})
	k, _ := bare.Create(ctx, "a", "", nil, "")
	if err := bare.Close(ctx, k, CloseCompleted, ""); err == nil || !strings.Contains(err.Error(), "no completed column") {
		t.Errorf("no completed column: %v", err)
	}
}

// AddLabels uses the bulk route, which adds: a label another writer put on the
// issue first is kept.
func TestItsaplanAddLabelsKeepsOthers(t *testing.T) {
	ctx := context.Background()
	it, s := fakeItsaplan(t, itsaplantest.Options{})
	key, _ := it.Create(ctx, "a", "", []string{"bug"}, "")
	s.SetLabels("MKT-1", "bug", "other")
	if err := it.AddLabels(ctx, key, []string{"in-progress", "bug"}, true); err != nil {
		t.Fatal(err)
	}
	if got := s.Labels("MKT-1"); !slices.Equal(got, []string{"bug", "other", "in-progress"}) {
		t.Errorf("labels %v", got)
	}
	if err := it.AddLabels(ctx, key, []string{"nope"}, false); !IsKind(err, KindNotFound) {
		t.Errorf("a missing label with autoCreate off: %v", err)
	}
	if err := it.EnsureLabels(ctx, []string{"made"}, true); err != nil {
		t.Fatal(err)
	}
}

// An empty label set clears every label, so removing the last one must send
// [] on purpose; a name no one made is a no-op.
func TestItsaplanRemoveLastLabelSendsEmptySet(t *testing.T) {
	ctx := context.Background()
	it, s := fakeItsaplan(t, itsaplantest.Options{})
	key, _ := it.Create(ctx, "a", "", []string{"only"}, "")
	if err := it.RemoveLabels(ctx, key, []string{"ghost"}); err != nil || !slices.Equal(s.Labels("MKT-1"), []string{"only"}) {
		t.Fatalf("a label that does not exist: %v %v", err, s.Labels("MKT-1"))
	}
	if err := it.RemoveLabels(ctx, key, []string{"ONLY"}); err != nil || len(s.Labels("MKT-1")) != 0 {
		t.Errorf("%v labels %v", err, s.Labels("MKT-1"))
	}
	if err := it.RemoveLabels(ctx, key, []string{"only"}); err != nil {
		t.Errorf("removing a label the issue lacks: %v", err)
	}
}

// RemoveLabels replaces the whole set, so another writer's PATCH that lands on
// top of it undoes it: the second read sees that and writes once more.
func TestItsaplanRemoveLabelsRetriesAfterAnOverwrite(t *testing.T) {
	ctx := context.Background()
	it, s := fakeItsaplan(t, itsaplantest.Options{})
	key, _ := it.Create(ctx, "a", "", []string{"a", "b", "c"}, "")
	overwrote := false
	s.After = func(req string) {
		if req == "PATCH /issues/1" && !overwrote {
			overwrote = true
			s.SetLabels("MKT-1", "a", "b", "c", "d") // a concurrent full-set write
		}
	}
	if err := it.RemoveLabels(ctx, key, []string{"b"}); err != nil {
		t.Fatal(err)
	}
	if got := s.Labels("MKT-1"); !slices.Equal(got, []string{"a", "c", "d"}) {
		t.Errorf("labels %v: b gone, the concurrent d kept", got)
	}
	// a writer that always wins is an error, not a silent loss
	s.After = func(req string) {
		if req == "PATCH /issues/1" {
			s.SetLabels("MKT-1", "a", "b", "c", "d")
		}
	}
	if err := it.RemoveLabels(ctx, key, []string{"a"}); err == nil || !strings.Contains(err.Error(), "kept changing its labels") {
		t.Errorf("%v", err)
	}
}

func TestItsaplanCommentsCRUDKeepMarkers(t *testing.T) {
	ctx := context.Background()
	it, s := fakeItsaplan(t, itsaplantest.Options{})
	key, _ := it.Create(ctx, "a", "", nil, "")
	marked := "<!-- rota:feedback -->\nIn progress"
	id, err := it.AddComment(ctx, key, marked)
	if err != nil || id == "" {
		t.Fatalf("%q %v", id, err)
	}
	cs, _ := it.Comments(ctx, key)
	if len(cs) != 1 || cs[0].Body != marked || cs[0].ID != id {
		t.Fatalf("%+v", cs)
	}
	if err := it.EditComment(ctx, key, id, "edited"); err != nil || !slices.Equal(s.CommentBodies("MKT-1"), []string{"edited"}) {
		t.Errorf("%v %v", err, s.CommentBodies("MKT-1"))
	}
	if u, err := it.CommentURL(ctx, key, id); err != nil || u != "" {
		t.Errorf("%q %v", u, err)
	}
	if err := it.DeleteComment(ctx, key, id); err != nil || len(s.CommentBodies("MKT-1")) != 0 {
		t.Errorf("%v %v", err, s.CommentBodies("MKT-1"))
	}
	if err := it.DeleteComment(ctx, key, id); !IsKind(err, KindNotFound) {
		t.Errorf("deleting a comment twice: %v", err)
	}
	for _, bad := range []string{"abc", "0", ""} {
		if err := it.EditComment(ctx, key, bad, "x"); !IsKind(err, KindNotFound) {
			t.Errorf("comment id %q: %v", bad, err)
		}
	}
	if err := it.EditComment(ctx, "OTHER-1", "1", "x"); !IsKind(err, KindNotFound) {
		t.Errorf("a key of another project: %v", err)
	}
}
