package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/roundtick"
	"github.com/l4ci/rota/internal/roundwatch"
)

func TestWatchHeartbeatExit(t *testing.T) {
	checkWatchHeartbeatExit(t, nil)
}

func TestWatchHeartbeatExitWithAuxiliaryText(t *testing.T) {
	tick := &roundtick.Result{Deferred: "gate running, deferred: held for #1900"}
	res := checkWatchHeartbeatExit(t, tick)
	if !strings.Contains(res.Text, "deferred\tgate running, deferred: held for #1900") {
		t.Errorf("missing autopilot text: %q", res.Text)
	}
}

func checkWatchHeartbeatExit(t *testing.T, tick *roundtick.Result) Result {
	t.Helper()
	root := gitRepo(t)
	cd, err := rotastate.CommonDir(root)
	if err != nil {
		t.Fatal(err)
	}
	c := &Ctx{Deps: testDeps(), ctx: context.Background()}
	res := watchResult(c, root, cd, c.deps().LeaseEnv(), roundwatch.Result{Reason: roundwatch.ReasonHeartbeat}, tick, "")
	b, err := jsonx.Marshal(res.Data)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.Unmarshal(b, &data); err != nil {
		t.Fatal(err)
	}
	digest, ok := data["digest"].(string)
	if !ok {
		t.Fatalf("missing JSON digest: %s", b)
	}
	for name, output := range map[string]string{"JSON digest": digest, "text": res.Text} {
		if strings.Contains(output, "NO WATCH ARMED") || !strings.Contains(output, "Watch exiting; re-arm `rota round watch`") {
			t.Errorf("%s must describe watch exit and re-arming: %q", name, output)
		}
	}
	return res
}
