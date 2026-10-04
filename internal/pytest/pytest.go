// Package pytest checks Go ports against goldens: a frozen record of what the
// retired Python helpers in bin/ produced.
//
// A parity test goes through Golden (or GoldenJSON), which reads the recorded
// outputs from testdata/golden/<test>.json in the test's package and compares
// Go against them. The file also records the inputs, and the test fails when
// the Go test's inputs differ from the recorded ones, so a case cannot change
// without the golden noticing.
//
// Nothing here runs Python and there is no way to regenerate a golden. A
// golden changes only by a hand-reviewed edit of the JSON: when a port changes
// behaviour on purpose, edit the affected inputs and outputs in the file and
// justify the change in the PR.
package pytest

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
)

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

// Golden loads testdata/golden/<test>.json, fails when its recorded inputs
// differ from inputs, and decodes the recorded outputs into out, a pointer.
func Golden(t testing.TB, inputs, out any) {
	t.Helper()
	path := goldenPath(t)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no golden %s (%v); goldens are frozen, see the internal/pytest doc", path, err)
	}
	var g golden
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	var have, want any
	if err := json.Unmarshal(marshal(t, inputs), &have); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(g.Inputs, &want); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if !reflect.DeepEqual(have, want) {
		t.Fatalf("%s: the test's inputs differ from the recorded ones; goldens are frozen, see the internal/pytest doc", path)
	}
	if err := json.Unmarshal(g.Outputs, out); err != nil {
		t.Fatalf("%s: decode outputs: %v", path, err)
	}
}

// GoldenJSON is Golden for a batch of cases: the golden records in as
// {"input": in} and out is read from its outputs.
func GoldenJSON(t testing.TB, in, out any) {
	t.Helper()
	Golden(t, map[string]any{"input": in}, out)
}

// Compare checks that got and want, two slices with one result per case,
// agree after a JSON round trip, and reports up to five mismatches with the
// input that produced them. It returns the number of cases compared.
func Compare(t testing.TB, name string, inputs, got, want any) int {
	t.Helper()
	in, g, w := roundTrip(t, inputs), roundTrip(t, got), roundTrip(t, want)
	if len(g) != len(w) || len(g) != len(in) {
		t.Fatalf("%s: %d inputs, %d Go results, %d recorded results", name, len(in), len(g), len(w))
	}
	bad := 0
	for i := range g {
		if reflect.DeepEqual(g[i], w[i]) {
			continue
		}
		bad++
		if bad <= 5 {
			t.Errorf("%s case %d\n input:  %s\n go:     %s\n golden: %s", name, i, show(in[i]), show(g[i]), show(w[i]))
		}
	}
	if bad > 0 {
		t.Fatalf("%s: %d of %d cases differ from the golden", name, bad, len(g))
	}
	return len(g)
}

func roundTrip(t testing.TB, v any) []any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out []any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func show(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}
