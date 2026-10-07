package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/golden"
	"github.com/l4ci/rota/internal/tui"
)

// cfgRun runs the CLI in root with the terminal faked and RunView swapped for
// a recorder, so a test reads the screen a verb built.
func cfgRun(t *testing.T, root string, tty bool, args ...string) (code int, models []tui.Model, errs string) {
	t.Helper()
	t.Setenv("TERM", "xterm")
	wd, _ := os.Getwd()
	defer os.Chdir(wd) // -C changes the process directory
	deps := testDeps()
	deps.IsTerminal = func(any) bool { return tty }
	deps.RunView = func(c *Ctx, m tui.Model) error {
		models = append(models, m)
		return nil
	}
	var out, errb bytes.Buffer
	code = mainWith(deps, append([]string{"-C", root}, args...), strings.NewReader(""), &out, &errb)
	return code, models, errb.String()
}

func readConfig(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, ".rota", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestConfigEditRefusesOffATerminal(t *testing.T) {
	root := trackerProject(t, "{}\n")
	code, models, errs := cfgRun(t, root, false, "config", "edit")
	if code != ExitRefused || !strings.Contains(errs, "rota config set") || len(models) != 0 {
		t.Errorf("exit %d, %d screens: %s", code, len(models), errs)
	}
	if code, models, _ := cfgRun(t, root, true, "--json", "config", "edit"); code != ExitRefused || len(models) != 0 {
		t.Errorf("--json: exit %d, %d screens", code, len(models))
	}
	t.Setenv("TERM", "dumb")
	deps := testDeps()
	deps.IsTerminal = func(any) bool { return true }
	deps.RunView = func(*Ctx, tui.Model) error { t.Error("a dumb terminal opened the screen"); return nil }
	var out, errb bytes.Buffer
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if code := mainWith(deps, []string{"-C", root, "config", "edit"}, strings.NewReader(""), &out, &errb); code != ExitRefused {
		t.Errorf("dumb TERM: exit %d", code)
	}
}

func TestConfigUIRefusals(t *testing.T) {
	root := trackerProject(t, "{}\n")
	for _, args := range [][]string{{"config", "--ui"}, {"config", "show", "--ui"}} {
		code, models, errs := cfgRun(t, root, false, args...)
		if code != ExitUsage || len(models) != 0 || !strings.Contains(errs, "hint: run: rota config show") {
			t.Errorf("%v off a terminal: exit %d, %d screens: %s", args, code, len(models), errs)
		}
		if code, models, _ := cfgRun(t, root, true, append([]string{"--json"}, args...)...); code != ExitUsage || len(models) != 0 {
			t.Errorf("%v --json: exit %d, %d screens", args, code, len(models))
		}
	}
}

func TestConfigScreenOpensFromEveryEntry(t *testing.T) {
	root := trackerProject(t, `{"work":{"workerSlots":5}}`+"\n")
	for _, args := range [][]string{{"config", "edit"}, {"config", "--ui"}, {"config", "show", "--ui"}} {
		code, models, errs := cfgRun(t, root, true, args...)
		if code != 0 || len(models) != 1 {
			t.Fatalf("%v: exit %d, %d screens: %s", args, code, len(models), errs)
		}
		frame := tui.Strip(models[0].Render(100, 30, tui.Style{}))
		if !strings.Contains(frame, "▾ work (") {
			t.Errorf("%v: no work group in the frame:\n%s", args, frame)
		}
	}
	// A plain `config show` is untouched by the view.
	if code, models, _ := cfgRun(t, root, true, "config", "show"); code != 0 || len(models) != 0 {
		t.Errorf("plain show: exit %d, %d screens", code, len(models))
	}
}

// ---- the screen against a fake store ---------------------------------------------

func fxRow(key, typ, src string, val, def any, desc string, choices ...string) cfgRow {
	group, _, _ := strings.Cut(key, ".")
	return cfgRow{Key: key, Type: typ, Source: src, Value: val, Default: def, Desc: desc, Group: group, Choices: choices}
}

func fxRows() []cfgRow {
	return []cfgRow{
		fxRow("models.orchestrator", "string", "default", "opus", "opus", "Model for planning, exploration, verification and design. Usually opus, sonnet or haiku."),
		fxRow("models.worker", "string", "project", "haiku", "sonnet", "Model for implementation sub-tasks and round workers."),
		fxRow("work.isolation", "enum", "default", "branch", "branch", "How /rota-work isolates changes from main: a feature branch in this checkout, or a separate git worktree.", "branch", "worktree"),
		fxRow("work.mergeStrategy", "enum", "local", "pr", "direct", "How finished work lands: merged straight into the base branch, or through a pull request.", "direct", "pr"),
		fxRow("work.workerSlots", "int", "project", json.Number("5"), json.Number("3"), "Number of worker slots rota round start provisions (at least 1)."),
		fxRow("work.tdd", "bool", "default", true, true, "Whether /rota-work and workers require a recorded red-first run before a behavior change."),
		fxRow("work.accounts", "list", "default", []any{}, []any{}, "Claude accounts for worker slots, as {name, configDir} objects."),
		fxRow("ship.review", "bool", "project", false, true, "Whether /rota-ship runs /rota-review first."),
		fxRow("ship.qa", "bool", "default", false, false, "Opt-in product-QA gate."),
		fxRow("round.scope", "enum", "default", "milestone", "milestone", "Which issues a round may take.", "milestone", "slate", "next", "open"),
		fxRow("round.roster", "list", "default", []any{"ben", "dana"}, []any{"ben", "dana"}, "Agent names slots are provisioned under."),
		fxRow("test.fast", "list", "default", []any{}, []any{}, "Shell commands for the quick per-task and worker checks."),
		fxRow("release.versionFile", "path", "default", "", "", "Project-relative file /rota-release reads and bumps."),
		fxRow("orchestrator.keepaliveBackoffSeconds", "int", "default", json.Number("5"), json.Number("5"), "Seconds rota keepalive run waits before a restart."),
	}
}

type fakeStore struct {
	rows  []cfgRow
	calls []string
	fail  error
}

func (f *fakeStore) Rows() ([]cfgRow, error) { return append([]cfgRow(nil), f.rows...), nil }

func (f *fakeStore) Set(key, raw string, local bool) (bool, error) {
	f.calls = append(f.calls, fmt.Sprintf("set %s %s local=%v", key, raw, local))
	if f.fail != nil {
		return false, f.fail
	}
	for i := range f.rows {
		if f.rows[i].Key == key {
			f.rows[i].Value, f.rows[i].Source = config.Coerce(raw), "project"
			if local {
				f.rows[i].Source = "local"
			}
		}
	}
	return true, nil
}

func (f *fakeStore) Reset(key string) error {
	f.calls = append(f.calls, "reset "+key)
	for i := range f.rows {
		if f.rows[i].Key == key {
			f.rows[i].Value, f.rows[i].Source = f.rows[i].Default, "default"
		}
	}
	return nil
}

func cfgKey(name string) tui.Key {
	switch name {
	case "up":
		return tui.Key{Kind: tui.KeyUp}
	case "down":
		return tui.Key{Kind: tui.KeyDown}
	case "left":
		return tui.Key{Kind: tui.KeyLeft}
	case "right":
		return tui.Key{Kind: tui.KeyRight}
	case "enter":
		return tui.Key{Kind: tui.KeyEnter}
	case "esc":
		return tui.Key{Kind: tui.KeyEsc}
	case "backspace":
		return tui.Key{Kind: tui.KeyBackspace}
	case "pgdn":
		return tui.Key{Kind: tui.KeyPgDn}
	}
	return tui.Rune([]rune(name)[0])
}

func typedKeys(s string) []string {
	var out []string
	for _, r := range s {
		out = append(out, string(r))
	}
	return out
}

func rep(name string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = name
	}
	return out
}

