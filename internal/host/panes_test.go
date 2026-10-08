package host

import (
	"context"
	"strings"
	"testing"
	"time"
)

const outAck = `{"id":"rota-limit-watch","result":{"type":"subscription_started"}}`

func outEvent(pane, line, text string) string {
	return `{"event":"pane.output_matched","data":{"pane_id":"` + pane + `","matched_line":"` + line +
		`","read":{"pane_id":"` + pane + `","workspace_id":"w1","tab_id":"w1:t1","source":"recent","format":"text","text":"` + text + `","revision":4,"truncated":false}}}`
}

func TestWatchOutputSubscribesPerPaneWithTheRegex(t *testing.T) {
	d, s, f := pipeDeps(t, "herdr 0.9.3", nil)
	s.serve(t, outAck, `{"event":"pane.scroll_changed","data":{"pane_id":"w1:p1"}}`, `not json`, outEvent("w1:p2", "usage limit reached", "a\\nusage limit reached"))
	w, err := New("herdr", d).(OutputWatcher).WatchOutput(context.Background(), []string{"w1:p1", "w1:p2"}, `(?i)(?:usage limit reached)`)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m, err := w.Next(ctx)
	if err != nil || m.Pane != "w1:p2" || m.Line != "usage limit reached" || m.Text != "a\nusage limit reached" {
		t.Fatalf("Next = %+v, %v", m, err)
	}
	req := <-s.req
	if req["method"] != "events.subscribe" {
		t.Errorf("request = %v", req)
	}
	subs := req["params"].(map[string]any)["subscriptions"].([]any)
	if len(subs) != 2 {
		t.Fatalf("subscriptions = %v", subs)
	}
	for i, pane := range []string{"w1:p1", "w1:p2"} {
		m := subs[i].(map[string]any)
		match := m["match"].(map[string]any)
		if m["type"] != "pane.output_matched" || m["pane_id"] != pane || m["source"] != "recent" ||
			match["type"] != "regex" || match["value"] != `(?i)(?:usage limit reached)` {
			t.Errorf("subscription %d = %v", i, m)
		}
		if _, set := m["strip_ansi"]; set {
			t.Errorf("strip_ansi must stay at its default: %v", m)
		}
	}
	if f.count("herdr --version") != 1 {
		t.Errorf("version not checked: %s", f.log())
	}
}

func TestWatchOutputTreatsAMatchingReplyAsAMatch(t *testing.T) {
	d, s, _ := pipeDeps(t, "herdr 0.9.3", nil)
	s.serve(t, `{"id":"rota-limit-watch","result":{"type":"output_matched","pane_id":"w1:p1","revision":2,"matched_line":"usage limit reached","read":{"pane_id":"w1:p1","workspace_id":"w1","tab_id":"w1:t1","source":"recent","format":"text","text":"usage limit reached","revision":2,"truncated":false}}}`)
	w, err := New("herdr", d).(OutputWatcher).WatchOutput(context.Background(), []string{"w1:p1"}, "x")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m, err := w.Next(ctx)
	if err != nil || m.Pane != "w1:p1" || m.Text != "usage limit reached" {
		t.Fatalf("Next = %+v, %v", m, err)
	}
}

func TestWatchOutputFailsOnARefusedSubscriptionOrVersion(t *testing.T) {
	d, s, _ := pipeDeps(t, "herdr 0.9.3", nil)
	s.serve(t, `{"id":"rota-limit-watch","error":{"code":"invalid_request","message":"bad regex"}}`)
	if _, err := New("herdr", d).(OutputWatcher).WatchOutput(context.Background(), []string{"w1:p1"}, "("); err == nil || !strings.Contains(err.Error(), "bad regex") {
		t.Errorf("err = %v", err)
	}
	d, _, _ = pipeDeps(t, "herdr 0.10.0", nil)
	if _, err := New("herdr", d).(OutputWatcher).WatchOutput(context.Background(), []string{"w1:p1"}, "x"); err == nil || !strings.Contains(err.Error(), "unsupported herdr") {
		t.Errorf("err = %v", err)
	}
}

func TestHerdrSendPaneTypesTextAndEnter(t *testing.T) {
	d, s, _ := pipeDeps(t, "herdr 0.9.3", nil)
	s.serve(t, `{"id":"rota-pane-send_input","result":{"type":"ok"}}`)
	if err := New("herdr", d).(PaneHost).SendPane(context.Background(), "w1:p1", "Continue."); err != nil {
		t.Fatal(err)
	}
	req := <-s.req
	p := req["params"].(map[string]any)
	if req["method"] != "pane.send_input" || p["pane_id"] != "w1:p1" || p["text"] != "Continue." {
		t.Errorf("request = %v", req)
	}
	if keys := p["keys"].([]any); len(keys) != 1 || keys[0] != "enter" {
		t.Errorf("keys = %v", keys)
	}
	d, s, _ = pipeDeps(t, "herdr 0.9.3", nil)
	s.serve(t, `{"id":"x","error":{"code":"pane_not_found","message":"no such pane"}}`)
	if err := New("herdr", d).(PaneHost).SendPane(context.Background(), "w1:p9", "x"); err == nil || !strings.Contains(err.Error(), "no such pane") {
		t.Errorf("err = %v", err)
	}
}

