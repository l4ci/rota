// Package golden pins Go behaviour to approved records under
// testdata/golden/<test>.json in the test's package.
//
// Check reads a record and compares the Go output with it. The file also
// records the inputs, and the test fails when the Go test's inputs differ from
// the recorded ones, so a case cannot change without the golden noticing.
//
// Regenerating: go test ./internal/<pkg> -run '^TestX$' -update-golden accepts
// the current Go output, provided the rest of the test passes: the record is
// written in a cleanup, only if the test did not fail. A record changes only
// when the output differs; the inputs stay as recorded. Read the JSON diff and
// say why in the PR: a changed record is a behaviour change. The cmd/rota
// frozen records are the same idea with their own flag, -update-frozen,
// because they hold whole CLI runs rather than helper outputs.
package golden

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

var update = flag.Bool("update-golden", false, "accept the current Go output as the golden, if the test otherwise passes")

var goldenSeq sync.Map // test name -> calls so far, so a test can record more than one golden

// goldenPath is testdata/golden/<test name>[-N].json relative to the test's
// package directory, the working directory of go test.
func goldenPath(t testing.TB) string {
	name := strings.NewReplacer("/", "__", " ", "_").Replace(t.Name())
	n, _ := goldenSeq.LoadOrStore(t.Name(), new(int))
	p := n.(*int)
	*p++
	if *p > 1 {
		name += "-" + strconv.Itoa(*p)
	}
	t.Cleanup(func() { goldenSeq.Delete(t.Name()) })
	return filepath.Join("testdata", "golden", name+".json")
}

type golden struct {
	Inputs  json.RawMessage `json:"inputs"`
	Outputs json.RawMessage `json:"outputs"`
}

func marshal(t testing.TB, v any) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatalf("marshal golden: %v", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// Check compares got with the outputs recorded in testdata/golden/<test>.json.
// It fails when the record's inputs differ from inputs, or when got differs
// from the recorded outputs after a JSON round trip, naming up to five
// differing paths with the go and golden values and the input at the same path
// when there is one.
//
// Under -update-golden a differing got is not a failure: it replaces the
// recorded outputs once the test has finished without failing. Call Check once
// per golden, after computing everything it covers.
func Check(t testing.TB, inputs, got any) {
	t.Helper()
	path := goldenPath(t)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no golden %s (%v); record it by hand", path, err)
	}
	var g golden
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	in, rec, have := decode(t, marshal(t, inputs)), decode(t, g.Inputs), decode(t, marshal(t, got))
	if !reflect.DeepEqual(in, rec) {
		t.Fatalf("%s: the test's inputs differ from the recorded ones; inputs are never regenerated, edit them in the file", path)
	}
	want := decode(t, g.Outputs)
	if reflect.DeepEqual(have, want) {
		return
	}
	if *update {
		t.Logf("-update-golden: %s will change", path)
		t.Cleanup(func() {
			if t.Failed() {
				return
			}
			out := golden{Inputs: g.Inputs, Outputs: marshal(t, got)}
			if err := os.WriteFile(path, append(marshal(t, out), '\n'), 0o644); err != nil {
				t.Errorf("write %s: %v", path, err)
			}
		})
		return
	}
	var diffs []diff
	walk("", have, want, in, &diffs)
	for i, d := range diffs {
		if i == 5 {
			break
		}
		t.Errorf("%s at %s\n input:  %s\n go:     %s\n golden: %s", path, d.path, show(d.in), show(d.got), show(d.want))
	}
	t.Fatalf("%s: %d differences from the golden", path, len(diffs))
}

type diff struct {
	path          string
	in, got, want any
}

// walk collects the leaves where got and want differ, descending through
// objects and equal-length arrays, with the input found at the same path.
func walk(path string, got, want, in any, out *[]diff) {
	switch g := got.(type) {
	case map[string]any:
		if w, ok := want.(map[string]any); ok {
			im, _ := in.(map[string]any)
			for _, k := range sortedKeys(g, w) {
				walk(path+"."+k, g[k], w[k], im[k], out)
			}
			return
		}
	case []any:
		if w, ok := want.([]any); ok && len(g) == len(w) {
			ia, _ := in.([]any)
			for i := range g {
				var iv any
				if len(ia) == len(g) {
					iv = ia[i]
				}
				walk(path+"["+strconv.Itoa(i)+"]", g[i], w[i], iv, out)
			}
			return
		}
	}
	if !reflect.DeepEqual(got, want) {
		*out = append(*out, diff{path, in, got, want})
	}
}

func sortedKeys(a, b map[string]any) []string {
	seen := map[string]bool{}
	var keys []string
	for _, m := range []map[string]any{a, b} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	sort.Strings(keys)
	return keys
}

func decode(t testing.TB, raw []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode golden JSON: %v", err)
	}
	return v
}

func show(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}
