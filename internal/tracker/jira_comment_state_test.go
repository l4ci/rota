package tracker

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tracker/jiratest"
)

// A multi-part store write that fails part-way must leave the shown text and
// the stored body agreeing: old/old, never new/old or a torn mix.
func TestJiraMultiPartStoreFailureStaysConsistent(t *testing.T) {
	ctx := context.Background()
	oldBody := "<!-- rota:proof -->\nOLD " + strings.Repeat("alpha ", 12000)
	newBody := "<!-- rota:proof -->\nNEW " + strings.Repeat("omega ", 12000)
	shrunk := "<!-- rota:proof -->\nNEW short"
	prop := func(id string, part int) string { return "/properties/" + commentProp(id, part) }

	check := func(t *testing.T, j *Jira, s *jiratest.Server, k IssueKey, id, wantStored string, visibleBefore []string) {
		t.Helper()
		cs, err := j.Comments(ctx, k)
		if err != nil || len(cs) != 1 {
			t.Fatalf("Comments: %v, %d", err, len(cs))
		}
		if cs[0].Body != wantStored {
			t.Errorf("stored body is torn or new: got %d bytes, want the old %d", len(cs[0].Body), len(wantStored))
		}
		if got := s.CommentBodies(string(k)); strings.Join(got, "|") != strings.Join(visibleBefore, "|") {
			t.Errorf("shown text changed:\n got %v\nwant %v", got, visibleBefore)
		}
	}

	t.Run("a middle part fails", func(t *testing.T) {
		eachJira(t, jiratest.Options{}, func(t *testing.T, j *Jira, s *jiratest.Server) {
			k := mustCreate(t, j, "t", "", nil, "")
			id, err := j.AddComment(ctx, k, oldBody)
			if err != nil {
				t.Fatal(err)
			}
			before := s.CommentBodies(string(k))
			s.FailRequest("PUT /issue/"+string(k)+prop(id, 1), 400)
			if err := j.EditComment(ctx, k, id, newBody); err == nil {
				t.Fatal("edit succeeded despite the injected failure")
			}
			check(t, j, s, k, id, oldBody, before)
		})
	})

	t.Run("dropping the leftover parts fails", func(t *testing.T) {
		eachJira(t, jiratest.Options{}, func(t *testing.T, j *Jira, s *jiratest.Server) {
			k := mustCreate(t, j, "t", "", nil, "")
			id, err := j.AddComment(ctx, k, oldBody)
			if err != nil {
				t.Fatal(err)
			}
			before := s.CommentBodies(string(k))
			s.FailRequest("DELETE /issue/"+string(k)+prop(id, 1), 400)
			if err := j.EditComment(ctx, k, id, shrunk); err == nil {
				t.Fatal("edit succeeded despite the injected failure")
			}
			check(t, j, s, k, id, oldBody, before)
		})
	})

	t.Run("dropping the state of an unmarked edit fails", func(t *testing.T) {
		eachJira(t, jiratest.Options{}, func(t *testing.T, j *Jira, s *jiratest.Server) {
			k := mustCreate(t, j, "t", "", nil, "")
			id, err := j.AddComment(ctx, k, oldBody)
			if err != nil {
				t.Fatal(err)
			}
			before := s.CommentBodies(string(k))
			s.FailRequest("DELETE /issue/"+string(k)+prop(id, 1), 400)
			if err := j.EditComment(ctx, k, id, "plain text, no marker"); err == nil {
				t.Fatal("edit succeeded despite the injected failure")
			}
			check(t, j, s, k, id, oldBody, before)
		})
	})
}

// A failed Add rollback reports the store error and the rollback error.
func TestJiraAddRollbackFailureReportsBoth(t *testing.T) {
	ctx := context.Background()
	body := "<!-- rota:proof -->\nOLD " + strings.Repeat("alpha ", 12000)
	eachJira(t, jiratest.Options{}, func(t *testing.T, j *Jira, s *jiratest.Server) {
		k := mustCreate(t, j, "t", "", nil, "")
		s.FailRequest("PUT /issue/"+string(k)+"/properties/", 400)
		s.FailRequest("DELETE /issue/"+string(k)+"/comment/", 400)
		_, err := j.AddComment(ctx, k, body)
		if err == nil {
			t.Fatal("AddComment succeeded")
		}
		msg := err.Error()
		if !strings.Contains(msg, "injected failure") || !strings.Contains(msg, "delete it in Jira") {
			t.Errorf("error does not name both failures: %v", err)
		}
	})
}

// The shown text a failed edit restores is the stored source, not the
// markdown the tracker lists back. Only Cloud (ADF) round-trips lossily.
func TestJiraEditRestoresStoredSource(t *testing.T) {
	ctx := context.Background()
	// a code span holding a backtick: the listed markdown breaks it, so a restore from the listing differs
	old := "<!-- rota:proof -->\nproof row\n\n`` a`b ``"
	eachJira(t, jiratest.Options{}, func(t *testing.T, j *Jira, s *jiratest.Server) {
		k := mustCreate(t, j, "t", "", nil, "")
		id, err := j.AddComment(ctx, k, old)
		if err != nil {
			t.Fatal(err)
		}
		before := s.CommentBodies(string(k))
		if j.cloud {
			listed := j.text(json.RawMessage(before[0]))
			if listed == shownText(old) {
				t.Fatal("sample survives the markdown round trip: the test cannot tell the stored source from the listed text")
			}
		}
		s.FailRequest("PUT /issue/"+string(k)+"/properties/", 400)
		if err := j.EditComment(ctx, k, id, "<!-- rota:proof -->\nnew"); err == nil {
			t.Fatal("edit succeeded")
		}
		if got := s.CommentBodies(string(k)); strings.Join(got, "|") != strings.Join(before, "|") {
			t.Errorf("restored text differs from the original:\n got %v\nwant %v", got, before)
		}
	})
}

// When the old stored body cannot be read, nothing is written: a failed write
// could not be undone and the stored body must survive.
func TestJiraUnreadableOldStateIsNotOverwritten(t *testing.T) {
	ctx := context.Background()
	oldBody := "<!-- rota:proof -->\nOLD " + strings.Repeat("alpha ", 12000)
	newBody := "<!-- rota:proof -->\nNEW " + strings.Repeat("omega ", 12000)
	eachJira(t, jiratest.Options{}, func(t *testing.T, j *Jira, s *jiratest.Server) {
		k := mustCreate(t, j, "t", "", nil, "")
		id, err := j.AddComment(ctx, k, oldBody)
		if err != nil {
			t.Fatal(err)
		}
		// the edit reads the old state twice: for the text to restore, then for the write
		s.FailRequest("GET /issue/"+string(k)+"/properties/"+commentProp(id, 1), 400)
		s.FailRequest("GET /issue/"+string(k)+"/properties/"+commentProp(id, 1), 400)
		s.FailRequest("PUT /issue/"+string(k)+"/properties/", 400)
		if err := j.EditComment(ctx, k, id, newBody); err == nil {
			t.Fatal("edit succeeded")
		}
		cs, err := j.Comments(ctx, k)
		if err != nil || len(cs) != 1 || cs[0].Body != oldBody {
			t.Fatalf("stored body lost: err %v, %d comments", err, len(cs))
		}
	})
}
