package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
)

// TestRoundStatusReadsTheForgeOnce pins the gh calls of one `round status` on
// a repo with seven open issues: no identical call is made twice, an issue the
// list returned is not viewed again, and the total stays small. The same
// cache serves every verb, so candidates and start get the same saving.
func TestRoundStatusReadsTheForgeOnce(t *testing.T) {
	root := gitRepo(t)
	if err := os.WriteFile(filepath.Join(root, ".rota", "config.json"),
		[]byte(`{"backlog":{"backend":"issues"},"issues":{"provider":"github"}}`), 0o666); err != nil {
		t.Fatal(err)
	}
	var issues []string
	for n := 1; n <= 7; n++ {
		issues = append(issues, fmt.Sprintf(`{"number":%d,"title":"item %d","body":"body","labels":[],"state":"OPEN"}`, n, n))
	}
	list := "[" + strings.Join(issues, ",") + "]"

	var mu sync.Mutex
	var calls []string
	deps := testDeps()
	deps.TrackerOptions = []tracker.Option{tracker.WithExec(func(_ context.Context, _, name string, args []string, _ []byte) ([]byte, []byte, int, error) {
		mu.Lock()
		calls = append(calls, name+" "+strings.Join(args, " "))
		mu.Unlock()
		switch {
		case len(args) > 1 && args[0] == "issue" && args[1] == "list" && !strings.Contains(strings.Join(args, " "), "--label"):
			return []byte(list), nil, 0, nil
		case len(args) > 1 && args[0] == "issue" && args[1] == "view":
			return []byte(issues[0]), nil, 0, nil
		}
		return []byte("[]"), nil, 0, nil
	}, func(n string) (string, error) { return "/fake/" + n, nil })}

	if code, out, errOut := rotaInWith(t, deps, root, "--json", "round", "status"); code != 0 {
		t.Fatalf("status exit %d: %s %s", code, out, errOut)
	}
	seen := map[string]bool{}
	for _, c := range calls {
		if seen[c] {
			t.Errorf("call made twice: %s", c)
		}
		seen[c] = true
		if strings.Contains(c, " issue view ") {
			t.Errorf("a listed issue was viewed again: %s", c)
		}
	}
	if len(calls) > 12 {
		t.Errorf("%d forge calls, want at most 12:\n%s", len(calls), strings.Join(calls, "\n"))
	}
}
