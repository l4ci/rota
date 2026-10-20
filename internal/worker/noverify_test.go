package worker

import "testing"

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
		if got := (Env{Config: memConfig(t, tc.cfg)}).NothingToVerify(""); got != tc.want {
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

// The gate's input reads config through Env.Config, not the project's file.
func TestGateInputDefaultReadsEnvConfig(t *testing.T) {
	root := newProject(t, `{"test":{"full":["from disk"]}}`)
	e := Env{Config: memConfig(t, `{"test":{"full":["from memory"]}}`)}.WithDefaults()
	in, err := e.GateInput(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(in.cfg.Full) != 1 || in.cfg.Full[0] != "from memory" {
		t.Errorf("Full = %v, want the in-memory config's", in.cfg.Full)
	}
}
