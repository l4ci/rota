package cli

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/golden"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tui"
)

const vw, vh = 100, 30

var keyBytes = map[string]string{"down": "\x1b[B", "up": "\x1b[A", "enter": "\r", "esc": "\x1b"}

// viewIn is a golden's recorded input: the keys pressed and the frame size.
type viewIn struct {
	Keys []string `json:"keys"`
	W    int      `json:"w"`
	H    int      `json:"h"`
}

// drive presses keys on m the way the driver would: a Cmd's Exec runs and its
// error comes back as Done. quit reports whether a key ended the screen.
func viewDrive(t *testing.T, m tui.Model, names ...string) (tui.Model, bool) {
	t.Helper()
	for _, n := range names {
		b, ok := keyBytes[n]
		if !ok {
			b = n
		}
		for _, k := range tui.DecodeKeys([]byte(b)) {
			var cmd tui.Cmd
			m, cmd = m.Update(k)
			if cmd.Exec != nil {
				m, cmd = m.Update(tui.Done{Err: cmd.Exec()})
			}
			if cmd.Quit {
				return m, true
			}
		}
	}
	return m, false
}

func viewFrame(t *testing.T, m tui.Model) []string {
	t.Helper()
	lines := strings.Split(tui.Strip(m.Render(vw, vh, tui.Style{Color: true})), "\n")
	if len(lines) != vh {
		t.Errorf("frame has %d lines, want %d", len(lines), vh)
	}
	for i, l := range lines {
		if n := len([]rune(l)); n > vw {
			t.Errorf("line %d is %d runes: %q", i, n, l)
		}
	}
	return lines
}

func checkFrame(t *testing.T, m tui.Model, keys []string) {
	t.Helper()
	golden.Check(t, viewIn{keys, vw, vh}, viewFrame(t, m))
}

func objList(items ...*jsonx.Object) []any {
	out := make([]any, len(items))
	for i, o := range items {
		out[i] = o
	}
	return out
}

func buildBrowser(t *testing.T, v ViewFunc, data any) browser {
	t.Helper()
	m, err := v(&Ctx{}, Result{Data: data})
	if err != nil {
		t.Fatal(err)
	}
	return m.(browser)
}

func viewDoctorData() *jsonx.Object {
	return knObj("ok", false, "checks", objList(
		knObj("name", "git", "status", "pass", "detail", "git 2.45"),
		knObj("name", "gh auth", "status", "pass", "detail", "logged in as l4ci"),
		knObj("name", "herdr hook", "status", "warn", "detail", "hook not installed", "hint", "run: rota hook install"),
		knObj("name", "skills", "status", "fail", "detail", "3 skills older than the binary", "hint", "run: rota skills update"),
		knObj("name", "codex", "status", "pass", "detail", "codex 0.3"),
		knObj("name", "forge", "status", "fail", "detail", "no token for github.com", "hint", "run: gh auth login"),
	))
}

func TestDoctorViewFailingRowsFirst(t *testing.T) {
	b := buildBrowser(t, doctorView, viewDoctorData())
	var order []string
	for _, r := range b.Rows {
		order = append(order, r.Key)
	}
	if got, want := strings.Join(order, ","), "skills,forge,herdr hook,git,gh auth,codex"; got != want {
		t.Errorf("order = %s, want %s", got, want)
	}
	if !strings.Contains(b.Rows[0].Label, "→ run: rota skills update") {
		t.Errorf("hint is not inline: %q", b.Rows[0].Label)
	}
	checkFrame(t, b, nil)
	keys := []string{"down", "down"}
	m, _ := viewDrive(t, b, keys...)
	checkFrame(t, m, keys)
}

func TestDoctorViewFilterAndQuit(t *testing.T) {
	m := tui.Model(buildBrowser(t, doctorView, viewDoctorData()))
	keys := []string{"/", "g", "i", "t", "enter"}
	m, quit := viewDrive(t, m, keys...)
	if quit {
		t.Fatal("typing a filter quit the screen")
	}
	checkFrame(t, m, keys)
	if _, quit := viewDrive(t, m, "q"); !quit {
		t.Error("q with a filter applied should still go back")
	}
}

func viewAccountsData() *jsonx.Object {
	return knObj("accounts", objList(
		knObj("name", "work", "configDir", "/home/u/.claude-work", "verdict", "ok", "reason", "", "fiveHour", 20.0, "sevenDay", 35.0, "resetsAt", "2026-10-08T04:00:00Z", "headroom", 65.0),
		knObj("name", "personal", "configDir", "/home/u/.claude", "verdict", "tight", "reason", "", "fiveHour", 91.0, "sevenDay", 40.0, "resetsAt", "2026-10-07T23:30:00Z", "headroom", 9.0),
		knObj("name", "spare", "configDir", "/home/u/.claude-spare", "verdict", "unknown", "reason", "no usage reading"),
	))
}

