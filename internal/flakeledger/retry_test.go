package flakeledger

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

const assertionFail = "--- FAIL: TestA (0.00s)\n    a_test.go:9: got 1 want 2\nFAIL\nFAIL\texample.com/p\t0.01s\nFAIL\n"

func TestRetryEligible(t *testing.T) {
	cases := []struct {
		name     string
		code     int
		out      string
		excused  bool
		wantOK   bool
		wantTest []string
	}{
		{"assertion failure retries", 1, assertionFail, false, true, []string{"TestA"}},
		{"exit 2 stays red", 2, assertionFail, false, false, nil},
		{"excused by ledger stays red", 1, assertionFail, true, false, nil},
		{"no assertion summary stays red", 1, "boom\n", false, false, nil},
		{"infrastructure signature stays red", 1, assertionFail + "fatal error: out of memory\n", false, false, nil},
		{"timeout stays red", 1, assertionFail + "panic: test timed out after 10m\n", false, false, nil},
		{"build failure stays red", 1, assertionFail + "FAIL\texample.com/q [build failed]\n", false, false, nil},
	}
	for _, c := range cases {
		tests, ok := RetryEligible(c.code, c.out, c.excused)
		if ok != c.wantOK || !reflect.DeepEqual(tests, c.wantTest) {
			t.Errorf("%s: got (%v, %v), want (%v, %v)", c.name, tests, ok, c.wantTest, c.wantOK)
		}
	}
}

// Every line of the shared marker file must reach the compiled rule; the
// Python gate helper reads the same file, so this pins both languages.
func TestInfrastructureMarkersAllTakeEffect(t *testing.T) {
	lines := strings.Split(strings.TrimSpace(infrastructureMarkers), "\n")
	if len(lines) < 10 {
		t.Fatalf("marker file has %d lines", len(lines))
	}
	for _, m := range lines {
		sample := strings.NewReplacer(`\b`, "", `\[`, "[", `\]`, "]", "^", "").Replace(m)
		if _, ok := RetryEligible(1, assertionFail+sample+"\n", false); ok {
			t.Errorf("marker %q (sample %q) did not block retry", m, sample)
		}
	}
}

func TestNewEntryFillsBothRuns(t *testing.T) {
	ts := time.Date(2026, 10, 30, 12, 0, 0, 0, time.FixedZone("x", 3600))
	first, retry := Run{Output: "red", ExitCode: 1}, Run{Output: "green", WallSeconds: 1.5}
	got := NewEntry("chk", []string{"TestA"}, "tree", ts, first, retry)
	want := Entry{Check: "chk", Tests: []string{"TestA"}, Tree: "tree", TS: ts.UTC(), First: first, Retry: retry}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v want %+v", got, want)
	}
}
