package worker

import (
	"strings"
	"testing"

	_ "github.com/l4ci/rota/internal/testiso" // tests never read the real global config
)

// Every host kind's session variables must be absent from a worker sandbox,
// and unrelated ones kept.
func TestIsolatedEnvironDropsEveryHostSession(t *testing.T) {
	in := []string{"HERDR_PANE_ID=p", "TMUX=/s,1,0", "HC_SESSION=x", "ORCA_PANE=y", "KEEP_ME=1", "PATH=/bin"}
	out, err := IsolatedEnviron(in, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kept := map[string]bool{}
	for _, kv := range out {
		k, _, _ := strings.Cut(kv, "=")
		kept[k] = true
	}
	for _, k := range []string{"HERDR_PANE_ID", "TMUX", "HC_SESSION", "ORCA_PANE"} {
		if kept[k] {
			t.Errorf("host variable %s survived isolation", k)
		}
	}
	if !kept["KEEP_ME"] {
		t.Error("isolation dropped an unrelated variable")
	}
}