func TestHerdrCapturePaneReadsUnwrappedText(t *testing.T) {
	d, s, _ := pipeDeps(t, "herdr 0.9.3", nil)
	s.serve(t, `{"id":"x","result":{"type":"pane_read","read":{"pane_id":"w1:p1","workspace_id":"w1","tab_id":"w1:t1","source":"recent_unwrapped","format":"text","text":"hello\nusage limit reached","revision":1,"truncated":false}}}`)
	got := New("herdr", d).(PaneHost).CapturePane(context.Background(), "w1:p1", 50)
	if got != "hello\nusage limit reached" {
		t.Errorf("CapturePane = %q", got)
	}
	req := <-s.req
	p := req["params"].(map[string]any)
	if req["method"] != "pane.read" || p["source"] != "recent_unwrapped" || p["pane_id"] != "w1:p1" {
		t.Errorf("request = %v", req)
	}
}

func TestHerdrPaneOfResolvesTheAgentPane(t *testing.T) {
	d, _, _ := pipeDeps(t, "herdr 0.9.3", panes())
	h := New("herdr", d).(PaneHost)
	if got := h.PaneOf(context.Background(), "w1", "w1:t1"); got != "w1:p1" {
		t.Errorf("PaneOf = %q", got)
	}
	if got := h.PaneOf(context.Background(), "w9", "w1:t9"); got != "" {
		t.Errorf("PaneOf of a missing agent = %q", got)
	}
	if got := h.PaneOf(context.Background(), "w1", ""); got != "" {
		t.Errorf("PaneOf without a handle = %q", got)
	}
}

func TestTmuxSendPaneTypesLiterallyThenEnter(t *testing.T) {
	f := &fake{handler: func(string, []string) Result { return Result{} }}
	if err := New("tmux", deps(f, nil, &clock{})).(PaneHost).SendPane(context.Background(), "%3", "Continue -x."); err != nil {
		t.Fatal(err)
	}
	want := "tmux send-keys -t %3 -l -- Continue -x.\ntmux send-keys -t %3 C-m"
	if f.log() != want {
		t.Errorf("calls:\n%s\nwant:\n%s", f.log(), want)
	}
	if err := New("tmux", deps(f, nil, &clock{})).(PaneHost).SendPane(context.Background(), "", "x"); err == nil {
		t.Error("empty pane accepted")
	}
}

func TestTmuxCapturePane(t *testing.T) {
	f := &fake{handler: func(_ string, a []string) Result { return Result{Stdout: "pane text\n"} }}
	h := New("tmux", deps(f, nil, &clock{})).(PaneHost)
	if got := h.CapturePane(context.Background(), "%3", 40); got != "pane text\n" {
		t.Errorf("CapturePane = %q", got)
	}
	if f.log() != "tmux capture-pane -pJ -t %3" {
		t.Errorf("calls: %s", f.log())
	}
	if got := h.CapturePane(context.Background(), "", 40); got != "" || f.count("tmux") != 1 {
		t.Errorf("empty pane captured %q", got)
	}
	if got := h.PaneOf(context.Background(), "w1", "rota:w1"); got != "rota:w1" {
		t.Errorf("PaneOf = %q", got)
	}
}

func TestCurrentPaneByKind(t *testing.T) {
	both := func(k string) string {
		return map[string]string{"HERDR_PANE_ID": "w1:p3", "TMUX_PANE": "%3"}[k]
	}
	none := func(string) string { return "" }
	for _, tc := range []struct {
		name, kind string
		env        func(string) string
		want       string
	}{
		{"herdr", "herdr", both, "w1:p3"},
		{"tmux", "tmux", both, "%3"},
		{"neither", "herdr", none, ""},
		{"neither tmux", "tmux", none, ""},
		{"solo falls back to tmux", Solo, both, "%3"},
	} {
		if got := CurrentPane(tc.kind, tc.env); got != tc.want {
			t.Errorf("%s: CurrentPane(%q) = %q, want %q", tc.name, tc.kind, got, tc.want)
		}
	}
}

func TestCurrentPaneAnyPrefersHerdr(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for _, tc := range []struct {
		name       string
		env        map[string]string
		pane, kind string
	}{
		{"both", map[string]string{"HERDR_PANE_ID": "w1:p3", "TMUX_PANE": "%3"}, "w1:p3", "herdr"},
		{"tmux only", map[string]string{"TMUX_PANE": "%3"}, "%3", "tmux"},
		{"neither", nil, "", ""},
	} {
		if pane, kind := CurrentPaneAny(env(tc.env)); pane != tc.pane || kind != tc.kind {
			t.Errorf("%s: CurrentPaneAny = %q, %q; want %q, %q", tc.name, pane, kind, tc.pane, tc.kind)
		}
	}
}
