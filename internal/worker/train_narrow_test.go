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
// isolated-retry line.
func redSection(red string, retried bool) func(string) string {
	retry := ""
	if retried {
		retry = `printf 'FAIL smoke-shard1 isolated retry (exit 1)\n'; `
	}
	return func(full string) string {
		return fmt.Sprintf(`echo full >> %s; if %s; then %sprintf 'FAIL smoke-shard1 (exit 1)\nFAILED-SECTION %s\n'; exit 1; fi`, full, red, retry, narrowSection)
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

// AC-4: a different failure that shows only in the full run of the members
// that would land is reported, and they do not land.
func TestTrainSecondFailureInTheFullPrefixIsReported(t *testing.T) {
	// Section 28 fails once b3 is merged. The doclint check fails with b2
	// merged and b3 not: only the full run of b1+b2 shows it.
	gate := func(full string) string {
		return fmt.Sprintf(`echo full >> %s; if test -f b3.txt; then printf 'FAIL smoke-shard1 isolated retry (exit 1)\nFAIL smoke-shard1 (exit 1)\nFAILED-SECTION %s\n'; exit 1; fi; if test -f b2.txt; then printf 'FAIL doclint (exit 1)\n'; exit 1; fi`, full, narrowSection)
	}
	nw := newNarrowWorld(t, gate, failsWhen("test -f b3.txt"))
	res, err := nw.train(TrainOpts{Targets: []string{"b1", "b2", "b3"}, LandGreen: true})
	if err != nil || res.Verdict != landing.GateVerifyFailed {
		t.Fatalf("%+v %v", res, err)
	}
	if strings.Join(res.Landed, ",") != "b1" {
		t.Errorf("only b1 passes the full gate, landed %v", res.Landed)
	}
	if !strings.Contains(res.Err+strings.Join(res.Notes, "\n"), "doclint") {
		t.Errorf("the second failure is not reported: err %q notes %q", res.Err, res.Notes)
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
		{"a red section runs alone", "FAIL smoke-shard1 (exit 1)\nFAILED-SECTION test/sections/28_a.sh\nFAILED-SECTION test/sections/30_b.sh\n", check, false,
			[]string{"SECTION_LIST='test/sections/28_a.sh' bash test/gate.sh", "SECTION_LIST='test/sections/30_b.sh' bash test/gate.sh"}, true},
		{"no key keeps the full command", "FAIL smoke-shard1 (exit 1)\nFAILED-SECTION test/sections/28_a.sh\n", "", false, []string{"bash test/gate.sh"}, false},
		{"a template without {check} keeps the full command", "FAIL smoke-shard1 (exit 1)\nFAILED-SECTION test/sections/28_a.sh\n", "bash test/gate.sh", false, []string{"bash test/gate.sh"}, false},
		{"a shard that named no section keeps the full command", "FAIL smoke-shard1 (exit 1)\n", check, false, []string{"bash test/gate.sh"}, false},
		{"another red check keeps the full command", "FAIL doclint (exit 1)\nFAIL smoke-shard1 (exit 1)\nFAILED-SECTION test/sections/28_a.sh\n", check, false, []string{"bash test/gate.sh"}, false},
		{"a leak keeps the full command", "FAIL smoke-shard1 (exit 1)\nFAILED-SECTION test/sections/28_a.sh\nFAIL leak: checks left temp entries\n", check, false, []string{"bash test/gate.sh"}, false},
		{"a retried shard is skipped by the train re-run", "FAIL smoke-shard1 isolated retry (exit 1)\nFAIL smoke-shard1 (exit 1)\nFAILED-SECTION test/sections/28_a.sh\n", check, true, nil, false},
		{"a retried shard still narrows a bisect step", "FAIL smoke-shard1 isolated retry (exit 1)\nFAIL smoke-shard1 (exit 1)\nFAILED-SECTION test/sections/28_a.sh\n", check, false,
			[]string{"SECTION_LIST='test/sections/28_a.sh' bash test/gate.sh"}, true},
		{"a retried shard does not hide an unretried one", "FAIL smoke-shard1 isolated retry (exit 1)\nFAIL smoke-shard1 (exit 1)\nFAILED-SECTION test/sections/28_a.sh\nFAIL smoke-shard2 (exit 1)\nFAILED-SECTION test/sections/40_c.sh\n", check, true,
			[]string{"SECTION_LIST='test/sections/40_c.sh' bash test/gate.sh"}, true},
		{"a Go package past the retry budget is not retried", "gate: skipping Go retries (4 failed packages; limit 3)\nFAIL gotest (exit 1)\n", check, true, []string{"bash test/gate.sh"}, false},
		{"a Go package retried red is skipped", "FAIL go:internal/x isolated retry (exit 1)\nFAIL gotest (exit 1)\n", check, true, nil, false},
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
