package cli

import (
	"strings"
	"testing"
	"time"
)

func TestEscalateSendRequiresOptionsAndRecommend(t *testing.T) {
	for name, extra := range map[string][]string{
		"neither":           nil,
		"options only":      {"--options", "a", "--options", "b"},
		"recommend only":    {"--recommend", "1"},
		"recommend too big": {"--options", "a", "--options", "b", "--recommend", "3"},
		"recommend zero":    {"--options", "a", "--options", "b", "--recommend", "0"},
		"one option":        {"--options", "a", "--recommend", "1"},
		"blank option":      {"--options", "a", "--options", " ", "--recommend", "1"},
	} {
		dir := escProject(t, ghCfg)
		f := &fakeThread{}
		deps := useThread(t, f)
		h := &escHost{cliHost: cliHost{herdr: true}}
		clock := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
		useEscalation(deps, h, "1", &clock)
		args := append([]string{"round", "escalate", "send", "7", "--title", "Pick one", "--body-file", "body.md"}, extra...)
		code, out, errOut := rotaInWith(t, deps, dir, args...)
		if code != 4 {
			t.Errorf("%s: exit %d, want 4: %s %s", name, code, out, errOut)
		}
		if len(f.comments) != 0 || len(h.notes) != 0 {
			t.Errorf("%s: a refused send must post and notify nothing: %v %v", name, f.comments, h.notes)
		}
	}
}

func TestEscalateOptionsPostedAndBareOkResolves(t *testing.T) {
	dir := escProject(t, ghCfg)
	f := &fakeThread{}
	deps := useThread(t, f)
	h := &escHost{cliHost: cliHost{herdr: true}}
	clock := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	useEscalation(deps, h, "1", &clock)

	code, out, errOut := rotaInWith(t, deps, dir, "round", "escalate", "send", "7", "--title", "Keep the flag?", "--body-file", "body.md",
		"--options", "keep it", "--options", "drop it", "--recommend", "2", "--cost-if-wrong", "one more release", "--json")
	if code != 0 {
		t.Fatalf("send: %d %s %s", code, out, errOut)
	}
	body := f.comments[0]["body"].(string)
	for _, want := range []string{"1. keep it\n", "2. drop it (recommended)\n", "Recommended: 2. drop it\n", "Cost if wrong: one more release\n"} {
		if !strings.Contains(body, want) {
			t.Errorf("comment lacks %q:\n%s", want, body)
		}
	}
	e := data(t, out)["escalation"].(map[string]any)
	if e["recommend"] != 2.0 || e["cost"] != "one more release" || len(e["options"].([]any)) != 2 {
		t.Errorf("entry: %v", e)
	}

	f.add("ok")
	code, out, errOut = rotaInWith(t, deps, dir, "round", "escalate", "check", "--json")
	if code != 0 {
		t.Fatalf("check: %d %s %s", code, out, errOut)
	}
	row := data(t, out)["escalations"].([]any)[0].(map[string]any)
	ans := row["answer"].(map[string]any)
	if row["status"] != "answered" || ans["option"] != 2.0 || ans["body"] != "ok" || row["resolved"] != "drop it" {
		t.Errorf("a bare ok must resolve to option 2: %v", row)
	}
}

func TestEscalateProseReplyResolvesNothing(t *testing.T) {
	dir := escProject(t, ghCfg)
	f := &fakeThread{}
	deps := useThread(t, f)
	h := &escHost{cliHost: cliHost{herdr: true}}
	clock := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	useEscalation(deps, h, "1", &clock)
	if code, out, _ := rotaInWith(t, deps, dir, "round", "escalate", "send", "7", "--title", "q", "--body-file", "body.md",
		"--options", "a", "--options", "b", "--recommend", "1"); code != 0 {
		t.Fatalf("send: %d %s", code, out)
	}
	f.add("ok but only for the docs")
	_, out, _ := rotaInWith(t, deps, dir, "round", "escalate", "check", "--json")
	row := data(t, out)["escalations"].([]any)[0].(map[string]any)
	if _, has := row["resolved"]; has {
		t.Errorf("prose is not resolved: %v", row)
	}
}
