package host

import (
	"os"
	"reflect"
	"testing"
)

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The Claude first-run theme picker is a startup dialog to drive through
// (Enter keeps the highlighted default), not an unknown screen (#1777).
func TestDialogKeysOnboardingThemePicker(t *testing.T) {
	pane := readFixture(t, "claude-onboarding-theme-2.1.290.txt")
	if keys, ok := DialogKeys(pane); !ok || !reflect.DeepEqual(keys, []string{"enter"}) {
		t.Errorf("DialogKeys = %v %v, want [enter] true", keys, ok)
	}
	numbered := "Choose the text style that looks best with your terminal\n\n ❯ 1. Dark mode ✔\n   2. Light mode\n"
	if keys, ok := DialogKeys(numbered); !ok || !reflect.DeepEqual(keys, []string{"enter"}) {
		t.Errorf("numbered: DialogKeys = %v %v, want [enter] true", keys, ok)
	}
}

// The picker's highlighted option is not a human draft on the prompt line.
func TestHumanDraftThemePickerIsNotADraft(t *testing.T) {
	if got := humanDraft(readFixture(t, "claude-onboarding-theme-2.1.290.txt"), "brief"); got != "" {
		t.Errorf("theme picker option read as a draft: %q", got)
	}
}

// A booted session idle at its empty prompt, which herdr reported as not
// ready, is booted: Spawn returns its tab instead of failing (#1791).
func TestHerdrSpawnBootedIdlePromptIsBooted(t *testing.T) {
	pane := readFixture(t, "claude-booted-2.1.289.txt")
	f := &fake{handler: func(_ string, a []string) Result {
		switch a[0] + " " + a[1] {
		case "tab create":
			return Result{Stdout: tabCreated}
		case "agent start":
			return Result{ExitCode: 1, Stderr: herdrErr("agent_not_ready")}
		case "agent read":
			return Result{Stdout: pane}
		}
		return Result{}
	}}
	got, err := New("herdr", deps(f, herdrEnv, &clock{})).Spawn(bg, SpawnOpts{Slot: "w1", Cwd: "/wt", Launch: "claude", BootTimeout: 5})
	if err != nil || got != "w9:t7" {
		t.Fatalf("Spawn = %q, %v", got, err)
	}
	if f.count("herdr agent send-keys") != 0 {
		t.Errorf("a booted session got keys:\n%s", f.log())
	}
}

// The theme picker is driven through even though its banner reads as booted.
func TestHerdrSpawnDrivesThemePicker(t *testing.T) {
	panes := []string{readFixture(t, "claude-onboarding-theme-2.1.290.txt")}
	read := 0
	f := &fake{handler: func(_ string, a []string) Result {
		switch a[0] + " " + a[1] {
		case "tab create":
			return Result{Stdout: tabCreated}
		case "agent start":
			return Result{ExitCode: 1, Stderr: herdrErr("agent_not_ready")}
		case "agent read":
			read++
			return Result{Stdout: panes[0]}
		case "agent wait":
			return Result{Stdout: agentJSON("idle")}
		}
		return Result{}
	}}
	got, err := New("herdr", deps(f, herdrEnv, &clock{})).Spawn(bg, SpawnOpts{Slot: "w1", Cwd: "/wt", Launch: "claude", BootTimeout: 5})
	if err != nil || got != "w9:t7" {
		t.Fatalf("Spawn = %q, %v", got, err)
	}
	if f.count("herdr agent send-keys rota-w1-w9-t7 enter") != 1 {
		t.Errorf("want the picker answered once:\n%s", f.log())
	}
}

// A Spawn that fails with its tab still open returns the tab, so the caller
// can record it and the next dispatch reaps it (#1791).
func TestHerdrSpawnFailureReturnsTheOpenTab(t *testing.T) {
	f := &fake{handler: func(_ string, a []string) Result {
		switch a[0] + " " + a[1] {
		case "tab create":
			return Result{Stdout: tabCreated}
		case "agent start":
			return Result{ExitCode: 1, Stderr: herdrErr("agent_not_ready")}
		case "agent read":
			return Result{Stdout: "What is this?\n1. Foo\n"}
		}
		return Result{}
	}}
	got, err := New("herdr", deps(f, herdrEnv, &clock{})).Spawn(bg, SpawnOpts{Slot: "w1", Cwd: "/wt", Launch: "claude", BootTimeout: 5})
	if err == nil || got != "w9:t7" {
		t.Errorf("Spawn = %q, %v; want the open tab and an error", got, err)
	}
}