// play feeds keys to m the way the driver does: an Exec runs and its error
// comes back as Done. quit reports whether a key left the screen.
func play(m tui.Model, keys []string) (out tui.Model, quit bool) {
	for _, name := range keys {
		var cmd tui.Cmd
		m, cmd = m.Update(cfgKey(name))
		if cmd.Exec != nil {
			m, _ = m.Update(tui.Done{Err: cmd.Exec()})
		}
		if cmd.Quit {
			return m, true
		}
	}
	return m, false
}

func frame(t *testing.T, m tui.Model) []string {
	t.Helper()
	lines := strings.Split(tui.Strip(m.Render(100, 30, tui.Style{Color: true})), "\n")
	if len(lines) != 30 {
		t.Errorf("frame has %d lines, want 30", len(lines))
	}
	for i, l := range lines {
		if n := utf8.RuneCountInString(l); n > 100 {
			t.Errorf("line %d is %d runes: %q", i, n, l)
		}
	}
	return lines
}

type cfgGoldenIn struct {
	Keys []string `json:"keys"`
	W    int      `json:"w"`
	H    int      `json:"h"`
}

func newFxScreen(store configStore) tui.Model {
	rows, _ := store.Rows()
	return newConfigScreen(rows, store, new([]string))
}

func TestConfigScreenGolden(t *testing.T) {
	scripts := [][]string{
		nil,                                     // grouped, a header selected
		cat(rep("down", 2)),                     // a key row: detail pane
		cat([]string{"enter"}, rep("down", 1)),  // the first group folded
		cat(rep("down", 4), []string{"enter"}),  // an enum picker open
		cat(rep("down", 10), []string{"enter"}), // a bool toggle open
		cat([]string{"/"}, typedKeys("versionFile"), []string{"enter", "enter"}, typedKeys("/etc/passwd"), []string{"enter"}), // a refused path
		cat([]string{"/"}, typedKeys("worker")), // filtering
	}
	var frames [][]string
	for _, s := range scripts {
		m, _ := play(newFxScreen(&fakeStore{rows: fxRows()}), s)
		frames = append(frames, frame(t, m))
	}
	golden.Check(t, map[string]any{"scripts": scripts, "w": 100, "h": 30}, frames)
}

