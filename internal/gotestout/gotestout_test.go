package gotestout

import (
	"reflect"
	"testing"
)

func TestFailingTests(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   []string
		ok     bool
	}{
		{"named failures, subtest and dedupe", "--- FAIL: TestA (0.00s)\n    --- FAIL: TestA/sub (0.00s)\n--- FAIL: TestB (0.00s)\n--- FAIL: TestA (0.00s)\nFAIL\nFAIL\tpkg\t0.01s\n", []string{"TestA", "TestA/sub", "TestB"}, true},
		{"empty", "", nil, false},
		{"no fail lines", "something broke\nFAIL\n", nil, false},
		{"no trailing FAIL summary", "--- FAIL: TestA (0.00s)\n", nil, false},
		{"build failed", "--- FAIL: TestA (0s)\nFAIL\tpkg [build failed]\n", nil, false},
		{"setup failed", "--- FAIL: TestA (0s)\nFAIL\tpkg [setup failed]\nFAIL\n", nil, false},
		{"panic", "--- FAIL: TestA (0s)\npanic: boom\nFAIL\n", nil, false},
		{"timeout", "--- FAIL: TestA (0s)\n    panic: test timed out after 1m\nFAIL\n", nil, false},
		{"rota test lock trailer", "--- FAIL: TestA (0s)\nFAIL\nFAIL\tpkg\t0.01s\nrota test lock: sh exited 1\n", []string{"TestA"}, true},
		{"later step failed", "--- FAIL: TestA (0s)\nFAIL\nFAIL\tpkg\t0.01s\n# pkg\nvet: unused variable\n", nil, false},
	}
	for _, c := range cases {
		got, ok := FailingTests(c.output)
		if ok != c.ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, %v; want %v, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

const (
	playwrightOut = "\n  1) [desktop] › tests/visual/quests.spec.ts:176:1 › quests add fixed gold once @desktop ────────────\n\n    Error: expect(locator).toHaveJSProperty(expected) failed\n\n    Expected: false\n    Timeout: 5000ms\n\n  1 failed\n    [desktop] › tests/visual/quests.spec.ts:176:1 › quests add fixed gold once @desktop \n  7 skipped\n  2152 passed (15.1m)\n"
	vitestOut     = " FAIL  tests/unit/x.test.ts > adds\nAssertionError: expected 1 to be 2 // Object.is equality\n ❯ tests/unit/x.test.ts:5:15\n\n Test Files  1 failed (1)\n      Tests  1 failed | 3 passed (4)\n"
	nodeTapFail   = "TAP version 13\nnot ok 1 - adds\n  ---\n  error: 'Expected values to be strictly equal'\n  ...\n1..1\n# tests 1\n# pass 0\n# fail 1\n"
	nodeSpecFail  = "✖ adds (0.7ms)\nℹ tests 1\nℹ pass 0\nℹ fail 1\n\n✖ failing tests:\n\ntest at a.test.js:1:1\n✖ adds (0.7ms)\n  AssertionError [ERR_ASSERTION]: x\n"
)

func TestJSFailures(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   []string
		ok     bool
	}{
		{"playwright list reporter", playwrightOut, []string{"[desktop] › tests/visual/quests.spec.ts:176:1 › quests add fixed gold once @desktop"}, true},
		{"vitest", vitestOut, []string{"tests/unit/x.test.ts > adds"}, true},
		{"node tap", nodeTapFail, []string{"adds"}, true},
		{"node spec", nodeSpecFail, []string{"adds"}, true},
		{"empty", "", nil, false},
		{"passing run", "  1 passed (1.0s)\n", nil, false},
		{"numbered list without a failed summary", "  1) first step\n  2) second step\n", nil, false},
		{"summary without a named failure", "  1 failed\n", nil, false},
		{"go output is not js", "--- FAIL: TestA (0s)\nFAIL\n", nil, false},
	}
	for _, c := range cases {
		got, ok := JSFailures(c.output)
		if ok != c.ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, %v; want %v, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}
