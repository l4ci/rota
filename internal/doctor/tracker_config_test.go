package doctor

import (
	"context"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
)

// settingsFor fills one source's key field and the connection; "" leaves it unset.
func settingsFor(source, key, conn string) tracker.Settings {
	s := tracker.Settings{Source: source, Connection: conn}
	switch source {
	case "jira":
		s.JiraProject = key
	case "linear":
		s.LinearTeam = key
	case "itsaplan":
		s.ItsaplanProject = key
	}
	return s
}

// Every table source fails its config the same three ways, each naming the key
// or connection to set; a missing key or connection beats a team problem.
func TestTrackerSourcesFailTheirConfig(t *testing.T) {
	f := &fake{have: map[string]bool{"git": true, "jq": true, "gh": true}, reply: map[string]Result{}}
	for source, src := range trackerSources {
		for _, tc := range []struct {
			name, key, conn, problem string
			detail, hint             string
		}{
			{"no key", "", "work", "", src.keyName + " is not set", "rota config set " + src.keyName},
			{"no connection", "K", "", "", "issues.connection is not set", "--kind " + source + src.connectArgs},
			{"key and connection missing reports the key", "", "", "", src.keyName + " is not set", ""},
			{"team the key cannot see", "K", "work", "team K is not visible", "team K is not visible", src.probeHint},
		} {
			t.Run(source+" "+tc.name, func(t *testing.T) {
				if tc.problem != "" && src.probeHint == "" {
					t.Skip("source has no team probe")
				}
				in := Input{Tracker: settingsFor(source, tc.key, tc.conn), TeamProblem: tc.problem, Exec: f.exec, Look: f.look}
				c := statusOf(Run(context.Background(), in), "tracker")
				if c.Status != Fail || !strings.Contains(c.Detail, tc.detail) || !strings.Contains(c.Hint, tc.hint) {
					t.Errorf("got %+v, want fail with detail %q hint %q", c, tc.detail, tc.hint)
				}
			})
		}
	}
}

// A team problem on a source with no probe (jira) is not this check's to report.
func TestTeamProblemIgnoredWithoutProbe(t *testing.T) {
	f := &fake{have: map[string]bool{"git": true, "jq": true, "gh": true}, reply: map[string]Result{"git remote get-url origin": {Stdout: "git@github.com:a/b.git"}, "gh auth status": {}}}
	in := Input{Tracker: settingsFor("jira", "P", "work"), TeamProblem: "stray", Exec: f.exec, Look: f.look}
	if c := statusOf(Run(context.Background(), in), "tracker"); c.Status == Fail {
		t.Errorf("jira failed on a team problem: %+v", c)
	}
}

func TestTeamProbeApplies(t *testing.T) {
	for _, tc := range []struct {
		name string
		s    tracker.Settings
		ok   bool
		want bool
	}{
		{"linear with team and working connection", settingsFor("linear", "ENG", "work"), true, true},
		{"itsaplan with project and working connection", settingsFor("itsaplan", "MKT", "plan"), true, true},
		{"linear connection failed", settingsFor("linear", "ENG", "work"), false, false},
		{"linear without team", settingsFor("linear", "", "work"), true, false},
		{"linear without connection name", settingsFor("linear", "ENG", ""), true, false},
		{"jira has no probe", settingsFor("jira", "P", "work"), true, false},
		{"github has no probe", tracker.Settings{Source: "github", Connection: "work"}, true, false},
		{"no source", tracker.Settings{}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := TeamProbeApplies(tc.s, tc.ok); got != tc.want {
				t.Errorf("TeamProbeApplies = %v, want %v", got, tc.want)
			}
		})
	}
}
