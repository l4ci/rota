package verdict

import (
	"fmt"
	"regexp"
	"strings"
)

// Floors of review.fileDeferred: the least severe deferred finding that is filed.
const (
	FloorMinor = "minor"
	FloorMajor = "major"
	FloorOff   = "off"
)

// FollowUpTitlePrefix starts every item that collects deferred review findings.
const FollowUpTitlePrefix = "Review follow-ups from PR"

// IsFollowUpTitle is whether an item title names a deferred-findings item, the
// kind filed by verdict add --file-deferred. A PR that closes one files no
// further follow-ups.
func IsFollowUpTitle(title string) bool {
	return strings.HasPrefix(strings.TrimSpace(title), FollowUpTitlePrefix)
}

// DeferredAbove is the deferred findings at or above the floor, in order. The
// floor off keeps none.
func DeferredAbove(findings []Finding, floor string) []Finding {
	var out []Finding
	for _, f := range findings {
		if !f.IsDeferred() || floor == FloorOff || (floor == FloorMajor && f.Severity != "major") {
			continue
		}
		out = append(out, f)
	}
	return out
}

// DeferredLine is the line a filed --file-deferred item carries to name the
// verdict it came from. IsDeferredFinding recognises it: change both together.
func DeferredLine(severity, kind, v, pr string) string {
	return fmt.Sprintf("Deferred %s finding from the %s %s verdict on %s.", severity, kind, v, pr)
}

var deferredRe = regexp.MustCompile(`(?m)^Deferred \w+ finding from the .* verdict on `)

// IsDeferredFinding reports whether body holds a DeferredLine: the item was
// filed by `verdict add --file-deferred`, so its fix builds on the source PR.
func IsDeferredFinding(body string) bool { return deferredRe.MatchString(body) }
