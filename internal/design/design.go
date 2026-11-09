// Package design holds the per-item design documents (rota design add, show,
// put, rm). A design is one text per item, kept by a Store: a file under
// .rota/designs/<ID>.md (Files) or a `design` note on the item's issue
// (NewNotes). The verbs here are the same for both.
package design

import (
	"errors"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/section"
	"github.com/l4ci/rota/internal/stamp"
)

// Store is where designs live. Errors are exitcode.Errors: Create is exit 4
// when the design exists, Read, Replace and Remove are exit 3 when it does not.
type Store interface {
	// CheckID is the ID rule of the store, applied before it is touched: file
	// designs are minted as B07 and addressed by that spelling, the issue
	// backend takes any item reference the backlog reads.
	CheckID(id string) error
	Create(id, text string) error
	Read(id string) (string, error)
	Replace(id, text string) (changed bool, err error)
	Remove(id string) error
	// Update rewrites an existing design from its stored text; changed is
	// false when fn returns the text it was given. fn's error aborts the write.
	Update(id string, fn func(old string) (string, error)) (changed bool, err error)
}

// Add creates the design stub, dated by today; an existing design is exit 4.
func Add(s Store, id, title string, today func() time.Time) error {
	if err := s.CheckID(id); err != nil {
		return err
	}
	return s.Create(id, stubText(normID(id), title, stamp.Date(today())))
}

// Show is the stored design.
func Show(s Store, id string) (string, error) {
	if err := s.CheckID(id); err != nil {
		return "", err
	}
	return s.Read(id)
}

// Put replaces an existing design's text; changed is false when the text is
// already identical.
func Put(s Store, id, text string) (bool, error) {
	if err := s.CheckID(id); err != nil {
		return false, err
	}
	changed, err := s.Replace(id, text)
	var ae *exitcode.Error
	if errors.As(err, &ae) && ae.Exit == exitcode.ExitResolution {
		ae.WithHint("rota design add " + id + " --title <text>")
	}
	return changed, err
}

// Rm deletes a design; a missing one is exit 3, never a no-op.
func Rm(s Store, id string) error {
	if err := s.CheckID(id); err != nil {
		return err
	}
	return s.Remove(id)
}

// normID is the item reference in its one spelling: no "#", upper case. The
// stub carries it too, since a "#" would turn the front-matter id into a YAML
// comment.
func normID(id string) string {
	if r, err := backlog.ParseIssueRef(id); err == nil {
		return r.ID
	}
	return id
}

// stubText is the starter text of a design.
func stubText(id, title, date string) string {
	return "---\nid: " + id + "\ntitle: " + title + "\nstatus: draft\ncreated: " + date + "\n---\n\n# " + id + " — " + title + `

## Goal

_(one sentence — what shipping this design means)_

## Design

_(3–8 sentences — the chosen shape, the moving parts, where they live)_

## Approaches considered

_(2–3 alternatives weighed with Pros / Cons / Why this might or might not be the right answer)_

## Open questions

_(unresolved questions to answer before /rota-plan or during execution)_

## Assumptions

_(named assumptions made implicit by the chosen design)_
`
}

// Amend replaces or appends to the body of "## <heading>" in a design: trailing
// newlines of text are dropped and the block is "\n<text>\n\n". mode is
// "append" or "replace". A missing design or heading is exit 3. changed is
// false when the design would not change.
func Amend(s Store, id, heading, mode, text string) (changed bool, err error) {
	if err = s.CheckID(id); err != nil {
		return
	}
	if mode != "append" && mode != "replace" {
		return false, exitcode.Errf(exitcode.ExitUsage, "mode must be 'append' or 'replace', got %q", mode)
	}
	block := "\n" + strings.TrimRight(text, "\n") + "\n\n"
	return s.Update(id, func(content string) (string, error) {
		if _, _, ok := section.Find(content, heading); !ok {
			return "", exitcode.Errf(exitcode.ExitResolution, "section '## %s' not found in design %s", heading, normID(id))
		}
		if mode == "replace" {
			return section.Replace(content, heading, block), nil
		}
		return section.Append(content, heading, block), nil
	})
}
