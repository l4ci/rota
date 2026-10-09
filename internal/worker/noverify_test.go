package worker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNothingToVerify(t *testing.T) {
	for _, tc := range []struct {
		name, cfg string
		want      bool
	}{
		{"both empty", `{"test":{"full":[],"e2e":[]}}`, true},
		{"no config keys", `{}`, true},
		{"full set", `{"test":{"full":["go test ./..."]}}`, false},
		{"e2e only still verifies", `{"test":{"full":[],"e2e":["make e2e"]}}`, false},
		{"ci verifies elsewhere", `{"test":{"fullWhere":"ci","ciChecks":["test"]}}`, false},
		{"bad fullWhere is the gate's error", `{"test":{"fullWhere":"nope"}}`, false},
	} {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".rota", "config.json"), []byte(tc.cfg), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := NothingToVerify(root); got != tc.want {
			t.Errorf("%s: NothingToVerify = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestNoVerifyRule(t *testing.T) {
	for _, tc := range []struct {
		name      string
		where     string
		full, e2e []string
		want      bool
	}{
		{"local, both empty", WhereLocal, nil, nil, true},
		{"local, full set", WhereLocal, []string{"go test"}, nil, false},
		{"local, e2e only", WhereLocal, nil, []string{"make e2e"}, false},
		{"local, both set", WhereLocal, []string{"go test"}, []string{"make e2e"}, false},
		{"ci, commands set", WhereCI, []string{"go test"}, []string{"make e2e"}, false},
		{"ci verifies elsewhere", WhereCI, nil, nil, false},
		{"unresolved where", "", nil, nil, false},
	} {
		if got := noVerifyRule(tc.where, tc.full, tc.e2e); got != tc.want {
			t.Errorf("%s: noVerifyRule = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The rule is symmetric in full and e2e today, so swapping the adjacent
// []string arguments at a call site is harmless. This pins that: if the rule
// ever becomes asymmetric, this fails and forces an audit of the three call
// sites (gate.go, train.go, NothingToVerify) instead of a silent swap.
func TestNoVerifyRuleSymmetricInTiers(t *testing.T) {
	a, b := []string{"go test"}, []string{"make e2e"}
	for _, where := range []string{WhereLocal, WhereCI, ""} {
		for _, tc := range [][2][]string{{nil, nil}, {a, nil}, {a, b}} {
			if noVerifyRule(where, tc[0], tc[1]) != noVerifyRule(where, tc[1], tc[0]) {
				t.Errorf("where=%q full=%v e2e=%v: rule differs when tiers are swapped", where, tc[0], tc[1])
			}
		}
	}
}
