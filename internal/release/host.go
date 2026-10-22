package release

import "github.com/l4ci/rota/internal/tracker"

// Host is hv-release-detect-host: the hosting kind of provider, the forge
// tracker.ResolveProvider found, at origin url (github, github-enterprise,
// gitlab, gitlab-self-hosted, or none for the local forge). Only the
// canonical-host split is release's own.
func Host(provider, url string) string {
	canonical := tracker.RemoteHost(url) == provider+".com"
	switch {
	case provider == "github" && canonical:
		return "github"
	case provider == "github":
		return "github-enterprise"
	case provider == "gitlab" && canonical:
		return "gitlab"
	case provider == "gitlab":
		return "gitlab-self-hosted"
	}
	return "none"
}