func TestAccountsViewGolden(t *testing.T) {
	b := buildBrowser(t, accountsView, viewAccountsData())
	if got := headroomBar(65); got != "[█████████████░░░░░░░]" {
		t.Errorf("bar(65) = %s", got)
	}
	checkFrame(t, b, nil)
	keys := []string{"down", "down"}
	m, _ := viewDrive(t, b, keys...)
	checkFrame(t, m, keys)
}

func TestAccountsViewEmpty(t *testing.T) {
	b := buildBrowser(t, accountsView, knObj("accounts", []any{}))
	if out := tui.Strip(b.Render(vw, vh, tui.Style{})); !strings.Contains(out, "no accounts configured") {
		t.Errorf("empty frame: %s", out)
	}
}

func viewBacklogData() *jsonx.Object {
	return knObj(
		"inProgress", objList(knObj("id", "546", "type", "feature", "title", "Read-only views", "branch", "ben/546-views")),
		"bugs", objList(
			knObj("id", "501", "priority", "P1", "title", "Gate hangs on a dead worktree", "related", []any{"502"}, "milestone", "m3"),
			knObj("id", "502", "priority", "P2", "title", "Palette flickers on resize", "related", []any{}),
		),
		"features", objList(knObj("id", "540", "size", "L", "title", "Stdlib TUI kit", "related", []any{}, "milestone", "m3")),
		"tasks", objList(knObj("id", "550", "title", "Prune stale labels", "related", []any{})),
	)
}

func TestBacklogViewFiltersAndReadsABody(t *testing.T) {
	b := buildBrowser(t, backlogView, viewBacklogData())
	var asked []string
	b.Load = func(id string) (string, error) {
		asked = append(asked, id)
		return "Gate hangs when the worktree\nis gone.\n\n- repro: remove it", nil
	}
	checkFrame(t, b, nil)

	keys := []string{"/", "m", "3", "enter", "down"}
	m, _ := viewDrive(t, b, keys...)
	checkFrame(t, m, keys)

	keys = append(keys, "enter")
	m, _ = viewDrive(t, b, keys...)
	if len(asked) != 1 || asked[0] != "540" {
		t.Fatalf("loader asked for %v, want [540]", asked)
	}
	checkFrame(t, m, keys)

	// The body is cached: leaving the reader and opening again does not reload.
	m, _ = viewDrive(t, m, "esc", "enter")
	if len(asked) != 1 {
		t.Errorf("reopening reloaded: %v", asked)
	}
}

func TestBacklogViewLoadErrorShowsInTheReader(t *testing.T) {
	b := buildBrowser(t, backlogView, viewBacklogData())
	b.Load = func(string) (string, error) { return "", errors.New("item 546 not found") }
	m, _ := viewDrive(t, b, "enter")
	if out := tui.Strip(m.Render(vw, vh, tui.Style{})); !strings.Contains(out, "item 546 not found") {
		t.Errorf("error not shown:\n%s", out)
	}
}

func TestBacklogViewSelectionChangeClosesTheReader(t *testing.T) {
	b := buildBrowser(t, backlogView, viewBacklogData())
	b.Load = func(string) (string, error) { return "BODY", nil }
	m, _ := viewDrive(t, b, "enter")
	if !m.(browser).opened {
		t.Fatal("enter did not open the reader")
	}
	// j scrolls the reader, it does not move the list.
	m, _ = viewDrive(t, m, "j")
	if cur, _ := m.(browser).current(); cur.Key != "546" {
		t.Errorf("selection moved to %s while reading", cur.Key)
	}
	m, quit := viewDrive(t, m, "q")
	if quit || m.(browser).opened {
		t.Errorf("q in the reader should close it (quit=%v)", quit)
	}
}

func viewTopicData(names ...string) *jsonx.Object {
	var ts []any
	for i, n := range names {
		ts = append(ts, knObj("name", n, "bullets", i+2, "bytes", 400*(i+1)))
	}
	return knObj("topics", ts)
}

func TestKnowledgeViewGolden(t *testing.T) {
	b := buildBrowser(t, knowledgeView, viewTopicData("Architecture: Skill authoring", "Build & Tooling: Smoke testing", "Rounds: Orchestration"))
	b.Load = func(topic string) (string, error) {
		return "## " + topic + "\n\n- **Reader pane.** Long bullet text that wraps once it reaches the right edge of the reader pane in the frame.\n- **Second.** More.", nil
	}
	keys := []string{"down", "enter", "j"}
	m, _ := viewDrive(t, b, keys...)
	checkFrame(t, m, keys)
}

