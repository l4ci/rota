package design

import (
	"regexp"
	"time"

	"github.com/l4ci/rota/internal/artifact"
)

// Issue mode: the design is a `design` note on the item's issue, and the
// ID is an item ID with any number of digits ([BFT]\d+).

var issueIDRe = regexp.MustCompile(`^[BFT]\d+$`)

// ValidIssueID reports whether id is an issue-mode design ID: [BFT]\d+.
func ValidIssueID(id string) bool { return issueIDRe.MatchString(id) }

func checkIssue(id string) error {
	if !ValidIssueID(id) {
		return artifact.Errf(artifact.ExitUsage, "ID must match [BFT]\\d+ (e.g. B7, F3, T11); designs are per-item, not per-slice or per-milestone, got %q", id)
	}
	return nil
}

func noteMissing(id string) *artifact.Error {
	return artifact.Errf(artifact.ExitResolution, "design note for %s not found", id)
}

// Option changes what Add and AddNote write.
type Option func(*options)

type options struct{ auto bool }

// Auto marks the design as written by a loop run: `auto: true` after status.
func Auto() Option { return func(o *options) { o.auto = true } }

func collect(opts []Option) (o options) {
	for _, f := range opts {
		f(&o)
	}
	return
}

// stubText is the starter text of a design (the same in both modes).
func stubText(id, title, date string, o options) string {
	auto := ""
	if o.auto {
		auto = "auto: true\n"
	}
	return "---\nid: " + id + "\ntitle: " + title + "\nstatus: draft\n" + auto + "created: " + date + "\n---\n\n# " + id + " — " + title + `

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

// AddNote creates the design note on the item's issue; an existing one is
// exit 4. ref is the item as the tracker resolves it, id as the caller typed it.
func AddNote(n artifact.Notes, ref, id, title string, opts ...Option) error {
	if err := checkIssue(id); err != nil {
		return err
	}
	if _, ok, err := n.NoteGet(ref, "design"); err != nil {
		return err
	} else if ok {
		return artifact.Errf(artifact.ExitRefused, "design note for %s already exists", id)
	}
	_, err := n.NotePut(ref, "design", stubText(id, title, time.Now().Format("2006-01-02"), collect(opts)))
	return err
}

// ShowNote is the design note; the old helper printed it with a newline.
func ShowNote(n artifact.Notes, ref, id string) (string, error) {
	if err := checkIssue(id); err != nil {
		return "", err
	}
	text, ok, err := n.NoteGet(ref, "design")
	if err != nil {
		return "", err
	}
	if !ok {
		return "", noteMissing(id)
	}
	return text + "\n", nil
}

// PutNote replaces an existing design note; changed is false when it
// already reads as text.
func PutNote(n artifact.Notes, ref, id, text string) (bool, error) {
	if err := checkIssue(id); err != nil {
		return false, err
	}
	if _, ok, err := n.NoteGet(ref, "design"); err != nil {
		return false, err
	} else if !ok {
		return false, noteMissing(id).WithHint("rota design add " + id + " --title <text>")
	}
	return n.NotePut(ref, "design", text)
}

// RmNote deletes the design note; a missing one is exit 3.
func RmNote(n artifact.Notes, ref, id string) error {
	if err := checkIssue(id); err != nil {
		return err
	}
	removed, err := n.NoteRm(ref, "design")
	if err != nil {
		return err
	}
	if !removed {
		return noteMissing(id)
	}
	return nil
}
