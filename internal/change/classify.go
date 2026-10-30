// Package change puts a branch's diff in a tier, so the merge gate and the
// review depth do only the work that tier needs (#2348). It is a leaf: callers
// read the diff, the PR body and the config and hand the facts in.
package change

import (
	"fmt"
	"path"
	"slices"
	"strings"
)

// Tier is how much checking a change needs.
type Tier string

const (
	// TierDocs: every changed file is documentation.
	TierDocs Tier = "docs"
	// TierSmall: a code change under change.smallBelow changed lines.
	TierSmall Tier = "small"
	// TierFull: anything else, and every case the classifier cannot vouch for.
	TierFull Tier = "full"
)

// rank orders tiers by how much checking they need; an unknown tier is full.
func (t Tier) rank() int {
	switch t {
	case TierDocs:
		return 0
	case TierSmall:
		return 1
	}
	return 2
}

// Higher is the stricter of two tiers. Anything but docs or small, the empty
// tier included, is full.
func Higher(a, b Tier) Tier {
	if b.rank() > a.rank() {
		a = b
	}
	if a.rank() == 2 {
		return TierFull
	}
	return a
}

// Policy is the change.* config.
type Policy struct {
	DocsPaths  []string
	RiskyPaths []string
	SmallBelow int
	// SmallPaths are the globs the small tier's check covers; a diff with a file
	// outside them is never small. Empty turns the small tier off.
	SmallPaths []string
}

// DefaultDocsPaths is change.docsPaths when unset.
var DefaultDocsPaths = []string{"*.md", "docs/**", "**/*.png", "**/*.jpg", "**/*.jpeg", "**/*.gif", "**/*.svg", "**/*.webp"}

// Input is the diff the policy classifies. Files lists every changed path,
// both names of a rename. Lines is the changed-line count. Door is a
// "Door: one-way" line in the PR body.
type Input struct {
	Files []string
	Lines int
	Door  bool
}

// Result is a tier with the reason and the files that decided it.
type Result struct {
	Tier   Tier
	Reason string
	Files  []string
	Lines  int
	// Risky is true when a change.riskyPaths glob decided the tier.
	Risky bool
}

// Classify picks the tier. The rules, first match wins: a one-way Door, then
// any risky file, then an empty diff are full; a diff whose every file is
// documentation is docs; a diff under SmallBelow changed lines is small;
// anything else is full. It fails closed: a file no docs glob matches is code,
// and an unknown line count (negative) is never small.
func Classify(in Input, p Policy) Result {
	full := func(files []string, why string) Result {
		return Result{Tier: TierFull, Reason: why, Files: files, Lines: in.Lines}
	}
	if in.Door {
		return full(nil, "PR body says Door: one-way")
	}
	if risky := matching(in.Files, p.RiskyPaths, true); len(risky) > 0 {
		r := full(risky, "matches change.riskyPaths: "+list(risky))
		r.Risky = true
		return r
	}
	if len(in.Files) == 0 {
		return full(nil, "no changed files")
	}
	code := slices.DeleteFunc(slices.Clone(in.Files), func(f string) bool { return isDoc(f, p.DocsPaths) })
	if len(code) == 0 {
		return Result{Tier: TierDocs, Reason: "docs-only change: " + list(in.Files), Files: in.Files, Lines: in.Lines}
	}
	if p.SmallBelow > 0 && in.Lines >= 0 && in.Lines < p.SmallBelow {
		if unmapped := matching(code, p.SmallPaths, false); len(unmapped) > 0 {
			return full(unmapped, "change.smallPaths does not cover: "+list(unmapped))
		}
		return Result{Tier: TierSmall, Reason: fmt.Sprintf("%d changed lines, under change.smallBelow %d: %s", in.Lines, p.SmallBelow, list(code)), Files: code, Lines: in.Lines}
	}
	why := "not documentation: " + list(code)
	if p.SmallBelow > 0 && in.Lines >= p.SmallBelow {
		why = fmt.Sprintf("%d changed lines, not under change.smallBelow %d; %s", in.Lines, p.SmallBelow, why)
	}
	return full(code, why)
}

// docExts are the file types the docs tier admits: Markdown and images.
var docExts = []string{".md", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp"}

// isDoc reports whether f is documentation: a docs glob matches it and it is a
// Markdown or image file. A script or source file under docs/ is code.
func isDoc(f string, globs []string) bool {
	return slices.Contains(docExts, strings.ToLower(path.Ext(f))) &&
		slices.ContainsFunc(globs, func(g string) bool { return Match(g, f) })
}

// matching is the files some glob matches (want true) or none does (false).
func matching(files, globs []string, want bool) []string {
	var out []string
	for _, f := range files {
		if slices.ContainsFunc(globs, func(g string) bool { return Match(g, f) }) == want {
			out = append(out, f)
		}
	}
	return out
}

// list names up to five files, then how many more.
func list(files []string) string {
	const most = 5
	if len(files) <= most {
		return strings.Join(files, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(files[:most], ", "), len(files)-most)
}
