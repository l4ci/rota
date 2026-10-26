package tracker

import (
	"context"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/marker"
)

// MRNoteEditor edits a PR conversation comment or merge request note in place.
type MRNoteEditor interface {
	EditMRNote(ctx context.Context, pr int, commentID, body string) error
}

// PostReport upserts the rendered report on the PR, keyed by its first marker line.
func PostReport(ctx context.Context, forge Forge, pr int, markdown string) error {
	key, _, _ := strings.Cut(markdown, "\n")
	if !strings.HasPrefix(key, marker.Prefix+marker.KindReport+" ") || !strings.HasSuffix(key, " -->") {
		return fmt.Errorf("report must start with a rota report marker")
	}
	notes, err := forge.MRNotes(ctx, pr)
	if err != nil {
		return err
	}
	for _, note := range notes {
		line, _, _ := strings.Cut(note.Body, "\n")
		if line != key {
			continue
		}
		if note.Body == markdown {
			return nil
		}
		editor, ok := forge.(MRNoteEditor)
		if !ok {
			return fmt.Errorf("forge cannot edit the existing report comment %s", note.ID)
		}
		return editor.EditMRNote(ctx, pr, note.ID, markdown)
	}
	_, err = forge.AddMRNote(ctx, pr, markdown)
	return err
}

// EditMRNote edits the issue comment containing the GitHub PR conversation note.
func (g *GitHub) EditMRNote(ctx context.Context, pr int, commentID, body string) error {
	return g.EditComment(ctx, NumKey(pr), commentID, body)
}

// EditMRNote updates a GitLab merge request note, which has a separate API
// endpoint from the issue notes handled by EditComment.
func (g *GitLab) EditMRNote(ctx context.Context, pr int, commentID, body string) error {
	_, err := g.run(ctx, []string{"api", "-X", "PUT", fmt.Sprintf("projects/:id/merge_requests/%d/notes/%s", pr, commentID), "-f", "body=" + body}, "")
	return err
}
