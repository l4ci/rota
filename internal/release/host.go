package release

import "github.com/l4ci/rota/internal/tracker"

// Host is hv-release-detect-host: the hosting kind of an origin URL
// (github, github-enterprise, gitlab, gitlab-self-hosted or none). The forge
// comes from tracker; only the canonical-host split is release's own.
func Host(url string) string {
	switch p := tracker.ProviderFromURL(url); {
	case p == "github" && tracker.RemoteHost(url) == "github.com":
		return "github"
	case p == "github":
		return "github-enterprise"
	case p == "gitlab" && tracker.RemoteHost(url) == "gitlab.com":
		return "gitlab"
	case p == "gitlab":
		return "gitlab-self-hosted"
	}
	return "none"
}
