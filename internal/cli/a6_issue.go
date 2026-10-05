package cli

import (
	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/jsonx"
)

// Issue-backend helpers the plan store and the milestone verbs share. Items
// resolve through the A4 workflow (so "F7", "#7" and "7" all answer "7" and
// an unknown item is exit 3).

// modeRoot is the project root and whether backlog.backend is "issues".
func modeRoot(c *Ctx) (root string, issue bool, err error) {
	root, err = c.Root()
	if err != nil {
		return "", false, err
	}
	return root, artifact.IssueMode(root), nil
}

// failAny maps a domain error (artifact or backlog) onto the exit table,
// keeping the exit-4 refusal data a duplicate create carries.
func failAny(err error) (Result, error) {
	if ae := asArtifact(err); ae != nil {
		return Result{Data: refusal(err)}, err
	}
	return a4Fail(err)
}

func typedData(id, typ string, changed any) *jsonx.Object {
	d := jsonx.NewObject()
	d.Set("id", id)
	d.Set("type", typ)
	if changed != nil {
		d.Set("changed", changed)
	}
	return d
}

// issuesBackend opens the issue backend for verbs that address a milestone
// rather than an item; duplicate-tracking-issue notices go to stderr and
// into the envelope's warnings.
func issuesBackend(c *Ctx) (*backlog.Issues, error) {
	be, err := openIssues(c)
	if err != nil {
		return nil, err
	}
	switch b := be.(type) {
	case *backlog.Issues:
		b.Warn = func(msg string) { c.Warn("%s", msg) }
		return b, nil
	case *backlog.Umbrella:
		// Slice plans, and the plan of a milestone, live on the home sub-repo.
		home, err := b.HomeSub()
		if err != nil {
			return nil, err
		}
		home.Warn = func(msg string) { c.Warn("%s", msg) }
		return home, nil
	}
	return nil, Refused("%s works on the issue backend only", c.Path)
}

// openIssues opens the backlog backend for a verb that addresses a milestone
// rather than an item.
func openIssues(c *Ctx) (backlog.Backend, error) {
	root, err := a4Scope(c)
	if err != nil {
		return nil, err
	}
	return a4Open(c, root, false, "")
}
