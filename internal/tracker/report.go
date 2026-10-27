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

// ReportAuthor resolves the authenticated comment author. A marker is never
// proof of ownership; adapters without an identity seam must fail closed.
func ReportAuthor(ctx context.Context, adapter any) (string, error) {
	identity, ok := adapter.(interface {
		AuthenticatedUser(context.Context) (string, error)
	})
	if !ok {
		return "", fmt.Errorf("tracker cannot resolve the authenticated report author")
	}
	author, err := identity.AuthenticatedUser(ctx)
	if err != nil {
		return "", err
	}
	if author == "" {
		return "", fmt.Errorf("tracker returned an empty authenticated report author")
	}
	return author, nil
}

func (g *GitHub) AuthenticatedUser(ctx context.Context) (string, error) {
	var user struct {
		Login string `json:"login"`
	}
	if err := g.json(ctx, []string{"api", "user"}, &user); err != nil {
		return "", err
	}
	return user.Login, nil
}

func (g *GitLab) AuthenticatedUser(ctx context.Context) (string, error) { return g.username(ctx) }

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
	var author string
	for _, note := range notes {
		line, _, _ := strings.Cut(note.Body, "\n")
		if line != key {
			continue
		}
		if author == "" {
			author, err = ReportAuthor(ctx, forge)
			if err != nil {
				return err
			}
		}
		if note.Author != author {
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
