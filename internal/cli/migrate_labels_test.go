package cli

import (
	"bytes"
	"errors"
	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/tracker"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestMigrateLabelsUsesConfiguredValue(t *testing.T) {
	root := trackerProject(t, `{"issues":{"source":"github","labels":{"support":"remote"}},"round":{"support":"mac"}}`)
	fake := &trackertest.Fake{Issues: []tracker.Issue{{Key: tracker.NumKey(7), State: "open", Labels: []string{"remote"}}}}
	deps := supportReturnDeps(fake)
	for _, args := range [][]string{{}, {"--apply"}, {"--apply"}} {
		code, env, stderr := rotaRunWith(t, deps, append([]string{"--json", "-C", root, "migrate", "labels"}, args...)...)
		if code != ExitOK {
			t.Fatalf("%v exit %d: %v %s", args, code, env, stderr)
		}
		if len(args) == 0 && !slices.Equal(fake.Issues[0].Labels, []string{"remote"}) {
			t.Fatal("preview wrote labels")
		}
	}
	if !slices.Equal(fake.Issues[0].Labels, []string{"machine:mac"}) {
		t.Fatalf("labels = %v", fake.Issues[0].Labels)
	}
	raw, _ := os.ReadFile(filepath.Join(root, ".rota/config.json"))
	if strings.Contains(string(raw), "remote") {
		t.Fatalf("key retained: %s", raw)
	}
}
func TestMigrateLabelsRefusesWithoutSupport(t *testing.T) {
	root := trackerProject(t, `{"issues":{"source":"github"}}`)
	code, env, _ := rotaRun(t, "--json", "-C", root, "migrate", "labels")
	if code != ExitRefused || envObj(env, "data")["blockedBy"] != "config" {
		t.Fatalf("exit %d data=%v err=%v", code, envObj(env, "data"), envObj(env, "error"))
	}
}
func TestMigrateLabelsFencedOnSupport(t *testing.T) {
	code, env, _ := rotaRun(t, "--json", "-C", supportProject(t), "migrate", "labels", "--apply")
	if code != ExitRefused || envObj(env, "data")["blockedBy"] != "role" {
		t.Fatalf("exit %d data=%v err=%v", code, envObj(env, "data"), envObj(env, "error"))
	}
}
func TestMigrateLabelsConflictKeepsKey(t *testing.T) {
	root := trackerProject(t, `{"issues":{"source":"github","labels":{"support":"remote"}},"round":{"support":"mac"}}`)
	fake := &trackertest.Fake{Issues: []tracker.Issue{{Key: tracker.NumKey(7), State: "open", Labels: []string{"remote", "machine:main"}}, {Key: tracker.NumKey(8), State: "open", Labels: []string{"remote"}}}}
	code, env, _ := rotaRunWith(t, supportReturnDeps(fake), "--json", "-C", root, "migrate", "labels", "--apply")
	if code != ExitRefused {
		t.Fatalf("exit %d data=%v err=%v", code, envObj(env, "data"), envObj(env, "error"))
	}
	if !slices.Equal(fake.Issues[0].Labels, []string{"remote", "machine:main"}) || !slices.Equal(fake.Issues[1].Labels, []string{"machine:mac"}) {
		t.Fatalf("issues = %v", fake.Issues)
	}
	raw, _ := os.ReadFile(filepath.Join(root, ".rota/config.json"))
	if !strings.Contains(string(raw), "remote") {
		t.Fatal("key removed despite conflict")
	}
}
func TestMigrateLabelsErrorKeepsKey(t *testing.T) {
	root := trackerProject(t, `{"issues":{"source":"github","labels":{"support":"remote"}},"round":{"support":"mac"}}`)
	fake := &trackertest.Fake{Issues: []tracker.Issue{{Key: tracker.NumKey(7), State: "open", Labels: []string{"remote"}}}, Fail: map[string]error{"remove_labels": errors.New("offline")}}
	code, _, _ := rotaRunWith(t, supportReturnDeps(fake), "--json", "-C", root, "migrate", "labels", "--apply")
	if code == ExitOK {
		t.Fatal("tracker failure succeeded")
	}
	raw, _ := os.ReadFile(filepath.Join(root, ".rota/config.json"))
	if !strings.Contains(string(raw), "remote") {
		t.Fatal("key removed despite error")
	}
}
func TestConfigCheckSupportWithoutName(t *testing.T) {
	root := trackerProject(t, `{"issues":{"source":"github"}}`)
	writeLocalConfig(t, root, `{"round":{"role":"support"}}`)
	code, env, _ := rotaRun(t, "--json", "-C", root, "config", "check")
	if code != ExitFailed || envObj(env, "data")["blockedBy"] != "config" {
		t.Fatalf("exit %d data=%v err=%v", code, envObj(env, "data"), envObj(env, "error"))
	}
}
func TestConfigCheckRemovedSupportHint(t *testing.T) {
	root := trackerProject(t, `{"issues":{"labels":{"support":"remote"}}}`)
	_, env, _ := rotaRun(t, "--json", "-C", root, "config", "check")
	if !slices.ContainsFunc(envObj(env, "data")["removed"].([]any), func(v any) bool { return v == "issues.labels.support" }) {
		t.Fatalf("removed=%v", env)
	}
	var stdout, stderr bytes.Buffer
	mainWith(testDeps(), []string{"-C", root, "config", "check"}, strings.NewReader(""), &stdout, &stderr)
	if !strings.Contains(stdout.String(), "REMOVED: issues.labels.support") || !strings.Contains(stdout.String(), "rota migrate labels") {
		t.Fatalf("config check output=%q", stdout.String())
	}

}
