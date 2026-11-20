package tracker

import (
	"regexp"
	"strconv"
	"strings"
)

// PRRef is a PR or MR URL read apart: the forge it belongs to, the repo path
// on that host and the PR number.
type PRRef struct {
	Provider string
	Repo     string
	Number   int
}

// rePRURL reads the path, not the host, so a self-hosted forge classifies too:
// /pull/ is GitHub, /merge_requests/ (with or without GitLab's /-/) is GitLab.
var rePRURL = regexp.MustCompile(`(?i)^https?://[^/\s]+/(?:(.+?)(?:/-)?/)?(pull|merge_requests)/(\d+)(?:[/?#]\S*)?$`)

// ParsePRURL is the one reader of a PR or MR URL. ok is false for anything
// else: a bare number, a branch, a remote URL, a malformed link.
func ParsePRURL(s string) (PRRef, bool) {
	m := rePRURL.FindStringSubmatch(s)
	if m == nil {
		return PRRef{}, false
	}
	n, err := strconv.Atoi(m[3])
	if err != nil {
		return PRRef{}, false
	}
	provider := "github"
	if strings.EqualFold(m[2], "merge_requests") {
		provider = "gitlab"
	}
	return PRRef{Provider: provider, Repo: m[1], Number: n}, true
}

// rePRRefDigits is a bare PR ref as a person types or stores it: `77`, `#77` or
// GitLab's `!77`, with at most one prefix and an optional single trailing `/`.
var rePRRefDigits = regexp.MustCompile(`^[#!]?(\d+)/?$`)

// PRRefNumber is the one reader of a PR ref's number, as the digits written
// (so `007` stays `007`). A ref is a bare number in rePRRefDigits' forms or a
// full http(s) PR/MR URL (ParsePRURL); anything else, such as `12/foo`,
// `x/pull/12` or `#https://...`, has no number. ok is false then. Verdict
// follow-up dedupe and round's Related parse both call it, so they agree.
func PRRefNumber(ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	if m := rePRRefDigits.FindStringSubmatch(ref); m != nil {
		return m[1], true
	}
	if _, ok := ParsePRURL(ref); !ok {
		return "", false
	}
	return rePRURL.FindStringSubmatch(ref)[3], true
}

// PRNumber is PRRefNumber as a positive int: the one grammar for naming a PR.
// ok is false for a ref with no number, a zero number or one that overflows.
func PRNumber(ref string) (int, bool) {
	d, ok := PRRefNumber(ref)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(d)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}
