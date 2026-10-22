package cli

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/verdict"
)

// neutralAC is the one criterion of a deferred finding that carries no fix.
const neutralAC = "the problem above no longer reproduces, and a test or check that failed before the fix passes after it"

// Floors of review.fileDeferred: the least severe deferred finding that is filed.
const (
	floorMinor = "minor"
	floorMajor = "major"
	floorOff   = "off"
)

// prNumberRe is the number at the end of a --pr ref: `77`, `#77` or a PR URL.
var prNumberRe = regexp.MustCompile(`^(?:#|\S*/(?:pull|merge_requests)/)?(\d+)(?:[/?#]\S*)?$`)

// prKey is the PR number of a --pr or Related ref, so `77`, `#77` and a PR URL
// name the same PR; a ref with no number is its own key.
func prKey(ref string) string {
	if m := prNumberRe.FindStringSubmatch(strings.TrimSpace(ref)); m != nil {
		return m[1]
	}
	return strings.TrimSpace(ref)
}

// relatesTo is whether a Related field names the PR.
func relatesTo(related, pr string) bool {
	want := prKey(pr)
	for _, ref := range strings.FieldsFunc(related, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
		if prKey(ref) == want {
			return true
		}
	}
	return false
}

// followUpTitle names the one item that collects a PR's deferred findings.
func followUpTitle(pr string) string {
	if k := prKey(pr); prNumberRe.MatchString(k) {
		return "Review follow-ups from PR #" + k
	}
	return "Review follow-ups from PR " + prKey(pr)
}

// deferredAbove is the deferred findings at or above the floor, in order. The
// floor off keeps none.
func deferredAbove(findings []verdict.Finding, floor string) []verdict.Finding {
	var out []verdict.Finding
	for _, f := range findings {
		if !f.IsDeferred() || floor == floorOff || (floor == floorMajor && f.Severity != "major") {
			continue
		}
		out = append(out, f)
	}
	return out
}

// followUp is one deferred finding as it is written into the item: its Problem
// block and its acceptance criterion.
type followUp struct {
	key   string
	block string
	ac    string
}

func findingKey(title, file string) string { return strings.TrimSpace(title) + "\x00" + file }

func newFollowUp(f verdict.Finding, pr, kind, v string) followUp {
	title := strings.TrimSpace(f.Title)
	var b strings.Builder
	fmt.Fprintf(&b, "### %s\n", title)
	if f.Detail != "" {
		b.WriteString("\n" + f.Detail + "\n")
	}
	if f.File != "" {
		loc := f.File
		if f.Line > 0 {
			loc += ":" + strconv.Itoa(f.Line)
		}
		fmt.Fprintf(&b, "\nWhere: `%s`\n", loc)
	}
	// The line backlog's deferredDependency keys on: keep its wording.
	fmt.Fprintf(&b, "\nDeferred %s finding from the %s %s verdict on %s.\n", f.Severity, kind, v, pr)
	ac := neutralAC
	if fix := strings.TrimSpace(f.Fix); fix != "" {
		ac = fix
	}
	return followUp{key: findingKey(title, f.File), block: b.String(), ac: ac}
}

var (
	whereRe      = regexp.MustCompile("(?m)^Where: `([^`]+)`$")
	followUpACRe = regexp.MustCompile(`(?m)^- \[[ xX]\] AC-(\d+)`)
)

// presentFindings is the findings (title and file) an item body already holds.
func presentFindings(body string) map[string]bool {
	have := map[string]bool{}
	for _, blk := range strings.Split("\n"+body, "\n### ")[1:] {
		title, rest, _ := strings.Cut(blk, "\n")
		if i := strings.Index(rest, "\n## "); i >= 0 {
			rest = rest[:i]
		}
		file := ""
		if m := whereRe.FindStringSubmatch(rest); m != nil {
			file = m[1]
			if i := strings.LastIndex(file, ":"); i >= 0 {
				if _, err := strconv.Atoi(file[i+1:]); err == nil {
					file = file[:i]
				}
			}
		}
		have[findingKey(title, file)] = true
	}
	return have
}

const acceptanceHead = "\n## Acceptance\n"

