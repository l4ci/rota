// Package plan holds the milestone and item plans (rota plan add, list, show,
// put, rm): one text per key <milestone>-<unit> (M01-B07, M01-S03), kept by a
// Store: files under .rota/plans/<key>.md (Files), or notes on the issue
// backend (NewNotes), where an item plan is a `plan` note on the item's issue
// and a slice plan a note on the milestone's tracking issue.
package plan

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/artifact"
)

// Store is where plans live. Errors are artifact.Errors: Create is exit 4 when
// the plan exists, Read, Replace and Remove are exit 3 when it does not.
type Store interface {
	// Digits is the fewest digits a unit may carry when a plan is added:
	// file plans are minted as B07 (2), issue numbers have any count (1).
	Digits() int
	// DesignRef checks --design and returns the pointer the stub carries.
	// slice tells a slice plan from an item plan.
	DesignRef(slice bool, design string) (string, error)
	// Create stores the plan for unit of milestone; unit "" mints the next
	// S<NN>. render builds the text once the unit is settled. It returns the key.
	Create(milestone, unit string, render func(unit string) string) (key string, err error)
	Read(key string) (string, error)
	Replace(key, text string) (changed bool, err error)
	Remove(key string) error
	List(milestone string) ([]Entry, error)
}

var (
	keyRe       = regexp.MustCompile(`^M\d{2,}-(?:S\d+|[BFT]\d+)$`)
	milestoneRe = regexp.MustCompile(`^M\d{2,}$`)
)

// ValidKey reports whether key is a plan key: M\d{2,}-(S\d+|[BFT]\d+).
func ValidKey(key string) bool { return keyRe.MatchString(key) }

// ValidMilestone reports whether s is M\d{2,}.
func ValidMilestone(s string) bool { return milestoneRe.MatchString(s) }

func checkKey(key string) error {
	if !ValidKey(key) {
		return artifact.Errf(artifact.ExitUsage, "key must look like M01-B07 or M01-S02, got %q", key)
	}
	return nil
}

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

// parseAdd settles which plan AddOpts names: its milestone, and its unit
// ("" for a slice that still has to be minted).
func parseAdd(o AddOpts, digits int) (milestone, unit string, err error) {
	switch {
	case o.Key != "" && (o.Slice || o.Milestone != ""):
		return "", "", artifact.Errf(artifact.ExitUsage, "a key cannot be combined with --slice or --milestone")
	case o.Key != "":
		re := regexp.MustCompile(fmt.Sprintf(`^(M\d{2,})-([BFTS]\d{%d,})$`, digits))
		m := re.FindStringSubmatch(o.Key)
		if m == nil {
			return "", "", artifact.Errf(artifact.ExitUsage, "key must look like M01-B07 or M01-S02, got %q", o.Key)
		}
		return m[1], m[2], nil
	case o.Slice && o.Milestone == "":
		return "", "", artifact.Errf(artifact.ExitUsage, "--slice needs --milestone <M01>")
	case o.Slice:
		if !ValidMilestone(o.Milestone) {
			return "", "", artifact.Errf(artifact.ExitUsage, "milestone must look like M01/M02/…, got %q", o.Milestone)
		}
		return o.Milestone, "", nil
	}
	return "", "", artifact.Errf(artifact.ExitUsage, "give a plan key (M01-B07) or --milestone <M01> --slice")
}

// extras validates --title, --design and --repos and returns the stub's
// design pointer and repo list. unit "" is a slice still to be minted.
func extras(root string, s Store, o AddOpts, unit string) (design, repo string, err error) {
	if o.Title == "" {
		return "", "", artifact.Errf(artifact.ExitUsage, "--title is required")
	}
	if o.Design != "" {
		slice := unit == "" || strings.HasPrefix(unit, "S")
		if design, err = s.DesignRef(slice, o.Design); err != nil {
			return "", "", err
		}
	}
	if o.Repos != "" {
		names := artifact.SplitCSV(o.Repos)
		if len(names) == 0 {
			return "", "", artifact.Errf(artifact.ExitUsage, "--repos %q is empty after parsing", o.Repos)
		}
		regs := artifact.Repos(root)
		var missing []string
		for _, n := range names {
			if _, ok := regs[n]; !ok {
				missing = append(missing, n)
			}
		}
		if len(missing) > 0 {
			return "", "", artifact.Errf(artifact.ExitResolution, "--repos name(s) not in .rota/repos.json: %s", strings.Join(missing, ", "))
		}
		repo = strings.Join(names, ", ")
	}
	return design, repo, nil
}

// stub is the starter text of a plan.
func stub(key, milestone, unit, unitKind, repo, design, title string) string {
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
  - Files: _(paths the orchestrator will touch or create)_
  - Verify: _(command or manual check that proves T1 done)_

## Open questions

- _(unresolved decisions — answer before or during execution)_

## Assumptions

- _(named assumptions made implicit by the approach)_
`, key, milestone, unit, unitKind, repoLine, designLine, title, time.Now().Format("2006-01-02"))
}

func kindOfUnit(unit string) string {
	if strings.HasPrefix(unit, "S") {
		return "slice"
	}
	return "item"
}

// Add creates a plan and returns its key and kind ("slice"|"item"). root is
// where --repos names are looked up.
func Add(root string, s Store, o AddOpts) (key, unitKind string, err error) {
	milestone, unit, err := parseAdd(o, s.Digits())
	if err != nil {
		return "", "", err
	}
	design, repo, err := extras(root, s, o, unit)
	if err != nil {
		return "", "", err
	}
	key, err = s.Create(milestone, unit, func(unit string) string {
		return stub(milestone+"-"+unit, milestone, unit, kindOfUnit(unit), repo, design, o.Title)
	})
	if err != nil {
		return "", "", err
	}
	return key, kindOfUnit(strings.TrimPrefix(key, milestone+"-")), nil
}

// Entry is one row of List.
type Entry struct {
	Key, Milestone, Unit, UnitKind, Title, Status, Created string
	Repos                                                  []string
}

// List is the stored plans, optionally only one milestone's.
func List(s Store, milestone string) ([]Entry, error) { return s.List(milestone) }

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// Show is the stored plan.
func Show(s Store, key string) (string, error) {
	if err := checkKey(key); err != nil {
		return "", err
	}
	return s.Read(key)
}

// Put replaces an existing plan's text; changed is false when identical.
func Put(s Store, key, text string) (bool, error) {
	if err := checkKey(key); err != nil {
		return false, err
	}
	changed, err := s.Replace(key, text)
	var ae *artifact.Error
	if errors.As(err, &ae) && ae.Exit == artifact.ExitResolution {
		ae.WithHint("rota plan add " + key + " --title <text>")
	}
	return changed, err
}

// Rm deletes a plan; a missing one is exit 3, never a no-op.
func Rm(s Store, key string) error {
	if err := checkKey(key); err != nil {
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
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, _ := cmd.Output()
	files := []string{}
	for _, l := range strings.Split(string(out), "\n") {
		if l != "" {
			files = append(files, l)
		}
	}
	return files
}
