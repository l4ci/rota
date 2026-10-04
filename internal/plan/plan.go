// Package plan ports hv-plan-add, -list, -show, -put, -rm and
// -rename-check for file mode: plan files under .rota/plans/<key>.md, where
// key is <milestone>-<unit> (M01-B07, M01-S03).
package plan

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/frontmatter"
	"github.com/l4ci/rota/internal/fsio"
)

var (
	keyRe       = regexp.MustCompile(`^M\d{2,}-(?:S\d+|[BFT]\d+)$`)
	addKeyRe    = regexp.MustCompile(`^(M\d{2,})-([BFTS]\d{2,})$`)
	addKeyIssue = regexp.MustCompile(`^(M\d{2,})-([BFTS]\d+)$`) // issue numbers have any number of digits
	milestoneRe = regexp.MustCompile(`^M\d{2,}$`)
	designIDRe  = regexp.MustCompile(`^[BFT]\d{2,}$`)
	// issueDesignRe is --design in issue mode: an issue number ("3") or the
	// lettered form with any digit count ("F3").
	issueDesignRe = regexp.MustCompile(`^[BFT]?\d+$`)
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

func path(root, key string) string { return filepath.Join(root, ".rota", "plans", key+".md") }

func notFound(key string) *artifact.Error {
	return artifact.Errf(artifact.ExitResolution, "plan %s not found (.rota/plans/%s.md)", key, key)
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
	Auto      bool   // written by a loop run: adds `auto: true` after status
}

// parseAdd settles which plan AddOpts names: its milestone, and its unit
// ("" for a slice that still has to be minted).
func parseAdd(o AddOpts, issue bool) (milestone, unit string, err error) {
	switch {
	case o.Key != "" && (o.Slice || o.Milestone != ""):
		return "", "", artifact.Errf(artifact.ExitUsage, "a key cannot be combined with --slice or --milestone")
	case o.Key != "":
		re := addKeyRe
		if issue {
			re = addKeyIssue
		}
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
// design pointer and repo list. The design pointer is a file path in file
// mode (the file must exist) and "note:design" in issue mode, where the
// design is a note on the item's issue.
func extras(root string, o AddOpts, issue bool) (design, repo string, err error) {
	if o.Title == "" {
		return "", "", artifact.Errf(artifact.ExitUsage, "--title is required")
	}
	if o.Design != "" {
		re, like := designIDRe, "B07"
		if issue {
			re, like = issueDesignRe, "3 or F3"
		}
		if !re.MatchString(o.Design) {
			return "", "", artifact.Errf(artifact.ExitUsage, "--design must be an item ID like %s, got %q", like, o.Design)
		}
		if issue {
			design = "note:design"
		} else {
			design = ".rota/designs/" + o.Design + ".md"
			if _, serr := os.Stat(filepath.Join(root, design)); serr != nil {
				return "", "", artifact.Errf(artifact.ExitResolution, "--design file not found: %s", design)
			}
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
func stub(key, milestone, unit, unitKind, repo, design, title string, auto bool) string {
	repoLine, designLine, autoLine := "", "", ""
	if auto {
		autoLine = "auto: true\n"
	}
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
%[9]screated: %[8]s
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
`, key, milestone, unit, unitKind, repoLine, designLine, title, time.Now().Format("2006-01-02"), autoLine)
}

func kindOfUnit(unit string) string {
	if strings.HasPrefix(unit, "S") {
		return "slice"
	}
	return "item"
}

// Add creates a plan stub and returns its key and kind ("slice"|"item").
func Add(root string, o AddOpts) (key, unitKind string, err error) {
	milestone, unit, err := parseAdd(o, false)
	if err != nil {
		return "", "", err
	}
	design, repo, err := extras(root, o, false)
	if err != nil {
		return "", "", err
	}
	dir := filepath.Join(root, ".rota", "plans")
	// One lock per milestone for every S-unit, minted or explicit, so the
	// existence check and the minted number cannot race; an item plan locks
	// its own key.
	lockPath := path(root, milestone+"-slice")
	if unit != "" && !strings.HasPrefix(unit, "S") {
		lockPath = path(root, milestone+"-"+unit)
	}
	err = fsio.Locked(lockPath, fsio.LockTimeout, func() error {
		if unit == "" {
			unit = fmt.Sprintf("S%02d", nextSlice(dir, milestone))
		}
		key, unitKind = milestone+"-"+unit, kindOfUnit(unit)
		p := path(root, key)
		if _, serr := os.Stat(p); serr == nil {
			return artifact.Errf(artifact.ExitRefused, ".rota/plans/%s.md already exists", key)
		}
		if err := os.MkdirAll(dir, 0o777); err != nil {
			return err
		}
		return fsio.WriteFileAtomic(p, []byte(stub(key, milestone, unit, unitKind, repo, design, o.Title, o.Auto)))
	})
	if err != nil {
		return "", "", err
	}
	return key, unitKind, nil
}

// nextSlice is 1 + the highest S<NN> among the milestone's plan files.
func nextSlice(dir, milestone string) int {
	files, _ := filepath.Glob(filepath.Join(dir, milestone+"-S*.md"))
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(milestone) + `-S(\d+)`)
	max := 0
	for _, f := range files {
		if m := re.FindStringSubmatch(strings.TrimSuffix(filepath.Base(f), ".md")); m != nil {
			if n, _ := strconv.Atoi(m[1]); n > max {
				max = n
			}
		}
	}
	return max + 1
}

// Entry is one row of List.
type Entry struct {
	Key, Milestone, Unit, UnitKind, Title, Status, Created string
	Repos                                                  []string
}

// List reads .rota/plans/*.md in name order, optionally only one milestone's.
func List(root, milestone string) ([]Entry, error) {
	docs, err := artifact.ListDocs(filepath.Join(root, ".rota", "plans"))
	if err != nil {
		return nil, err
	}
	out := []Entry{}
	for _, d := range docs {
		ms := frontmatter.Str(d.FM, "milestone")
		if milestone != "" && ms != milestone {
			continue
		}
		e := Entry{Key: orDefault(frontmatter.Str(d.FM, "key"), d.Stem), Milestone: ms,
			Unit: frontmatter.Str(d.FM, "unit"), UnitKind: orDefault(frontmatter.Str(d.FM, "unitKind"), "item"),
			Title: frontmatter.Str(d.FM, "title"), Status: orDefault(frontmatter.Str(d.FM, "status"), "planned"),
			Created: frontmatter.Str(d.FM, "created"), Repos: []string{}}
		switch r := d.FM["repo"].(type) {
		case string:
			e.Repos = artifact.SplitCSV(r)
		case []string:
			e.Repos = append(e.Repos, r...)
		}
		out = append(out, e)
	}
	return out, nil
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// Show is the stored plan, verbatim.
func Show(root, key string) (string, error) {
	if err := checkKey(key); err != nil {
		return "", err
	}
	b, err := os.ReadFile(path(root, key))
	if err != nil {
		return "", notFound(key)
	}
	return string(b), nil
}

// Put replaces an existing plan's text; changed is false when identical.
func Put(root, key, text string) (changed bool, err error) {
	if err = checkKey(key); err != nil {
		return
	}
	p := path(root, key)
	if _, serr := os.Stat(p); serr != nil {
		return false, notFound(key).WithHint("rota plan add " + key + " --title <text>")
	}
	err = fsio.Locked(p, fsio.LockTimeout, func() error {
		old, rerr := os.ReadFile(p)
		if rerr != nil {
			return notFound(key)
		}
		if string(old) == text {
			return nil
		}
		changed = true
		return fsio.WriteFileAtomic(p, []byte(text))
	})
	return
}

// Rm deletes a plan; a missing one is exit 3, never a no-op.
func Rm(root, key string) error {
	if err := checkKey(key); err != nil {
		return err
	}
	p := path(root, key)
	if _, err := os.Stat(p); err != nil {
		return notFound(key)
	}
	return fsio.Locked(p, fsio.LockTimeout, func() error {
		if err := os.Remove(p); err != nil {
			return notFound(key)
		}
		return nil
	})
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
