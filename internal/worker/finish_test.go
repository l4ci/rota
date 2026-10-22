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
	tier := []string{FastTierCheck, "'rota' 'test' 'run' 'fast'"}
	want := []string{"go test ./..."}
	if got := MissingFastProof([]proof.Row{pass}, head, tier, want); got != nil {
		t.Errorf("PASS at head: missing = %v", got)
	}
	if got := MissingFastProof([]proof.Row{pass, fail}, head, tier, want); len(got) != 1 {
		t.Errorf("PASS then FAIL at head must be unproven, missing = %v", got)
	}
	if got := MissingFastProof([]proof.Row{{Check: "'rota' 'test' 'run' 'fast'", Result: "PASS", Sha: "abc1234"}}, head, tier, want); got != nil {
		t.Errorf("quoted tier form must prove the tier, missing = %v", got)
	}
	own := proof.Row{Check: "go test ./...", Result: "PASS", Sha: "abc1234"}
	if got := MissingFastProof([]proof.Row{own}, head, tier, want); got != nil {
		t.Errorf("own PASS row: missing = %v", got)
	}
	if got := MissingFastProof([]proof.Row{{Check: "go test ./...", Result: "PASS", Sha: "other00"}}, head, tier, want); len(got) != 1 {
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

func bundle(ids ...string) func() []string { return func() []string { return ids } }

func TestMissingRed(t *testing.T) {
	fail := []proof.Row{{Result: "FAIL"}}
	pass := []proof.Row{{Result: "PASS"}}
	rows := func(m map[string][]proof.Row) func(string) ([]proof.Row, error) {
		return func(id string) ([]proof.Row, error) { return m[id], nil }
	}
	prod := []string{"internal/x/x.go", "README.md"}
	cases := []struct {
		name   string
		files  []string
		bundle []string
		rows   map[string][]proof.Row
		want   bool
	}{
		{"tests only", []string{"a_test.go", "testdata/f.go", "x.md"}, []string{"1"}, nil, false},
		{"prod go, no rows", prod, []string{"1"}, nil, true},
		{"prod go, only PASS", prod, []string{"1"}, map[string][]proof.Row{"1": pass}, true},
		{"prod go, FAIL row", prod, []string{"1"}, map[string][]proof.Row{"1": fail}, false},
		{"bundle: FAIL on a member", prod, []string{"1", "2"}, map[string][]proof.Row{"1": pass, "2": fail}, false},
		{"bundle: none FAIL", prod, []string{"1", "2"}, map[string][]proof.Row{"1": pass, "2": pass}, true},
	}
	for _, tc := range cases {
		got, err := MissingRed(tc.files, func() []string { return tc.bundle }, rows(tc.rows))
		if err != nil || got != tc.want {
			t.Errorf("%s: got %v, %v want %v", tc.name, got, err, tc.want)
		}
	}
	boom := errors.New("boom")
	if _, err := MissingRed(prod, bundle("1"), func(string) ([]proof.Row, error) { return nil, boom }); !errors.Is(err, boom) {
		t.Errorf("rows error must surface, got %v", err)
	}
}

// TestMissingRedStopsAtFirstFail pins the lazy walk: rows is asked per issue in
// bundle order, a FAIL stops it before a later issue's error, and an earlier
// error surfaces without asking the later issue.
func TestMissingRedStopsAtFirstFail(t *testing.T) {
	prod := []string{"internal/x/x.go"}
	boom := errors.New("boom")
	t.Run("FAIL before a later error", func(t *testing.T) {
		var asked []string
		got, err := MissingRed(prod, bundle("1", "2"), func(id string) ([]proof.Row, error) {
			asked = append(asked, id)
			if id == "1" {
				return []proof.Row{{Result: "FAIL"}}, nil
			}
			return nil, boom
		})
		if err != nil || got || !slices.Equal(asked, []string{"1"}) {
			t.Errorf("got %v, %v, asked %v; want false, nil, [1]", got, err, asked)
		}
	})
	t.Run("error before a later issue", func(t *testing.T) {
		var asked []string
		got, err := MissingRed(prod, bundle("1", "2"), func(id string) ([]proof.Row, error) {
			asked = append(asked, id)
			return nil, boom
		})
		if !errors.Is(err, boom) || got || !slices.Equal(asked, []string{"1"}) {
			t.Errorf("got %v, %v, asked %v; want false, boom, [1]", got, err, asked)
		}
	})
}

// TestMissingRedNoProdGoSkipsBundle: a diff without prod Go never resolves the
// bundle, so the backlog behind it stays closed.
func TestMissingRedNoProdGoSkipsBundle(t *testing.T) {
	got, err := MissingRed([]string{"a_test.go", "x.md"}, func() []string {
		t.Error("bundle resolved for a diff without prod Go")
		return nil
	}, nil)
	if err != nil || got {
		t.Errorf("got %v, %v want false, nil", got, err)
	}
}
