package golden

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
)

// fakeTB records failures instead of failing the real test, so Check's failure
// paths can be asserted. Fatalf ends the calling goroutine like testing.T does.
type fakeTB struct {
	testing.TB
	name     string
	errs     []string
	fatal    string
	cleanups []func()
}

func (f *fakeTB) Helper()      {}
func (f *fakeTB) Name() string { return f.name }
func (f *fakeTB) Failed() bool { return len(f.errs) > 0 || f.fatal != "" }
func (f *fakeTB) Errorf(format string, a ...any) {
	f.errs = append(f.errs, sprintf(format, a...))
}
func (f *fakeTB) Fatalf(format string, a ...any) {
	f.fatal = sprintf(format, a...)
	runtime.Goexit()
}
func (f *fakeTB) Logf(string, ...any) {}
func (f *fakeTB) Cleanup(fn func())   { f.cleanups = append(f.cleanups, fn) }

// run executes fn on its own goroutine (Fatalf calls Goexit) and then the
// registered cleanups, newest first.
func (f *fakeTB) run(fn func()) {
	done := make(chan struct{})
	go func() { defer close(done); fn() }()
	<-done
	for i := len(f.cleanups) - 1; i >= 0; i-- {
		f.cleanups[i]()
	}
}

func sprintf(format string, a ...any) string {
	return strings.TrimSpace(strings.NewReplacer("\n", " ").Replace(fmt.Sprintf(format, a...)))
}

type in struct {
	N int `json:"n"`
}

type out struct {
	Double int    `json:"double"`
	List   []int  `json:"list"`
	Name   string `json:"name"`
}

func TestCheck(t *testing.T) {
	Check(t, in{N: 2}, out{Double: 4, List: []int{1, 2}, Name: "x"})
}

// Two Check calls in one test read TestCheckSecond.json, then TestCheckSecond-2.json.
func TestCheckSecond(t *testing.T) {
	Check(t, in{N: 2}, out{Double: 4, List: []int{1, 2}, Name: "x"})
	Check(t, map[string]any{"n": 2}, map[string]any{"double": 4, "list": []int{1, 2}, "name": "x"})
}

func TestCheckFailures(t *testing.T) {
	good := out{Double: 4, List: []int{1, 2}, Name: "x"}
	tests := []struct {
		name      string
		test      string
		inputs    any
		got       any
		wantFatal string
		wantErrs  []string
	}{
		{"missing record", "TestCheckNoSuchRecord", in{2}, good, "no golden", nil},
		{"invalid record JSON", "TestCheckBadJSON", in{2}, good, "TestCheckBadJSON.json", nil},
		{"inputs differ", "TestCheckInputsDiffer", in{N: 3}, good, "inputs differ from the recorded", nil},
		{"output differs", "TestCheckOutputDiffers", in{2},
			out{Double: 5, List: []int{1, 3}, Name: "x"},
			"2 differences",
			[]string{".double", ".list[1]"}},
		{"extra key in got", "TestCheckOutputDiffers", in{2},
			map[string]any{"double": 4, "list": []int{1, 2}, "name": "x", "extra": true},
			"1 differences", []string{".extra"}},
		{"array length differs", "TestCheckOutputDiffers", in{2},
			map[string]any{"double": 4, "list": []int{1}, "name": "x"},
			"1 differences", []string{".list"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeTB{name: tc.test}
			f.run(func() { Check(f, tc.inputs, tc.got) })
			if !strings.Contains(f.fatal, tc.wantFatal) {
				t.Errorf("fatal = %q, want it to contain %q", f.fatal, tc.wantFatal)
			}
			if len(f.errs) != len(tc.wantErrs) {
				t.Fatalf("errs = %q, want %d", f.errs, len(tc.wantErrs))
			}
			for i, want := range tc.wantErrs {
				if !strings.Contains(f.errs[i], want) {
					t.Errorf("err %d = %q, want it to contain %q", i, f.errs[i], want)
				}
			}
		})
	}
}

func TestCheckCapsReportedDifferences(t *testing.T) {
	f := &fakeTB{name: "TestCheckOutputDiffers"}
	got := map[string]any{"double": 0, "list": []int{9, 9}, "name": "y", "a": 1, "b": 2, "c": 3}
	f.run(func() { Check(f, in{2}, got) })
	if len(f.errs) != 5 || !strings.Contains(f.fatal, "7 differences") {
		t.Errorf("errs = %d, fatal = %q; want 5 reported of 7", len(f.errs), f.fatal)
	}
}

func TestWalkReportsInputAtPath(t *testing.T) {
	var diffs []diff
	walk("", map[string]any{"a": []any{1.0, 2.0}}, map[string]any{"a": []any{1.0, 3.0}},
		map[string]any{"a": []any{"i0", "i1"}}, &diffs)
	if len(diffs) != 1 || diffs[0].path != ".a[1]" || diffs[0].in != "i1" || diffs[0].got != 2.0 || diffs[0].want != 3.0 {
		t.Errorf("diffs = %+v", diffs)
	}
}

func TestGoldenPathSuffixesRepeatCalls(t *testing.T) {
	p1 := goldenPath(t)
	p2 := goldenPath(t)
	if !strings.HasSuffix(p1, "TestGoldenPathSuffixesRepeatCalls.json") || !strings.HasSuffix(p2, "TestGoldenPathSuffixesRepeatCalls-2.json") {
		t.Errorf("paths = %q, %q", p1, p2)
	}
	t.Run("sub case", func(t *testing.T) {
		if p := goldenPath(t); !strings.HasSuffix(p, "TestGoldenPathSuffixesRepeatCalls__sub_case.json") {
			t.Errorf("subtest path = %q", p)
		}
	})
}

func TestCheckUpdateRewritesOutputsOnly(t *testing.T) {
	const rec = "testdata/golden/TestCheckUpdateScratch.json"
	orig := `{"inputs":{"n":2},"outputs":{"v":1}}` + "\n"
	write := func() {
		if err := os.WriteFile(rec, []byte(orig), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { os.Remove(rec) })
	*update = true
	t.Cleanup(func() { *update = false })

	// A passing test accepts the new output; the inputs stay as recorded.
	write()
	f := &fakeTB{name: "TestCheckUpdateScratch"}
	f.run(func() { Check(f, in{N: 2}, map[string]any{"v": 2}) })
	raw, _ := os.ReadFile(rec)
	if f.Failed() || string(raw) != `{"inputs":{"n":2},"outputs":{"v":2}}`+"\n" {
		t.Errorf("record = %q, failed = %v", raw, f.Failed())
	}

	// A failing test leaves the record alone.
	write()
	g := &fakeTB{name: "TestCheckUpdateScratch"}
	g.run(func() {
		Check(g, in{N: 2}, map[string]any{"v": 3})
		g.Errorf("other failure")
	})
	if raw, _ := os.ReadFile(rec); string(raw) != orig {
		t.Errorf("record changed despite failure: %q", raw)
	}
}
