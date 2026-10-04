package release

import (
	"regexp"
	"strings"
)

var hostRe = regexp.MustCompile(`(?i)^(https?://|ssh://)?(git@)?([^:/]+)[:/].*`)

// Host is hv-release-detect-host: the hosting kind of an origin URL
// (github, github-enterprise, gitlab, gitlab-self-hosted or none).
func Host(url string) string {
	if url == "" {
		return "none"
	}
	host := url
	if m := hostRe.FindStringSubmatch(url); m != nil {
		host = m[3]
	}
	host = strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, host)
	switch {
	case host == "github.com":
		return "github"
	case host == "gitlab.com":
		return "gitlab"
	case strings.Contains(host, "github"):
		return "github-enterprise"
	case strings.Contains(host, "gitlab"):
		return "gitlab-self-hosted"
	}
	return "none"
}
