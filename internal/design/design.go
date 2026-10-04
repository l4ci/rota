// Package design holds the per-item design documents (rota design add, show,
// put, rm). A design is one text per item, kept by a Store: a file under
// .rota/designs/<ID>.md (Files) or a `design` note on the item's issue
// (NewNotes). The verbs here are the same for both.
package design

import (
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/l4ci/rota/internal/artifact"
)

// Store is where designs live. Errors are artifact.Errors: Create is exit 4
// when the design exists, Read, Replace and Remove are exit 3 when it does not.
type Store interface {
	// Digits is the fewest digits an ID may carry: file designs are minted
	// as B07 (2), issue numbers have any count (1).
	Digits() int
	Create(id, text string) error
	Read(id string) (string, error)
	Replace(id, text string) (changed bool, err error)
	Remove(id string) error
}

// Type is the item type letter of a valid ID.
func Type(id string) string { return id[:1] }

func idRe(digits int) *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(`^[BFT]\d{%d,}$`, digits))
}

// CheckID is the ID rule every verb applies before it touches the store.
func CheckID(s Store, id string) error {
	if idRe(s.Digits()).MatchString(id) {
		return nil
	}
	if s.Digits() <= 1 {
		return artifact.Errf(artifact.ExitUsage, "ID must match [BFT]\\d+ (e.g. B7, F3, T11); designs are per-item, not per-slice or per-milestone, got %q", id)
	}
	return artifact.Errf(artifact.ExitUsage, "ID must match [BFT]\\d{%d,} (e.g. B07, F03, T11); designs are per-item, not per-slice or per-milestone, got %q", s.Digits(), id)
}

// Add creates the design stub; an existing design is exit 4.
func Add(s Store, id, title string) error {
	if err := CheckID(s, id); err != nil {
		return err
	}
	return s.Create(id, stubText(id, title, time.Now().Format("2006-01-02")))
}

// Show is the stored design.
func Show(s Store, id string) (string, error) {
	if err := CheckID(s, id); err != nil {
		return "", err
	}
	return s.Read(id)
}

// Put replaces an existing design's text; changed is false when the text is
// already identical.
func Put(s Store, id, text string) (bool, error) {
	if err := CheckID(s, id); err != nil {
		return false, err
	}
	changed, err := s.Replace(id, text)
	var ae *artifact.Error
	if errors.As(err, &ae) && ae.Exit == artifact.ExitResolution {
		ae.WithHint("rota design add " + id + " --title <text>")
	}
	return changed, err
}

// Rm deletes a design; a missing one is exit 3, never a no-op.
func Rm(s Store, id string) error {
	if err := CheckID(s, id); err != nil {
		return err
	}
	return s.Remove(id)
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
