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
