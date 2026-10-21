package hook

import "testing"

func TestSettingsPath(t *testing.T) {
	cases := []struct {
		scope Scope
		base  string
		want  string
	}{
		{ScopeProjectLocal, "/p", "/p/.claude/settings.local.json"},
		{ScopeProject, "/p", "/p/.claude/settings.json"},
		{ScopeUser, "/cfg", "/cfg/settings.json"},
		{ScopeProject, "", ""},
		{ScopeUser, "", ""},
		{Scope("bogus"), "/p", ""},
	}
	for _, c := range cases {
		if got := SettingsPath(c.scope, c.base); got != c.want {
			t.Errorf("SettingsPath(%q, %q) = %q, want %q", c.scope, c.base, got, c.want)
		}
	}
}
