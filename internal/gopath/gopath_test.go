package gopath

import "testing"

func TestIsProdGo(t *testing.T) {
	for f, want := range map[string]bool{
		"cmd/rota/main.go":                  true,
		"internal/cli/worker_done.go":       true,
		"internal/cli/worker_done_test.go":  false,
		"internal/cli/testdata/fixture.go":  false,
		"testdata/sample.go":                false,
		"internal/cli/testdata/golden.json": false,
		"cmd/rota/README.md":                false,
		"go.mod":                            false,
	} {
		if got := IsProdGo(f); got != want {
			t.Errorf("IsProdGo(%q) = %v, want %v", f, got, want)
		}
	}
}

func TestIsTestGo(t *testing.T) {
	for f, want := range map[string]bool{
		"a_test.go":               true,
		"internal/x/y_test.go":    true,
		"internal/x/y.go":         false,
		"testdata/a.go":           false,
		"internal/x/y_test.go.md": false,
	} {
		if got := IsTestGo(f); got != want {
			t.Errorf("IsTestGo(%q) = %v, want %v", f, got, want)
		}
	}
}
