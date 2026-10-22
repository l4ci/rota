package worker

import (
	"errors"
	"slices"
	"testing"

	"github.com/l4ci/rota/internal/proof"
)

func TestMissingFastProofLatestRowWins(t *testing.T) {
	const head = "abc1234def5678"
	pass := proof.Row{Check: FastTierCheck, Result: "PASS", Sha: "abc1234"}
	fail := proof.Row{Check: FastTierCheck, Result: "FAIL", Sha: "abc1234"}
	want := []string{"go test ./..."}
	if got := MissingFastProof([]proof.Row{pass}, head, want); got != nil {
		t.Errorf("PASS at head: missing = %v", got)
	}
	if got := MissingFastProof([]proof.Row{pass, fail}, head, want); len(got) != 1 {
		t.Errorf("PASS then FAIL at head must be unproven, missing = %v", got)
	}
	if got := MissingFastProof([]proof.Row{{Check: "'rota' 'test' 'run' 'fast'", Result: "PASS", Sha: "abc1234"}}, head, want); got != nil {
		t.Errorf("quoted tier form must prove the tier, missing = %v", got)
	}
	own := proof.Row{Check: "go test ./...", Result: "PASS", Sha: "abc1234"}
	if got := MissingFastProof([]proof.Row{own}, head, want); got != nil {
		t.Errorf("own PASS row: missing = %v", got)
	}
	if got := MissingFastProof([]proof.Row{{Check: "go test ./...", Result: "PASS", Sha: "other00"}}, head, want); len(got) != 1 {
		t.Errorf("PASS at another sha proves nothing, missing = %v", got)
	}
}

func TestOpenBundle(t *testing.T) {
	closed := func(m map[string]bool) func(string) bool { return func(id string) bool { return m[id] } }
	if got := OpenBundle([]string{"1"}, closed(nil)); !slices.Equal(got, []string{"1"}) {
		t.Errorf("single = %v", got)
	}
	if got := OpenBundle([]string{"1", "2", "3"}, closed(map[string]bool{"2": true})); !slices.Equal(got, []string{"1", "3"}) {
		t.Errorf("closed member must drop = %v", got)
	}
	if got := OpenBundle([]string{"1", "2"}, closed(map[string]bool{"1": true})); !slices.Equal(got, []string{"1", "2"}) {
		t.Errorf("lead always stays = %v", got)
	}
}

func TestMissingRed(t *testing.T) {
	fail := []proof.Row{{Result: "FAIL"}}
	pass := []proof.Row{{Result: "PASS"}}
	rows := func(m map[string][]proof.Row) func(string) ([]proof.Row, error) {
		return func(id string) ([]proof.Row, error) { return m[id], nil }
	}
	prod := []string{"internal/x/x.go", "README.md"}
	cases := []struct {
		name   string
		tdd    bool
		files  []string
		bundle []string
		rows   map[string][]proof.Row
		want   bool
	}{
		{"tdd off", false, prod, []string{"1"}, nil, false},
		{"tests only", true, []string{"a_test.go", "testdata/f.go", "x.md"}, []string{"1"}, nil, false},
		{"prod go, no rows", true, prod, []string{"1"}, nil, true},
		{"prod go, only PASS", true, prod, []string{"1"}, map[string][]proof.Row{"1": pass}, true},
		{"prod go, FAIL row", true, prod, []string{"1"}, map[string][]proof.Row{"1": fail}, false},
		{"bundle: FAIL on a member", true, prod, []string{"1", "2"}, map[string][]proof.Row{"1": pass, "2": fail}, false},
		{"bundle: none FAIL", true, prod, []string{"1", "2"}, map[string][]proof.Row{"1": pass, "2": pass}, true},
	}
	for _, tc := range cases {
		got, err := MissingRed(tc.tdd, tc.files, tc.bundle, rows(tc.rows))
		if err != nil || got != tc.want {
			t.Errorf("%s: got %v, %v want %v", tc.name, got, err, tc.want)
		}
	}
	boom := errors.New("boom")
	if _, err := MissingRed(true, prod, []string{"1"}, func(string) ([]proof.Row, error) { return nil, boom }); !errors.Is(err, boom) {
		t.Errorf("rows error must surface, got %v", err)
	}
}