// followUpHead is the body of a new follow-up item, with no findings yet.
func followUpHead(pr string) string {
	return "## Problem\n\nDeferred review findings from " + strings.TrimPrefix(followUpTitle(pr), "Review follow-ups from ") + ".\n" + acceptanceHead
}

// addFollowUps writes fs into body: the blocks end the Problem section, the
// criteria end the Acceptance section, numbered after the highest AC id.
// ok is false when the body has no Acceptance section to extend.
func addFollowUps(body string, fs []followUp) (out string, ok bool) {
	i := strings.Index(body, acceptanceHead)
	if i < 0 {
		return body, false
	}
	next := 1
	for _, m := range followUpACRe.FindAllStringSubmatch(body, -1) {
		if n, _ := strconv.Atoi(m[1]); n >= next {
			next = n + 1
		}
	}
	var blocks, acs strings.Builder
	for _, f := range fs {
		blocks.WriteString("\n" + f.block)
		fmt.Fprintf(&acs, "- [ ] AC-%d: %s\n", next, f.ac)
		next++
	}
	head, tail := strings.TrimRight(body[:i], "\n"), strings.TrimRight(body[i:], "\n")
	if strings.HasSuffix(tail, "## Acceptance") {
		tail += "\n" // an empty section: keep the blank line before the first criterion
	}
	return head + "\n" + blocks.String() + tail + "\n" + acs.String(), true
}

// fileFollowUpsLocked is fileFollowUps under a lock keyed by the PR: two
// verdicts for one PR can be recorded together, and the read-modify-write of
// its follow-up item must not interleave.
func fileFollowUpsLocked(be backlog.Backend, root string, fs []followUp, pr string) (id string, added int, err error) {
	lock := rotatree.File(root, "followup-"+prKey(pr))
	err = fsio.Locked(lock, fsio.LockTimeout, func() error {
		var e error
		id, added, e = fileFollowUps(be, fs, pr)
		return e
	})
	return id, added, err
}

// fileFollowUps adds the findings to the PR's open follow-up item, or files it
// when there is none. A finding the PR's items already hold (same title and
// file) is not added again. It returns the item id ("" when nothing was added
// and no item exists; also on an error once the item was found) and how many
// criteria it added.
func fileFollowUps(be backlog.Backend, fs []followUp, pr string) (id string, added int, err error) {
	if len(fs) == 0 {
		return "", 0, nil
	}
	title := followUpTitle(pr)
	items, err := be.List(true)
	if err != nil {
		return "", 0, err
	}
	have := map[string]bool{}
	var open *backlog.Item
	for _, it := range items {
		if it.Title != title || !relatesTo(it.Fields.Related, pr) {
			continue
		}
		body, _, _ := be.Detail(it.ID)
		for k := range presentFindings(body) {
			have[k] = true
		}
		if !it.Closed && open == nil {
			it := it
			open = &it
		}
	}
	var fresh []followUp
	for _, f := range fs {
		if !have[f.key] {
			have[f.key] = true // two findings with one title and file add once
			fresh = append(fresh, f)
		}
	}
	if open != nil {
		id = open.ID
	}
	if len(fresh) == 0 {
		return id, 0, nil
	}
	if open != nil {
		dw, ok := be.(backlog.DetailWriter)
		if !ok {
			return id, 0, fmt.Errorf("this backlog backend cannot append to %s", open.ID)
		}
		body, _, err := be.Detail(open.ID)
		if err != nil {
			return id, 0, err
		}
		merged, ok := addFollowUps(body, fresh)
		if !ok {
			return id, 0, Usage("follow-up %s has no ## Acceptance section to add to; add the findings by hand or close it", open.ID)
		}
		if err := dw.SetDetail(open.ID, merged); err != nil {
			return id, 0, err
		}
		return id, len(fresh), nil
	}
	created, _ := addFollowUps(followUpHead(pr), fresh)
	in, err := backlog.BuildCreateInput(backlog.CaptureRequest{Kind: "tasks", Title: title, Desc: title,
		Fields: map[string]string{"related": pr}, Given: map[string]bool{"related": true},
		Body: []byte(created), HasBody: true})
	if err != nil {
		return "", 0, Usage("cannot file the follow-up item: %v", err)
	}
	res, err := be.Create(in)
	if err != nil {
		return "", 0, err
	}
	return res.ID, len(fresh), nil
}