func TestDecisionsViewGolden(t *testing.T) {
	b := buildBrowser(t, decisionsView, viewTopicData("Architecture", "Rounds: Orchestration"))
	b.Load = func(topic string) (string, error) {
		return "## " + topic + "\n\n### Keep modules small\n\n*Why.* Reviews stay short.", nil
	}
	keys := []string{"enter"}
	m, _ := viewDrive(t, b, keys...)
	checkFrame(t, m, keys)
}

func TestDecisionsStatsListsTopicsAndTheViewReadsThemThroughTheVerb(t *testing.T) {
	dir := decProject(t, decFixture, "")
	got := knNew(t, dir, "", "decisions", "stats", "--json")
	if got.rc != 0 || !strings.Contains(got.stdout, `"name": "Build"`) || !strings.Contains(got.stdout, `"name": "Architecture"`) {
		t.Fatalf("decisions stats: rc %d\n%s%s", got.rc, got.stdout, got.stderr)
	}
	if got := knNew(t, dir, "", "decisions", "stats", "extra"); got.rc != 2 {
		t.Errorf("stats with an argument: rc=%d", got.rc)
	}

	// The real loader runs `decisions query` in process, from the project.
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
	c := &Ctx{Path: "rota decisions stats", Deps: testDeps()}
	text, err := runVerbText(c, false, "decisions", "query", "Build")
	if err != nil || !strings.Contains(text, "### Gate first") || strings.Contains(text, "Keep modules small") {
		t.Errorf("query Build = %q, %v", text, err)
	}
	// A heading that looks like a flag is still a topic after "--".
	if text, err := runVerbText(c, false, "decisions", "query", "--", "--json"); err != nil || strings.Contains(text, `"ok"`) {
		t.Errorf("a flag-like topic was parsed as a flag: %q, %v", text, err)
	}
	if _, err := runVerbText(c, false, "decisions", "query"); err == nil {
		t.Error("a verb failure should come back as an error")
	}
}

func TestFailedVerbWithDataStillOpensItsView(t *testing.T) {
	r := newUIRig(t)
	r.root.Subs[0].Subs[0].Verb = func(*flag.FlagSet) RunFunc {
		return func(*Ctx, []string) (Result, error) {
			return Result{Data: map[string]any{"n": 1}, Text: "x\n"}, Failed("a check failed")
		}
	}
	code, out, errs := r.do("demo", "show", "--ui")
	if code != 1 || out != "" || errs != "" || len(r.models) != 1 {
		t.Errorf("exit %d stdout %q stderr %q views %d", code, out, errs, len(r.models))
	}
	r = newUIRig(t)
	code, _, _ = r.do("demo", "boom", "--ui") // no Data: the plain failure
	if code != 1 || len(r.models) != 0 {
		t.Errorf("exit %d views %d", code, len(r.models))
	}
}

func TestViewsDropControlCharactersFromRowsAndBodies(t *testing.T) {
	data := knObj("bugs", objList(knObj("id", "9", "priority", "P1", "title", "evil \x1b[2J\x1b]0;pwn\x07title", "related", []any{})))
	b := buildBrowser(t, backlogView, data)
	b.Load = func(string) (string, error) { return "body \x1b[31mred\x1b[0m\x00 ok", nil }
	m, _ := viewDrive(t, b, "enter")
	out := m.Render(vw, vh, tui.Style{})
	if strings.ContainsAny(out, "\x00\x07") || strings.Contains(out, "\x1b[2J") || strings.Contains(out, "\x1b[31m") || strings.Contains(out, "\x1b]") {
		t.Errorf("control characters reached the frame: %q", out)
	}
	if !strings.Contains(tui.Strip(out), "body red ok") {
		t.Errorf("body text lost: %q", tui.Strip(out))
	}
}

func TestViewLoadersAcceptDashDashBeforeThePositional(t *testing.T) {
	dir := knProject(t, false)
	knWrite(t, filepath.Join(dir, ".rota", "KNOWLEDGE.md"), "# Knowledge\n\n## Build\n\n- **Gate.** runs fast\n")
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
	c := &Ctx{Path: "rota knowledge stats", Deps: testDeps()}
	text, err := runVerbText(c, true, "knowledge", "query", "--", "Build")
	if err != nil || !strings.Contains(text, "Gate") {
		t.Errorf("knowledge query = %q, %v", text, err)
	}
	if _, err := runVerbText(c, true, "item", "field", "get", "--name", "detail", "--", "nope"); err == nil || strings.Contains(err.Error(), "unknown flag") || strings.Contains(err.Error(), "usage") {
		t.Errorf("item field get with -- : %v", err)
	}
}
