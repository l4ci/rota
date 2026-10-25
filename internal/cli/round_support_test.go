package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// #1774 AC-1: one verb on main turns the support orchestrator on with a
// name, in the tracked config, and takes it off again.
func TestRoundSupportEnable(t *testing.T) {
	root := trackerProject(t, `{"issues":{"source":"github"}}`+"\n")
	tracked := filepath.Join(root, ".rota", "config.json")

	code, env, stderr := rotaRun(t, "--json", "-C", root, "round", "support", "enable", "mac")
	if code != ExitOK {
		t.Fatalf("enable: exit %d: %s %v", code, stderr, env)
	}
	data := envObj(env, "data")
	if data["name"] != "mac" || data["changed"] != true || data["file"] != ".rota/config.json" {
		t.Errorf("enable data = %v", data)
	}
	if raw, _ := os.ReadFile(tracked); !strings.Contains(string(raw), `"support": "mac"`) {
		t.Errorf("config.json = %s", raw)
	}
	if _, err := os.Stat(filepath.Join(root, ".rota", "config.local.json")); err == nil {
		t.Error("enable wrote config.local.json")
	}
	code, env, _ = rotaRun(t, "--json", "-C", root, "config", "show", "round.support")
	entries, _ := envObj(env, "data")["entries"].([]any)
	if code != ExitOK || len(entries) != 1 {
		t.Fatalf("config show round.support: exit %d: %v", code, envObj(env, "data"))
	}
	if e, _ := entries[0].(*jsonx.Object); e == nil {
		t.Errorf("entry = %v", entries[0])
	} else if v, _ := e.Get("value"); v != "mac" {
		t.Errorf("config show round.support value = %v", v)
	}

	code, env, _ = rotaRun(t, "--json", "-C", root, "round", "support", "enable", "mac")
	if code != ExitOK || envObj(env, "data")["changed"] != false {
		t.Errorf("enable again: exit %d: %v", code, env)
	}

	code, env, stderr = rotaRun(t, "--json", "-C", root, "round", "support", "disable")
	if code != ExitOK {
		t.Fatalf("disable: exit %d: %s %v", code, stderr, env)
	}
	if data := envObj(env, "data"); data["name"] != "mac" || data["changed"] != true {
		t.Errorf("disable data = %v", data)
	}
	if raw, _ := os.ReadFile(tracked); strings.Contains(string(raw), "support") {
		t.Errorf("config.json after disable = %s", raw)
	}
	code, env, _ = rotaRun(t, "--json", "-C", root, "round", "support", "disable")
	if code != ExitOK || envObj(env, "data")["changed"] != false {
		t.Errorf("disable again: exit %d: %v", code, env)
	}
}

// A support round needs a tracker: both subverbs refuse in file mode and
// leave the config alone.
func TestRoundSupportEnableRefusedInFileMode(t *testing.T) {
	root := trackerProject(t, `{"round":{"support":"mac"}}`+"\n")
	for _, args := range [][]string{{"enable", "mac"}, {"disable"}} {
		code, env, _ := rotaRun(t, append([]string{"--json", "-C", root, "round", "support"}, args...)...)
		if code != ExitRefused {
			t.Fatalf("%v: exit %d, want %d: %v", args, code, ExitRefused, env)
		}
		if got := envObj(env, "data")["blockedBy"]; got != "backend" {
			t.Errorf("%v: blockedBy = %v", args, got)
		}
	}
	if raw, _ := os.ReadFile(filepath.Join(root, ".rota", "config.json")); !strings.Contains(string(raw), `"support":"mac"`) {
		t.Errorf("config.json changed: %s", raw)
	}
}

func TestRoundSupportEnableBadName(t *testing.T) {
	root := trackerProject(t, `{"issues":{"source":"github"}}`+"\n")
	for _, args := range [][]string{{"enable"}, {"enable", ""}, {"enable", "Mac"}, {"enable", "my mac"}, {"enable", "-mac"}, {"enable", "a", "b"}, {"disable", "mac"}} {
		code, env, _ := rotaRun(t, append([]string{"--json", "-C", root, "round", "support"}, args...)...)
		if code != ExitUsage {
			t.Errorf("%q: exit %d, want %d: %v", args, code, ExitUsage, env)
		}
	}
	if raw, _ := os.ReadFile(filepath.Join(root, ".rota", "config.json")); strings.Contains(string(raw), "support") {
		t.Errorf("config.json = %s", raw)
	}
}

