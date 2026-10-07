package testledger

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

const goodLedger = `[
 {"test":"TestFlaky","owner":"dana","receipt":"#378","expires":"2026-10-07"},
 {"test":"TestOld","owner":"kit","receipt":"#300","expires":"2026-10-06"},
 {"test":"TestLater","owner":"ben","receipt":"#400","expires":"2026-12-01T00:00:00Z"}
]`

func TestLoadMissingAndEmptyAreNoExclusions(t *testing.T) {
	root := t.TempDir()
	if l, err := Load(root); err != nil || len(l.Entries) != 0 {
		t.Fatalf("missing file: %+v %v", l, err)
	}
	os.MkdirAll(filepath.Join(root, ".rota"), 0o755)
	for _, body := range []string{"", "[]", "  \n"} {
		os.WriteFile(filepath.Join(root, ".rota", "test-ledger.json"), []byte(body), 0o644)
		if l, err := Load(root); err != nil || len(l.Entries) != 0 {
			t.Errorf("%q: %+v %v", body, l, err)
		}
	}
}

func TestExpiryIsEndOfDayForADate(t *testing.T) {
	l, err := Parse([]byte(goodLedger))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range l.Expired(now) {
		names = append(names, e.Test)
	}
	if strings.Join(names, ",") != "TestOld" {
		t.Errorf("expired = %v; a date holds through its last day", names)
	}
	if got := len(l.Active(now)); got != 2 {
		t.Errorf("active = %d", got)
	}
}

func TestMalformedEntriesNameTheEntry(t *testing.T) {
	cases := map[string]string{
		`[{"test":"TestA","owner":"x","receipt":"#1"}]`:                                      "entry 1 (TestA): missing expires",
		`[{"test":"TestA","owner":"x","receipt":"#1","expires":"2026-01-01"},{"owner":"x"}]`: "entry 2: missing test",
		`[{"test":"TestB","owner":"x","receipt":"#1","expires":"soon"}]`:                     `entry 1 (TestB): expires "soon"`,
		`{"test":"x"}`: "not a JSON list",
	}
	for body, want := range cases {
		_, err := Parse([]byte(body))
		var bad *MalformedError
		if !errors.As(err, &bad) || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", body, err, want)
		}
	}
}

const failTwo = "=== RUN TestFlaky\n--- FAIL: TestFlaky (0.00s)\n    --- FAIL: TestFlaky/sub (0.00s)\n--- FAIL: TestOther (0.00s)\nFAIL\nFAIL\tpkg\t0.01s\n"

func TestExcuses(t *testing.T) {
	l, _ := Parse([]byte(goodLedger))
	if ex, ok := l.Excuses("--- FAIL: TestFlaky (0.00s)\n    --- FAIL: TestFlaky/sub (0.00s)\nFAIL\nFAIL\tpkg\t0.01s\n", now); !ok || len(ex) != 1 || ex[0].Test != "TestFlaky" {
		t.Errorf("unexpired entry (and its subtest) should excuse: %v %v", ex, ok)
	}
	if _, ok := l.Excuses(failTwo, now); ok {
		t.Error("a failing test with no entry must still fail")
	}
	if _, ok := l.Excuses("--- FAIL: TestOld (0.00s)\nFAIL\n", now); ok {
		t.Error("an expired entry excuses nothing")
	}
	if _, ok := (Ledger{}).Excuses("--- FAIL: TestFlaky (0.00s)\nFAIL\n", now); ok {
		t.Error("an empty ledger excuses nothing")
	}
	for _, out := range []string{"", "something broke\n", "--- FAIL: TestFlaky (0s)\nFAIL\tpkg [build failed]\n", "--- FAIL: TestFlaky (0s)\npanic: boom\n", "--- FAIL: TestFlaky (0s)\nFAIL\tpkg\t0.01s\n# pkg\nvet: unused variable\n"} {
		if _, ok := l.Excuses(out, now); ok {
			t.Errorf("output with an unnamed failure must not be excused: %q", out)
		}
	}
}