func cat(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func TestConfigScreenWritesThroughTheStore(t *testing.T) {
	store := &fakeStore{rows: fxRows()}
	var changed []string
	m := tui.Model(newConfigScreen(fxRows(), store, &changed))
	// Walk by filter instead of counting rows: "/" filters, Enter ends it.
	do := func(filter string, keys []string) {
		t.Helper()
		m, _ = play(m, cat([]string{"/"}, typedKeys(filter), []string{"enter"}, keys, []string{"esc"}))
	}
	do("models.worker", cat([]string{"enter"}, rep("backspace", 5), typedKeys("sonnet"), []string{"enter"})) // text, project
	do("ship.review", []string{"enter", " ", "enter"})                                                       // bool toggled on
	do("work.isolation", []string{"enter", "right", "enter"})                                                // enum -> worktree
	do("work.workerSlots", cat([]string{"l"}, rep("backspace", 1), typedKeys("8"), []string{"enter"}))       // local write
	do("work.mergeStrategy", []string{"r"})                                                                  // reset
	do("ship.qa", []string{"r"})                                                                             // already default: no call
	want := []string{
		`set models.worker sonnet local=false`,
		`set ship.review true local=false`,
		`set work.isolation worktree local=false`,
		`set work.workerSlots 8 local=true`,
		`reset work.mergeStrategy`,
	}
	if strings.Join(store.calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("writes:\n%s\nwant:\n%s", strings.Join(store.calls, "\n"), strings.Join(want, "\n"))
	}
	if strings.Join(changed, ",") != "models.worker,ship.review,work.isolation,work.workerSlots,work.mergeStrategy" {
		t.Errorf("changed = %v", changed)
	}
}

func TestConfigScreenValidatesLikeConfigSetAndKeepsEditing(t *testing.T) {
	store := &fakeStore{rows: fxRows()}
	m, _ := play(newFxScreen(store), cat([]string{"/"}, typedKeys("versionFile"), []string{"enter", "enter"}, typedKeys("/etc/passwd"), []string{"enter"}))
	if len(store.calls) != 0 {
		t.Fatalf("a refused value reached the store: %v", store.calls)
	}
	got := strings.Join(frame(t, m), "\n")
	if !strings.Contains(got, "is not inside the project") {
		t.Errorf("no validation message:\n%s", got)
	}
	// Esc cancels the edit without a write; a valid value goes through.
	m, _ = play(m, []string{"esc"})
	m, _ = play(m, cat([]string{"enter"}, typedKeys("VERSION"), []string{"enter"}))
	if len(store.calls) != 1 || store.calls[0] != "set release.versionFile VERSION local=false" {
		t.Errorf("calls = %v", store.calls)
	}
}

func TestConfigScreenShowsAWriteError(t *testing.T) {
	store := &fakeStore{rows: fxRows(), fail: fmt.Errorf("disk full")}
	m, _ := play(newFxScreen(store), cat([]string{"/"}, typedKeys("ship.review"), []string{"enter", "enter", " ", "enter"}))
	if got := strings.Join(frame(t, m), "\n"); !strings.Contains(got, "disk full") {
		t.Errorf("error not shown:\n%s", got)
	}
}

func TestConfigScreenQuitsOnQAndEsc(t *testing.T) {
	for _, k := range []string{"q", "esc"} {
		if _, quit := play(newFxScreen(&fakeStore{rows: fxRows()}), []string{k}); !quit {
			t.Errorf("%q did not leave the screen", k)
		}
	}
	// Esc first clears a filter, then leaves.
	m, quit := play(newFxScreen(&fakeStore{rows: fxRows()}), cat([]string{"/"}, typedKeys("ship"), []string{"enter", "esc"}))
	if quit {
		t.Fatal("Esc left the screen with a filter on")
	}
	if _, quit := play(m, []string{"esc"}); !quit {
		t.Error("second Esc did not leave")
	}
}

// ---- the real store ---------------------------------------------------------------

func TestConfigScreenWritesTheSameFilesAsConfigSet(t *testing.T) {
	root := trackerProject(t, `{"ship":{"review":true},"work":{"mergeStrategy":"pr"}}`+"\n")
	t.Setenv("TERM", "xterm")
	local := filepath.Join(root, ".rota", "config.local.json")
	if err := os.WriteFile(local, []byte(`{"work":{"mergeStrategy":"direct"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	store := rootStore{root}
	rows, err := store.Rows()
	if err != nil {
		t.Fatal(err)
	}
	var changed []string
	m := tui.Model(newConfigScreen(rows, store, &changed))
	pick := func(filter string, keys ...string) {
		t.Helper()
		m, _ = play(m, cat([]string{"/"}, typedKeys(filter), []string{"enter"}, keys, []string{"esc"}))
	}
	pick("ship.review", "enter", " ", "enter") // toggle off
	pick("work.dispatch", "enter", "right", "enter")
	pick("work.workerSlots", "l", "backspace", "7", "enter")
	pick("work.mergeStrategy", "r") // drops the local override and the project value
	cfg := readConfig(t, root)
	for _, want := range []string{`"review": false`, `"dispatch": "tmux"`} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config.json lacks %s:\n%s", want, cfg)
		}
	}
	if strings.Contains(cfg, "workerSlots") || !strings.Contains(cfg, `"mergeStrategy": "direct"`) {
		t.Errorf("config.json holds the local write, or the reset key lacks its default:\n%s", cfg)
	}
	lb, _ := os.ReadFile(local)
	if !strings.Contains(string(lb), `"workerSlots": 7`) || strings.Contains(string(lb), "mergeStrategy") {
		t.Errorf("config.local.json = %s", lb)
	}
	rows, _ = store.Rows()
	for _, r := range rows {
		switch r.Key {
		case "work.mergeStrategy":
			if r.Source != "project" || r.differs() {
				t.Errorf("reset key = %v from %s; a required key is written with its default", r.Value, r.Source)
			}
		case "work.workerSlots":
			if r.Source != "local" || compact(r.Value) != "7" {
				t.Errorf("local key = %v from %s", r.Value, r.Source)
			}
		}
	}
	// What the screen wrote is what `config set` writes: same file, same layout.
	code, _, _ := rotaRun(t, "--json", "-C", root, "config", "set", "ship.review", "false")
	if code != 0 || readConfig(t, root) != cfg {
		t.Errorf("config set rewrote the file differently: exit %d\n%s\nvs\n%s", code, readConfig(t, root), cfg)
	}
}

func TestConfigScreenNeverDrawsAControlCharacterFromTheConfig(t *testing.T) {
	rows := fxRows()
	rows[0].Value = "opus\x1b[2J\x07evil"
	m, _ := play(newFxScreen(&fakeStore{rows: rows}), cat([]string{"/"}, typedKeys("orchestrator"), []string{"enter", "enter"}))
	raw := m.Render(100, 30, tui.Style{})
	if strings.ContainsAny(raw, "\x1b\x07") {
		t.Errorf("a control character reached the frame: %q", raw)
	}
	if !strings.Contains(raw, `\u001b`) {
		t.Errorf("the escape is not shown as JSON:\n%s", raw)
	}
}
