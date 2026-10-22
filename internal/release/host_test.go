package release

import "testing"

// TestHost pins the canonical-host split; which forge it is comes from the
// resolver.
func TestHost(t *testing.T) {
	for _, c := range []struct{ provider, url, want string }{
		{"github", "git@github.com:o/r.git", "github"},
		{"github", "https://GitHub.com/o/r", "github"},
		{"github", "https://github.example.com/o/r", "github-enterprise"},
		{"github", "https://git.example.com/o/r", "github-enterprise"}, // forge.provider github on its own host
		{"gitlab", "https://gitlab.com/o/r.git", "gitlab"},
		{"gitlab", "git@gitlab.internal:grp/r.git", "gitlab-self-hosted"},
		{"local", "https://github.com/o/r", "none"},
		{"local", "", "none"},
	} {
		if got := Host(c.provider, c.url); got != c.want {
			t.Errorf("Host(%q, %q) = %q, want %q", c.provider, c.url, got, c.want)
		}
	}
}
