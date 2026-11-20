package worker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/landing"
)

// #3992: a red train re-runs and bisects only the failing check when
// test.rerunCheck says how to run one check; the full gate runs once more
// before anything lands. The fake gate below prints what test/gate.sh prints
// for a red smoke section, so the train reads the same markers it reads in
// production.

const narrowSection = "test/sections/28_config_set.sh"

// narrowWorld is a train of b1..b3 whose test.full is a fake gate. gate builds
// the script from the file the fake logs its full runs to; check builds
// test.rerunCheck ("" leaves the key unset) from the file narrowed runs log to.
type narrowWorld struct {
	*world
	full, narrow string
}

func newNarrowWorld(t *testing.T, gate func(full string) string, check func(narrow string) string) *narrowWorld {
	t.Helper()
	dir := t.TempDir()
	nw := &narrowWorld{full: filepath.Join(dir, "full"), narrow: filepath.Join(dir, "narrow")}
	script := gate(nw.full)
	nw.world = trainWorld(t, script, "b1", "b2", "b3")
	cfg := fmt.Sprintf(`{"test":{"full":[%q]`, script)
	if check != nil {
		cfg += fmt.Sprintf(`,"rerunCheck":%q`, check(nw.narrow))
	}
	nw.setConfig(cfg + `}}`)
	return nw
}

// redSection is a fake gate that fails section 28 while red holds on the
// merged tree, printing what test/gate.sh prints; retried adds the gate's own
// RETRIED-CHECK line.
func redSection(red string, retried bool) func(string) string {
	retry := ""
	if retried {
		retry = `RETRIED-CHECK smoke-shard1\n`
	}
	return func(full string) string {
		return fmt.Sprintf(`echo full >> %s; if %s; then printf 'FAIL smoke-shard1 (exit 1)\n%sFAILED-CHECK %s\n'; exit 1; fi`, full, red, retry, narrowSection)
	}
}

// failsWhen is a one-section command: it logs the section it was handed and
// fails exactly when the gate would, i.e. when red holds.
func failsWhen(red string) func(string) string {
	return func(narrow string) string { return fmt.Sprintf(`echo {check} >> %s; ! (%s)`, narrow, red) }
}

// passes is a one-section command that always passes.
func passes(narrow string) string { return "echo {check} >> " + narrow }

