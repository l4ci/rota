// Package plan holds the milestone and item plans (rota plan add, list, show,
// put, rm): one text per key <milestone>-<unit> (M01-B07, M01-S03), kept by a
// Store: files under .rota/plans/<key>.md (Files), or notes on the issue
// backend (NewNotes), where an item plan is a `plan` note on the item's issue
// and a slice plan a note on the milestone's tracking issue.
package plan

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"context"
	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/pystr"
)

// Store is where plans live. Errors are exitcode.Errors: Create is exit 4 when
// the plan exists, Read, Replace and Remove are exit 3 when it does not.
type Store interface {
	// Resolve is the key rule every verb applies before it touches the store:
	// the plan the key names, or exit 2. A file plan needs its milestone
	// (M01-B07); on the issue backend an item has one plan, whatever its
	// milestone, so a bare item reference (#7, B7, PROJ-123) names it too.
	Resolve(key string) (Ref, error)
	// CheckNew is the extra rule when a plan is added: file plans are minted
	// as B07 and S02, so a shorter unit is a typo there.
	CheckNew(r Ref) error
	// DesignRef checks --design and returns the pointer the stub carries.
	// slice tells a slice plan from an item plan.
	DesignRef(slice bool, design string) (string, error)
	// Create stores the plan r names; a slice with no unit mints the next
	// S<NN>. render builds the text once the unit is settled. It returns the key.
	Create(r Ref, render func(unit string) string) (key string, err error)
	Read(key string) (string, error)
	Replace(key, text string) (changed bool, err error)
	Remove(key string) error
	List(milestone string) ([]Entry, error)
}

// Ref is what a plan key names, read once: an item (Item set), or a slice of
// a milestone (Slice set, Unit "" while still to be minted). Milestone is ""
// for a milestone-free item reference.
type Ref struct {
	Milestone string
	Slice     bool
	Unit      string           // the slice unit, S03
	Item      backlog.IssueRef // the item of an item plan
}

// UnitID is the unit of the plan: the slice unit or the item's ID.
func (r Ref) UnitID() string {
	if r.Slice {
		return r.Unit
	}
	return r.Item.ID
}

// Kind is "slice" or "item".
func (r Ref) Kind() string {
	if r.Slice {
		return "slice"
	}
	return "item"
}

var (
	qualifiedRe = regexp.MustCompile(`^(` + backlog.MilestonePattern + `)-(?:(` + backlog.SlicePattern + `)|(` + backlog.IDPattern(1) + `))$`)
	milestoneRe = regexp.MustCompile(`^` + backlog.MilestonePattern + `$`)
)

// Resolve reads a plan key: M01-S02 (a slice), M01-B07 (an item under a
// milestone), or an item reference the backlog reads (#7, B7, PROJ-123). It
// is the one place a key is split; an item's identity comes from the
// backlog's own grammar, so tracker keys keep their hyphen. Milestone-less
// refs are only valid where Store.Resolve allows them.
func Resolve(key string) (Ref, bool) {
	if m := qualifiedRe.FindStringSubmatch(key); m != nil {
		if m[2] != "" {
			return Ref{Milestone: m[1], Slice: true, Unit: m[2]}, true
		}
		if it, err := backlog.ParseIssueRef(m[3]); err == nil {
			return Ref{Milestone: m[1], Item: it}, true
		}
		return Ref{}, false
	}
	if it, err := backlog.ParseIssueRef(key); err == nil {
		return Ref{Item: it}, true
	}
	return Ref{}, false
}

// ValidMilestone reports whether s is M\d{2,}.
func ValidMilestone(s string) bool { return milestoneRe.MatchString(s) }

// AddOpts are the arguments of Add. Exactly one of Key or (Milestone with
// Slice) names the plan.
type AddOpts struct {
	Key       string
	Milestone string
	Slice     bool
	Title     string
	Design    string // design ID, "" for none
	Repos     string // comma list, "" for none
}

// parseAdd settles which plan AddOpts names. A slice that still has to be
// minted has an empty Unit.
func parseAdd(o AddOpts, s Store) (Ref, error) {
	switch {
	case o.Key != "" && (o.Slice || o.Milestone != ""):
		return Ref{}, exitcode.Errf(exitcode.ExitUsage, "a key cannot be combined with --slice or --milestone")
	case o.Key != "":
		r, err := s.Resolve(o.Key)
		if err != nil {
			return Ref{}, err
		}
		return r, s.CheckNew(r)
	case o.Slice && o.Milestone == "":
		return Ref{}, exitcode.Errf(exitcode.ExitUsage, "--slice needs --milestone <M01>")
	case o.Slice:
		if !ValidMilestone(o.Milestone) {
			return Ref{}, exitcode.Errf(exitcode.ExitUsage, "milestone must look like M01/M02/…, got %q", o.Milestone)
		}
		return Ref{Milestone: o.Milestone, Slice: true}, nil
	}
	return Ref{}, exitcode.Errf(exitcode.ExitUsage, "give a plan key (#7 or M01-B07) or --milestone <M01> --slice")
}

