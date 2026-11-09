package remoteurl

import "testing"

func TestParse(t *testing.T) {
	for name, tc := range map[string]struct {
		in   string
		want Parts
	}{
		"https":               {"https://github.com/acme/x", Parts{"github.com", "acme", "x"}},
		"https .git":          {"https://github.com/acme/x.git", Parts{"github.com", "acme", "x"}},
		"https trailing /":    {"https://github.com/acme/x/", Parts{"github.com", "acme", "x"}},
		"https port":          {"https://git.example.com:8443/acme/x.git", Parts{"git.example.com", "acme", "x"}},
		"https userinfo":      {"https://user:tok@git.example.com/acme/x.git", Parts{"git.example.com", "acme", "x"}},
		"https userinfo port": {"https://user:tok@git.example.com:8443/acme/x", Parts{"git.example.com", "acme", "x"}},
		"ssh":                 {"ssh://git@gitlab.example.com/acme/x.git", Parts{"gitlab.example.com", "acme", "x"}},
		"ssh port":            {"ssh://git@gitlab.example.com:2222/acme/x.git", Parts{"gitlab.example.com", "acme", "x"}},
		"scp":                 {"git@github.com:acme/x.git", Parts{"github.com", "acme", "x"}},
		"scp no user":         {"github.com:acme/x", Parts{"github.com", "acme", "x"}},
		"scp nested group":    {"git@gitlab.com:grp/sub/x.git", Parts{"gitlab.com", "grp/sub", "x"}},
		"upper host":          {"git@GitHub.COM:Acme/X.git", Parts{"github.com", "Acme", "X"}},
		"surrounding space":   {"  https://github.com/acme/x  ", Parts{"github.com", "acme", "x"}},
		"bare host":           {"GitHub.com", Parts{"github.com", "", ""}},
		"empty":               {"", Parts{}},
	} {
		t.Run(name, func(t *testing.T) {
			if got := Parse(tc.in); got != tc.want {
				t.Errorf("Parse(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}
