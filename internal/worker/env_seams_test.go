package worker

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/host"
)

// failCloser is a host whose workspace close fails.
type failCloser struct{ *fakeHost }

func (g *failCloser) Capabilities() host.Capabilities { return host.CapabilitiesOf(g) }
func (g *failCloser) CloseWorkspace(context.Context, string) error {
	return errors.New("workspace gone")
}

// The close-failure hint goes through Env.Stderr, not the process's stderr.
func TestKillSlotCloseFailureHintGoesThroughEnvStderr(t *testing.T) {
	dir := groupProject(t, "", map[string]string{"w1": "w9N:t2"})
	UpdateSlot(dir, "w1", func(s *Slot) { s.SetWorkspaceID("w9N") })
	var buf bytes.Buffer
	e := envWith(&failCloser{tmuxFake()})
	e.Stderr = &buf
	if err := e.KillSlot(bg, dir, "w1"); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "rota: slot w1: workspace gone\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

// The herdr handoff hint names the cwd Env reports.
func TestSessionEnsureHerdrHintNamesEnvGetwd(t *testing.T) {
	dir := newProject(t, `{"round":{"dispatch":"herdr"}}`)
	e := envWith(&fakeHost{name: "herdr"})
	e.Getwd = func() (string, error) { return "/fake/cwd", nil }
	_, err := e.SessionEnsure(bg, dir, SessionOpts{})
	we, ok := err.(*exitcode.Error)
	if !ok || !strings.Contains(we.Hint, "/fake/cwd") {
		t.Errorf("err = %#v, want a hint naming /fake/cwd", err)
	}
}

// Account config dirs expand ~/ against Env.Home.
func TestAccountsExpandHomeFromEnv(t *testing.T) {
	acc := &Accounts{Env: Env{Config: memConfig(t, `{"round":{"accounts":[{"name":"x","configDir":"~/.x"}]}}`),
		Home: func() (string, error) { return "/fake/home", nil }}}
	if got := acc.AccountOf("", "/fake/home/.x"); got != "x" {
		t.Errorf("AccountOf = %q, want x", got)
	}
}

// The Keychain service name treats Credentials.Home's ~/.claude as the
// default login: no suffix.
func TestCredentialsHomeSeamPicksDefaultKeychainService(t *testing.T) {
	kc, _ := fakeKeychain("darwin", map[string]string{"Claude Code-credentials": `{"claudeAiOauth":{"accessToken":"kc"}}`})
	kc.Home = func() (string, error) { return "/fake/home", nil }
	if got := kc.Problem("/fake/home/.claude"); got != "" {
		t.Errorf("Problem = %q, want usable through the default service", got)
	}
}
