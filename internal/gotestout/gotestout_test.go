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
		{"later step failed", "--- FAIL: TestA (0s)\nFAIL\nFAIL\tpkg\t0.01s\n# pkg\nvet: unused variable\n", nil, false},
	}
	for _, c := range cases {
		got, ok := FailingTests(c.output)
		if ok != c.ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, %v; want %v, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}
