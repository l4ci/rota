package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/roundlease"
)

var t0 = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

func pf(f float64) *float64 { return &f }

func payload(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestContextPct(t *testing.T) {
	cases := []struct {
		name, in string
		want     *float64
	}{
		{"used_percentage wins", `{"context_window":{"used_percentage":42.5,"context_window_size":1000,"current_usage":{"input_tokens":900}}}`, pf(42.5)},
		{"derived", `{"context_window":{"context_window_size":200000,"current_usage":{"input_tokens":100000,"cache_creation_input_tokens":20000,"cache_read_input_tokens":30000}}}`, pf(75)},
		{"null used_percentage falls back", `{"context_window":{"used_percentage":null,"context_window_size":1000,"current_usage":{"input_tokens":500}}}`, pf(50)},
		{"null current_usage", `{"context_window":{"context_window_size":1000,"current_usage":null}}`, nil},
		{"no size", `{"context_window":{"current_usage":{"input_tokens":5}}}`, nil},
		{"no window", `{}`, nil},
	}
	for _, c := range cases {
		got := ContextPct(payload(t, c.in))
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestDumpKeepsCountersAndSweeps(t *testing.T) {
	cd := t.TempDir()
	in := `{"session_id":"s1","cwd":"/w","context_window":{"used_percentage":10},"rate_limits":{"five_hour":{"used_percentage":3}}}`
	if err := Dump(cd, []byte(in), t0); err != nil {
		t.Fatal(err)
	}
	p, _ := StatePath(cd, "s1")
	st, found, err := ReadState(p)
	if err != nil || !found || st.UpdatedAt != "2026-10-03T12:00:00Z" || *st.ContextPct != 10 || !strings.Contains(string(st.RateLimits), "five_hour") {
		t.Fatalf("state: %+v %v %v", st, found, err)
	}
	// Stop hook counters survive the next refresh.
	UpdateState(p, func(s State, _ bool) State { s.HandoffBlocks, s.HandoffFailed = 2, true; return s })
	if err := Dump(cd, []byte(`{"session_id":"s1","context_window":{"used_percentage":20}}`), t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	st, _, _ = ReadState(p)
	if st.HandoffBlocks != 2 || !st.HandoffFailed || *st.ContextPct != 20 || st.RateLimits != nil {
		t.Fatalf("counters lost or stale fields kept: %+v", st)
	}
	// An old session is swept by another session's dump; a fresh one stays.
	Dump(cd, []byte(`{"session_id":"old"}`), t0.Add(-48*time.Hour))
	Dump(cd, []byte(`{"session_id":"s2"}`), t0.Add(2*time.Minute))
	if _, err := os.Stat(filepath.Join(cd, "rota", "session", "old.json")); !os.IsNotExist(err) {
		t.Fatalf("old state should be swept: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("fresh state swept: %v", err)
	}
}

func TestDumpRejectsBadInput(t *testing.T) {
	cd := t.TempDir()
	for _, in := range []string{``, `not json`, `[]`, `{}`, `{"session_id":"../x"}`, `{"session_id":"a/b"}`} {
		if err := Dump(cd, []byte(in), t0); err == nil {
			t.Errorf("%q should fail", in)
		}
	}
	if _, err := os.Stat(filepath.Join(cd, "rota")); err == nil {
		t.Error("a rejected payload must write nothing")
	}
}

func TestDecideStop(t *testing.T) {
	set := Settings{Threshold: 75, StateMaxAge: 120, HandoffMaxAge: 900, HandoffMaxBlks: 2}
	st := func(pct float64) State {
		return State{UpdatedAt: t0.Add(-10 * time.Second).Format(time.RFC3339), ContextPct: pf(pct)}
	}
	none := Handoff{}
	for _, c := range []struct {
		pct   float64
		block bool
	}{{74, false}, {74.9, false}, {75, true}, {99, true}} {
		d := DecideStop(StopIn{}, st(c.pct), set, none, "/h.md", t0)
		if d.Block != c.block {
			t.Errorf("pct %v: block=%v", c.pct, d.Block)
		}
	}
	d := DecideStop(StopIn{}, st(80), set, none, "/h.md", t0)
	if !strings.Contains(d.Reason, "80%") || !strings.Contains(d.Reason, "75%") || !strings.Contains(d.Reason, "/h.md") || !strings.Contains(d.Reason, "/exit") {
		t.Errorf("reason: %s", d.Reason)
	}
	// A stale reading or a missing percentage passes.
	old := st(99)
	old.UpdatedAt = t0.Add(-3 * time.Minute).Format(time.RFC3339)
	if DecideStop(StopIn{}, old, set, none, "", t0).Block {
		t.Error("stale state blocked")
	}
	if DecideStop(StopIn{}, State{UpdatedAt: st(0).UpdatedAt}, set, none, "", t0).Block {
		t.Error("no percentage blocked")
	}
	// A fresh handoff passes without a block.
	fresh := Handoff{Exists: true, ModTime: t0.Add(-time.Minute)}
	if DecideStop(StopIn{}, st(99), set, fresh, "", t0).Block {
		t.Error("fresh handoff still blocked")
	}
	if !DecideStop(StopIn{}, st(99), set, Handoff{Exists: true, ModTime: t0.Add(-time.Hour)}, "", t0).Block {
		t.Error("a day-old handoff must not count")
	}
}

func TestDecideStopLoopPrevention(t *testing.T) {
	set := Settings{Threshold: 75, StateMaxAge: 120, HandoffMaxAge: 900, HandoffMaxBlks: 2}
	cur := State{UpdatedAt: t0.Format(time.RFC3339), ContextPct: pf(90)}
	// First stop: block, stamp.
	d := DecideStop(StopIn{}, cur, set, Handoff{}, "", t0)
	if !d.Block || !d.Persist || d.State.BlockedAt == "" || d.State.HandoffBlocks != 0 {
		t.Fatalf("first: %+v", d)
	}
	cur = d.State
	// Active, handoff written after the block: pass.
	written := Handoff{Exists: true, ModTime: t0.Add(5 * time.Second)}
	if DecideStop(StopIn{StopHookActive: true}, cur, set, written, "", t0.Add(10*time.Second)).Block {
		t.Error("active + new handoff should pass")
	}
	// Active, handoff older than the block: not the answer to it.
	older := Handoff{Exists: true, ModTime: t0.Add(-time.Second)}
	if !DecideStop(StopIn{StopHookActive: true}, cur, set, older, "", t0.Add(10*time.Second)).Block {
		t.Error("active + older handoff should block")
	}
	// Active, no handoff: block up to the cap, then pass and record failure.
	for i := 1; i <= 2; i++ {
		d = DecideStop(StopIn{StopHookActive: true}, cur, set, Handoff{}, "", t0.Add(time.Duration(i)*10*time.Second))
		if !d.Block || d.State.HandoffBlocks != i {
			t.Fatalf("reblock %d: %+v", i, d)
		}
		cur = d.State
	}
	d = DecideStop(StopIn{StopHookActive: true}, cur, set, Handoff{}, "", t0.Add(time.Minute))
	if d.Block || !d.State.HandoffFailed || !d.Persist {
		t.Fatalf("cap: %+v", d)
	}
	cur = d.State
	// Failed sessions are never held again, even without stop_hook_active.
	if DecideStop(StopIn{}, cur, set, Handoff{}, "", t0.Add(time.Minute)).Block {
		t.Error("failed session blocked again")
	}
}

func TestShouldInject(t *testing.T) {
	ho := Handoff{Exists: true, ModTime: t0.Add(-time.Minute)}
	max := 15 * time.Minute
	for _, c := range []struct {
		name       string
		source     string
		orch, free bool
		ho         Handoff
		head       string
		want       bool
	}{
		{"orchestrator startup", "startup", true, false, ho, "x", true},
		{"orchestrator clear", "clear", true, false, ho, "x", true},
		{"resume keeps", "resume", true, false, ho, "x", false},
		{"compact keeps", "compact", true, false, ho, "x", false},
		{"no file", "startup", true, false, Handoff{}, "", false},
		{"other holder", "startup", false, false, ho, HandoffMarker, false},
		{"no lease, marked, fresh", "startup", false, true, ho, HandoffMarker, true},
		{"no lease, unmarked", "startup", false, true, ho, "# pause", false},
		{"no lease, marked, old", "startup", false, true, Handoff{Exists: true, ModTime: t0.Add(-time.Hour)}, HandoffMarker, false},
	} {
		if got := ShouldInject(c.source, c.orch, c.free, c.ho, c.head, max, t0); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

func TestConsumeRenames(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "main.md")
	os.WriteFile(p, []byte("body"), 0o644)
	os.WriteFile(p+".consumed", []byte("older"), 0o644)
	body, ok := Consume(p)
	if !ok || body != "body" {
		t.Fatal(body, ok)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("file should be gone")
	}
	if b, _ := os.ReadFile(p + ".consumed"); string(b) != "body" {
		t.Errorf("consumed: %q", b)
	}
	if _, ok := Consume(p); ok {
		t.Error("second consume must fail")
	}
}

func TestIdentify(t *testing.T) {
	cd := t.TempDir()
	alive := map[int]uint64{10: 100, 20: 200}
	env := roundlease.Env{
		Host:      "h",
		Alive:     func(p int) bool { _, ok := alive[p]; return ok },
		StartTime: func(p int) (uint64, bool) { s, ok := alive[p]; return s, ok },
		Now:       func() time.Time { return t0 },
	}
	noenv := func(string) string { return "" }
	if w := Identify(env, noenv, 10, cd); w.Orchestrator || !w.LeaseFree {
		t.Fatalf("no lease: %+v", w)
	}
	env.Acquire(cd, "/r", roundlease.Holder{PID: 10, Start: 100}, 1)
	if w := Identify(env, noenv, 10, cd); !w.Orchestrator || w.LeaseFree {
		t.Fatalf("holder: %+v", w)
	}
	if w := Identify(env, noenv, 20, cd); w.Orchestrator || w.LeaseFree {
		t.Fatalf("worker: %+v", w)
	}
	delete(alive, 10)
	if w := Identify(env, noenv, 10, cd); w.Orchestrator || !w.LeaseFree {
		t.Fatalf("stale: %+v", w)
	}
}

const original = `{
  "model": "opus",
  "statusLine": {
    "type": "command",
    "command": "~/bin/my line.sh 'x'",
    "padding": 0
  },
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "notify-done"
          }
        ]
      }
    ]
  }
}
`

func parse(t *testing.T, s string) *jsonx.Object {
	t.Helper()
	v, err := jsonx.Decode([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jsonx.Object)
}

func dump(t *testing.T, o *jsonx.Object) string {
	t.Helper()
	b, err := jsonx.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

func TestInstallBlocksWithoutWrap(t *testing.T) {
	o := parse(t, original)
	out, err := Install(InstallIn{Scope: ScopeProjectLocal, Files: map[Scope]*jsonx.Object{ScopeProjectLocal: o}})
	if err != nil || !out.Blocked || out.Changed {
		t.Fatalf("%+v %v", out, err)
	}
	if dump(t, o) != original {
		t.Error("a blocked install must not touch the settings")
	}
}

func TestWrapRoundTripIsByteExact(t *testing.T) {
	o := parse(t, original)
	files := map[Scope]*jsonx.Object{ScopeProjectLocal: o}
	out, err := Install(InstallIn{Scope: ScopeProjectLocal, Wrap: true, Files: files})
	if err != nil || out.Blocked || !out.Changed || out.Statusline != SLWrapped {
		t.Fatalf("%+v %v", out, err)
	}
	got := dump(t, o)
	for _, want := range []string{
		`rota statusline dump --then '~/bin/my line.sh '\\''x'\\'''`,
		`"rotaWrapped": "~/bin/my line.sh 'x'"`,
		`rota hook stop # rota-hook`, `rota hook session-start # rota-hook`, `^(startup|clear)$`, `notify-done`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in\n%s", want, got)
		}
	}
	// Re-run: nothing changes.
	again := parse(t, got)
	out, _ = Install(InstallIn{Scope: ScopeProjectLocal, Wrap: true, Files: map[Scope]*jsonx.Object{ScopeProjectLocal: again}})
	if out.Changed || out.Statusline != SLKept || dump(t, again) != got {
		t.Fatalf("rerun: %+v", out)
	}
	// Uninstall restores the original file exactly.
	rm := Uninstall(again)
	if dump(t, again) != original {
		t.Errorf("not restored:\n%s", dump(t, again))
	}
	if len(rm) != 5 {
		t.Errorf("removed %v", rm)
	}
	if rm2 := Uninstall(again); len(rm2) != 0 {
		t.Errorf("second uninstall removed %v", rm2)
	}
}

func TestInstallFreshAndUninstall(t *testing.T) {
	o := jsonx.NewObject()
	out, err := Install(InstallIn{Scope: ScopeProject, Files: map[Scope]*jsonx.Object{ScopeProject: o}})
	if err != nil || out.Statusline != SLInstalled || !out.Changed || out.Blocked {
		t.Fatalf("%+v %v", out, err)
	}
	if e := MarkedEvents(o); len(e) != 4 {
		t.Errorf("events %v", e)
	}
	Uninstall(o)
	if dump(t, o) != "{}\n" {
		t.Errorf("left %s", dump(t, o))
	}
}

func TestInstallGuardEntry(t *testing.T) {
	o := jsonx.NewObject()
	if _, err := Install(InstallIn{Scope: ScopeProject, Files: map[Scope]*jsonx.Object{ScopeProject: o}}); err != nil {
		t.Fatal(err)
	}
	got := dump(t, o)
	if !strings.Contains(got, `"PreToolUse"`) || !strings.Contains(got, `"matcher": "Bash"`) || !strings.Contains(got, GuardCommand) {
		t.Errorf("guard entry missing:\n%s", got)
	}
	if MarkedEvents(o)[EventGuard] != GuardCommand {
		t.Errorf("MarkedEvents: %v", MarkedEvents(o))
	}
	if out, _ := Install(InstallIn{Scope: ScopeProject, Files: map[Scope]*jsonx.Object{ScopeProject: o}}); out.Changed {
		t.Error("second install changed the file")
	}
	// A user's own PreToolUse hook is kept through install and uninstall.
	own := parse(t, `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"mine.sh"}]}]}}`)
	Install(InstallIn{Scope: ScopeProject, Files: map[Scope]*jsonx.Object{ScopeProject: own}})
	Uninstall(own)
	if !strings.Contains(dump(t, own), "mine.sh") || strings.Contains(dump(t, own), Marker) {
		t.Errorf("uninstall:\n%s", dump(t, own))
	}
}

func TestWrapUserStatuslineFromProjectLocalShadows(t *testing.T) {
	user := parse(t, original)
	local := jsonx.NewObject()
	files := map[Scope]*jsonx.Object{ScopeProjectLocal: local, ScopeUser: user}
	out, _ := Install(InstallIn{Scope: ScopeProjectLocal, Wrap: true, Files: files})
	if out.Statusline != SLWrapped || dump(t, user) != original {
		t.Fatalf("%+v; user touched", out)
	}
	got := dump(t, local)
	if !strings.Contains(got, `"rotaWrappedFrom": "user"`) || !strings.Contains(got, `"padding": 0`) {
		t.Errorf("shadow entry:\n%s", got)
	}
	Uninstall(local)
	if dump(t, local) != "{}\n" {
		t.Errorf("shadow not removed:\n%s", dump(t, local))
	}
}

func TestInstallPresentWhenHigherScopeWins(t *testing.T) {
	local := parse(t, original)
	user := jsonx.NewObject()
	files := map[Scope]*jsonx.Object{ScopeProjectLocal: local, ScopeUser: user}
	out, _ := Install(InstallIn{Scope: ScopeUser, Files: files})
	if out.Blocked || out.Statusline != SLPresent || !out.Changed {
		t.Fatalf("%+v", out)
	}
	if _, _, ok := StatusLine(user); ok {
		t.Error("must not write a shadowed statusline")
	}
	// Lower-ranked existing statusline would be shadowed: blocked.
	user2 := parse(t, original)
	out, _ = Install(InstallIn{Scope: ScopeProjectLocal, Files: map[Scope]*jsonx.Object{ScopeProjectLocal: jsonx.NewObject(), ScopeUser: user2}})
	if !out.Blocked {
		t.Fatalf("%+v", out)
	}
}

func TestInstallRefusesNonArrayHooks(t *testing.T) {
	o := parse(t, `{"hooks":{"Stop":"nope"}}`)
	if _, err := Install(InstallIn{Scope: ScopeProject, Files: map[Scope]*jsonx.Object{ScopeProject: o}}); err == nil {
		t.Error("should refuse")
	}
}

func TestLoadSettings(t *testing.T) {
	cfg := func(s string) any { v, _ := jsonx.Decode([]byte(s)); return v }
	s, err := LoadSettings(cfg(`{}`))
	if err != nil || s != (Settings{75, 120, 900, 2, false, 0}) {
		t.Fatalf("%+v %v", s, err)
	}
	// A bad usageThreshold matters only once switchOnUsage is on.
	if s, err := LoadSettings(cfg(`{"orchestrator":{"usageThreshold":0}}`)); err != nil || s.Threshold != 75 {
		t.Errorf("usageThreshold 0 with switching off: %+v %v", s, err)
	}
	if s, err := LoadSettings(cfg(`{"orchestrator":{"switchOnUsage":true}}`)); err != nil || !s.SwitchOnUsage || s.UsageThreshold != 90 {
		t.Errorf("switching on: %+v %v", s, err)
	}
	if _, err := LoadSettings(cfg(`{"orchestrator":{"switchOnUsage":true,"usageThreshold":0}}`)); err == nil {
		t.Error("usageThreshold 0 with switching on must be an error")
	}
	for _, bad := range []string{`{"orchestrator":{"handoffThreshold":0}}`, `{"orchestrator":{"handoffThreshold":101}}`, `{"orchestrator":{"handoffThreshold":"x"}}`, `{"orchestrator":{"handoffMaxBlocks":-1}}`, `{"orchestrator":{"stateMaxAgeSeconds":0}}`} {
		if _, err := LoadSettings(cfg(bad)); err == nil {
			t.Errorf("%s should fail", bad)
		}
	}
}