// extras validates --title, --design and --repos and returns the stub's
// design pointer and repo list.
func extras(root string, s Store, o AddOpts, r Ref) (design, repo string, err error) {
	if o.Title == "" {
		return "", "", exitcode.Errf(exitcode.ExitUsage, "--title is required")
	}
	if o.Design != "" {
		if design, err = s.DesignRef(r.Slice, o.Design); err != nil {
			return "", "", err
		}
	}
	if o.Repos != "" {
		names := pystr.SplitCSV(o.Repos)
		if len(names) == 0 {
			return "", "", exitcode.Errf(exitcode.ExitUsage, "--repos %q is empty after parsing", o.Repos)
		}
		regs := artifact.Repos(root)
		var missing []string
		for _, n := range names {
			if _, ok := regs[n]; !ok {
				missing = append(missing, n)
			}
		}
		if len(missing) > 0 {
			return "", "", exitcode.Errf(exitcode.ExitResolution, "--repos name(s) not in .rota/repos.json: %s", strings.Join(missing, ", "))
		}
		repo = strings.Join(names, ", ")
	}
	return design, repo, nil
}

// stub is the starter text of a plan.
func stub(key, milestone, unit, unitKind, repo, design, title string, today func() time.Time) string {
	repoLine, designLine := "", ""
	if repo != "" {
		repoLine = "repo: " + repo + "\n"
	}
	if design != "" {
		designLine = "design: " + design + "\n"
	}
	return fmt.Sprintf(`---
key: %[1]s
milestone: %[2]s
unit: %[3]s
unitKind: %[4]s
%[5]s%[6]stitle: %[7]s
status: planned
created: %[8]s
---

# %[1]s — %[7]s

## Goal

_(one sentence — what shipping this means)_

## Approach

_(3–6 sentences — the shape of the implementation, the design choice, why this over alternatives)_

## Tasks

- **T1** — _(observable behavior)_
  - Serves: _(AC-n of the item's acceptance criteria; several allowed: AC-1, AC-2)_
  - Files: _(paths the orchestrator will touch or create)_
  - Verify: _(command or manual check that proves T1 done)_

## Open questions

- _(unresolved decisions — answer before or during execution)_

## Assumptions

- _(named assumptions made implicit by the approach)_

## Relies on

- _(KNOWLEDGE <Topic>: **<title>**, or DECISIONS <title>: each entry the plan depends on; none if neither)_
`, key, milestone, unit, unitKind, repoLine, designLine, title, today().Format("2006-01-02"))
}

// Add creates a plan and returns its key and kind ("slice"|"item"). root is
// where --repos names are looked up.
func Add(root string, s Store, o AddOpts, today func() time.Time) (key, unitKind string, err error) {
	r, err := parseAdd(o, s)
	if err != nil {
		return "", "", err
	}
	design, repo, err := extras(root, s, o, r)
	if err != nil {
		return "", "", err
	}
	key, err = s.Create(r, func(unit string) string {
		k := r.Milestone + "-" + unit
		if r.Milestone == "" { // milestone-free: the key is the item ref as given
			k = o.Key
		}
		return stub(k, r.Milestone, unit, r.Kind(), repo, design, o.Title, today)
	})
	if err != nil {
		return "", "", err
	}
	if r.Milestone == "" { // milestone-free: the key is the item ref as given
		return o.Key, "item", nil
	}
	return key, r.Kind(), nil
}

// Entry is one row of List.
type Entry struct {
	Key, Milestone, Unit, UnitKind, Title, Status, Created string
	Repos                                                  []string
}

// List is the stored plans, optionally only one milestone's.
func List(s Store, milestone string) ([]Entry, error) { return s.List(milestone) }

// Show is the stored plan.
func Show(s Store, key string) (string, error) {
	if _, err := s.Resolve(key); err != nil {
		return "", err
	}
	return s.Read(key)
}

// Put replaces an existing plan's text; changed is false when identical.
func Put(s Store, key, text string) (bool, error) {
	if _, err := s.Resolve(key); err != nil {
		return false, err
	}
	changed, err := s.Replace(key, text)
	var ae *exitcode.Error
	if errors.As(err, &ae) && ae.Exit == exitcode.ExitResolution {
		ae.WithHint("rota plan add " + key + " --title <text>")
	}
	return changed, err
}

// Rm deletes a plan; a missing one is exit 3, never a no-op.
func Rm(s Store, key string) error {
	if _, err := s.Resolve(key); err != nil {
		return err
	}
	return s.Remove(key)
}

// RenameCheck lists the files git tracks that mention old (a git-grep basic
// regex), limited to pathspecs. No match, a non-git directory and git
// failures all give an empty list, as the old helper swallowed them.
func RenameCheck(dir, old string, pathspecs []string) []string {
	args := []string{"grep", "-l", "--", old}
	if len(pathspecs) > 0 {
		args = append(args, "--")
		args = append(args, pathspecs...)
	}
	res, _ := git.Repo{Dir: dir}.Run(context.Background(), args...)
	files := []string{}
	for _, l := range strings.Split(res.Stdout, "\n") {
		if l != "" {
			files = append(files, l)
		}
	}
	return files
}
