package worker

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/tracker"
)

// An Env that no cli wired still reads the machine's connections: the default
// Issues must reach connections.json, not stop at "no machine environment".
func TestWithDefaultsIssuesResolvesMachineConnections(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	e := Env{}.WithDefaults()
	_, err := e.Issues(t.TempDir(), tracker.Settings{
		Source: config.SourceJira, Connection: "work", JiraProject: "ROTA",
	})
	if err == nil {
		t.Fatal("want an error for a connection the machine does not define")
	}
	if strings.Contains(err.Error(), "no machine environment") {
		t.Fatalf("default Issues has no machine wired: %v", err)
	}
}