func lines(t *testing.T, path string) []string {
	t.Helper()
	b, _ := os.ReadFile(path)
	s := strings.TrimSpace(string(b))
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// AC-1, AC-3, AC-5, AC-8: a deterministic red section the gate already
// retried costs the train run, the narrowed bisect steps and one full run on
// the members that land, never a full re-run or a full prefix per step.
func TestTrainNarrowsDeterministicRedSection(t *testing.T) {
	red := "test -f b2.txt"
	nw := newNarrowWorld(t, redSection(red, true), failsWhen(red))
	res, err := nw.train(TrainOpts{Targets: []string{"b1", "b2", "b3"}, LandGreen: true})
	if err != nil || res.Verdict != landing.GateVerifyFailed || res.Culprit != "b2" || strings.Join(res.Landed, ",") != "b1" {
		t.Fatalf("want b2 named, b1 landed: %+v %v", res, err)
	}
	if got := lines(t, nw.full); len(got) != 2 {
		t.Errorf("full gate runs = %d, want 2 (the train, then the members that land): %v", len(got), got)
	}
	narrow := lines(t, nw.narrow)
	if len(narrow) == 0 {
		t.Fatal("no narrowed run: the bisect ran the full gate")
	}
	for _, l := range narrow {
		if l != narrowSection {
			t.Errorf("narrowed run named %q, want only the failing section", l)
		}
	}
}

// AC-1: no isolated retry in the gate log, so the re-run happens, and it runs
// the section alone.
func TestTrainRerunsOnlyTheFailingSection(t *testing.T) {
	red := "test -f b2.txt"
	nw := newNarrowWorld(t, redSection(red, false), failsWhen(red))
	res, err := nw.train(TrainOpts{Targets: []string{"b1", "b2", "b3"}, LandGreen: true})
	if err != nil || res.Culprit != "b2" {
		t.Fatalf("%+v %v", res, err)
	}
	if got := lines(t, nw.narrow); len(got) < 2 {
		t.Errorf("narrowed runs: %v", got)
	}
	if got := lines(t, nw.full); len(got) != 2 {
		t.Errorf("full gate runs = %d, want 2: %v", len(got), got)
	}
}

// AC-6: without test.rerunCheck the re-run is the full command, as before.
func TestTrainWithoutRerunCheckKeepsTheFullRerun(t *testing.T) {
	nw := newNarrowWorld(t, redSection("test -f b2.txt", false), nil)
	res, err := nw.train(TrainOpts{Targets: []string{"b1", "b2", "b3"}, LandGreen: true})
	if err != nil || res.Culprit != "b2" {
		t.Fatalf("%+v %v", res, err)
	}
	if got := lines(t, nw.full); len(got) < 3 {
		t.Errorf("full gate runs = %d, want the train plus the full re-run plus the bisect: %v", len(got), got)
	}
	if got := lines(t, nw.narrow); len(got) != 0 {
		t.Errorf("a narrowed run happened without the key: %v", got)
	}
}

// AC-7: green narrowed is no flake until a full re-run is green too.
func TestTrainNarrowedPassIsFlakyOnlyAfterAGreenFullRerun(t *testing.T) {
	// The gate is red on its first run with b2 merged only; the narrowed
	// re-run and the full re-run are green.
	seen := filepath.Join(t.TempDir(), "seen")
	nw := newNarrowWorld(t, redSection("test -f b2.txt && ! test -f "+seen+" && touch "+seen, false), func(n string) string { return passes(n) })
	res, err := nw.train(TrainOpts{Targets: []string{"b1", "b2", "b3"}})
	if err != nil || !res.Unreproduced || len(res.Flaky) != 1 || res.Culprit != "" {
		t.Fatalf("want a flake once the full re-run is green: %+v %v", res, err)
	}
	if got := lines(t, nw.full); len(got) != 2 {
		t.Errorf("full gate runs = %d, want the train and one full re-run: %v", len(got), got)
	}
	if got := lines(t, nw.narrow); len(got) != 1 {
		t.Errorf("narrowed runs = %v, want exactly one", got)
	}
}

// AC-7: red whenever b2 is merged but green for the section alone is an
// interaction or ordering bug, not a flake.
func TestTrainNarrowedPassButRedFullRerunIsNotFlaky(t *testing.T) {
	nw := newNarrowWorld(t, redSection("test -f b2.txt", false), func(n string) string { return passes(n) })
	res, err := nw.train(TrainOpts{Targets: []string{"b1", "b2", "b3"}})
	if err != nil || res.Unreproduced || len(res.Flaky) != 0 || res.Verdict == landing.GatePass || len(res.Landed) != 0 {
		t.Fatalf("a check that fails only in the full gate is no flake: %+v %v", res, err)
	}
}

// AC-4: a different failure from another command that shows only in the full
// run of the members that would land is attributed to its member.
func TestTrainSecondFailureInTheFullPrefixIsAttributed(t *testing.T) {
	dir := t.TempDir()
	full, narrow := filepath.Join(dir, "full"), filepath.Join(dir, "narrow")
	// The smoke gate fails once b3 is merged. The lint command fails with b2
	// merged and b3 not: only the full run of b1+b2 shows it.
	smoke := fmt.Sprintf(`echo full >> %s; if test -f b3.txt; then printf 'FAIL smoke-shard1 (exit 1)\nRETRIED-CHECK smoke-shard1\nFAILED-CHECK %s\n'; exit 1; fi`, full, narrowSection)
	lint := `if test -f b2.txt && ! test -f b3.txt; then echo lint failed; exit 1; fi`
	w := trainWorld(t, smoke, "b1", "b2", "b3")
	w.setConfig(fmt.Sprintf(`{"test":{"full":[%q,%q],"rerunCheck":%q}}`, smoke, lint, "echo {check} >> "+narrow+"; ! test -f b3.txt"))
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2", "b3"}, LandGreen: true})
	if err != nil || res.Verdict != landing.GateVerifyFailed || res.Culprit != "b2" || strings.Join(res.Landed, ",") != "b1" {
		t.Fatalf("want the lint failure attributed to b2, b1 landed: %+v %v", res, err)
	}
	if res.Original == nil || !strings.Contains(res.Original.Command, "smoke-shard1") {
		t.Errorf("the first failure's evidence is gone: %+v", res.Original)
	}
}

// AC-7: a two-member train has no separate red prefix, so the full confirmation
// must not be skipped there.
func TestTrainTwoMemberNarrowedPassButRedFullIsAnInteraction(t *testing.T) {
	gate := func(full string) string {
		return fmt.Sprintf(`echo full >> %s; if test -f b2.txt; then printf 'FAIL smoke-shard1 (exit 1)\nFAILED-CHECK %s\n'; exit 1; fi`, full, narrowSection)
	}
	dir := t.TempDir()
	narrow := filepath.Join(dir, "narrow")
	w := trainWorld(t, gate(filepath.Join(dir, "full")), "b1", "b2")
	w.setConfig(fmt.Sprintf(`{"test":{"full":[%q],"rerunCheck":%q}}`, gate(filepath.Join(dir, "full")), "echo {check} >> "+narrow))
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2"}})
	if err != nil || res.Unreproduced || len(res.Flaky) != 0 || res.Verdict != landing.GateInteraction {
		t.Fatalf("want an interaction, not a flake: %+v %v", res, err)
	}
}

// A check the gate already retried red stays red when another failed command
// recovers: the train is no flake.
func TestTrainRetriedRedCheckBesideARecoveringCommandIsNoFlake(t *testing.T) {
	dir := t.TempDir()
	seen, narrow := filepath.Join(dir, "seen"), filepath.Join(dir, "narrow")
	smoke := fmt.Sprintf(`if test -f b2.txt; then printf 'FAIL smoke-shard1 (exit 1)\nRETRIED-CHECK smoke-shard1\nFAILED-CHECK %s\n'; exit 1; fi`, narrowSection)
	other := fmt.Sprintf(`if test -f b2.txt && ! test -f %s; then touch %s; echo recovers; exit 1; fi`, seen, seen)
	w := trainWorld(t, smoke, "b1", "b2", "b3")
	w.setConfig(fmt.Sprintf(`{"test":{"full":[%q,%q],"rerunCheck":%q}}`, smoke, other, "echo {check} >> "+narrow+"; ! test -f b2.txt"))
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2", "b3"}})
	if err != nil || res.Unreproduced || len(res.Flaky) != 0 || res.Culprit != "b2" {
		t.Fatalf("the retried-red smoke gate is deterministic: %+v %v", res, err)
	}
}

// AC-2, AC-7, AC-8: a Go-only failure narrows through the -run template too.
func TestTrainNarrowsGoOnlyFailures(t *testing.T) {
	dir := t.TempDir()
	full, narrow := filepath.Join(dir, "full"), filepath.Join(dir, "narrow")
	cmd := fmt.Sprintf("echo full >> %s; if test -f b2.txt; then %s; fi", full, goFail("TestX", "ex.com/p"))
	w := trainWorld(t, cmd, "b1", "b2", "b3")
	w.setConfig(fmt.Sprintf(`{"test":{"full":[%q],"vacuousCommand":%q,"rerunCheck":"true {check}"}}`,
		cmd, "echo {packages} {tests} >> "+narrow+"; ! test -f b2.txt"))
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2", "b3"}, LandGreen: true})
	if err != nil || res.Culprit != "b2" || strings.Join(res.Landed, ",") != "b1" {
		t.Fatalf("%+v %v", res, err)
	}
	if got := lines(t, full); len(got) != 3 {
		t.Errorf("full runs = %d, want 3 (the train and its in-tree retry, then the members that land): %v", len(got), got)
	}
	if got := lines(t, narrow); len(got) < 3 {
		t.Errorf("narrowed Go runs = %v", got)
	}
}

// AC-6: without the key a retried log changes nothing: the full re-run stays.
func TestTrainWithoutRerunCheckIgnoresARetriedLog(t *testing.T) {
	nw := newNarrowWorld(t, redSection("test -f b2.txt", true), nil)
	res, err := nw.train(TrainOpts{Targets: []string{"b1", "b2", "b3"}, LandGreen: true})
	if err != nil || res.Culprit != "b2" {
		t.Fatalf("%+v %v", res, err)
	}
	if got := lines(t, nw.full); len(got) < 3 {
		t.Errorf("full runs = %d, want the train, the full re-run and the bisect: %v", len(got), got)
	}
	if trainHasNote(res, "RERUN-SKIPPED") {
		t.Errorf("the re-run was skipped without the key: %q", res.Notes)
	}
}

func TestRerunCommandsReadsTheGateLog(t *testing.T) {
	const check = "SECTION_LIST={check} bash test/gate.sh"
	const template = "go test -json -count=1 {packages} -run {tests}"
	log := func(body string) VerifyResult {
		return VerifyResult{Failed: []string{"bash test/gate.sh"}, Log: "== bash test/gate.sh\n" + body}
	}
	for _, c := range []struct {
		name, log, check string
		skipRetried      bool
		want             []string
		narrowed         bool
	}{
		{"a red section runs alone", "FAIL smoke-shard1 (exit 1)\nFAILED-CHECK test/sections/28_a.sh\nFAILED-CHECK test/sections/30_b.sh\n", check, false,
			[]string{"SECTION_LIST='test/sections/28_a.sh' bash test/gate.sh", "SECTION_LIST='test/sections/30_b.sh' bash test/gate.sh"}, true},
		{"no key keeps the full command", "FAIL smoke-shard1 (exit 1)\nFAILED-CHECK test/sections/28_a.sh\n", "", false, []string{"bash test/gate.sh"}, false},
		{"a template without {check} keeps the full command", "FAIL smoke-shard1 (exit 1)\nFAILED-CHECK test/sections/28_a.sh\n", "bash test/gate.sh", false, []string{"bash test/gate.sh"}, false},
		{"a shard that named no section keeps the full command", "FAIL smoke-shard1 (exit 1)\n", check, false, []string{"bash test/gate.sh"}, false},
		{"another red check keeps the full command", "FAIL doclint (exit 1)\nFAIL smoke-shard1 (exit 1)\nFAILED-CHECK test/sections/28_a.sh\n", check, false, []string{"bash test/gate.sh"}, false},
		{"a leak keeps the full command", "FAIL smoke-shard1 (exit 1)\nFAILED-CHECK test/sections/28_a.sh\nFAIL leak: checks left temp entries\n", check, false, []string{"bash test/gate.sh"}, false},
		{"a retried shard is skipped by the train re-run", "FAIL smoke-shard1 (exit 1)\nRETRIED-CHECK smoke-shard1\nFAILED-CHECK test/sections/28_a.sh\n", check, true, nil, false},
		{"a retried shard still narrows a bisect step", "FAIL smoke-shard1 (exit 1)\nRETRIED-CHECK smoke-shard1\nFAILED-CHECK test/sections/28_a.sh\n", check, false,
			[]string{"SECTION_LIST='test/sections/28_a.sh' bash test/gate.sh"}, true},
		{"a retried shard does not hide an unretried one", "FAIL smoke-shard1 (exit 1)\nRETRIED-CHECK smoke-shard1\nFAILED-CHECK test/sections/28_a.sh\nFAIL smoke-shard2 (exit 1)\nFAILED-CHECK test/sections/40_c.sh\n", check, true,
			[]string{"SECTION_LIST='test/sections/40_c.sh' bash test/gate.sh"}, true},
		{"a Go package past the retry budget is not retried", "gate: skipping Go retries (4 failed packages; limit 3)\nFAIL gotest (exit 1)\n", check, true, []string{"bash test/gate.sh"}, false},
		{"a Go package retried red is skipped", "FAIL gotest (exit 1)\nRETRIED-CHECK gotest\n", check, true, nil, false},
		{"a non-smoke check runs alone through the template", "FAIL lint (exit 1)\nFAILED-CHECK lint:style\n", "lint-one {check}", false, []string{"lint-one 'lint:style'"}, true},
		{"a retried log changes nothing without the key", "FAIL smoke-shard1 (exit 1)\nRETRIED-CHECK smoke-shard1\nFAILED-CHECK test/sections/28_a.sh\n", "", true, []string{"bash test/gate.sh"}, false},
		{"output that is no gate log keeps the old rule", "boom\n", check, true, []string{"bash test/gate.sh"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, narrowed := rerunCommands(log(c.log), template, c.check, c.skipRetried)
			if strings.Join(got, "|") != strings.Join(c.want, "|") || narrowed != c.narrowed {
				t.Errorf("rerunCommands = %q narrowed=%v, want %q narrowed=%v", got, narrowed, c.want, c.narrowed)
			}
		})
	}
}
