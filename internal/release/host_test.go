package release

import "testing"

func TestHost(t *testing.T) {
	for url, want := range map[string]string{
		"git@github.com:o/r.git":         "github",
		"https://GitHub.com/o/r":         "github",
		"https://github.example.com/o/r": "github-enterprise",
		"https://gitlab.com/o/r.git":     "gitlab",
		"git@gitlab.internal:grp/r.git":  "gitlab-self-hosted",
		"https://git.example.com/o/r":    "none",
		"":                               "none",
	} {
		if got := Host(url); got != want {
			t.Errorf("Host(%q) = %q, want %q", url, got, want)
		}
	}
}
