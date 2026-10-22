package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/jsonx"
)

// limitedDispatch dispatches w1, bound to account a at weekly utilization pct,
// and returns the refusal and the host's calls.
func limitedDispatch(t *testing.T, pct int) (error, *fakeHost) {
	t.Helper()
	dir := waitProject(t, 1, nil)
	cfg, err := jsonx.Decode([]byte(`{"round":{"accounts":[{"name":"a","configDir":"/nowhere/a"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateSlot(dir, "w1", func(s *Slot) { s.SetAccount("a", "/nowhere/a") }); err != nil {
		t.Fatal(err)
	}
	h := tmuxFake()
	e := envWith(h)
	e.Config = func(string) any { return cfg }
	reset := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	e.Accounts = &Accounts{Env: e, Fetch: func(context.Context, string, string) (*jsonx.Object, string) {
		v, _ := jsonx.Decode([]byte(fmt.Sprintf(`{"five_hour":{"utilization":1},"seven_day":{"utilization":%d,"resets_at":%q}}`, pct, reset)))
		return v.(*jsonx.Object), ""
	}}
	_, err = e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "task\n"), Task: "T1"})
	return err, h
}

func TestDispatchRefusesLimitedWhenTheSlotsAccountIsExhausted(t *testing.T) {
	err, h := limitedDispatch(t, 100)
	var ee *exitcode.Error
	if !errors.As(err, &ee) || ee.Exit != exitcode.ExitRefused {
		t.Fatalf("want a refusal, got %v", err)
	}
	if bd, ok := ee.Data.(BlockData); !ok || bd.BlockedBy != "limited" || !strings.Contains(ee.Message, "a") {
		t.Errorf("data %+v message %q: want blockedBy limited naming the account", ee.Data, ee.Message)
	}
	for _, c := range h.calls {
		if strings.HasPrefix(c, "spawn") || strings.HasPrefix(c, "kill") {
			t.Errorf("host touched before the refusal: %v", h.calls)
		}
	}
}

func TestDispatchLaunchesWhenTheSlotsAccountHasHeadroom(t *testing.T) {
	if err, _ := limitedDispatch(t, 37); err != nil {
		t.Fatal(err)
	}
}