// Both CLI entry points must reserve main for the main machine.
func TestSupportNameMainReserved(t *testing.T) {
	for _, args := range [][]string{{"round", "support", "enable", "main"}, {"config", "set", "round.support", "main"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root := trackerProject(t, `{"issues":{"source":"github"}}`)
			before, err := os.ReadFile(filepath.Join(root, ".rota", "config.json"))
			if err != nil {
				t.Fatal(err)
			}
			code, env, stderr := rotaRun(t, append([]string{"--json", "-C", root}, args...)...)
			if code != ExitUsage || !strings.Contains(fmt.Sprint(env)+stderr, "reserved") {
				t.Fatalf("reserved name: exit %d env=%v stderr=%s", code, env, stderr)
			}
			after, err := os.ReadFile(filepath.Join(root, ".rota", "config.json"))
			if err != nil || string(after) != string(before) {
				t.Fatalf("reserved name changed config: %s, %v", after, err)
			}
		})
	}
}

// Only main names the support machine: the support side is fenced.
func TestRoundSupportEnableRefusedOnSupport(t *testing.T) {
	root := supportProject(t)
	for _, args := range [][]string{{"enable", "mac"}, {"disable"}} {
		code, env, _ := rotaRun(t, append([]string{"--json", "-C", root, "round", "support"}, args...)...)
		if code != ExitRefused {
			t.Fatalf("%v: exit %d, want %d: %v", args, code, ExitRefused, env)
		}
		if got := envObj(env, "data")["blockedBy"]; got != "role" {
			t.Errorf("%v: blockedBy = %v", args, got)
		}
	}
}

// supportReturnDeps serves a fake tracker and a forge that shares its labels.
func supportReturnDeps(fake *trackertest.Fake) *Deps {
	deps := testDeps()
	deps.NewTracker = func(context.Context, string, any) (backlog.Tracker, error) { return fake, nil }
	deps.RoundEnv = func(context.Context, string) round.Env {
		return round.Env{Worker: round.FromWorkerEnv(worker.Env{}), Git: git.Exec, Base: "main", Forge: returnForge{fake}, SupportLabel: "machine:mac",
			Round: roundcfg.Settings{Support: "mac", Role: roundcfg.RoleSupport}}
	}
	return deps
}

// returnForge is the round's Forge over the fake tracker: only labels are used.
type returnForge struct{ *trackertest.Fake }

func (returnForge) OpenPRs(context.Context) ([]tracker.PR, error)           { return nil, nil }
func (returnForge) MergedPRs(context.Context, string) ([]tracker.PR, error) { return nil, nil }
func (returnForge) PRState(context.Context, int) (string, error)            { return "", nil }
func (returnForge) PRView(context.Context, int) (tracker.PRInfo, error)     { return tracker.PRInfo{}, nil }
func (returnForge) PRSetBase(context.Context, int, string) error            { return nil }
func (returnForge) ClosedNumbers(string) []int                              { return nil }
func (returnForge) List(context.Context, tracker.ListFilter) ([]tracker.Issue, error) {
	return nil, nil
}

// #1827 AC-1: `round support return` on the support machine posts the reason
// under the support-return marker and drops the support label.
func TestRoundSupportReturn(t *testing.T) {
	root := supportProject(t)
	reason := filepath.Join(t.TempDir(), "reason.md")
	if err := os.WriteFile(reason, []byte("not ready: the premise is wrong\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := &trackertest.Fake{Issues: []backlog.Issue{
		{Key: tracker.NumKey(7), Title: "Add export", Labels: []string{"type:feature", "machine:mac"}, State: "open"},
	}}
	deps := supportReturnDeps(fake)

	code, env, stderr := rotaRunWith(t, deps, "--json", "-C", root, "round", "support", "return", "7", "--reason-file", reason)
	if code != ExitOK {
		t.Fatalf("exit %d: %s %v", code, stderr, env)
	}
	d := issueData(t, env)
	if issueGet(d, "id") != "7" || issueGet(d, "changed") != true || issueGet(d, "comment") == "" {
		t.Errorf("data = %v", d)
	}
	is, _ := fake.Get(context.Background(), tracker.NumKey(7), true)
	if slices.Contains(is.Labels, "machine:mac") {
		t.Errorf("labels = %v", is.Labels)
	}
	if len(is.Comments) != 1 || !strings.Contains(is.Comments[0].Body, "\n<!-- rota:support-return -->\nnot ready") || !strings.Contains(is.Comments[0].Body, "the premise is wrong") {
		t.Errorf("comments = %v", is.Comments)
	}
}

