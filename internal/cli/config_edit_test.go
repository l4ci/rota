package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// editRun runs `config edit` in root with typed as the terminal's lines.
func editRun(t *testing.T, root string, tty bool, typed string) (int, string, string) {
	t.Helper()
	wd, _ := os.Getwd()
	defer os.Chdir(wd) // -C changes the process directory
	deps := testDeps()
	deps.IsTerminal = func(any) bool { return tty }
	var out, errb bytes.Buffer
	code := mainWith(deps, []string{"-C", root, "config", "edit"}, strings.NewReader(typed), &out, &errb)
	return code, out.String(), errb.String()
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
	code, _, errs := editRun(t, root, false, "")
	if code != ExitRefused || !strings.Contains(errs, "rota config set") {
		t.Errorf("exit %d: %s", code, errs)
	}
	deps := testDeps()
	deps.IsTerminal = func(any) bool { return true }
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	var out, errb bytes.Buffer
	if code := mainWith(deps, []string{"--json", "-C", root, "config", "edit"}, strings.NewReader(""), &out, &errb); code != ExitRefused {
		t.Errorf("--json: exit %d", code)
	}
}

func TestConfigEditListsEveryKeyWithValueAndSource(t *testing.T) {
	root := trackerProject(t, `{"work":{"workerSlots":5}}`+"\n")
	code, _, errs := editRun(t, root, true, "\n")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, want := range []string{"models.orchestrator = \"opus\"  (source: default)", "work.workerSlots = 5  (source: project)", "doctor.minFreeDiskPercent"} {
		if !strings.Contains(errs, want) {
			t.Errorf("listing lacks %q:\n%s", want, errs)
		}
	}
}

func TestConfigEditShowsMergedDefault(t *testing.T) {
	root := trackerProject(t, `{"ship":{"qa":true}}`)
	if err := os.WriteFile(filepath.Join(root, ".rota", "config.local.json"), []byte(`{"ship":{"qa":null}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errs := editRun(t, root, true, "q\n")
	if code != 0 || !strings.Contains(errs, "ship.qa = false  (source: default)") {
		t.Fatalf("edit: exit %d: %s", code, errs)
	}
	code, env, _ := rotaRun(t, "--json", "-C", root, "config", "show", "ship.qa")
	rows, _ := get(dataOf(env), "entries").([]any)
	if code != 0 || len(rows) != 1 || get(rows[0], "value") != false || get(rows[0], "source") != "default" {
		t.Fatalf("show: exit %d: %v", code, env)
	}
}

func TestConfigEditTogglesBoolsPicksEnumsAndValidatesFreeValues(t *testing.T) {
	root := trackerProject(t, `{"ship":{"review":true}}`+"\n")
	typed := strings.Join([]string{
		"ship.review",      // bool: toggles true -> false
		"work.dispatch",    // enum: pick by number
		"2",                // tmux
		"work.workerSlots", // free number
		"abc",              // rejected
		"7",                // accepted
		"q",
	}, "\n") + "\n"
	code, out, errs := editRun(t, root, true, typed)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	cfg := readConfig(t, root)
	for _, want := range []string{`"review": false`, `"dispatch": "tmux"`, `"workerSlots": 7`} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config lacks %s:\n%s", want, cfg)
		}
	}
	if !strings.Contains(errs, "want a number") {
		t.Errorf("no validation message:\n%s", errs)
	}
	if !strings.Contains(out, "ship.review") || !strings.Contains(out, "work.workerSlots") {
		t.Errorf("summary missing changes: %s", out)
	}
}

func TestConfigEditAmbiguousAndEnterKeepsAndEOFEnds(t *testing.T) {
	root := trackerProject(t, `{"work":{"workerSlots":3}}`+"\n")
	// "labels" matches many keys; a number edit with Enter keeps the value; EOF ends.
	code, _, errs := editRun(t, root, true, "labels\nwork.workerSlots\n\nnosuchkey")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !strings.Contains(errs, "be more specific") || !strings.Contains(errs, `no key matches "nosuchkey"`) {
		t.Errorf("prompts:\n%s", errs)
	}
	if got := readConfig(t, root); got != `{"work":{"workerSlots":3}}`+"\n" {
		t.Errorf("value changed on Enter")
	}
}
