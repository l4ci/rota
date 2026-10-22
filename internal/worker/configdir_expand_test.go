package worker

import (
	"slices"
	"strings"
	"testing"
)

func homeAt(h string) func() (string, error) { return func() (string, error) { return h, nil } }

// round.accounts configDir "~/..." is expanded before it is cached on the slot
// and before it reaches the pane's CLAUDE_CONFIG_DIR (#1777).
func TestAccountAssignCachesTheExpandedConfigDir(t *testing.T) {
	dir := newProject(t, `{"round":{"accounts":[{"name":"a","configDir":"~/.claude-personal"}]}}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	acc := &Accounts{Env: Env{Home: homeAt("/home/u"), Getenv: func(string) string { return t.TempDir() }}}
	if _, _, err := acc.Assign(bg, dir, "w1", "a"); err != nil {
		t.Fatal(err)
	}
	if got := slotField(t, dir, "w1", "configDir"); got != "/home/u/.claude-personal" {
		t.Errorf("cached configDir = %s", got)
	}
	f := tmuxFake()
	e := envWith(f)
	e.Home = homeAt("/home/u")
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "x"), Task: "T1"}); err != nil {
		t.Fatal(err)
	}
	if got := accountEnv(f.spawnOpts); got != "CLAUDE_CONFIG_DIR=/home/u/.claude-personal" {
		t.Errorf("account env = %q", got)
	}
}

// A slot cached with an unexpanded path by an older rota still launches under
// the expanded one.
func TestDispatchExpandsACachedTildeConfigDir(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	UpdateSlot(dir, "w1", func(s *Slot) { s.SetAccount("a", "~/.claude-personal") })
	f := tmuxFake()
	e := envWith(f)
	e.Home = homeAt("/home/u")
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "x"), Task: "T1"}); err != nil {
		t.Fatal(err)
	}
	if got := accountEnv(f.spawnOpts); got != "CLAUDE_CONFIG_DIR=/home/u/.claude-personal" {
		t.Errorf("account env = %q", got)
	}
}

// round.codexAccounts codexHome "~/.codex" reaches CODEX_HOME expanded (#1757).
func TestDispatchCodexAccountExpandsATildeHome(t *testing.T) {
	dir := newProject(t, `{"round":{"dispatch":"herdr","codexAccounts":[{"name":"a","codexHome":"~/.codex"}]}}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	rig := &codexRig{loggedIn: true, installed: true}
	f := herdrFake()
	e := rig.env(f)
	e.Home = homeAt("/home/u")
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "go\n"), Task: "T1", Kind: "codex"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.spawnOpts.Env, "CODEX_HOME=/home/u/.codex") {
		t.Errorf("spawn env %v", f.spawnOpts.Env)
	}
	if rig.ran("codex login status | CODEX_HOME=/home/u/.codex") == 0 {
		t.Errorf("login was not checked in the expanded home: %v", rig.calls)
	}
	for _, c := range rig.calls {
		if strings.Contains(c, "CODEX_HOME=~") {
			t.Errorf("unexpanded home reached a call: %q", c)
		}
	}
}
