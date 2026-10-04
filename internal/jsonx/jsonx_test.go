package jsonx

import (
	"os"
	"testing"

	"github.com/l4ci/rota/internal/golden"
)

// The golden is json.dumps(json.loads(file), indent=2).
func TestRoundTripMatchesPython(t *testing.T) {
	raw, err := os.ReadFile("testdata/mixed.json")
	if err != nil {
		t.Fatal(err)
	}
	v, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var want string
	golden.Golden(t, map[string]any{"file": string(raw)}, &want)
	if string(got) != want {
		t.Fatalf("Go and Python disagree.\n--- go\n%s\n--- python\n%s", got, want)
	}
}

// The golden is json.dumps(json.loads(file)) with Python's default separators.
func TestCompactMatchesPython(t *testing.T) {
	raw, _ := os.ReadFile("testdata/mixed.json")
	v, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := MarshalCompact(v)
	var want string
	golden.Golden(t, map[string]any{"file": string(raw)}, &want)
	if string(got) != want {
		t.Fatalf("\n--- go\n%s\n--- python\n%s", got, want)
	}
}

func TestDuplicateKeyKeepsFirstPosition(t *testing.T) {
	v, err := Decode([]byte(`{"a": 1, "b": 2, "a": 3}`))
	if err != nil {
		t.Fatal(err)
	}
	o := v.(*Object)
	if k := o.Keys(); len(k) != 2 || k[0] != "a" {
		t.Fatalf("keys %v", k)
	}
	if got, _ := o.Get("a"); got.(interface{ String() string }).String() != "3" {
		t.Fatalf("a = %v, want last value 3", got)
	}
}

func TestDecodeRejectsTrailingData(t *testing.T) {
	if _, err := Decode([]byte(`{} {}`)); err == nil {
		t.Fatal("want error for two documents")
	}
}

func TestPyFloat(t *testing.T) {
	cases := map[float64]string{1: "1.0", 0.1: "0.1", 1e16: "1e+16", 1e15: "1000000000000000.0",
		1e-5: "1e-05", 0.0001: "0.0001", 1.5e300: "1.5e+300", -2.5: "-2.5", 123.456: "123.456"}
	for f, want := range cases {
		if got := PyFloat(f); got != want {
			t.Errorf("PyFloat(%v) = %q, want %q", f, got, want)
		}
	}
}

func TestObjectSetDelete(t *testing.T) {
	o := NewObject()
	o.Set("x", 1)
	o.Set("y", 2)
	o.Set("x", 3)
	o.Delete("x")
	o.Delete("missing")
	got, _ := Marshal(o)
	if string(got) != "{\n  \"y\": 2\n}" {
		t.Fatalf("got %s", got)
	}
}
