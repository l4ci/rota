package round

import (
	"errors"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

// Solo behaviour comes from Env.HostName: none of these roots has a registry
// file, so nothing on disk says the round is solo.

func soloEnv(t *testing.T, cfg string) (Env, string) {
	t.Helper()
	root := newRepo(t, nil)
	(&assignFixture{root: root}).config(t, cfg)
	e := Env{HostName: host.Solo, Git: git.Exec, Base: "main", Lease: fakeLease("h", 100), Worker: wk(worker.Env{Getenv: noEnv, Config: diskConfig})}
	e = e.WithAccounts(&worker.Accounts{Env: worker.Env{Config: diskConfig}})
	return e, root
}

const soloAccountsCfg = `{"round":{"accounts":[{"name":"a","configDir":"/nonexistent"}]},"review":{"accounts":["a"]}}`

func wantUsage(t *testing.T, err error, msg string) {
	t.Helper()
	var we *exitcode.Error
	if !errors.As(err, &we) || we.Exit != exitcode.ExitUsage || !strings.Contains(we.Message, msg) {
		t.Fatalf("want a usage error naming %q, got %v", msg, err)
	}
}

func TestSoloHostNameNeverPlacesAccounts(t *testing.T) {
	e, root := soloEnv(t, soloAccountsCfg)
	if e.placesAccounts(root) {
		t.Error("a solo round places no account")
	}
}

func TestSoloHostNameSkipsReviewOnlyRefusalAndAccountPick(t *testing.T) {
	e, root := soloEnv(t, soloAccountsCfg)
	set := roundcfg.Settings{Models: map[string]map[string]string{harness.Claude: {"standard": "sonnet"}}}
	_, _, pl, err := e.placeWorker(bg, root, preflightIn{FlagKind: harness.Claude, Settings: set, Tier: "standard"}, preflightOut{})
	if err != nil || pl != nil {
		t.Fatalf("no refusal and no pick under solo: %+v %v", pl, err)
	}
}

func TestSoloHostNameRefusesSwitch(t *testing.T) {
	e, root := soloEnv(t, `{}`)
	_, err := e.planSwitch(bg, root, nil, SwitchOpts{Slot: "ben"}, &Switched{})
	wantUsage(t, err, "a solo round has none")
}

func TestSoloHostNameRefusesACodexDelivery(t *testing.T) {
	e, root := soloEnv(t, `{}`)
	_, err := e.deliveryRefusals(bg, root, harness.Codex, "ben", "", nil)
	wantUsage(t, err, "Claude subagents")
}

func TestSoloHostNameDeliversByHandOff(t *testing.T) {
	e, root := soloEnv(t, `{}`)
	steps, err := (&delivery{Slot: "ben", Kind: harness.Claude}).steps(bg, e, root)
	if err != nil {
		t.Fatal(err)
	}
	if got := steps[len(steps)-1].name; got != "solo hand-off" {
		t.Errorf("last step = %q, want solo hand-off", got)
	}
}
