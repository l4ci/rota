// Package golden pins Go behaviour to approved records under
// testdata/golden/<test>.json in the test's package.
//
// Golden (or GoldenJSON) reads a record and decodes its outputs into the
// caller's value; the test compares Go against them. The file also records the
// inputs, and the test fails when the Go test's inputs differ from the
// recorded ones, so a case cannot change without the golden noticing.
//
// Regenerating: go test ./internal/<pkg> -run '^TestX$' -update-golden accepts
// the current Go output for tests that finish with Compare, provided the rest
// of the test passes: the record is written in a cleanup, only if the test did
// not fail. A record changes only when the output differs; the inputs stay as
// recorded. Read the JSON diff and say why in the PR: a changed record is a
// behaviour change. Tests that compare by hand cannot be regenerated; under
// -update-golden they fail with a message saying so, and the file is edited by
// hand. The cmd/rota frozen records are the same idea with their own flag,
// -update-frozen, because they hold whole CLI runs rather than helper outputs.
package golden

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
)

var update = flag.Bool("update-golden", false, "accept the current Go output as the golden for tests that end in Compare, if the test otherwise passes")

// loaded is the golden a test read last, by test name, for Compare to update.
var loaded sync.Map

type loadedGolden struct {
	path        string
	rec         golden
	regenerated bool
}

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
		t.Fatalf("no golden %s (%v); run -update-golden to record it if the test ends in Compare", path, err)
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
		t.Fatalf("%s: the test's inputs differ from the recorded ones; inputs are never regenerated, edit them in the file", path)
	}
	if err := json.Unmarshal(g.Outputs, out); err != nil {
		t.Fatalf("%s: decode outputs: %v", path, err)
	}
	cur := &loadedGolden{path: path, rec: g}
	loaded.Store(t.Name(), cur)
	if *update {
		t.Cleanup(func() {
			if t.Failed() && !cur.regenerated {
				t.Errorf("-update-golden cannot regenerate %s: this test compares by hand, so edit the file and justify the change in the PR", path)
			}
		})
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
//
// Under -update-golden, a mismatch is not a failure: got replaces the outputs
// of the golden the test loaded last, once the test has finished without
// failing. That needs the golden's outputs to be one result per case, as many
// as got; otherwise Compare fails and the file is edited by hand.
func Compare(t testing.TB, name string, inputs, got, want any) int {
	t.Helper()
	in, g, w := roundTrip(t, inputs), roundTrip(t, got), roundTrip(t, want)
	if len(g) != len(w) || len(g) != len(in) {
		t.Fatalf("%s: %d inputs, %d Go results, %d recorded results", name, len(in), len(g), len(w))
	}
	if *update && !reflect.DeepEqual(g, w) {
		regenerate(t, name, g)
		return len(g)
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

// regenerate queues got as the outputs of the golden the test loaded last.
func regenerate(t testing.TB, name string, got []any) {
	t.Helper()
	v, ok := loaded.Load(t.Name())
	if !ok {
		t.Fatalf("%s: -update-golden: the test loaded no golden to regenerate", name)
	}
	cur := v.(*loadedGolden)
	var recorded []any
	if err := json.Unmarshal(cur.rec.Outputs, &recorded); err != nil || len(recorded) != len(got) {
		t.Fatalf("%s: -update-golden cannot regenerate %s: its outputs are not one result per case (%d recorded, %d from Go); edit the file", name, cur.path, len(recorded), len(got))
	}
	cur.regenerated = true
	t.Logf("%s: -update-golden: %s will change", name, cur.path)
	t.Cleanup(func() {
		if t.Failed() {
			return
		}
		rec := golden{Inputs: cur.rec.Inputs, Outputs: marshal(t, got)}
		if err := os.WriteFile(cur.path, append(marshal(t, rec), '\n'), 0o644); err != nil {
			t.Errorf("write %s: %v", cur.path, err)
		}
	})
}