// Main hands nothing back; a missing reason is a usage error.
func TestRoundSupportReturnRefusals(t *testing.T) {
	reason := filepath.Join(t.TempDir(), "reason.md")
	os.WriteFile(reason, []byte("why\n"), 0o644)
	fake := &trackertest.Fake{Issues: []backlog.Issue{{Key: tracker.NumKey(7), Title: "x", State: "open"}}}

	main := trackerProject(t, `{"issues":{"source":"github"},"round":{"support":"mac"}}`+"\n")
	deps := supportReturnDeps(fake)
	mainEnv := deps.RoundEnv
	deps.RoundEnv = func(ctx context.Context, r string) round.Env {
		e := mainEnv(ctx, r)
		e.Round.Role = roundcfg.RoleMain
		return e
	}
	code, env, _ := rotaRunWith(t, deps, "--json", "-C", main, "round", "support", "return", "7", "--reason-file", reason)
	if code != ExitRefused || issueGet(issueData(t, env), "blockedBy") != "role" {
		t.Errorf("on main: exit %d %v", code, env)
	}

	root := supportProject(t)
	if code, _, _ := rotaRunWith(t, supportReturnDeps(fake), "--json", "-C", root, "round", "support", "return", "7"); code != ExitUsage {
		t.Errorf("no --reason-file: exit %d", code)
	}
	if code, _, _ := rotaRunWith(t, supportReturnDeps(fake), "--json", "-C", root, "round", "support", "return", "--reason-file", reason); code != ExitUsage {
		t.Errorf("no id: exit %d", code)
	}
}

func TestRoundCandidatesWarnLegacySupport(t *testing.T) {
	for _, tc := range []struct{ name, label, issues string }{
		{"default", "site:support", `"issues":{"source":"github"}`},
		{"custom", "remote", `"issues":{"source":"github","labels":{"support":"remote"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := trackerProject(t, `{`+tc.issues+`,"round":{"support":"mac","scope":"open"}}`)
			fake := &trackertest.Fake{Issues: []tracker.Issue{{Key: tracker.NumKey(7), Title: "Legacy offer", State: "open", Labels: []string{tc.label}}}}
			code, env, stderr := rotaRunWith(t, supportReturnDeps(fake), "--json", "-C", root, "round", "candidates")
			if code != ExitOK {
				t.Fatalf("exit %d: %v %s", code, env, stderr)
			}
			if !strings.Contains(stderr, "rota migrate labels --apply") || !strings.Contains(stderr, tc.label) || env["warnings"] == nil {
				t.Errorf("missing migration warning: env=%v stderr=%s", env, stderr)
			}
			candidates, _ := envObj(env, "data")["candidates"].([]any)
			if len(candidates) != 1 {
				t.Fatalf("candidates = %v", candidates)
			}
			c := candidates[0].(*jsonx.Object)
			if v, _ := c.Get("blockedBy"); v != "site" {
				t.Errorf("candidate = %v", c)
			}
			if v, _ := c.Get("ready"); v != false {
				t.Errorf("candidate ready = %v", v)
			}
		})
	}
}

// Returning a pinned issue releases the support machine's authoritative claim
// and derived label as well as its pin, without changing tracked .rota files.
func TestRoundSupportReturnReleasesClaimAndPin(t *testing.T) {
	root := supportProject(t)
	tracked := filepath.Join(root, ".rota", "config.json")
	before, err := os.ReadFile(tracked)
	if err != nil {
		t.Fatal(err)
	}
	reason := filepath.Join(t.TempDir(), "reason.md")
	if err := os.WriteFile(reason, []byte("premise is wrong\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := &trackertest.Fake{Issues: []backlog.Issue{{Key: tracker.NumKey(7), Title: "Add export", State: "open",
		Labels:   []string{"machine:mac", "in-progress", "claimed:ben@mac", "keep"},
		Comments: []tracker.Comment{{ID: "old", Body: "<!-- rota:claim mac-ben@3 -->"}},
	}}}
	deps := supportReturnDeps(fake)
	code, env, stderr := rotaRunWith(t, deps, "--json", "-C", root, "round", "support", "return", "7", "--reason-file", reason)
	if code != ExitOK || issueGet(issueData(t, env), "changed") != true {
		t.Fatalf("return: %d %v %s", code, env, stderr)
	}
	is, _ := fake.Get(context.Background(), tracker.NumKey(7), true)
	if !slices.Equal(is.Labels, []string{"keep"}) {
		t.Errorf("labels after return %v want [keep]", is.Labels)
	}
	released, returned := false, false
	for _, c := range is.Comments {
		released = released || strings.Contains(c.Body, "<!-- rota:release mac-ben@3 -->")
		returned = returned || strings.Contains(c.Body, "<!-- rota:support-return -->\npremise is wrong")
	}
	if !released || !returned {
		t.Errorf("missing release or reason: %+v", is.Comments)
	}
	after, err := os.ReadFile(tracked)
	if err != nil || string(before) != string(after) {
		t.Fatal("return changed tracked config")
	}
	n := len(is.Comments)
	code, env, stderr = rotaRunWith(t, deps, "--json", "-C", root, "round", "support", "return", "7", "--reason-file", reason)
	if code != ExitOK || issueGet(issueData(t, env), "changed") != false || len(fake.Issues[0].Comments) != n {
		t.Fatalf("repeat return: %d %v %s", code, env, stderr)
	}
}
